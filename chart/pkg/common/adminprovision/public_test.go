package adminprovision_test

import (
	"context"
	"errors"
	"testing"
	"time"

	p "github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/passwordstore"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func publicCreate(t *testing.T, s *p.Service, store database.ChatDatabaseInterface, id, wire string, code ...uint) (*p.PublicCreation, error) {
	t.Helper()
	c := &p.PublicCreation{ReceiptID: id, ServerID: s.ServerID}
	now := time.Now().UTC()
	e := store.RegisterUser(p.WithPublicCreation(context.Background(), c), &table.Register{UserID: "public-owner", Mode: "user", CreateTime: now}, &table.Account{UserID: "public-owner", Password: wire, OperatorUserID: "public-owner", CreateTime: now, ChangeTime: now}, &table.Attribute{UserID: "public-owner", Email: "public@fixture.invalid", Nickname: "Public", CreateTime: now, ChangeTime: now, BirthTime: now}, code...)
	return c, e
}

func TestPublicDirectAndSignedRouteShareAtomicDurableOwnership(t *testing.T) {
	for _, route := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary direct", true: "signed route"}[route], func(t *testing.T) {
			s, store, im, upstream := fixture(t)
			id := uuid.NewString()
			ctx := context.Background()
			if route {
				ctx = registration.WithID(ctx, id)
			}
			generated := p.NewPublicCreation(ctx, s.ServerID)
			if route && generated.ReceiptID != id {
				t.Fatal("trusted route receipt ID was not retained")
			}
			if !route && (generated.ReceiptID == id || !registration.ValidID(generated.ReceiptID)) {
				t.Fatal("direct receipt is not independently generated")
			}
			id = generated.ReceiptID
			code := &table.VerifyCode{Account: "public@fixture.invalid", Platform: "email", Code: "123456", UsedFor: 1}
			if e := s.DB.Create(code).Error; e != nil {
				t.Fatal(e)
			}
			c, e := publicCreate(t, s, store, id, "", code.ID)
			if e != nil {
				t.Fatal(e)
			}
			var receipt registration.Receipt
			if e := s.DB.First(&receipt).Error; e != nil {
				t.Fatal(e)
			}
			var job p.Job
			if e := s.DB.First(&job).Error; e != nil {
				t.Fatal(e)
			}
			if !receipt.ManagedByOutbox || receipt.RegistrationID != id || job.Source != "public_creation" || job.RouteRequired || job.ReceiptID != id || c.UserID != "public-owner" {
				t.Fatal("account and ownership receipts not atomic")
			}
			if e := s.DB.First(code, code.ID).Error; e != nil || !code.Used {
				t.Fatal("OTP not consumed atomically", e)
			}
			if s.RequireReady(context.Background(), c.UserID) == nil {
				t.Fatal("pending public account passed lifecycle gate")
			}
			if status, e := s.Lookup(context.Background(), c.UserID, "", c.ReceiptID, false); e != nil || status.Found {
				t.Fatal("same text adminID acquired ordinary business-principal receipt", e)
			}
			if status, e := s.PublicStatus(context.Background(), c); e != nil || !status.Found || status.State != "awaiting_im" {
				t.Fatal("server-owned public tuple cannot recover its receipt", e)
			}
			if e := s.Process(context.Background(), id); e != nil {
				t.Fatal(e)
			}
			if e := s.DB.First(&receipt).Error; e != nil || receipt.IMProvisionedAt == nil {
				t.Fatal("worker did not atomically confirm route receipt", e)
			}
			if s.RequireReady(context.Background(), c.UserID) != nil || im.users[c.UserID].ProvisioningPurpose != p.PublicPurpose || im.users[c.UserID].ProvisioningID != id {
				t.Fatal("public ownership not confirmed by the correct marker")
			}
			if im.creates != 1 || upstream.commits != 0 {
				t.Fatal("public worker stole route reservation or recreated IM")
			}
			if e := s.Process(context.Background(), id); e != nil || im.creates != 1 {
				t.Fatal("public delivery not idempotent", e)
			}
			// A separately authorized super recovery may promote direct-only to a
			// central binding without changing the original IM timestamp/marker.
			if _, e := s.Recover(context.Background(), "authorized-super", c.UserID, "synthetic explicit central binding"); e != nil {
				t.Fatal(e)
			}
			if e := s.Process(context.Background(), id); e != nil || upstream.commits != 1 {
				t.Fatal("idempotent timestamp blocked audited promotion", e)
			}
		})
	}
}

func TestPublicAccountReceiptJobAndOTPAllRollBackOnOutboxFailure(t *testing.T) {
	s, store, _, _ := fixture(t)
	code := &table.VerifyCode{Account: "public@fixture.invalid", Code: "123456", UsedFor: 1}
	if e := s.DB.Create(code).Error; e != nil {
		t.Fatal(e)
	}
	s.DB.Callback().Create().Before("gorm:create").Register("synthetic-public-job-failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "admin_provision_jobs" {
			tx.AddError(errors.New("synthetic outbox failure"))
		}
	})
	if _, e := publicCreate(t, s, store, uuid.NewString(), "", code.ID); e == nil {
		t.Fatal("outbox fault accepted")
	}
	for _, model := range []any{&table.Account{}, &table.Register{}, &table.Attribute{}, &registration.Receipt{}, &p.Job{}} {
		var count int64
		if e := s.DB.Model(model).Count(&count).Error; e != nil || count != 0 {
			t.Fatal("partial public registration committed", e)
		}
	}
	if e := s.DB.First(code, code.ID).Error; e != nil || code.Used {
		t.Fatal("failed registration consumed OTP", e)
	}
}

type lostPublicACK struct {
	*imFixture
	loseRead bool
}

func (i *lostPublicACK) Create(ctx context.Context, u p.User) error {
	e := i.imFixture.Create(ctx, u)
	i.loseRead = true
	return e
}
func (i *lostPublicACK) Find(ctx context.Context, id string) (*p.User, error) {
	if i.loseRead {
		i.loseRead = false
		return nil, errors.New("synthetic lost create confirmation")
	}
	return i.imFixture.Find(ctx, id)
}

func TestPublicUnknownACKRecoversFromSQLWithoutClientReceivingIDOrPasswordReplay(t *testing.T) {
	s, store, im, _ := fixture(t)
	const wire = "0123456789abcdef0123456789abcdef"
	c, e := publicCreate(t, s, store, uuid.NewString(), wire)
	if e != nil {
		t.Fatal(e)
	}
	lost := &lostPublicACK{imFixture: im}
	s.IM = lost
	if s.Process(context.Background(), c.ReceiptID) == nil {
		t.Fatal("lost confirmation did not remain pending")
	}
	if s.RequireReady(context.Background(), c.UserID) == nil {
		t.Fatal("uncertain public ownership issued a credential")
	}
	var account table.Account
	if e := s.DB.First(&account).Error; e != nil {
		t.Fatal(e)
	}
	before := account.Password
	// New service instance resumes solely by durable SQL;no client ID,OTP or
	// password is passed to the worker and it never recreates the IM account.
	restarted := *s
	if e := restarted.Process(context.Background(), c.ReceiptID); e != nil {
		t.Fatal(e)
	}
	if im.creates != 1 || restarted.RequireReady(context.Background(), c.UserID) != nil {
		t.Fatal("restart re-created owner or left a dead-end gate")
	}
	if e := s.DB.First(&account).Error; e != nil || account.Password != before {
		t.Fatal("worker changed original password", e)
	}
	if ok, _, e := passwordstore.Verify(context.Background(), account.Password, wire); e != nil || !ok {
		t.Fatal("original password cannot recover login", e)
	}
}

func TestPublicMarkerNamespaceAndReceiptIntegrityFailClosed(t *testing.T) {
	for _, reason := range []string{"wrong marker namespace", "missing atomic receipt", "unknown future source"} {
		t.Run(reason, func(t *testing.T) {
			s, store, im, _ := fixture(t)
			c, e := publicCreate(t, s, store, uuid.NewString(), "")
			if e != nil {
				t.Fatal(e)
			}
			if reason == "wrong marker namespace" {
				im.users[c.UserID] = p.User{ID: c.UserID, ProvisioningID: c.ReceiptID, ProvisioningPurpose: p.AdminPurpose}
			} else if reason == "missing atomic receipt" {
				if e := s.DB.Where("registration_id=?", c.ReceiptID).Delete(&registration.Receipt{}).Error; e != nil {
					t.Fatal(e)
				}
			} else {
				im.users[c.UserID] = p.User{ID: c.UserID}
				if e := s.DB.Model(&p.Job{}).Where("receipt_id=?", c.ReceiptID).Update("source", "future_unknown").Error; e != nil {
					t.Fatal(e)
				}
			}
			if e := s.Process(context.Background(), c.ReceiptID); e != nil {
				t.Fatal(e)
			}
			var job p.Job
			s.DB.First(&job)
			if job.State != "conflict" || job.IMProvisionedAt != nil || im.creates != 0 || s.RequireReady(context.Background(), c.UserID) == nil {
				t.Fatal("unrelated public owner accepted")
			}
		})
	}
}
