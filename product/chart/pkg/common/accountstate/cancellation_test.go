package accountstate

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"
)

type memStore struct {
	records  map[string]*Cancellation
	failSave bool
}

func (m *memStore) Request(_ context.Context, id, actor string) (*Cancellation, error) {
	if m.failSave {
		return nil, errors.New("database unavailable")
	}
	if r := m.records[id]; r != nil {
		return r, nil
	}
	r := &Cancellation{UserID: id, RequestedBy: actor, RequestedAt: time.Now()}
	m.records[id] = r
	return r, nil
}
func (m *memStore) Complete(_ context.Context, id string, at time.Time) error {
	m.records[id].SessionsRevokedAt = &at
	return nil
}
func (m *memStore) ListAfter(_ context.Context, after string, limit int) ([]*Cancellation, error) {
	keys := []string{}
	for id := range m.records {
		if id > after {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := []*Cancellation{}
	for _, id := range keys {
		result = append(result, m.records[id])
	}
	return result, nil
}

type memSessions struct {
	store    *memStore
	disabled map[string]bool
	revoked  map[string]int
	failID   string
}

func (m *memSessions) Disable(_ context.Context, id string) error {
	if m.store.records[id] == nil {
		panic("side effect before durable intent")
	}
	m.disabled[id] = true
	return nil
}
func (m *memSessions) Revoke(_ context.Context, id string) error {
	if !m.disabled[id] {
		panic("revocation before disabling login")
	}
	if id == m.failID {
		return errors.New("IM temporarily unavailable")
	}
	m.revoked[id]++
	return nil
}
func cancellationFixture() (*Service, *memStore, *memSessions) {
	db := &memStore{records: map[string]*Cancellation{}}
	sessions := &memSessions{store: db, disabled: map[string]bool{}, revoked: map[string]int{}}
	return &Service{Store: db, Sessions: sessions}, db, sessions
}

func TestCancellationIsDurableAndIdempotent(t *testing.T) {
	s, db, sessions := cancellationFixture()
	ctx := context.Background()
	r, err := s.Request(ctx, "u1", "u1")
	if err != nil || r.SessionsRevokedAt == nil {
		t.Fatalf("cancellation failed: %v", err)
	}
	requested := r.RequestedAt
	if _, err := s.Request(ctx, "u1", "another-admin"); err != nil {
		t.Fatal(err)
	}
	if len(db.records) != 1 || sessions.revoked["u1"] != 1 || db.records["u1"].RequestedAt != requested || db.records["u1"].RequestedBy != "u1" {
		t.Fatal("repeated request changed ownership, timestamps, or repeated completed cleanup")
	}
}

func TestPartialFailureIsRetriedWithoutStarvingOtherAccounts(t *testing.T) {
	s, _, sessions := cancellationFixture()
	ctx := context.Background()
	sessions.failID = "u1"
	r, err := s.Request(ctx, "u1", "u1")
	if r == nil || err == nil || r.SessionsRevokedAt != nil {
		t.Fatal("partial failure must remain pending")
	}
	_, _ = s.Store.Request(ctx, "u2", "u2")
	if err := s.Reconcile(ctx); err == nil {
		t.Fatal("failed cleanup was hidden")
	}
	if sessions.revoked["u2"] != 1 {
		t.Fatal("one failed account blocked all other accounts")
	}
	sessions.failID = ""
	if err := s.Reconcile(ctx); err != nil || r.SessionsRevokedAt == nil {
		t.Fatalf("retry failed: %v", err)
	}
	// Redis restart must restore the disabled gate without re-erasing data.
	sessions.disabled = map[string]bool{}
	if err := s.Reconcile(ctx); err != nil || !sessions.disabled["u1"] || sessions.revoked["u1"] != 1 {
		t.Fatal("durable login gate was not restored")
	}
}

func TestNoSideEffectWhenPersistenceFails(t *testing.T) {
	s, db, sessions := cancellationFixture()
	db.failSave = true
	if r, err := s.Request(context.Background(), "u1", "u1"); r != nil || err == nil {
		t.Fatal("failed request accepted")
	}
	if len(sessions.disabled) != 0 || len(sessions.revoked) != 0 {
		t.Fatal("side effect without durable intent")
	}
}
