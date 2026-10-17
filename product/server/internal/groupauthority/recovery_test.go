package groupauthority

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGroupAuthorityRecoveryRequiresOwnedLifecycleAndStopsOnCancel(t *testing.T) {
	called := false
	batch := func(context.Context) error { called = true; return nil }
	if err := recoverCache(context.Background(), batch, nil); !errors.Is(err, ErrUnavailable) || called {
		t.Fatal("unowned background worker accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := recoverCache(ctx, batch, nil); !errors.Is(err, context.Canceled) || called {
		t.Fatal("cancelled worker dispatched recovery")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	observations := make(chan RecoveryStatus, 1)
	err := recoverCache(ctx, func(current context.Context) error {
		called = true
		deadline, ok := current.Deadline()
		if !ok || time.Until(deadline) > cacheBatchTimeout || time.Until(deadline) <= 0 {
			t.Fatal("recovery batch has no finite deadline")
		}
		cancel()
		return ErrCacheSyncPending
	}, observations)
	if !called || !errors.Is(err, context.Canceled) || !((<-observations).Pending) {
		t.Fatal("cancel/pending observation contract violated")
	}
}
