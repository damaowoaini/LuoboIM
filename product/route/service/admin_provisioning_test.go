package service

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"net/http"
	"openim-route-service/model"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func provisionFixture(server, user, phone, email string) AdminProvisionRequest {
	id, _ := registrationID()
	req := AdminProvisionRequest{Version: 1, ReceiptID: id, ServerID: server, UserID: user, ActorUserID: "synthetic-admin", IMProvisioned: true}
	if phone != "" {
		req.Accounts = append(req.Accounts, Account{AreaCode: "+86", PhoneNumber: phone})
	}
	if email != "" {
		req.Accounts = append(req.Accounts, Account{Email: email})
	}
	return req
}
func seedProvisionTenant(t *testing.T, g *gorm.DB, id string, current, maximum int) {
	t.Helper()
	if err := g.Create(&model.ServerConfig{ServerID: id, Domains: "tenant.example", Status: 1, IsDefault: 1, CurrentUsers: current, MaxUsers: maximum}).Error; err != nil {
		t.Fatal(err)
	}
}
func TestAdminProvisionSignatureTenantBodyPathAndTime(t *testing.T) {
	vectorBody := []byte(`{"version":1,"receiptID":"00000000-0000-4000-8000-000000000001","serverID":"tenant-a","userID":"synthetic-user","actorUserID":"synthetic-admin","imProvisioned":true,"accounts":[{"email":"person@fixture.example"}]}`)
	if AdminProvisionSignature("test-only-"+strings.Repeat("k", 40), "tenant-a", "1700000000", "0123456789abcdef0123456789abcdef", AdminProvisionCommitPath, vectorBody) != "28407abd8841f156ba117ea4e31fb085150f71dde329b5ac1524613d08066596" {
		t.Fatal("independent Python interoperability vector mismatch")
	}
	now := time.Now()
	secret := strings.Repeat("a", 32)
	body := []byte(`{"receiptID":"synthetic"}`)
	stamp := strconv.FormatInt(now.Unix(), 10)
	nonce := strings.Repeat("b", 32)
	valid := func() http.Header {
		h := http.Header{}
		h.Set("X-Luobo-Route-Server-ID", "a")
		h.Set("X-Luobo-Route-Timestamp", stamp)
		h.Set("X-Luobo-Route-Nonce", nonce)
		h.Set("X-Luobo-Route-Signature", AdminProvisionSignature(secret, "a", stamp, nonce, AdminProvisionCommitPath, body))
		return h
	}
	if id, err := VerifyAdminProvisionSignature(map[string]string{"a": secret}, valid(), AdminProvisionCommitPath, body, now); err != nil || id != "a" {
		t.Fatal("valid signature rejected")
	}
	for _, test := range []struct {
		name   string
		change func(http.Header)
		path   string
		body   []byte
		now    time.Time
	}{
		{"body", func(http.Header) {}, AdminProvisionCommitPath, []byte(`{"receiptID":"changed"}`), now},
		{"path", func(http.Header) {}, AdminProvisionQueryPath, body, now},
		{"expired", func(http.Header) {}, AdminProvisionCommitPath, body, now.Add(61 * time.Second)},
		{"future", func(http.Header) {}, AdminProvisionCommitPath, body, now.Add(-61 * time.Second)},
		{"tenant", func(h http.Header) { h.Set("X-Luobo-Route-Server-ID", "b") }, AdminProvisionCommitPath, body, now},
		{"nonce", func(h http.Header) { h.Set("X-Luobo-Route-Nonce", "bad") }, AdminProvisionCommitPath, body, now},
		{"unsigned", func(h http.Header) { h.Del("X-Luobo-Route-Signature") }, AdminProvisionCommitPath, body, now},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := valid()
			test.change(h)
			if _, err := VerifyAdminProvisionSignature(map[string]string{"a": secret, "b": strings.Repeat("c", 32)}, h, test.path, test.body, test.now); err == nil {
				t.Fatal("invalid signature accepted")
			}
		})
	}
	if _, err := VerifyAdminProvisionSignature(map[string]string{"*": secret}, valid(), AdminProvisionCommitPath, body, now); err == nil {
		t.Fatal("fallback secret authorized a tenant")
	}
}

func TestMySQLAdminProvisionAtomicIdentityPairAndIdempotence(t *testing.T) {
	g := isolatedRegistrationDB(t)
	seedProvisionTenant(t, g, "a", 0, 2)
	seedProvisionTenant(t, g, "b", 0, 2)
	req := provisionFixture("a", "user-a", "00011122233", "Account@Fixture.Example")
	var wg sync.WaitGroup
	results := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := CommitAdminProvision(context.Background(), req)
			if e == nil && r.BindingState != "bound" {
				e = errors.New("not bound")
			}
			results <- e
		}()
	}
	wg.Wait()
	close(results)
	for e := range results {
		if e != nil {
			t.Fatal(e)
		}
	}
	var server model.ServerConfig
	g.Where("server_id = ?", "a").First(&server)
	var bindings, owners, receipts int64
	g.Model(&model.UserServerBinding{}).Count(&bindings)
	g.Model(&model.AdminProvisionOwner{}).Count(&owners)
	g.Model(&model.AdminProvisionReceipt{}).Count(&receipts)
	if bindings != 2 || owners != 1 || receipts != 1 || server.CurrentUsers != 1 {
		t.Fatal("pair/counters not idempotent")
	}
	r, e := QueryAdminProvision(context.Background(), "a", req.ReceiptID)
	if e != nil || !r.Found || r.BindingState != "bound" {
		t.Fatal("lost ACK not queryable")
	}
	r, e = QueryAdminProvision(context.Background(), "b", req.ReceiptID)
	if e != nil || r.Found {
		t.Fatal("foreign tenant queried receipt")
	}
	changed := req
	changed.UserID = "other-owner"
	if _, e := CommitAdminProvision(context.Background(), changed); !errors.Is(e, ErrProvisionConflict) {
		t.Fatal("receipt payload replaced")
	}
	repeat := req
	repeat.ReceiptID, _ = registrationID()
	if _, e := CommitAdminProvision(context.Background(), repeat); e != nil {
		t.Fatal(e)
	}
	g.Where("server_id = ?", "a").First(&server)
	if server.CurrentUsers != 1 {
		t.Fatal("new event recounted owner")
	}
	foreign := provisionFixture("b", "user-b", "00011122233", "")
	r, e = CommitAdminProvision(context.Background(), foreign)
	if e != nil || r.BindingState != "conflict" {
		t.Fatal("tenant takeover accepted")
	}
	otherOwner := provisionFixture("a", "different-owner", "00011122233", "")
	r, e = CommitAdminProvision(context.Background(), otherOwner)
	if e != nil || r.BindingState != "conflict" || r.Reason != "identity_belongs_to_other_owner" {
		t.Fatal("same tenant identity reassigned to a different owner")
	}
}

func TestMySQLAdminProvisionConcurrentTenantReceiptsHaveOneOwner(t *testing.T) {
	g := isolatedRegistrationDB(t)
	seedProvisionTenant(t, g, "a", 0, 10)
	seedProvisionTenant(t, g, "b", 0, 10)
	start := make(chan struct{})
	errorsOut := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		tenant := "a"
		if i%2 == 1 {
			tenant = "b"
		}
		req := provisionFixture(tenant, "owner-"+tenant, "00088899900", "")
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			r, e := CommitAdminProvision(context.Background(), req)
			if e == nil && r.BindingState != "bound" && r.BindingState != "conflict" {
				e = errors.New("unexpected state")
			}
			errorsOut <- e
		}()
	}
	close(start)
	wg.Wait()
	close(errorsOut)
	for e := range errorsOut {
		if e != nil {
			t.Fatal(e)
		}
	}
	var bindings, owners int64
	g.Model(&model.UserServerBinding{}).Count(&bindings)
	g.Model(&model.AdminProvisionOwner{}).Count(&owners)
	var total int
	g.Model(&model.ServerConfig{}).Select("SUM(current_users)").Scan(&total)
	if bindings != 1 || owners != 1 || total != 1 {
		t.Fatal("concurrent tenants acquired or counted identity twice")
	}
}
func TestMySQLAdminProvisionConflictAndCapacityRecovery(t *testing.T) {
	g := isolatedRegistrationDB(t)
	seedProvisionTenant(t, g, "a", 0, 1)
	seedProvisionTenant(t, g, "b", 0, 1)
	legacy := "00022233344"
	g.Create(&model.UserServerBinding{UserID: legacy, ServerID: "b"})
	req := provisionFixture("a", "user-a", legacy, "new@fixture.example")
	r, e := CommitAdminProvision(context.Background(), req)
	if e != nil || r.BindingState != "conflict" {
		t.Fatal("legacy binding overwritten")
	}
	var count int64
	g.Model(&model.UserServerBinding{}).Where("server_id = ?", "a").Count(&count)
	if count != 0 {
		t.Fatal("partial identity pair committed")
	}
	g.Model(&model.ServerConfig{}).Where("server_id = ?", "a").UpdateColumn("current_users", 1)
	req = provisionFixture("a", "user-new", "00033344455", "")
	r, e = CommitAdminProvision(context.Background(), req)
	if e != nil || r.BindingState != "pending" || !r.Retryable {
		t.Fatal("capacity did not retain recoverable receipt")
	}
	g.Model(&model.ServerConfig{}).Where("server_id = ?", "a").UpdateColumn("max_users", 2)
	r, e = CommitAdminProvision(context.Background(), req)
	if e != nil || r.BindingState != "bound" {
		t.Fatal("capacity receipt not resumed")
	}
}
func TestMySQLAdminProvisionFailureRollsBackEveryEffect(t *testing.T) {
	g := isolatedRegistrationDB(t)
	seedProvisionTenant(t, g, "a", 0, 5)
	if err := g.Callback().Create().Before("gorm:create").Register("injected_owner_failure", func(tx *gorm.DB) {
		if tx.Statement.Table == "admin_provision_owner" {
			tx.AddError(errors.New("injected owner failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	req := provisionFixture("a", "user-a", "00044455566", "rollback@fixture.example")
	if _, err := CommitAdminProvision(context.Background(), req); err == nil {
		t.Fatal("write failure accepted")
	}
	var bindings int64
	g.Model(&model.UserServerBinding{}).Count(&bindings)
	if bindings != 0 {
		t.Fatal("partial binding survived failed transaction")
	}
	q, e := QueryAdminProvision(context.Background(), "a", req.ReceiptID)
	if e != nil || q.Found {
		t.Fatal("failed receipt looked committed")
	}
	g.Callback().Create().Remove("injected_owner_failure")
	if r, e := CommitAdminProvision(context.Background(), req); e != nil || r.BindingState != "bound" {
		t.Fatal("retry after rollback failed")
	}
}
func TestMySQLAdminProvisionNonceAndConcurrentRegistration(t *testing.T) {
	g := isolatedRegistrationDB(t)
	seedProvisionTenant(t, g, "a", 0, 5)
	seedProvisionTenant(t, g, "b", 0, 5)
	ctx := context.Background()
	now := time.Now()
	nonce := strings.Repeat("c", 32)
	if err := ConsumeAdminProvisionNonce(ctx, "a", nonce, now); err != nil {
		t.Fatal(err)
	}
	if err := ConsumeAdminProvisionNonce(ctx, "a", nonce, now); err == nil {
		t.Fatal("replayed nonce accepted after durable insert")
	}
	if err := ConsumeAdminProvisionNonce(ctx, "b", nonce, now); err != nil {
		t.Fatal("tenant nonce namespaces collided")
	}
	req := provisionFixture("b", "user-b", "00055566677", "")
	key := "phone:+86:00055566677"
	start := make(chan struct{})
	var registered *RegistrationAttempt
	var registerErr, errorCommit error
	var result *AdminProvisionResult
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		registered, registerErr = (&SQLRegistrationStore{}).Prepare(ctx, key, "")
	}()
	go func() { defer wg.Done(); <-start; result, errorCommit = CommitAdminProvision(ctx, req) }()
	close(start)
	wg.Wait()
	if errorCommit != nil {
		t.Fatal(errorCommit)
	}
	if registerErr == nil && registered != nil && result.BindingState == "bound" {
		t.Fatal("two tenants acquired same account")
	}
	if result.BindingState != "bound" && result.BindingState != "conflict" {
		t.Fatal("unexpected concurrent outcome")
	}
}

func TestMySQLAdminProvisionNonceCoversLastAcceptedUnixSecond(t *testing.T) {
	isolatedRegistrationDB(t)
	ctx := context.Background()
	start := time.Unix(1700000000, 0)
	secret, nonce := strings.Repeat("a", 32), strings.Repeat("b", 32)
	body := []byte(`{"receiptID":"synthetic"}`)
	header := http.Header{}
	header.Set("X-Luobo-Route-Server-ID", "a")
	header.Set("X-Luobo-Route-Timestamp", strconv.FormatInt(start.Unix()+60, 10))
	header.Set("X-Luobo-Route-Nonce", nonce)
	header.Set("X-Luobo-Route-Signature", AdminProvisionSignature(secret, "a", header.Get("X-Luobo-Route-Timestamp"), nonce, AdminProvisionCommitPath, body))
	if _, err := VerifyAdminProvisionSignature(map[string]string{"a": secret}, header, AdminProvisionCommitPath, body, start); err != nil {
		t.Fatal(err)
	}
	if err := ConsumeAdminProvisionNonce(ctx, "a", nonce, start); err != nil {
		t.Fatal(err)
	}
	lastAccepted := start.Add(120*time.Second + 900*time.Millisecond)
	if _, err := VerifyAdminProvisionSignature(map[string]string{"a": secret}, header, AdminProvisionCommitPath, body, lastAccepted); err != nil {
		t.Fatal("test must exercise still-valid last Unix second")
	}
	// A different valid request triggers expired-row cleanup before the replay.
	if err := ConsumeAdminProvisionNonce(ctx, "a", strings.Repeat("c", 32), lastAccepted); err != nil {
		t.Fatal(err)
	}
	if err := ConsumeAdminProvisionNonce(ctx, "a", nonce, lastAccepted); err == nil {
		t.Fatal("nonce expired while its signed timestamp was still accepted")
	}
	if _, err := VerifyAdminProvisionSignature(map[string]string{"a": secret}, header, AdminProvisionCommitPath, body, start.Add(121*time.Second)); err == nil {
		t.Fatal("expired signature accepted")
	}
}
