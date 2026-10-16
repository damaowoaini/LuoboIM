package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"openim-route-service/db"
	"openim-route-service/model"
	"regexp"
	"sort"
	"strings"
	"time"
)

type AdminProvisionRequest struct {
	Version       int       `json:"version"`
	ReceiptID     string    `json:"receiptID"`
	ServerID      string    `json:"serverID"`
	UserID        string    `json:"userID"`
	ActorUserID   string    `json:"actorUserID"`
	IMProvisioned bool      `json:"imProvisioned"`
	Accounts      []Account `json:"accounts"`
}
type AdminProvisionResult struct {
	Found        bool   `json:"found"`
	ReceiptID    string `json:"receiptID,omitempty"`
	ServerID     string `json:"serverID,omitempty"`
	UserID       string `json:"userID,omitempty"`
	BindingState string `json:"bindingState,omitempty"`
	Retryable    bool   `json:"retryable"`
	Reason       string `json:"reason,omitempty"`
}

var provisionUUID = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var ErrProvisionConflict = errors.New("开户回执与原请求冲突")

func ValidAdminProvisionReceiptID(id string) bool { return provisionUUID.MatchString(id) }
func ValidateAdminProvision(req AdminProvisionRequest) error {
	req.Accounts = append([]Account(nil), req.Accounts...)
	_, _, err := normalizeProvision(req)
	return err
}

func lockAccountKeys(tx *gorm.DB, keys []string) error {
	for _, key := range keys {
		row := model.AccountRouteLock{AccountKey: key}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_key = ?", key).First(&row).Error; err != nil {
			return err
		}
	}
	return nil
}
func normalizeProvision(req AdminProvisionRequest) ([]string, string, error) {
	if req.Version != 1 || !provisionUUID.MatchString(req.ReceiptID) || req.ServerID == "" || len(req.ServerID) > 50 || req.UserID == "" || len(req.UserID) > 100 || req.ActorUserID == "" || len(req.ActorUserID) > 100 || !req.IMProvisioned || len(req.Accounts) < 1 || len(req.Accounts) > 2 {
		return nil, "", errors.New("开户回执参数无效")
	}
	keys := make([]string, 0, len(req.Accounts))
	kinds := map[string]bool{}
	for i := range req.Accounts {
		if err := req.Accounts[i].Normalize(); err != nil {
			return nil, "", err
		}
		kind := "phone"
		if req.Accounts[i].Email != "" {
			kind = "email"
		}
		if kinds[kind] {
			return nil, "", errors.New("开户回执包含重复身份类型")
		}
		kinds[kind] = true
		keys = append(keys, req.Accounts[i].Key())
	}
	sort.Strings(keys)
	body, _ := json.Marshal(struct {
		Version                       int
		ServerID, UserID, ActorUserID string
		IMProvisioned                 bool
		Keys                          []string
	}{req.Version, req.ServerID, req.UserID, req.ActorUserID, req.IMProvisioned, keys})
	sum := sha256.Sum256(body)
	return keys, hex.EncodeToString(sum[:]), nil
}
func provisionResult(r model.AdminProvisionReceipt) *AdminProvisionResult {
	return &AdminProvisionResult{Found: true, ReceiptID: r.ReceiptID, ServerID: r.ServerID, UserID: r.UserID, BindingState: r.State, Retryable: r.State == "pending", Reason: r.Reason}
}

func QueryAdminProvision(ctx context.Context, serverID, id string) (*AdminProvisionResult, error) {
	if !provisionUUID.MatchString(id) {
		return nil, errors.New("开户回执ID无效")
	}
	var row model.AdminProvisionReceipt
	err := db.DB.WithContext(ctx).Where("server_id = ? AND receipt_id = ?", serverID, id).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &AdminProvisionResult{Found: false}, nil
	}
	if err != nil {
		return nil, err
	}
	return provisionResult(row), nil
}

func CommitAdminProvision(ctx context.Context, req AdminProvisionRequest) (*AdminProvisionResult, error) {
	req.Accounts = append([]Account(nil), req.Accounts...)
	keys, fingerprint, err := normalizeProvision(req)
	if err != nil {
		return nil, err
	}
	var result *AdminProvisionResult
	err = db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var server model.ServerConfig
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("server_id = ?", req.ServerID).First(&server).Error; err != nil {
			return err
		}
		// Also lock the legacy country-less phone key to avoid claiming its tenant.
		locks := append([]string{}, keys...)
		for _, a := range req.Accounts {
			if a.PhoneNumber != "" {
				locks = append(locks, strings.TrimSpace(a.PhoneNumber))
			}
		}
		sort.Strings(locks)
		if err := lockAccountKeys(tx, locks); err != nil {
			return err
		}
		var receipt model.AdminProvisionReceipt
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("server_id = ? AND receipt_id = ?", req.ServerID, req.ReceiptID).First(&receipt).Error
		if e == nil {
			if receipt.Fingerprint != fingerprint {
				return ErrProvisionConflict
			}
			if receipt.State == "bound" {
				result = provisionResult(receipt)
				return nil
			}
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		} else {
			keyJSON, _ := json.Marshal(keys)
			receipt = model.AdminProvisionReceipt{ServerID: req.ServerID, ReceiptID: req.ReceiptID, UserID: req.UserID, ActorUserID: req.ActorUserID, Fingerprint: fingerprint, AccountKeys: string(keyJSON)}
		}
		saveState := func(state, reason string) error {
			receipt.State, receipt.Reason = state, reason
			if err := tx.Save(&receipt).Error; err != nil {
				return err
			}
			result = provisionResult(receipt)
			return nil
		}
		if server.Status != 1 {
			return saveState("pending", "tenant_unavailable")
		}
		var owner model.AdminProvisionOwner
		e = tx.Where("server_id = ? AND user_id = ?", req.ServerID, req.UserID).First(&owner).Error
		counted := e == nil
		if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		for _, key := range locks {
			var binding model.UserServerBinding
			e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", key).First(&binding).Error
			if e == nil {
				if binding.ServerID != req.ServerID {
					return saveState("conflict", "identity_belongs_to_other_tenant")
				}
				if binding.UpstreamUserID != "" && binding.UpstreamUserID != req.UserID {
					return saveState("conflict", "identity_belongs_to_other_owner")
				}
				if binding.UpstreamUserID == "" {
					return saveState("conflict", "legacy_owner_requires_reconciliation")
				}
			} else if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
		}
		for _, key := range keys {
			var intent model.RegistrationIntent
			e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_key = ?", key).First(&intent).Error
			if e == nil {
				if intent.ServerID != req.ServerID || intent.UpstreamUserID != "" && intent.UpstreamUserID != req.UserID {
					return saveState("conflict", "registration_owner_conflict")
				}
				if intent.State != "bound" {
					return saveState("pending", "existing_registration_pending")
				}
				if intent.UpstreamUserID != req.UserID {
					return saveState("conflict", "registration_owner_unknown")
				}
				counted = true
			} else if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
		}
		if !counted && server.MaxUsers > 0 && server.CurrentUsers >= server.MaxUsers {
			return saveState("pending", "capacity_unavailable")
		}
		for _, key := range keys {
			binding := model.UserServerBinding{UserID: key, ServerID: req.ServerID, UpstreamUserID: req.UserID, BindTime: time.Now()}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
				return err
			}
		}
		owner = model.AdminProvisionOwner{ServerID: req.ServerID, UserID: req.UserID}
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&owner).Error; err != nil {
			return err
		}
		if !counted {
			if err := tx.Model(&server).UpdateColumn("current_users", gorm.Expr("current_users + 1")).Error; err != nil {
				return err
			}
		}
		return saveState("bound", "")
	})
	return result, err
}
