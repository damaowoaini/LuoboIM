package adminprovision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type User struct {
	ID, Nickname, FaceURL, ProvisioningID string
	ProvisioningPurpose                   string
	CreateTime                            int64
}
type Defaults struct {
	Friends []string `json:"friends"`
	Groups  []string `json:"groups"`
}
type IM interface {
	Find(context.Context, string) (*User, error)
	Create(context.Context, User) error
	PlanDefaults(context.Context, bool) (Defaults, error)
	ApplyDefaults(context.Context, string, Defaults) error
}
type Service struct {
	DB           *gorm.DB
	IM           IM
	Route        Route
	ServerID     string
	RouteEnabled bool
	FindExisting func(context.Context, string) (*User, error)
}
type Status struct {
	Found                bool   `json:"found"`
	AdminRequestID       string `json:"adminRequestID,omitempty"`
	ReceiptID            string `json:"receiptID,omitempty"`
	UserID               string `json:"userID,omitempty"`
	State                string `json:"state"`
	Created              bool   `json:"created"`
	DefinitiveNotCreated bool   `json:"definitiveNotCreated"`
	Retryable            bool   `json:"retryable"`
	Reason               string `json:"reason,omitempty"`
}

var errOwnerConflict = errors.New("canonical account ownership is unconfirmed")

func (s *Service) Reserve(ctx context.Context, actor, id, fingerprint string, all bool) (*Creation, error) {
	if actor == "" || len(actor) > 64 || !ValidID(id) || len(fingerprint) != 64 {
		return nil, errors.New("invalid creation intent")
	}
	now := time.Now().UTC()
	intent := Intent{ActorID: actor, RequestID: id, Fingerprint: fingerprint, State: "reserved", CreatedAt: now, UpdatedAt: now}
	if e := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&intent).Error; e != nil {
		return nil, e
	}
	if e := s.DB.WithContext(ctx).Where("actor_id=? AND request_id=?", actor, id).Take(&intent).Error; e != nil {
		return nil, e
	}
	if intent.Fingerprint != fingerprint {
		return nil, errors.New("creation intent payload conflict")
	}
	if intent.State == "rejected" {
		return nil, errors.New("creation intent was rejected;start a new intent")
	}
	return &Creation{ActorID: actor, RequestID: id, Fingerprint: fingerprint, UserID: intent.UserID, ReceiptID: intent.ReceiptID, Recovered: intent.State == "created", DefaultsAll: all, ServerID: s.ServerID, RouteEnabled: s.RouteEnabled}, nil
}

// Reject serializes with final creation. It may never mark a committed account
// absent, even if another retry reaches validation at the same time.
func (s *Service) Reject(ctx context.Context, c *Creation, reason string) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		reused, e := LockCreation(ctx, tx, c)
		if e != nil {
			return e
		}
		if reused {
			return nil
		}
		return tx.Model(&Intent{}).Where("actor_id=? AND request_id=? AND state=?", c.ActorID, c.RequestID, "reserved").Updates(map[string]any{"state": "rejected", "reason": reason, "updated_at": time.Now().UTC()}).Error
	})
}

// ResolveUncreated is an explicit cancellation of this administrator's pending
// creation intent. The same row lock gates the final account transaction, so a
// delayed request cannot create an account after definitiveNotCreated is issued.
// An unobserved ID gets a persistent tombstone rather than a timeout guess.
func (s *Service) ResolveUncreated(ctx context.Context, actor, id string) (Status, error) {
	if actor == "" || len(actor) > 64 || !ValidID(id) {
		return Status{}, errors.New("invalid creation resolution")
	}
	now := time.Now().UTC()
	e := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		tombstone := Intent{ActorID: actor, RequestID: id, Fingerprint: strings.Repeat("0", 64), State: "rejected", Reason: "cancelled_before_creation", CreatedAt: now, UpdatedAt: now}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&tombstone).Error; e != nil {
			return e
		}
		var intent Intent
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("actor_id=? AND request_id=?", actor, id).Take(&intent).Error; e != nil {
			return e
		}
		if intent.State == "created" || intent.State == "rejected" {
			return nil
		}
		if intent.State != "reserved" {
			return errors.New("unknown creation state;resolution denied")
		}
		return tx.Model(&Intent{}).Where("actor_id=? AND request_id=? AND state=?", actor, id, "reserved").Updates(map[string]any{"state": "rejected", "reason": "cancelled_before_creation", "updated_at": now}).Error
	})
	if e != nil {
		return Status{}, e
	}
	return s.Lookup(ctx, actor, id, "", false)
}
func (s *Service) Lookup(ctx context.Context, actor, requestID, receiptID string, super bool) (Status, error) {
	status := Status{State: "unobserved"}
	if (requestID == "") == (receiptID == "") || (requestID != "" && !ValidID(requestID)) || (receiptID != "" && !ValidID(receiptID)) {
		return status, errors.New("provide one valid intent or receipt ID")
	}
	if requestID != "" {
		var intent Intent
		e := s.DB.WithContext(ctx).Where("actor_id=? AND request_id=?", actor, requestID).Take(&intent).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return status, nil
		}
		if e != nil {
			return status, e
		}
		status = Status{Found: true, AdminRequestID: requestID, ReceiptID: intent.ReceiptID, UserID: intent.UserID, State: intent.State, Created: intent.State == "created", DefinitiveNotCreated: intent.State == "rejected", Retryable: intent.State == "reserved", Reason: intent.Reason}
		if intent.ReceiptID == "" {
			return status, nil
		}
		receiptID = intent.ReceiptID
	}
	var job Job
	query := s.DB.WithContext(ctx).Where("receipt_id=?", receiptID)
	if !super {
		query = query.Where("source<>? AND (actor_id=? OR original_creator_id=?)", publicSource, actor, actor)
	}
	if e := query.Take(&job).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return Status{State: "unobserved"}, nil
		}
		return status, e
	}
	return statusFor(job), nil
}
func statusFor(j Job) Status {
	return Status{Found: true, AdminRequestID: j.RequestID, ReceiptID: j.ReceiptID, UserID: j.UserID, State: j.State, Created: true, Retryable: j.State != "bound" && j.State != "direct_only" && j.State != "conflict", Reason: j.Reason}
}
func retryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 5 {
		attempts = 5
	}
	delay := 10 * time.Second * time.Duration(1<<attempts)
	if delay > 5*time.Minute {
		return 5 * time.Minute
	}
	return delay
}
func terminal(state string) bool {
	return state == "bound" || state == "direct_only" || state == "conflict"
}

// snapshot holds the canonical account rows while their outbound identity is
// checked/delivered. No credential column is selected, returned or logged.
func snapshot(ctx context.Context, tx *gorm.DB, id string) (*table.Attribute, []Account, error) {
	var account table.Account
	if e := tx.WithContext(ctx).Select("user_id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=?", id).Take(&account).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, nil, errOwnerConflict
		}
		return nil, nil, e
	}
	var attr table.Attribute
	if e := tx.WithContext(ctx).Select("user_id", "nickname", "face_url", "email", "area_code", "phone_number").Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=?", id).Take(&attr).Error; e != nil {
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil, nil, errOwnerConflict
		}
		return nil, nil, e
	}
	cancelled, e := accountstate.IsCancelled(ctx, tx, id)
	if e != nil {
		return nil, nil, e
	}
	if cancelled {
		return nil, nil, errOwnerConflict
	}
	accounts, e := Accounts(attr.Email, attr.AreaCode, attr.PhoneNumber)
	if e != nil {
		return nil, nil, errOwnerConflict
	}
	for _, a := range accounts {
		var count int64
		q := tx.WithContext(ctx).Model(&table.Attribute{})
		if a.Email != "" {
			q = q.Where("LOWER(TRIM(email))=?", a.Email)
		} else {
			q = q.Where("area_code=? AND phone_number=?", a.AreaCode, a.PhoneNumber)
		}
		if e = q.Count(&count).Error; e != nil {
			return nil, nil, e
		}
		if count != 1 {
			return nil, nil, errOwnerConflict
		}
	}
	return &attr, accounts, nil
}

// Recover is an explicit audited authorization for an already existing chart
// and IM owner. Historical operator_user_id is deliberately not inferred as a
// creator: the old registrar wrote the configured bootstrap identity there.
func (s *Service) Recover(ctx context.Context, actor, id, reason string) (Status, error) {
	reason = strings.TrimSpace(reason)
	if !s.RouteEnabled || s.ServerID == "" || s.Route == nil {
		return Status{}, errors.New("central route provisioning is disabled")
	}
	if actor == "" || id == "" || len(id) > 64 || len(reason) < 6 || len(reason) > 1024 || strings.ContainsAny(reason, "\x00\r\n") {
		return Status{}, errors.New("invalid recovery authorization")
	}
	find := s.FindExisting
	if find == nil {
		if s.IM == nil {
			return Status{}, errors.New("IM owner lookup unavailable")
		}
		find = s.IM.Find
	}
	existing, e := find(ctx, id)
	if e != nil || existing == nil || existing.ID != id || existing.CreateTime <= 0 {
		return Status{}, errors.New("existing IM owner could not be confirmed")
	}
	var result Status
	e = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		_, accounts, e := snapshot(ctx, tx, id)
		if e != nil {
			return e
		}
		if len(accounts) == 0 {
			return errors.New("account has no routable identity")
		}
		var job Job
		e = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=?", id).Take(&job).Error
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		now := time.Now().UTC()
		if errors.Is(e, gorm.ErrRecordNotFound) {
			receipt := uuid.NewString()
			payload := Payload{Version: 1, ReceiptID: receipt, ServerID: s.ServerID, UserID: id, ActorUserID: actor, IMProvisioned: true, Accounts: accounts}
			data, e := json.Marshal(payload)
			if e != nil {
				return e
			}
			job = Job{ReceiptID: receipt, UserID: id, ActorID: actor, Source: "legacy_recovery", Payload: string(data), PayloadHash: Hash(data), RouteRequired: s.RouteEnabled, State: "pending", IMProvisionedAt: &now, DefaultsCompletedAt: &now, NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
			if e = tx.Create(&job).Error; e != nil {
				return e
			}
		} else if !job.RouteRequired && job.State == "direct_only" && job.RouteBoundAt == nil {
			// A direct-only deployment never sent this receipt to route. An
			// explicit, newly audited superadmin recovery may now authorize its
			// central binding; it must not happen merely by toggling config.
			payload := Payload{Version: 1, ReceiptID: job.ReceiptID, ServerID: s.ServerID, UserID: id, ActorUserID: actor, IMProvisioned: true, Accounts: accounts}
			data, e := json.Marshal(payload)
			if e != nil {
				return e
			}
			job.ActorID = actor
			job.Payload = string(data)
			job.PayloadHash = Hash(data)
			job.RouteRequired = true
			job.State = "pending"
			job.Reason = ""
			job.NextAttemptAt = now
			job.UpdatedAt = now
			if e = tx.Save(&job).Error; e != nil {
				return e
			}
		}
		audit := RecoveryAudit{ID: uuid.NewString(), ActorID: actor, UserID: id, ReceiptID: job.ReceiptID, OriginalCreatorUnknown: job.OriginalCreatorID == "", Reason: reason, SnapshotHash: job.PayloadHash, IMCreateTime: existing.CreateTime, CreatedAt: now}
		if e = tx.Create(&audit).Error; e != nil {
			return e
		}
		result = statusFor(job)
		return nil
	})
	return result, e
}
func (s *Service) prepareDefaults(ctx context.Context, id string) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job Job
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("receipt_id=?", id).Take(&job).Error; e != nil {
			return e
		}
		if terminal(job.State) || job.DefaultsCompletedAt != nil || job.DefaultsPlan != nil {
			return nil
		}
		plan, e := s.IM.PlanDefaults(ctx, job.DefaultsAll)
		if e != nil {
			return e
		}
		data, e := json.Marshal(plan)
		if e != nil {
			return e
		}
		return tx.Model(&Job{}).Where("receipt_id=?", id).Update("defaults_plan", string(data)).Error
	})
}
func (s *Service) Process(ctx context.Context, id string) error {
	if s.IM == nil {
		return errors.New("IM provisioning adapter unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if e := s.prepareDefaults(ctx, id); e != nil {
		return s.failed(ctx, id, "defaults_unconfirmed", e)
	}
	e := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job Job
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("receipt_id=?", id).Take(&job).Error; e != nil {
			return e
		}
		if terminal(job.State) {
			return nil
		}
		now := time.Now().UTC()
		save := func(state, reason string) error {
			if job.Source == publicSource && (state == "direct_only" || state == "bound") {
				if job.IMProvisionedAt == nil || job.DefaultsCompletedAt == nil {
					return errors.New("public ownership delivery incomplete")
				}
				result := tx.Model(&registration.Receipt{}).Where("registration_id=? AND user_id=? AND managed_by_outbox=?", id, job.UserID, true).Update("im_provisioned_at", job.IMProvisionedAt)
				if result.Error != nil {
					return result.Error
				}
				// publicReceipt already locked and verified the row. A later
				// audited direct→route promotion may write the same timestamp;
				// MySQL reports zero changed rows for that idempotent update.
			}
			return tx.Model(&Job{}).Where("receipt_id=?", id).Updates(map[string]any{"state": state, "reason": reason, "im_provisioned_at": job.IMProvisionedAt, "defaults_completed_at": job.DefaultsCompletedAt, "route_bound_at": job.RouteBoundAt, "attempts": job.Attempts + 1, "next_attempt_at": now.Add(retryDelay(job.Attempts)), "updated_at": now}).Error
		}
		if Hash([]byte(job.Payload)) != job.PayloadHash {
			return save("conflict", "payload_integrity")
		}
		var payload Payload
		if e := json.Unmarshal([]byte(job.Payload), &payload); e != nil || payload.ReceiptID != id || payload.UserID != job.UserID || payload.ActorUserID != job.ActorID || payload.Version != 1 || !payload.IMProvisioned || payload.ServerID != s.ServerID {
			return save("conflict", "payload_integrity")
		}
		if job.Source != "new_creation" && job.Source != "legacy_recovery" && job.Source != publicSource {
			return save("conflict", "unknown_creation_source")
		}
		attr, accounts, e := snapshot(ctx, tx, job.UserID)
		if e != nil {
			if errors.Is(e, errOwnerConflict) {
				return save("conflict", "identity_unconfirmed")
			}
			return e
		}
		current, _ := json.Marshal(accounts)
		recorded, _ := json.Marshal(payload.Accounts)
		if string(current) != string(recorded) {
			return save("conflict", "identity_changed")
		}
		purpose := AdminPurpose
		if job.Source == publicSource {
			purpose = PublicPurpose
			if e := publicReceipt(ctx, tx, job, attr.Email, attr.AreaCode, attr.PhoneNumber); e != nil {
				if errors.Is(e, errOwnerConflict) || errors.Is(e, gorm.ErrRecordNotFound) {
					return save("conflict", "public_receipt_unconfirmed")
				}
				return e
			}
		}
		im, e := s.IM.Find(ctx, job.UserID)
		if e != nil {
			return e
		}
		createsIM := job.Source == "new_creation" || job.Source == publicSource
		if im == nil && createsIM {
			createErr := s.IM.Create(ctx, User{ID: job.UserID, Nickname: attr.Nickname, FaceURL: attr.FaceURL, ProvisioningID: id, ProvisioningPurpose: purpose, CreateTime: job.CreatedAt.UnixMilli()})
			im, e = s.IM.Find(ctx, job.UserID)
			if e != nil {
				return e
			}
			if im == nil {
				if createErr != nil {
					return createErr
				}
				return errors.New("IM creation not confirmed")
			}
		}
		if im == nil || im.ID != job.UserID || (createsIM && (im.ProvisioningID != id || im.ProvisioningPurpose != purpose)) {
			return save("conflict", "im_owner_unconfirmed")
		}
		if job.IMProvisionedAt == nil {
			job.IMProvisionedAt = &now
		}
		if job.DefaultsCompletedAt == nil {
			var plan Defaults
			if job.DefaultsPlan == nil {
				return errors.New("default plan unavailable")
			}
			if e = json.Unmarshal([]byte(*job.DefaultsPlan), &plan); e != nil {
				return e
			}
			if e = s.IM.ApplyDefaults(ctx, job.UserID, plan); e != nil {
				return e
			}
			job.DefaultsCompletedAt = &now
		}
		if !job.RouteRequired {
			return save("direct_only", "")
		}
		if !s.RouteEnabled || s.Route == nil {
			return errors.New("route provisioning is not configured")
		}
		route, e := s.Route.Query(ctx, id)
		if e != nil {
			return e
		}
		if route.Found {
			if e = checkResult(route, payload); e != nil {
				return e
			}
			if route.BindingState == "conflict" {
				return save("conflict", route.Reason)
			}
			if route.BindingState == "bound" {
				job.RouteBoundAt = &now
				return save("bound", "")
			}
		}
		route, e = s.Route.Commit(ctx, []byte(job.Payload))
		if e != nil {
			return e
		}
		if e = checkResult(route, payload); e != nil {
			return e
		}
		switch route.BindingState {
		case "bound":
			job.RouteBoundAt = &now
			return save("bound", "")
		case "conflict":
			return save("conflict", route.Reason)
		case "pending":
			return save("pending", route.Reason)
		}
		return errors.New("unknown provisioning state")
	})
	if e != nil {
		return s.failed(ctx, id, "delivery_unconfirmed", e)
	}
	return nil
}
func checkResult(r *RouteResult, p Payload) error {
	if r == nil || !r.Found || r.ReceiptID != p.ReceiptID || r.ServerID != p.ServerID || r.UserID != p.UserID || (r.BindingState != "bound" && r.BindingState != "pending" && r.BindingState != "conflict") || r.Retryable != (r.BindingState == "pending") || len(r.Reason) > 32 {
		return errors.New("provisioning receipt mismatch")
	}
	return nil
}
func (s *Service) failed(ctx context.Context, id, reason string, cause error) error {
	// A failed attempt never erases its durable intent or changes a terminal state.
	// Use a fresh short context if the delivery deadline itself has expired.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	e := s.DB.WithContext(recordCtx).Model(&Job{}).Where("receipt_id=? AND state NOT IN ?", id, []string{"bound", "direct_only", "conflict"}).Updates(map[string]any{"reason": reason, "attempts": gorm.Expr("attempts+1"), "next_attempt_at": time.Now().UTC().Add(time.Minute), "updated_at": time.Now().UTC()}).Error
	if e != nil {
		return fmt.Errorf("provisioning delivery and retry persistence failed")
	}
	return cause
}
func (s *Service) Reconcile(ctx context.Context) error {
	var jobs []Job
	if e := s.DB.WithContext(ctx).Select("receipt_id").Where("state NOT IN ? AND next_attempt_at<=?", []string{"bound", "direct_only", "conflict"}, time.Now()).Order("next_attempt_at,receipt_id").Limit(25).Find(&jobs).Error; e != nil {
		return e
	}
	var first error
	for _, job := range jobs {
		if e := s.Process(ctx, job.ReceiptID); e != nil && first == nil {
			first = e
		}
	}
	return first
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = s.Reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
