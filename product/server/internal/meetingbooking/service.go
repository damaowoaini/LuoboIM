package meetingbooking

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

type Service struct {
	Store  Store
	Access Access
	Now    func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC().Truncate(time.Millisecond)
	}
	return time.Now().UTC().Truncate(time.Millisecond)
}
func validID(v string) bool {
	return v != "" && len(v) <= 128 && utf8.ValidString(v) && strings.TrimSpace(v) == v && !strings.ContainsFunc(v, unicode.IsControl)
}
func validUUID(v string) bool {
	id, err := uuid.Parse(v)
	return err == nil && id.Version() == 4 && id.String() == v
}
func validPrincipal(p Principal) bool { return validID(p.Tenant) && validID(p.Owner) }
func canonical(p Principal, payload Payload) (Payload, error) {
	payload.MeetingName = strings.TrimSpace(payload.MeetingName)
	if !validPrincipal(p) || payload.MeetingName == "" || len(payload.MeetingName) > 256 || !utf8.ValidString(payload.MeetingName) || strings.ContainsFunc(payload.MeetingName, unicode.IsControl) || len(payload.Ex) > 8192 || !utf8.ValidString(payload.Ex) || payload.MeetingDuration < 60 || payload.MeetingDuration > 604800 || len(payload.InviteeUserIDs) > 100 {
		return Payload{}, ErrInvalid
	}
	set := map[string]bool{}
	ids := []string{}
	for _, id := range payload.InviteeUserIDs {
		if !validID(id) || len(id) > 64 || id == p.Owner || set[id] {
			return Payload{}, ErrInvalid
		}
		set[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)
	payload.InviteeUserIDs = ids
	return payload, nil
}
func validStart(payload Payload, now time.Time) bool {
	return payload.StartTime >= now.Add(-time.Hour).Unix() && payload.StartTime <= now.AddDate(1, 0, 0).Unix()
}
func hashRequest(action, id string, version int64, payload *Payload) string {
	data, _ := json.Marshal(struct {
		Action, Booking string
		Version         int64
		Payload         *Payload
	}{action, id, version, payload})
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}
func public(row *Booking) *PublicBooking {
	return &PublicBooking{BookingID: row.ID, HostUserID: row.Owner, State: row.State, Version: row.Version, Payload: row.Payload, CreatedAt: row.CreatedAt.UnixMilli(), UpdatedAt: row.UpdatedAt.UnixMilli()}
}
func result(ctx context.Context, tx Tx, intent *Intent) (*Result, error) {
	if !storedIntentValid(intent, Principal{Tenant: intent.Tenant, Owner: intent.Owner}, intent.RequestID) {
		return nil, malformed
	}
	r := &Result{Found: true, ClientRequestID: intent.RequestID, Action: intent.Action, Applied: !intent.Resolved, AppliedVersion: intent.AppliedVersion, DefinitiveNotApplied: intent.Resolved, ReplaySafe: true}
	if intent.BookingID != "" {
		row, err := tx.GetBooking(ctx, intent.BookingID)
		if err != nil {
			return nil, err
		}
		if !storedBookingValid(row, Principal{Tenant: intent.Tenant, Owner: intent.Owner}, intent.BookingID) || row.Version < intent.AppliedVersion || (intent.Action == "cancel" && row.State != "cancelled") {
			return nil, malformed
		}
		r.Booking = public(row) // Always CURRENT state, never replay an old scheduled snapshot after cancel.
	}
	return r, nil
}
func existing(ctx context.Context, tx Tx, id, hash string) (*Result, bool, error) {
	intent, err := tx.GetIntent(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if intent.Resolved || intent.Hash != hash {
		return nil, true, ErrConflict
	}
	r, err := result(ctx, tx, intent)
	return r, true, err
}
func (s *Service) probe(ctx context.Context, p Principal, id, hash string) (*Result, error) {
	return s.Store.Run(ctx, p, func(tx Tx) (*Result, error) { r, _, err := existing(ctx, tx, id, hash); return r, err })
}
func (s *Service) Create(ctx context.Context, p Principal, request CreateRequest) (*Result, error) {
	payload, err := canonical(p, request.Payload)
	if err != nil || !validUUID(request.ClientRequestID) {
		return nil, ErrInvalid
	}
	hash := hashRequest("create", "", 0, &payload)
	if old, err := s.probe(ctx, p, request.ClientRequestID, hash); err != nil || old != nil {
		return old, err
	}
	now := s.now()
	if !validStart(payload, now) {
		return nil, ErrInvalid
	}
	if s.Access == nil {
		return nil, ErrDenied
	}
	if err := s.Access.Check(ctx, p, payload.InviteeUserIDs); err != nil {
		return nil, err
	}
	id := uuid.NewString() // Stable throughout transaction retries; never public media room ID.
	return s.Store.Run(ctx, p, func(tx Tx) (*Result, error) {
		if r, found, err := existing(ctx, tx, request.ClientRequestID, hash); found || err != nil {
			return r, err
		}
		row := &Booking{ID: id, Tenant: p.Tenant, Owner: p.Owner, State: "scheduled", Version: 1, Payload: &payload, CreatedAt: now, UpdatedAt: now}
		if err := tx.InsertBooking(ctx, row); err != nil {
			return nil, err
		}
		intent := &Intent{Tenant: p.Tenant, Owner: p.Owner, RequestID: request.ClientRequestID, Hash: hash, Action: "create", BookingID: id, AppliedVersion: 1, CreatedAt: now}
		if err := tx.InsertIntent(ctx, intent); err != nil {
			return nil, err
		}
		return result(ctx, tx, intent)
	})
}
func (s *Service) Update(ctx context.Context, p Principal, request UpdateRequest) (*Result, error) {
	payload, err := canonical(p, request.Payload)
	if err != nil || !validUUID(request.ClientRequestID) || !validUUID(request.BookingID) || request.ExpectedVersion < 1 || request.ExpectedVersion >= 1000000000 {
		return nil, ErrInvalid
	}
	hash := hashRequest("update", request.BookingID, request.ExpectedVersion, &payload)
	if old, err := s.probe(ctx, p, request.ClientRequestID, hash); err != nil || old != nil {
		return old, err
	}
	now := s.now()
	if !validStart(payload, now) {
		return nil, ErrInvalid
	}
	// Verify ownership before any invite lookups; never use a foreign target as an oracle.
	if _, err := s.Detail(ctx, p, request.BookingID); err != nil {
		return nil, err
	}
	if s.Access == nil {
		return nil, ErrDenied
	}
	if err := s.Access.Check(ctx, p, payload.InviteeUserIDs); err != nil {
		return nil, err
	}
	return s.Store.Run(ctx, p, func(tx Tx) (*Result, error) {
		if r, found, err := existing(ctx, tx, request.ClientRequestID, hash); found || err != nil {
			return r, err
		}
		row, err := tx.GetBooking(ctx, request.BookingID)
		if err != nil {
			return nil, err
		}
		if row.State != "scheduled" || row.Version != request.ExpectedVersion {
			return nil, ErrConflict
		}
		row.Payload = &payload
		row.Version++
		if now.After(row.UpdatedAt) {
			row.UpdatedAt = now
		}
		if err := tx.ReplaceBooking(ctx, row, request.ExpectedVersion); err != nil {
			return nil, err
		}
		intent := &Intent{Tenant: p.Tenant, Owner: p.Owner, RequestID: request.ClientRequestID, Hash: hash, Action: "update", BookingID: row.ID, AppliedVersion: row.Version, CreatedAt: now}
		if err := tx.InsertIntent(ctx, intent); err != nil {
			return nil, err
		}
		return result(ctx, tx, intent)
	})
}
func (s *Service) Cancel(ctx context.Context, p Principal, request CancelRequest) (*Result, error) {
	if !validPrincipal(p) || !validUUID(request.ClientRequestID) || !validUUID(request.BookingID) || request.ExpectedVersion < 1 || request.ExpectedVersion >= 1000000000 {
		return nil, ErrInvalid
	}
	hash := hashRequest("cancel", request.BookingID, request.ExpectedVersion, nil)
	return s.Store.Run(ctx, p, func(tx Tx) (*Result, error) {
		if r, found, err := existing(ctx, tx, request.ClientRequestID, hash); found || err != nil {
			return r, err
		}
		row, err := tx.GetBooking(ctx, request.BookingID)
		if err != nil {
			return nil, err
		}
		if row.State != "cancelled" {
			if row.State != "scheduled" || row.Version != request.ExpectedVersion {
				return nil, ErrConflict
			}
			row.State = "cancelled"
			row.Payload = nil
			row.Version++
			if now := s.now(); now.After(row.UpdatedAt) {
				row.UpdatedAt = now
			}
			if err := tx.ReplaceBooking(ctx, row, request.ExpectedVersion); err != nil {
				return nil, err
			}
		}
		intent := &Intent{Tenant: p.Tenant, Owner: p.Owner, RequestID: request.ClientRequestID, Hash: hash, Action: "cancel", BookingID: row.ID, AppliedVersion: row.Version, CreatedAt: s.now()}
		if err := tx.InsertIntent(ctx, intent); err != nil {
			return nil, err
		}
		return result(ctx, tx, intent)
	})
}
func (s *Service) Receipt(ctx context.Context, p Principal, id string) (*Result, error) {
	if !validPrincipal(p) || !validUUID(id) {
		return nil, ErrInvalid
	}
	return s.Store.Run(ctx, p, func(tx Tx) (*Result, error) {
		intent, err := tx.GetIntent(ctx, id)
		if errors.Is(err, ErrNotFound) {
			return &Result{ClientRequestID: id, ReplaySafe: true}, nil
		}
		if err != nil {
			return nil, err
		}
		return result(ctx, tx, intent)
	})
}
func (s *Service) Resolve(ctx context.Context, p Principal, id string) (*Result, error) {
	if !validPrincipal(p) || !validUUID(id) {
		return nil, ErrInvalid
	}
	return s.Store.Run(ctx, p, func(tx Tx) (*Result, error) {
		intent, err := tx.GetIntent(ctx, id)
		if err == nil {
			return result(ctx, tx, intent)
		}
		if !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		intent = &Intent{Tenant: p.Tenant, Owner: p.Owner, RequestID: id, Action: "resolve", Resolved: true, CreatedAt: s.now()}
		if err := tx.InsertIntent(ctx, intent); err != nil {
			return nil, err
		}
		return result(ctx, tx, intent)
	})
}
func (s *Service) Detail(ctx context.Context, p Principal, id string) (*PublicBooking, error) {
	if !validPrincipal(p) || !validUUID(id) {
		return nil, ErrInvalid
	}
	r, err := s.Store.Run(ctx, p, func(tx Tx) (*Result, error) {
		row, err := tx.GetBooking(ctx, id)
		if err != nil {
			return nil, err
		}
		return &Result{Booking: public(row)}, nil
	})
	if err != nil {
		return nil, err
	}
	return r.Booking, nil
}

type cursor struct {
	Millis int64  `json:"t"`
	ID     string `json:"id"`
}

func (s *Service) List(ctx context.Context, p Principal, request ListRequest) (*ListResult, error) {
	if !validPrincipal(p) || request.PageSize < 1 || request.PageSize > 100 || len(request.Cursor) > 512 {
		return nil, ErrInvalid
	}
	query := ListQuery{Limit: request.PageSize, IncludeCancelled: request.IncludeCancelled}
	if request.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		var c cursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.Millis <= 0 || !validUUID(c.ID) {
			return nil, ErrInvalid
		}
		query.BeforeTime = time.UnixMilli(c.Millis)
		query.BeforeID = c.ID
	}
	rows, more, err := s.Store.List(ctx, p, query)
	if err != nil {
		return nil, err
	}
	response := &ListResult{Contract: "luobo-owner-booking", ContractVersion: 1, OwnerUserID: p.Owner, ReplaySafe: true, Bookings: []PublicBooking{}, HasMore: more}
	for i := range rows {
		response.Bookings = append(response.Bookings, *public(&rows[i]))
	}
	if more && len(rows) > 0 {
		last := rows[len(rows)-1]
		raw, _ := json.Marshal(cursor{Millis: last.CreatedAt.UnixMilli(), ID: last.ID})
		response.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return response, nil
}
