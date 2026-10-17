package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	drivermysql "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"openim-route-service/db"
	"openim-route-service/model"
)

// Opt-in integration runs only against the explicitly configured Unix-socket
// test instance. Each test creates and removes its own synthetic-data database.
func isolatedRegistrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LUOBO_ROUTE_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("set LUOBO_ROUTE_TEST_MYSQL_DSN to isolated Unix socket")
	}
	cfg, err := drivermysql.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" || !strings.HasSuffix(cfg.Addr, "/references/integration/mysql8.4/mysql.sock") {
		t.Fatal("integration test accepts only the named isolated Unix-socket instance")
	}
	cfg.DBName = ""
	conn, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	id, _ := registrationID()
	name := "luobo_route_test_" + strings.ReplaceAll(id, "-", "")
	if _, err := conn.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		t.Fatal(err)
	}
	cfg.DBName = name
	cfg.ParseTime = true
	g, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	old := db.DB
	db.DB = g
	t.Cleanup(func() {
		db.DB = old
		sqlDB, _ := g.DB()
		sqlDB.Close()
		cleanup, _ := sql.Open("mysql", strings.Replace(cfg.FormatDSN(), "/"+name+"?", "/?", 1))
		if cleanup != nil {
			cleanup.Exec("DROP DATABASE `" + name + "`")
			cleanup.Close()
		}
	})
	if err := g.AutoMigrate(&model.ServerConfig{}, &model.InviteCode{}, &model.UserServerBinding{}, &model.RegistrationIntent{}, &model.AccountRouteLock{}, &model.AdminProvisionReceipt{}, &model.AdminProvisionOwner{}, &model.AdminProvisionNonce{}); err != nil {
		t.Fatal(err)
	}
	return g
}
func TestMySQLRegistrationReservationCapacityAndRecovery(t *testing.T) {
	g := isolatedRegistrationDB(t)
	ctx := context.Background()
	if err := g.Create(&model.ServerConfig{ServerID: "a", ServerName: "test", Domains: "tenant.example", Status: 1, IsDefault: 1, MaxUsers: 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := g.Create(&model.InviteCode{Code: "invite", ServerID: "a", Status: 1, MaxUseCount: 1}).Error; err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var reserved []*RegistrationAttempt
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			key := "phone:+86:123456" + string(rune('0'+n))
			a, err := (&SQLRegistrationStore{}).Prepare(ctx, key, "invite")
			if err == nil {
				mu.Lock()
				reserved = append(reserved, a)
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if len(reserved) != 1 {
		t.Fatalf("capacity reserved %d times", len(reserved))
	}
	var server model.ServerConfig
	var invite model.InviteCode
	g.First(&server)
	g.First(&invite)
	if server.CurrentUsers != 1 || invite.UsedCount != 1 {
		t.Fatal("reservation counters drifted")
	}
	a := reserved[0]
	freshProcess := &SQLRegistrationStore{}
	if _, err := freshProcess.Prepare(ctx, a.Intent.AccountKey, "other"); err == nil {
		t.Fatal("intent moved to a different invitation")
	}
	if err := freshProcess.Confirm(ctx, a.Intent.RegistrationID, "synthetic-user"); err != nil {
		t.Fatal(err)
	}
	// Confirmed registrations can recover immediately after route restart.
	retry, err := freshProcess.Prepare(ctx, a.Intent.AccountKey, "invite")
	if err != nil || retry.Fresh {
		t.Fatalf("confirmed intent was not resumed: %v", err)
	}
	if err := freshProcess.Complete(ctx, a.Intent.RegistrationID); err != nil {
		t.Fatal(err)
	}
	if err := freshProcess.Complete(ctx, a.Intent.RegistrationID); err != nil {
		t.Fatal(err)
	}
	var count int64
	g.Model(&model.UserServerBinding{}).Count(&count)
	g.First(&server)
	g.First(&invite)
	if count != 1 || server.CurrentUsers != 1 || invite.UsedCount != 1 {
		t.Fatal("repeat completion changed binding or counters")
	}
}
func TestMySQLRejectedRegistrationReleasesQuotaAtomically(t *testing.T) {
	g := isolatedRegistrationDB(t)
	ctx := context.Background()
	g.Create(&model.ServerConfig{ServerID: "a", Domains: "tenant.example", Status: 1, IsDefault: 1, MaxUsers: 1})
	g.Create(&model.InviteCode{Code: "invite", ServerID: "a", Status: 1, MaxUseCount: 1})
	store := &SQLRegistrationStore{}
	a, err := store.Prepare(ctx, "phone:+86:12345678", "invite")
	if err != nil {
		t.Fatal(err)
	}
	if err := NewRouteService().BindUserToServer(a.Intent.AccountKey, "a", "invite"); err == nil {
		t.Fatal("manual bind bypassed pending reservation")
	}
	if err := store.Abort(ctx, a.Intent.RegistrationID); err != nil {
		t.Fatal(err)
	}
	var server model.ServerConfig
	var invite model.InviteCode
	var intents int64
	g.First(&server)
	g.First(&invite)
	g.Model(&model.RegistrationIntent{}).Count(&intents)
	if server.CurrentUsers != 0 || invite.UsedCount != 0 || intents != 0 {
		t.Fatal("aborted reservation retained quota")
	}
	if _, err := store.Prepare(ctx, "phone:+86:87654321", "invite"); err != nil {
		t.Fatal("released quota could not be reused", err)
	}
}
func TestMySQLBindingConflictKeepsConfirmedIntentRecoverable(t *testing.T) {
	g := isolatedRegistrationDB(t)
	ctx := context.Background()
	g.Create(&model.ServerConfig{ServerID: "a", Domains: "tenant.example", Status: 1, IsDefault: 1})
	store := &SQLRegistrationStore{}
	a, err := store.Prepare(ctx, "phone:+86:12345678", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Confirm(ctx, a.Intent.RegistrationID, "synthetic-user"); err != nil {
		t.Fatal(err)
	}
	g.Create(&model.UserServerBinding{UserID: a.Intent.AccountKey, ServerID: "different"})
	if err := store.Complete(ctx, a.Intent.RegistrationID); err == nil {
		t.Fatal("conflicting binding was overwritten")
	}
	var intent model.RegistrationIntent
	g.First(&intent)
	if intent.State != "confirmed" || intent.UpstreamUserID != "synthetic-user" {
		t.Fatal("binding rollback discarded confirmation")
	}
}

func TestMySQLServerFailureKeepsDurableIntentAndRecoversWithoutRegisteringAgain(t *testing.T) {
	for _, initiallyCommitted := range []bool{false, true} {
		t.Run(fmt.Sprint(initiallyCommitted), func(t *testing.T) {
			g := isolatedRegistrationDB(t)
			ctx := context.Background()
			if err := g.Create(&model.ServerConfig{ServerID: "a", Domains: "tenant.example", Status: 1, IsDefault: 1, MaxUsers: 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := g.Create(&model.InviteCode{Code: "invite", ServerID: "a", Status: 1, MaxUseCount: 1}).Error; err != nil {
				t.Fatal(err)
			}
			routes := NewRouteService()
			store := &SQLRegistrationStore{}
			first, err := store.Prepare(ctx, "phone:+86:12345678", "invite")
			if err != nil {
				t.Fatal(err)
			}
			// The test's first request uses the same real reservation as the service.
			prepared := &preparedRegistrationStore{SQLRegistrationStore: store, attempt: first}
			forward := &recoveryForward{result: map[string]interface{}{"errCode": float64(500)}, receipt: map[string]interface{}{"registrationID": first.Intent.RegistrationID, "normalizedIdentity": first.Intent.AccountKey, "registered": initiallyCommitted, "imProvisioned": false, "userID": "created-user"}}
			request := &RegisterRequest{InvitationCode: "invite", Platform: 2, User: map[string]interface{}{"phoneNumber": "12345678"}}
			result, err := (&RegisterService{routes, forward, prepared}).Register(ctx, request, "", "")
			if !errors.Is(err, ErrRegistrationPending) || result != nil {
				t.Fatal("uncertain registration was not pending")
			}
			var intent model.RegistrationIntent
			if err := g.Where("registration_id = ?", first.Intent.RegistrationID).First(&intent).Error; err != nil {
				t.Fatal("post-commit error deleted recovery ID", err)
			}
			var server model.ServerConfig
			var invite model.InviteCode
			g.First(&server)
			g.First(&invite)
			if intent.State != "reserved" || server.CurrentUsers != 1 || invite.UsedCount != 1 {
				t.Fatal("uncertain registration released reserved capacity")
			}
			// Simulate a later process retry after lease expiry. Only the signed receipt
			// is consulted; no password, OTP, token, or second registration is needed.
			if err := g.Model(&intent).Update("lease_until", time.Now().Add(-time.Minute)).Error; err != nil {
				t.Fatal(err)
			}
			forward.receipt["registered"], forward.receipt["imProvisioned"] = true, true
			result, err = (&RegisterService{NewRouteService(), forward, &SQLRegistrationStore{}}).Register(ctx, request, "", "")
			if !errors.Is(err, ErrRegistrationRecovered) || result != nil || forward.registrations != 1 || forward.queries != 2 {
				t.Fatal("restart did not recover solely by original receipt")
			}
			var count int64
			g.Model(&model.UserServerBinding{}).Where("user_id = ?", first.Intent.AccountKey).Count(&count)
			g.First(&server)
			g.First(&invite)
			g.First(&intent)
			if count != 1 || intent.State != "bound" || server.CurrentUsers != 1 || invite.UsedCount != 1 {
				t.Fatal("recovery duplicated binding/counters or lost intent")
			}
		})
	}
}

type preparedRegistrationStore struct {
	*SQLRegistrationStore
	attempt *RegistrationAttempt
}

func (s *preparedRegistrationStore) Prepare(context.Context, string, string) (*RegistrationAttempt, error) {
	return s.attempt, nil
}
