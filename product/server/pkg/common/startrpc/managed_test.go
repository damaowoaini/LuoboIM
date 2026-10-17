package startrpc

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeManagedServer struct {
	graceful chan struct{}
	stopped  chan struct{}
	once     sync.Once
}

func (s *fakeManagedServer) GracefulStop() { <-s.graceful }
func (s *fakeManagedServer) Stop()         { s.once.Do(func() { close(s.stopped); close(s.graceful) }) }
func TestManagedDrainCompletesOrForcesCancellation(t *testing.T) {
	t.Run("completed drain never forces stop", func(t *testing.T) {
		s := &fakeManagedServer{graceful: make(chan struct{}), stopped: make(chan struct{})}
		close(s.graceful)
		if err := drainManagedServer(s, time.Second); err != nil {
			t.Fatal(err)
		}
		select {
		case <-s.stopped:
			t.Fatal("unexpected force stop")
		default:
		}
	})
	t.Run("stuck drain force stops within budget", func(t *testing.T) {
		s := &fakeManagedServer{graceful: make(chan struct{}), stopped: make(chan struct{})}
		if err := drainManagedServer(s, 10*time.Millisecond); err == nil {
			t.Fatal("missing drain failure")
		}
		select {
		case <-s.stopped:
		default:
			t.Fatal("force stop not called")
		}
	})
}
func TestManagedCleanupGetsIndependentFiniteContext(t *testing.T) {
	want := errors.New("cleanup failure")
	if err := stopManaged(func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 8*time.Second || ctx.Err() != nil {
			t.Fatal("cleanup lacks finite live context")
		}
		return want
	}); !errors.Is(err, want) {
		t.Fatal("cleanup failure discarded")
	}
}
