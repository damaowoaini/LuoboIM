package chat

import (
	"context"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/config"
)

type fakeFailLock struct{ m map[string]int64 }

func (f *fakeFailLock) Count(_ context.Context, key string) (int64, error) { return f.m[key], nil }
func (f *fakeFailLock) Incr(_ context.Context, key string, _ time.Duration) (int64, error) {
	f.m[key]++
	return f.m[key], nil
}
func (f *fakeFailLock) Reset(_ context.Context, keys ...string) error {
	for _, k := range keys {
		delete(f.m, k)
	}
	return nil
}

func newLockSvr() (*chatSvr, *fakeFailLock) {
	f := &fakeFailLock{m: map[string]int64{}}
	return &chatSvr{LoginFailLock: f}, f
}

func TestLoginLockPerAccountIP(t *testing.T) {
	old := config.Config.LoginLock
	defer func() { config.Config.LoginLock = old }()
	config.Config.LoginLock.MaxFailPerAccountIP = 3
	config.Config.LoginLock.MaxFailPerAccount = 100

	o, _ := newLockSvr()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if err := o.ensureLoginNotLocked(ctx, "u1", "198.51.100.1"); err != nil {
			t.Fatalf("attempt %d should be allowed: %v", i, err)
		}
		o.recordLoginFailure(ctx, "u1", "198.51.100.1")
	}
	if err := o.ensureLoginNotLocked(ctx, "u1", "198.51.100.1"); err == nil {
		t.Fatal("same account+ip should be locked after 3 failures")
	}
	if err := o.ensureLoginNotLocked(ctx, "u1", "198.51.100.2"); err != nil {
		t.Fatalf("other ip must not be locked: %v", err)
	}
	if err := o.ensureLoginNotLocked(ctx, "u2", "198.51.100.1"); err != nil {
		t.Fatalf("other account must not be locked: %v", err)
	}
}

func TestLoginLockPerAccountAcrossIPs(t *testing.T) {
	old := config.Config.LoginLock
	defer func() { config.Config.LoginLock = old }()
	config.Config.LoginLock.MaxFailPerAccountIP = 100
	config.Config.LoginLock.MaxFailPerAccount = 4

	o, _ := newLockSvr()
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		o.recordLoginFailure(ctx, "u1", "198.51.100."+string(rune('1'+i)))
	}
	if err := o.ensureLoginNotLocked(ctx, "u1", "203.0.113.5"); err == nil {
		t.Fatal("account should be locked after failures from many ips")
	}
}

func TestLoginSuccessClearsAccountIPCounter(t *testing.T) {
	old := config.Config.LoginLock
	defer func() { config.Config.LoginLock = old }()
	config.Config.LoginLock.MaxFailPerAccountIP = 3
	config.Config.LoginLock.MaxFailPerAccount = 100

	o, f := newLockSvr()
	ctx := context.Background()
	o.recordLoginFailure(ctx, "u1", "198.51.100.1")
	o.recordLoginFailure(ctx, "u1", "198.51.100.1")
	o.clearLoginFailures(ctx, "u1", "198.51.100.1")
	kIP, kAcc := loginFailKeys("u1", "198.51.100.1")
	if f.m[kIP] != 0 {
		t.Fatalf("account+ip counter should be cleared, got %v", f.m)
	}
	if f.m[kAcc] != 2 {
		t.Fatalf("account counter must survive a success, got %v", f.m)
	}
}

func TestLoginLockDefaults(t *testing.T) {
	old := config.Config.LoginLock
	defer func() { config.Config.LoginLock = old }()
	config.Config.LoginLock.MaxFailPerAccountIP = 0
	config.Config.LoginLock.MaxFailPerAccount = 0
	config.Config.LoginLock.WindowMinutes = 0
	p := loginLockPolicyFromConfig()
	if p.perAccountIP != 10 || p.perAccount != 30 || p.window != 15*time.Minute {
		t.Fatalf("unexpected defaults %+v", p)
	}
}

func TestLoginLockNilStoreIsNoop(t *testing.T) {
	o := &chatSvr{}
	ctx := context.Background()
	o.recordLoginFailure(ctx, "u1", "198.51.100.1")
	if err := o.ensureLoginNotLocked(ctx, "u1", "198.51.100.1"); err != nil {
		t.Fatal(err)
	}
}
