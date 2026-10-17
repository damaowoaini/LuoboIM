package riskcontrol

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"gorm.io/gorm"
)

func TestRiskDatabaseInitializationFailureCanRecoverWithoutRestart(t *testing.T) {
	now := time.Unix(1, 0)
	want := &gorm.DB{}
	attempts := 0
	m := newRiskDatabaseManager(func(context.Context) (*gorm.DB, error) {
		attempts++
		if attempts == 1 {
			return nil, errors.New("synthetic initial config/dial failure")
		}
		return want, nil
	}, func() time.Time { return now })
	if db, err := m.get(context.Background()); db != nil || err == nil {
		t.Fatal("failed initialization admitted")
	}
	if db, err := m.get(context.Background()); db != nil || err == nil || attempts != 1 {
		t.Fatal("failure throttle reopened a pool")
	}
	now = now.Add(riskRetryDelay)
	if db, err := m.get(context.Background()); db != want || err != nil || attempts != 2 {
		t.Fatal("initial failure remained cached")
	}
	for i := 0; i < 3; i++ {
		if db, err := m.get(context.Background()); db != want || err != nil || attempts != 2 {
			t.Fatal("healthy shared pool replaced")
		}
	}
}

func TestRiskDatabaseConcurrentInitializationUsesOnePool(t *testing.T) {
	var attempts atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})
	want := &gorm.DB{}
	m := newRiskDatabaseManager(func(ctx context.Context) (*gorm.DB, error) {
		if attempts.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return want, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, time.Now)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan bool, 12)
	wg.Add(1)
	go func() { defer wg.Done(); db, err := m.get(ctx); results <- db == want && err == nil }()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("initializer did not start")
	}
	for i := 1; i < 12; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); db, err := m.get(ctx); results <- db == want && err == nil }()
	}
	close(release)
	wg.Wait()
	close(results)
	for ok := range results {
		if !ok {
			t.Fatal("concurrent waiter did not receive shared pool")
		}
	}
	if attempts.Load() != 1 {
		t.Fatal("concurrent callers created multiple pools")
	}
}

func TestRiskDatabaseWaitingCallerCanCancelWithoutCancelingLeader(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	want := &gorm.DB{}
	m := newRiskDatabaseManager(func(ctx context.Context) (*gorm.DB, error) {
		close(entered)
		select {
		case <-release:
			return want, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}, time.Now)
	leader, cancelLeader := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelLeader()
	go func() { _, err := m.get(leader); done <- err }()
	select {
	case <-entered:
	case <-leader.Done():
		t.Fatal("initializer did not start")
	}
	waiter, cancelWaiter := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelWaiter()
	if db, err := m.get(waiter); db != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("canceled waiter was admitted")
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal("waiter canceled leader")
		}
	case <-leader.Done():
		t.Fatal("leader did not finish")
	}
	if db, err := m.get(context.Background()); db != want || err != nil {
		t.Fatal("leader pool lost")
	}
}

func TestRiskDatabaseAttemptIsBoundedAndDriverErrorIsPrivate(t *testing.T) {
	before := time.Now()
	m := newRiskDatabaseManager(func(ctx context.Context) (*gorm.DB, error) {
		deadline, ok := ctx.Deadline()
		if !ok || deadline.After(before.Add(riskConnectBudget+time.Second)) {
			t.Fatal("initialization has no bounded deadline")
		}
		return nil, errors.New("synthetic credential-bearing driver detail")
	}, time.Now)
	if db, err := m.get(context.Background()); db != nil || err != errRiskDatabaseUnavailable {
		t.Fatal("driver detail escaped or failure admitted")
	}
}

func TestRiskDatabaseRejectsNilOrCanceledContextAndNilPool(t *testing.T) {
	calls := 0
	m := newRiskDatabaseManager(func(context.Context) (*gorm.DB, error) { calls++; return nil, nil }, time.Now)
	if _, err := m.get(nil); err == nil || calls != 0 {
		t.Fatal("nil context reached initializer")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.get(ctx); !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("canceled context reached initializer")
	}
	if db, err := m.get(context.Background()); db != nil || err == nil || calls != 1 {
		t.Fatal("nil initialized pool admitted")
	}
}

func TestRiskDatabaseFailureKeepsActionFailClosed(t *testing.T) {
	old := riskDatabase
	t.Cleanup(func() { riskDatabase = old })
	riskDatabase = newRiskDatabaseManager(func(context.Context) (*gorm.DB, error) { return nil, errors.New("synthetic unavailable") }, time.Now)
	if rows, err := GetConfigMap(context.Background()); rows != nil || err == nil {
		t.Fatal("policy read fell back on unavailable database")
	}
	if blocked, err := IsActionBlocked(context.Background(), RiskDisableGroupActionKey); blocked || err == nil {
		t.Fatal("unavailable policy was reported as permitted")
	}
}

func TestRiskDatabaseDriverInputPreservesCredentialsAndBoundsIO(t *testing.T) {
	for _, conf := range []*config.MYSQL{nil, {}, {Address: []string{""}, Database: "synthetic"}, {Address: []string{"127.0.0.1:38406"}}} {
		if driver, err := riskDriverConfig(conf); driver != nil || err == nil {
			t.Fatal("unbound database input accepted")
		}
	}
	conf := &config.MYSQL{Address: []string{"127.0.0.1:38406"}, Username: "synthetic-user", Password: "synthetic@:/?secret", Database: "synthetic_db"}
	driver, err := riskDriverConfig(conf)
	if err != nil || driver.User != conf.Username || driver.Passwd != conf.Password || driver.Addr != conf.Address[0] || driver.DBName != conf.Database {
		t.Fatal("structured driver binding changed")
	}
	if driver.Timeout != riskIOTimeout || driver.ReadTimeout != riskIOTimeout || driver.WriteTimeout != riskIOTimeout || !driver.ParseTime || driver.Params["charset"] != "utf8mb4" || driver.MultiStatements {
		t.Fatal("driver I/O or read-only input contract changed")
	}
}
