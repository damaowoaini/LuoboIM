package group

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestGroupOwnedLifecycleJoinsBeforeClosingPools(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	worker := make(chan error, 1)
	var order []string
	r := &ownedGroupRuntime{cancel: cancel, worker: worker,
		closeRecovery: func() error { order = append(order, "recovery"); return nil },
		closeShared:   func() error { order = append(order, "shared"); return nil },
		disconnect: func(ctx context.Context) error {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded disconnect")
			}
			order = append(order, "mongo")
			return nil
		}}
	go func() { <-ctx.Done(); worker <- context.Canceled }()
	stopCtx, done := context.WithTimeout(context.Background(), time.Second)
	defer done()
	if err := r.stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if err := r.stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(order, []string{"recovery", "shared", "mongo"}) {
		t.Fatalf("wrong cleanup order/count: %v", order)
	}
}
func TestGroupOwnedLifecycleDoesNotCloseUnjoinedWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	closed := false
	r := &ownedGroupRuntime{cancel: func() {}, worker: make(chan error), closeRecovery: func() error { closed = true; return nil }}
	if err := r.stop(ctx); err == nil || closed {
		t.Fatal("unjoined worker reported cleanup or closed pool")
	}
}
func TestGroupOwnedLifecycleReportsCleanupFailure(t *testing.T) {
	want := errors.New("exact close failure")
	r := &ownedGroupRuntime{closeRecovery: func() error { return want }}
	if err := r.stop(context.Background()); !errors.Is(err, want) {
		t.Fatal("cleanup error lost")
	}
}
