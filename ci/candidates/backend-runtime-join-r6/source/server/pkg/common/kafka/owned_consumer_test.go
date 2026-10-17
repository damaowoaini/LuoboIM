package kafka

import (
	"context"
	"errors"
	"github.com/IBM/sarama"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type ownedSessionFixture struct {
	sarama.ConsumerGroupSession
	ctx context.Context
}

func (s ownedSessionFixture) Context() context.Context { return s.ctx }

type ownedClaimFixture struct{ sarama.ConsumerGroupClaim }
type ownedGroupFixture struct {
	sarama.ConsumerGroup
	closes   atomic.Int32
	setupErr error
	issues   chan error
}

func (g *ownedGroupFixture) Consume(ctx context.Context, _ []string, h sarama.ConsumerGroupHandler) error {
	if g.setupErr != nil {
		return g.setupErr
	}
	s := ownedSessionFixture{ctx: ctx}
	if err := h.Setup(s); err != nil {
		return err
	}
	err := h.ConsumeClaim(s, ownedClaimFixture{})
	return errors.Join(err, h.Cleanup(s))
}
func (g *ownedGroupFixture) Errors() <-chan error { return g.issues }
func (g *ownedGroupFixture) Close() error         { g.closes.Add(1); return nil }

type ownedHandlerFixture struct{ entered, canceled, release chan struct{} }

func (h *ownedHandlerFixture) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *ownedHandlerFixture) Cleanup(sarama.ConsumerGroupSession) error { return nil }
func (h *ownedHandlerFixture) ConsumeClaim(s sarama.ConsumerGroupSession, _ sarama.ConsumerGroupClaim) error {
	close(h.entered)
	<-s.Context().Done()
	close(h.canceled)
	<-h.release
	return nil
}
func TestOwnedConsumerOriginalClaimAdmissionAndJoinPrecedeActualClientClose(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	group := &ownedGroupFixture{}
	handler := &ownedHandlerFixture{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	run, err := NewOwnedConsumerRun(parent, group, []string{"original-topic"}, handler, cancel)
	if err != nil {
		t.Fatal(err)
	}
	var releaseOnce sync.Once
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(handler.release) })
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		_ = run.Stop(cleanup)
	})
	if err := run.StartBounded(parent); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.entered:
		t.Fatal("claim entered before owned admission")
	default:
	}
	if run.Admitted() {
		t.Fatal("startup assignment granted admission")
	}
	if err := run.Enable(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-handler.entered:
	case <-time.After(time.Second):
		t.Fatal("real claim never entered")
	}
	if err := run.StopAdmission(parent); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan error, 1)
	go func() { stopped <- run.Stop(parent) }()
	select {
	case <-handler.canceled:
	case <-time.After(time.Second):
		t.Fatal("original claim was not canceled")
	}
	if group.closes.Load() != 0 || run.Admitted() {
		t.Fatal("consumer closed before claim join or admission survived")
	}
	releaseOnce.Do(func() { close(handler.release) })
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("original claim did not join")
	}
	if group.closes.Load() != 1 {
		t.Fatal("actual client not closed exactly once")
	}
	if err := run.Stop(parent); err != nil || group.closes.Load() != 1 {
		t.Fatal("second stop changed ownership")
	}
}
func TestOwnedConsumerOriginalSetupFailureNeverEnablesAndStillCloses(t *testing.T) {
	for _, kind := range []string{"setup-error", "async-offset-error"} {
		t.Run(kind, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			group := &ownedGroupFixture{issues: make(chan error, 1)}
			if kind == "setup-error" {
				group.setupErr = errors.New("fixture broker refused assignment")
			}
			h := &ownedHandlerFixture{make(chan struct{}), make(chan struct{}), make(chan struct{})}
			run, err := NewOwnedConsumerRun(parent, group, []string{"original-topic"}, h, cancel)
			if err != nil {
				t.Fatal(err)
			}
			var releaseOnce sync.Once
			t.Cleanup(func() {
				cancel()
				releaseOnce.Do(func() { close(h.release) })
				cleanup, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				_ = run.Stop(cleanup)
			})
			if kind == "setup-error" {
				if err := run.StartBounded(parent); err == nil {
					t.Fatal("failed assignment accepted")
				}
			} else {
				if err := run.StartBounded(parent); err != nil {
					t.Fatal(err)
				}
				if err := run.Enable(); err != nil {
					t.Fatal(err)
				}
				select {
				case <-h.entered:
				case <-time.After(time.Second):
					t.Fatal("real original claim missing")
				}
				group.issues <- errors.New("fixture offset commit outcome unconfirmed")
				select {
				case <-h.canceled:
				case <-time.After(time.Second):
					t.Fatal("actual async error did not cancel original claim")
				}
				releaseOnce.Do(func() { close(h.release) })
			}
			if run.Enable() == nil || run.Failure() == nil {
				t.Fatal("failed original owner became ready")
			}
			closing, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if err := run.Stop(closing); err != nil || group.closes.Load() != 1 {
				t.Fatal("failed assignment leaked actual consumer")
			}
		})
	}
}
