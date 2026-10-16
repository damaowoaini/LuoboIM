package cache

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestGroupAuthorityInvalidatorOwnsFiniteClientWithoutChangingSharedOptions(t *testing.T) {
	base := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	defer func() {
		if err := base.Close(); err != nil {
			t.Error("base fixture client close", err)
		}
	}()
	invalidator, err := NewGroupAuthorityInvalidator(base)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := invalidator.Close(); err != nil {
			t.Error("bounded client close", err)
		}
	}()
	opts := invalidator.client.(*redis.Client).Options()
	if invalidator.client == base || !opts.ContextTimeoutEnabled || opts.DialTimeout != time.Second || opts.ReadTimeout != time.Second || opts.WriteTimeout != time.Second || opts.PoolTimeout != time.Second || opts.MaxRetries != 0 {
		t.Fatal("dedicated invalidator is not bounded")
	}
	if base.Options().ContextTimeoutEnabled {
		t.Fatal("shared client options mutated")
	}
	for _, key := range []string{"", "UID_PID_TOKEN_STATUS:synthetic", "GROUP_INFO:", "GROUP_INFO:g\x00bad"} {
		if err := invalidator.Apply(context.Background(), key); err == nil {
			t.Fatal("unrelated key was admitted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := invalidator.Apply(ctx, "GROUP_INFO:synthetic"); err == nil {
		t.Fatal("cancelled invalidation admitted")
	}
}
