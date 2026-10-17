package msgtransfer

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Existing old positive-worker fixtures remain positive-only. Adding these
// interface methods does not change any prior assertion or transport behavior.
func (*ownedWorkerStoreFixture) ListKafkaEphemeralOfflinePending(context.Context, string, int64) ([]messagedelivery.KafkaOnlinePending, error) {
	return nil, nil
}
func (*ownedWorkerStoreFixture) RecoverKafkaEphemeralOffline(context.Context, string, messagedelivery.KafkaOfflineTransport) error {
	return messagedelivery.ErrInvalid
}

type ephemeralOnlineWorkerStore struct {
	required                    []string
	positive, ephemeral         atomic.Int64
	positiveDone, ephemeralDone chan struct{}
	positiveOnce, ephemeralOnce sync.Once
}

func (*ephemeralOnlineWorkerStore) CheckKafkaReady(context.Context) error { return nil }
func (s *ephemeralOnlineWorkerStore) RequiredKafkaOnlinePolicies(context.Context) ([]string, error) {
	return append([]string(nil), s.required...), nil
}
func (s *ephemeralOnlineWorkerStore) ListKafkaOnlinePending(context.Context, string, int64) ([]messagedelivery.KafkaOnlinePending, error) {
	return []messagedelivery.KafkaOnlinePending{{BranchID: "positive", PolicySHA256: strings.Repeat("a", 64), Cursor: "positive-cursor"}}, nil
}
func (s *ephemeralOnlineWorkerStore) ListKafkaEphemeralOnlinePending(context.Context, string, int64) ([]messagedelivery.KafkaOnlinePending, error) {
	return []messagedelivery.KafkaOnlinePending{{BranchID: "ephemeral", PolicySHA256: strings.Repeat("a", 64), Cursor: "ephemeral-cursor"}}, nil
}
func (s *ephemeralOnlineWorkerStore) RecoverKafkaOnline(_ context.Context, b string, _ messagedelivery.VisibilityReader, _ messagedelivery.Projection, _ messagedelivery.KafkaOnlineTransport) error {
	if b != "positive" {
		return messagedelivery.ErrInvalid
	}
	s.positive.Add(1)
	s.positiveOnce.Do(func() { close(s.positiveDone) })
	return nil
}
func (s *ephemeralOnlineWorkerStore) RecoverKafkaEphemeralOnline(_ context.Context, b string, _ messagedelivery.KafkaOnlineTransport) error {
	if b != "ephemeral" {
		return messagedelivery.ErrInvalid
	}
	s.ephemeral.Add(1)
	s.ephemeralOnce.Do(func() { close(s.ephemeralDone) })
	return messagedelivery.ErrOnlineUnconfirmed
}

type ephemeralWorkerProjection struct{}

func (ephemeralWorkerProjection) RaiseMaximum(context.Context, string, int64) error {
	return messagedelivery.ErrInvalid
}
func (ephemeralWorkerProjection) RaiseSenderRead(context.Context, string, string, int64) error {
	return messagedelivery.ErrInvalid
}

type ephemeralWorkerOnlineTransport struct{}

func (ephemeralWorkerOnlineTransport) PolicyFingerprint() string { return strings.Repeat("a", 64) }
func (ephemeralWorkerOnlineTransport) Deliver(context.Context, messagedelivery.OnlineCall) (messagedelivery.OnlineReport, error) {
	return messagedelivery.OnlineReport{}, messagedelivery.ErrInvalid
}

func TestOrdinaryOnlineWorkerRequiresRetainedPolicyAndSeparatesSeqZero(t *testing.T) {
	t.Run("missing-retained-policy", func(t *testing.T) {
		s := &ephemeralOnlineWorkerStore{required: []string{strings.Repeat("b", 64)}}
		w, e := newManagedKafkaOnlineWorker(s, ownedWorkerReaderFixture{}, ephemeralWorkerProjection{}, []messagedelivery.KafkaOnlineTransport{ephemeralWorkerOnlineTransport{}})
		if e != nil {
			t.Fatal(e)
		}
		if e = w.Start(context.Background()); !errors.Is(e, messagedelivery.ErrUnprepared) {
			t.Fatal("missing original policy started worker", e)
		}
		if s.positive.Load() != 0 || s.ephemeral.Load() != 0 || w.started {
			t.Fatal("failed startup entered a recovery path")
		}
	})
	t.Run("independent-positive-and-ephemeral", func(t *testing.T) {
		s := &ephemeralOnlineWorkerStore{required: []string{strings.Repeat("a", 64)}, positiveDone: make(chan struct{}), ephemeralDone: make(chan struct{})}
		w, e := newManagedKafkaOnlineWorker(s, ownedWorkerReaderFixture{}, ephemeralWorkerProjection{}, []messagedelivery.KafkaOnlineTransport{ephemeralWorkerOnlineTransport{}})
		if e != nil {
			t.Fatal(e)
		}
		if e = w.Start(context.Background()); e != nil {
			t.Fatal(e)
		}
		joined := false
		t.Cleanup(func() {
			if !joined {
				c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				if e := w.Stop(c); e != nil {
					t.Error(e)
				}
			}
		})
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		for _, done := range []chan struct{}{s.positiveDone, s.ephemeralDone} {
			select {
			case <-done:
			case <-c.Done():
				t.Fatal("separate family not admitted")
			}
		}
		if e = w.Stop(c); e != nil {
			t.Fatal(e)
		}
		joined = true
		if s.positive.Load() == 0 || s.ephemeral.Load() == 0 {
			t.Fatal("Seq0 starved or entered positive recovery")
		}
	})
}

type ephemeralOfflineWorkerStore struct {
	ownedWorkerStoreFixture
	done      chan struct{}
	once      sync.Once
	ephemeral atomic.Int64
}

func (s *ephemeralOfflineWorkerStore) ListKafkaEphemeralOfflinePending(context.Context, string, int64) ([]messagedelivery.KafkaOnlinePending, error) {
	return []messagedelivery.KafkaOnlinePending{{BranchID: "ephemeral", PolicySHA256: strings.Repeat("a", 64), Cursor: "ephemeral-cursor"}}, nil
}
func (s *ephemeralOfflineWorkerStore) RecoverKafkaEphemeralOffline(_ context.Context, b string, _ messagedelivery.KafkaOfflineTransport) error {
	if b != "ephemeral" {
		return messagedelivery.ErrInvalid
	}
	s.ephemeral.Add(1)
	s.once.Do(func() { close(s.done) })
	return messagedelivery.ErrKafkaOfflineUnconfirmed
}
func TestOrdinaryOfflineWorkerSeqZeroUsesSameOwnedPoolAndRetainsUnknown(t *testing.T) {
	s := &ephemeralOfflineWorkerStore{ownedWorkerStoreFixture: ownedWorkerStoreFixture{required: []string{strings.Repeat("a", 64)}}, done: make(chan struct{})}
	a := &ownedWorkerAdapterFixture{policy: strings.Repeat("a", 64)}
	w, e := newManagedKafkaOfflineWorker(s, ownedWorkerReaderFixture{}, []OwnedKafkaOfflineTransport{a})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { stopOwnedWorker(t, w) })
	if e = w.Start(context.Background()); e != nil {
		t.Fatal(e)
	}
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		t.Fatal("Seq0 offline path not reached")
	}
	stopOwnedWorker(t, w)
	if a.closed.Load() != 1 || s.ephemeral.Load() == 0 {
		t.Fatal("separate worker/pool owner or missing Seq0 dispatch")
	}
	_, unknown := w.Status()
	if !unknown {
		t.Fatal("local join reclassified unknown recipient ACK")
	}
}
