package accountstate

import (
	"context"
	"errors"
	"testing"
	"time"
)

type credentialStoreStub struct {
	job       CredentialRevocation
	completed bool
	race      bool
}

func (s *credentialStoreStub) Get(context.Context, string) (*CredentialRevocation, error) {
	copy := s.job
	return &copy, nil
}
func (s *credentialStoreStub) ListAfter(context.Context, string, int) ([]*CredentialRevocation, error) {
	copy := s.job
	return []*CredentialRevocation{&copy}, nil
}
func (s *credentialStoreStub) Complete(_ context.Context, _ string, generation int64) (bool, error) {
	if s.race {
		s.job.Generation++
		return false, nil
	}
	if generation != s.job.Generation {
		return false, nil
	}
	at := time.Now()
	s.job.CompletedAt = &at
	s.completed = true
	return true, nil
}

type credentialSessionsStub struct {
	disabled, revoked, cleared bool
	fail                       bool
}

func (*credentialSessionsStub) Disable(context.Context, string) error { return nil }
func (s *credentialSessionsStub) Revoke(context.Context, string) error {
	if s.fail {
		return errors.New("IM unavailable")
	}
	s.revoked = true
	return nil
}
func (s *credentialSessionsStub) DisableForCredentialChange(context.Context, string, int64) error {
	s.disabled = true
	return nil
}
func (s *credentialSessionsStub) ClearCredentialGate(context.Context, string, int64) error {
	s.cleared = true
	return nil
}

func TestCredentialRevocationRetriesAndKeepsAccountGated(t *testing.T) {
	store := &credentialStoreStub{job: CredentialRevocation{UserID: "u1", Generation: 1}}
	sessions := &credentialSessionsStub{fail: true}
	s := &CredentialService{Store: store, Sessions: sessions}
	if err := s.ProcessUser(context.Background(), "u1"); err == nil || !sessions.disabled || sessions.cleared || store.completed {
		t.Fatal("failed cleanup must keep authentication gated")
	}
	sessions.fail = false
	if err := s.ProcessUser(context.Background(), "u1"); err != nil || !sessions.revoked || !sessions.cleared || !store.completed {
		t.Fatalf("retry did not revoke both sessions: %v", err)
	}
}

func TestCredentialRevocationOldWorkerCannotCompleteNewPasswordChange(t *testing.T) {
	store := &credentialStoreStub{job: CredentialRevocation{UserID: "u1", Generation: 1}, race: true}
	sessions := &credentialSessionsStub{}
	if err := (&CredentialService{Store: store, Sessions: sessions}).ProcessUser(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if sessions.cleared || store.completed {
		t.Fatal("old worker lifted authentication gate for a new password change")
	}
}

func TestCredentialRevocationRepairsCrashAfterSQLCompletion(t *testing.T) {
	at := time.Now()
	store := &credentialStoreStub{job: CredentialRevocation{UserID: "u1", Generation: 1, CompletedAt: &at}}
	sessions := &credentialSessionsStub{}
	if err := (&CredentialService{Store: store, Sessions: sessions}).ProcessUser(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if !sessions.cleared || sessions.revoked || sessions.disabled {
		t.Fatal("completed cleanup did not repair stale temporary gate")
	}
}
