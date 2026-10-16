package adminprovision_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	p "github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type imFixture struct {
	raceOwner               bool
	mu                      sync.Mutex
	users                   map[string]p.User
	creates, plans, applies int
	unknownCreate           bool
}

func (i *imFixture) Find(_ context.Context, id string) (*p.User, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	u, ok := i.users[id]
	if !ok {
		return nil, nil
	}
	return &u, nil
}
func (i *imFixture) Create(_ context.Context, u p.User) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.creates++
	if i.raceOwner {
		i.users[u.ID] = p.User{ID: u.ID, ProvisioningID: "foreign-raced-owner"}
		return errors.New("synthetic IM duplicate owner")
	}
	i.users[u.ID] = u
	if i.unknownCreate {
		i.unknownCreate = false
		return errors.New("synthetic lost IM ACK")
	}
	return nil
}
func (i *imFixture) PlanDefaults(context.Context, bool) (p.Defaults, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.plans++
	return p.Defaults{Friends: []string{"fixed-friend"}, Groups: []string{"fixed-group"}}, nil
}
func (i *imFixture) ApplyDefaults(context.Context, string, p.Defaults) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.applies++
	return nil
}

type routeFixture struct {
	mu         sync.Mutex
	receipts   map[string]*p.RouteResult
	commits    int
	loseACK    bool
	conflict   bool
	wrongOwner bool
}

func (r *routeFixture) Query(_ context.Context, id string) (*p.RouteResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.receipts[id]
	if !ok {
		return &p.RouteResult{}, nil
	}
	copy := *v
	return &copy, nil
}
func (r *routeFixture) Commit(_ context.Context, body []byte) (*p.RouteResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var payload p.Payload
	if e := json.Unmarshal(body, &payload); e != nil {
		return nil, e
	}
	r.commits++
	state := "bound"
	if r.conflict {
		state = "conflict"
	}
	v := &p.RouteResult{Found: true, ReceiptID: payload.ReceiptID, UserID: payload.UserID, ServerID: payload.ServerID, BindingState: state}
	if r.wrongOwner {
		v.UserID = "wrong-owner"
	}
	r.receipts[payload.ReceiptID] = v
	if r.loseACK {
		r.loseACK = false
		return nil, errors.New("synthetic lost route ACK")
	}
	return v, nil
}

func fixture(t *testing.T) (*p.Service, database.ChatDatabaseInterface, *imFixture, *routeFixture) {
	t.Helper()
	socket := "/Users/chuzu/Documents/projects/LuoboIM/references/integration/mysql8.4/mysql.sock"
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	root, e := gorm.Open(mysql.Open("root@unix("+socket+")/luobo_integration?parseTime=true"), cfg)
	if e != nil {
		t.Skip("isolated MySQL unavailable")
	}
	name := "luobo_admin_provision_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if e = root.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; e != nil {
		t.Fatal(e)
	}
	db, e := gorm.Open(mysql.Open("root@unix("+socket+")/"+name+"?parseTime=true"), cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		sql, _ := db.DB()
		sql.Close()
		root.Exec("DROP DATABASE `" + name + "`")
		sql, _ = root.DB()
		sql.Close()
	})
	if e = db.AutoMigrate(&table.Account{}, &table.Attribute{}, &table.Register{}, &table.VerifyCode{}, &accountstate.Cancellation{}, &registration.Receipt{}, &p.Intent{}, &p.Job{}, &p.RecoveryAudit{}); e != nil {
		t.Fatal(e)
	}
	im := &imFixture{users: map[string]p.User{}}
	route := &routeFixture{receipts: map[string]*p.RouteResult{}}
	return &p.Service{DB: db, IM: im, Route: route, RouteEnabled: true, ServerID: "synthetic-tenant-A"}, database.NewChatDatabase(db), im, route
}
func create(t *testing.T, s *p.Service, store database.ChatDatabaseInterface, request string) (*p.Creation, error) {
	t.Helper()
	profile := p.Profile{Email: "synthetic@fixture.invalid", Nickname: "synthetic"}
	fp, e := profile.Fingerprint()
	if e != nil {
		t.Fatal(e)
	}
	c, e := s.Reserve(context.Background(), "real-admin", request, fp, false)
	if e != nil {
		return nil, e
	}
	id := uuid.NewString()
	now := time.Now().UTC()
	e = store.RegisterUser(p.WithCreation(context.Background(), c), &table.Register{UserID: id, Mode: "admin", CreateTime: now}, &table.Account{UserID: id, OperatorUserID: "real-admin", CreateTime: now, ChangeTime: now}, &table.Attribute{UserID: id, Email: profile.Email, Nickname: profile.Nickname, CreateTime: now, ChangeTime: now, BirthTime: now})
	return c, e
}
func TestCreationConcurrentSameIntentAtomicOwner(t *testing.T) {
	s, store, im, route := fixture(t)
	request := uuid.NewString()
	const n = 12
	var wg sync.WaitGroup
	errs := make(chan error, n)
	ids := make(chan string, n)
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, e := create(t, s, store, request)
			if e == nil {
				ids <- c.UserID
			}
			errs <- e
		}()
	}
	wg.Wait()
	close(errs)
	close(ids)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	owner := ""
	for id := range ids {
		if owner == "" {
			owner = id
		}
		if id != owner {
			t.Fatal("same intent created different owners")
		}
	}
	for _, model := range []any{&table.Account{}, &table.Attribute{}, &table.Register{}, &p.Intent{}, &p.Job{}} {
		var count int64
		if e := s.DB.Model(model).Count(&count).Error; e != nil || count != 1 {
			t.Fatalf("atomic creation count=%d error=%v", count, e)
		}
	}
	status, e := s.Lookup(context.Background(), "real-admin", request, "", false)
	if e != nil || !status.Created || status.DefinitiveNotCreated || status.State != "awaiting_im" {
		t.Fatal(status, e)
	}
	im.unknownCreate = true
	if e = s.Process(context.Background(), status.ReceiptID); e != nil {
		t.Fatal(e)
	}
	if im.creates != 1 || route.commits != 1 {
		t.Fatal("replayed account creation")
	}
	var job p.Job
	s.DB.First(&job)
	if job.OriginalCreatorID != "real-admin" {
		t.Fatal("creator replaced with bootstrap identity")
	}
	t.Log("LUOBO_ADMIN_PROVISION_CONCURRENT_INTENT_PASS")
}
func TestCreationWriteFailureRollsBackAccountAndOutbox(t *testing.T) {
	s, store, _, _ := fixture(t)
	request := uuid.NewString()
	if e := s.DB.Exec("CREATE TRIGGER fail_provision BEFORE INSERT ON admin_provision_jobs FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic' ").Error; e != nil {
		t.Fatal(e)
	}
	if _, e := create(t, s, store, request); e == nil {
		t.Fatal("injected outbox write succeeded")
	}
	for _, model := range []any{&table.Account{}, &table.Attribute{}, &table.Register{}, &p.Job{}} {
		var n int64
		s.DB.Model(model).Count(&n)
		if n != 0 {
			t.Fatal("partial account commit")
		}
	}
	status, e := s.Lookup(context.Background(), "real-admin", request, "", false)
	if e != nil || status.Created || status.DefinitiveNotCreated || status.State != "reserved" {
		t.Fatal(status, e)
	}
	s.DB.Exec("DROP TRIGGER fail_provision")
	if _, e = create(t, s, store, request); e != nil {
		t.Fatal(e)
	}
	status, e = s.Lookup(context.Background(), "other-admin", request, "", false)
	if e != nil || status.Found {
		t.Fatal("foreign intent exposed")
	}
}
func TestRouteUnknownACKRestartDoesNotRecreateUserOrRecount(t *testing.T) {
	s, store, im, route := fixture(t)
	c, e := create(t, s, store, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	route.loseACK = true
	if e = s.Process(context.Background(), c.ReceiptID); e == nil {
		t.Fatal("unknown ACK reported complete")
	}
	before, e := s.Lookup(context.Background(), "real-admin", "", c.ReceiptID, false)
	if e != nil || !before.Created || before.State == "bound" || before.DefinitiveNotCreated {
		t.Fatal(before, e)
	}
	restarted := &p.Service{DB: s.DB, IM: im, Route: route, RouteEnabled: true, ServerID: s.ServerID}
	if e = restarted.Process(context.Background(), c.ReceiptID); e != nil {
		t.Fatal(e)
	}
	after, e := restarted.Lookup(context.Background(), "real-admin", "", c.ReceiptID, false)
	if e != nil || after.State != "bound" || after.Retryable {
		t.Fatal(after, e)
	}
	if im.creates != 1 || im.plans != 1 || route.commits != 1 {
		t.Fatalf("replayed side effect: creates=%d plans=%d commits=%d", im.creates, im.plans, route.commits)
	}
	t.Log("LUOBO_ADMIN_PROVISION_UNKNOWN_ACK_RECOVERY_PASS")
}
func TestRecoveryAuditAtomicAndHistoricalCreatorUnknown(t *testing.T) {
	s, store, im, _ := fixture(t)
	c, e := create(t, s, store, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	// This row simulates an upgrade from the pre-outbox registrar. Its historical
	// bootstrap operator is not accepted as proof of who authorized creation.
	s.DB.Where("1=1").Delete(&p.Job{})
	s.DB.Where("1=1").Delete(&p.Intent{})
	im.users[c.UserID] = p.User{ID: c.UserID, CreateTime: time.Now().UnixMilli()}
	s.DB.Model(&table.Account{}).Where("user_id=?", c.UserID).Update("operator_user_id", "old-bootstrap")
	if e = s.DB.Exec("CREATE TRIGGER fail_recovery BEFORE INSERT ON admin_provision_recovery_audit FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT='synthetic'").Error; e != nil {
		t.Fatal(e)
	}
	if _, e = s.Recover(context.Background(), "current-super", c.UserID, "authoritative recovery review"); e == nil {
		t.Fatal("missing audit accepted")
	}
	var n int64
	s.DB.Model(&p.Job{}).Count(&n)
	if n != 0 {
		t.Fatal("outbox committed without audit")
	}
	s.DB.Exec("DROP TRIGGER fail_recovery")
	status, e := s.Recover(context.Background(), "current-super", c.UserID, "authoritative recovery review")
	if e != nil || !status.Created || status.State != "pending" {
		t.Fatal(status, e)
	}
	var audit p.RecoveryAudit
	s.DB.First(&audit)
	if !audit.OriginalCreatorUnknown || audit.ActorID != "current-super" {
		t.Fatal("historical actor falsely inferred")
	}
	if e = s.Process(context.Background(), status.ReceiptID); e != nil {
		t.Fatal(e)
	}
	if im.creates != 0 {
		t.Fatal("legacy recovery recreated IM user")
	}
	if _, e = s.Recover(context.Background(), "current-super", "missing", "authoritative recovery review"); e == nil {
		t.Fatal("missing IM owner accepted")
	}
}
func TestIMMarkerCollisionAndRouteOwnerMismatchFailClosed(t *testing.T) {
	for _, kind := range []string{"IM_collision", "IM_race", "route_owner"} {
		t.Run(kind, func(t *testing.T) {
			s, store, im, route := fixture(t)
			c, e := create(t, s, store, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			if kind == "IM_collision" {
				im.users[c.UserID] = p.User{ID: c.UserID, ProvisioningID: "foreign-intent"}
			} else if kind == "IM_race" {
				im.raceOwner = true
			} else {
				route.wrongOwner = true
			}
			e = s.Process(context.Background(), c.ReceiptID)
			status, lookupErr := s.Lookup(context.Background(), "real-admin", "", c.ReceiptID, false)
			if lookupErr != nil || status.State == "bound" || !status.Created || status.DefinitiveNotCreated {
				t.Fatal(status, e, lookupErr)
			}
			if kind == "IM_collision" && (status.State != "conflict" || route.commits != 0 || im.creates != 0) {
				t.Fatal("foreign IM owner overwritten")
			}
			if kind == "IM_race" && (status.State != "conflict" || route.commits != 0 || im.users[c.UserID].ProvisioningID != "foreign-raced-owner") {
				t.Fatal("IM duplicate registration accepted as account ownership")
			}
			if kind == "route_owner" && e == nil {
				t.Fatal("foreign route receipt accepted")
			}
		})
	}
}
func TestIntentFingerprintAndDefinitiveRejection(t *testing.T) {
	s, _, _, _ := fixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	fp, _ := (p.Profile{Email: " A@fixture.invalid "}).Fingerprint()
	same, _ := (p.Profile{Email: "a@fixture.invalid"}).Fingerprint()
	if fp != same {
		t.Fatal("identity normalization mismatch")
	}
	c, e := s.Reserve(ctx, "actor", id, fp, false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Reserve(ctx, "actor", id, fmt.Sprintf("%064d", 1), false); e == nil {
		t.Fatal("changed intent reused")
	}
	if e = s.Reject(ctx, c, "validation_rejected"); e != nil {
		t.Fatal(e)
	}
	status, e := s.Lookup(ctx, "actor", id, "", false)
	if e != nil || !status.DefinitiveNotCreated || status.Created || status.Retryable {
		t.Fatal(status, e)
	}
	unknown, e := s.Lookup(ctx, "actor", uuid.NewString(), "", false)
	if e != nil || unknown.DefinitiveNotCreated || unknown.Found {
		t.Fatal("unobserved treated as definitive absence")
	}
}

func TestDirectOnlyPromotionNeedsAuditedRecoveryAndCancelledOwnerStops(t *testing.T) {
	s, store, im, route := fixture(t)
	s.RouteEnabled = false
	s.ServerID = ""
	c, e := create(t, s, store, uuid.NewString())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Process(context.Background(), c.ReceiptID); e != nil {
		t.Fatal(e)
	}
	s.RouteEnabled = true
	s.ServerID = "tenant-new-central"
	if e = s.Process(context.Background(), c.ReceiptID); e != nil || route.commits != 0 {
		t.Fatal("configuration switch silently rebound direct user")
	}
	recovered, e := s.Recover(context.Background(), "current-super", c.UserID, "verified new central route authorization")
	if e != nil || recovered.State != "pending" {
		t.Fatal(recovered, e)
	}
	if e = s.Process(context.Background(), c.ReceiptID); e != nil || route.commits != 1 || im.creates != 1 {
		t.Fatal("audited promotion recreated user", e)
	}
	var job p.Job
	s.DB.First(&job)
	if job.OriginalCreatorID != "real-admin" || job.ActorID != "current-super" {
		t.Fatal("known creator or recovery authorizer lost")
	}
	oldCreator, e := s.Lookup(context.Background(), "real-admin", c.RequestID, "", false)
	if e != nil || oldCreator.State != "bound" {
		t.Fatal("original creation receipt lost", e)
	}
	now := time.Now().UTC()
	id := "cancelled-owner"
	s.DB.Create(&table.Account{UserID: id, CreateTime: now, ChangeTime: now})
	s.DB.Create(&table.Attribute{UserID: id, Email: "cancelled@fixture.invalid", CreateTime: now, ChangeTime: now, BirthTime: now})
	s.DB.Create(&accountstate.Cancellation{UserID: id, RequestedBy: id, RequestedAt: now})
	im.users[id] = p.User{ID: id, CreateTime: now.UnixMilli()}
	if _, e = s.Recover(context.Background(), "current-super", id, "verified ownership cannot bypass cancellation"); e == nil {
		t.Fatal("cancelled identity was rebound")
	}
}

func TestResolveUnobservedAndReservedRejectsDelayedCreation(t *testing.T) {
	s, store, _, _ := fixture(t)
	ctx := context.Background()
	id := uuid.NewString()
	status, e := s.ResolveUncreated(ctx, "real-admin", id)
	if e != nil || !status.Found || !status.DefinitiveNotCreated || status.Created {
		t.Fatal("unobserved close did not persist proof", e)
	}
	if _, e = create(t, s, store, id); e == nil {
		t.Fatal("delayed request created after definitive absence")
	}
	id = uuid.NewString()
	fp, _ := (p.Profile{Email: "synthetic@fixture.invalid", Nickname: "synthetic"}).Fingerprint()
	c, e := s.Reserve(ctx, "real-admin", id, fp, false)
	if e != nil {
		t.Fatal(e)
	}
	status, e = s.ResolveUncreated(ctx, "real-admin", id)
	if e != nil || !status.DefinitiveNotCreated {
		t.Fatal(status, e)
	}
	now := time.Now().UTC()
	owner := uuid.NewString()
	e = store.RegisterUser(p.WithCreation(ctx, c), &table.Register{UserID: owner, CreateTime: now}, &table.Account{UserID: owner, CreateTime: now, ChangeTime: now}, &table.Attribute{UserID: owner, Email: "synthetic@fixture.invalid", CreateTime: now, ChangeTime: now, BirthTime: now})
	if e == nil {
		t.Fatal("reserved request bypassed resolution lock")
	}
	var n int64
	s.DB.Model(&table.Account{}).Count(&n)
	if n != 0 {
		t.Fatal("cancelled intent wrote account")
	}
	foreign, e := s.Lookup(ctx, "foreign", id, "", false)
	if e != nil || foreign.Found {
		t.Fatal("foreign actor took intent")
	}
}

func TestResolveWaitsForAtomicAccountCommitAndCannotClaimAbsence(t *testing.T) {
	s, store, _, _ := fixture(t)
	ctx := context.Background()
	request := uuid.NewString()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	if e := s.DB.Callback().Create().Before("gorm:create").Register("synthetic-provision-before-account", func(tx *gorm.DB) {
		if tx.Statement.Table == "registers" {
			once.Do(func() { close(entered); <-release })
		}
	}); e != nil {
		t.Fatal(e)
	}
	type creationResult struct {
		c *p.Creation
		e error
	}
	created := make(chan creationResult, 1)
	go func() { c, e := create(t, s, store, request); created <- creationResult{c, e} }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		close(release)
		t.Fatal("account transaction did not reach locked boundary")
	}
	resolved := make(chan p.Status, 1)
	failures := make(chan error, 1)
	go func() { v, e := s.ResolveUncreated(ctx, "real-admin", request); resolved <- v; failures <- e }()
	select {
	case <-resolved:
		close(release)
		t.Fatal("resolution crossed active account transaction")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	made := <-created
	if made.e != nil {
		t.Fatal(made.e)
	}
	status := <-resolved
	if e := <-failures; e != nil || !status.Created || status.DefinitiveNotCreated || status.ReceiptID != made.c.ReceiptID {
		t.Fatal("committed account reported absent", e)
	}
	again, e := s.ResolveUncreated(ctx, "real-admin", request)
	if e != nil || !again.Created || again.DefinitiveNotCreated {
		t.Fatal("repeat resolution changed committed account", e)
	}
}
