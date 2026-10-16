package adminprovision_test

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	p "github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/profilesync"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"testing"
	"time"
)

type pendingCredentialStore struct {
	accountstate.CredentialStore
	id string
}

func (s pendingCredentialStore) Get(context.Context, string) (*accountstate.CredentialRevocation, error) {
	return &accountstate.CredentialRevocation{UserID: s.id, Generation: 1}, nil
}
func TestPendingOwnershipBlocksEveryIMLifecycleWorkerAndLegacyStillWorks(t *testing.T) {
	s, store, _, _ := fixture(t)
	c, e := create(t, s, store, "00000000-0000-4000-8000-000000000001")
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	guard := func(ctx context.Context, id string) error { return p.RequireReady(ctx, s.DB, id) }
	for _, state := range []string{"awaiting_im", "pending", "conflict"} {
		s.DB.Model(&p.Job{}).Where("receipt_id=?", c.ReceiptID).Update("state", state)
		if guard(ctx, c.UserID) == nil {
			t.Fatal("unfinished or conflicting account ready")
		}
		// Stores/session clients intentionally absent: no downstream side effect is
		// allowed before ownership,including reconciliation of an old queued job.
		cancel := &accountstate.Service{Ownership: guard}
		if row, e := cancel.Request(ctx, c.UserID, c.UserID); e == nil || row != nil {
			t.Fatal("cancellation crossed ownership boundary")
		}
		credentials := &accountstate.CredentialService{Ownership: guard, Store: pendingCredentialStore{id: c.UserID}}
		if e := credentials.ProcessUser(ctx, c.UserID); e == nil {
			t.Fatal("credential worker reached foreign IM")
		}
		session := &accountstate.SessionService{Ownership: guard}
		if row, e := session.Request(ctx, c.UserID, 10, "synthetic-chat", "synthetic-IM"); e == nil || row != nil {
			t.Fatal("logout request crossed ownership boundary")
		}
		if e := session.Process(ctx, &accountstate.SessionRevocation{UserID: c.UserID}); e == nil {
			t.Fatal("old exactlogout outbox reached foreign IM")
		}
		projection := &profilesync.Service{Ownership: guard}
		if e := projection.ProcessUser(ctx, c.UserID); e == nil {
			t.Fatal("profile worker reached foreign IM")
		}
	}
	for _, state := range []string{"bound", "direct_only"} {
		s.DB.Model(&p.Job{}).Where("receipt_id=?", c.ReceiptID).Updates(map[string]any{"state": state, "im_provisioned_at": nil})
		if guard(ctx, c.UserID) == nil {
			t.Fatal("ready label without IM proof accepted")
		}
		s.DB.Model(&p.Job{}).Where("receipt_id=?", c.ReceiptID).Updates(map[string]any{"im_provisioned_at": time.Now(), "route_bound_at": time.Now(), "route_required": state == "bound"})
		if e := guard(ctx, c.UserID); e != nil {
			t.Fatal("confirmed account blocked", e)
		}
	}
	if e := guard(ctx, "legacy-without-outbox"); e != nil {
		t.Fatal("legacy account lost compatibility", e)
	}
	receipt := registration.Receipt{RegistrationID: "00000000-0000-4000-8000-000000000002", NormalizedIdentity: "synthetic-identity", UserID: "public-route-pending", CreatedAt: time.Now()}
	if e := s.DB.Create(&receipt).Error; e != nil {
		t.Fatal(e)
	}
	if guard(ctx, receipt.UserID) == nil {
		t.Fatal("public route receipt minted credentials before IM ownership")
	}
	s.DB.Model(&registration.Receipt{}).Where("user_id=?", receipt.UserID).Update("im_provisioned_at", time.Now())
	if e := guard(ctx, receipt.UserID); e != nil {
		t.Fatal("confirmed public route registration blocked", e)
	}
	if e := s.DB.Migrator().DropTable(&p.Job{}); e != nil {
		t.Fatal(e)
	}
	if guard(ctx, "legacy-without-outbox") == nil {
		t.Fatal("ownership storage failure allowed credential issuance")
	}
}
