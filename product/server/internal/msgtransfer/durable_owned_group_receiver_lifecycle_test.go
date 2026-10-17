package msgtransfer

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push"
)

type actionLifecycleStore struct {
	ready   func(context.Context) error
	recover func(context.Context) error
	source  func(context.Context, string) error
	lists   atomic.Int64
}

func (s *actionLifecycleStore) CheckKafkaReady(ctx context.Context) error {
	if s.ready != nil {
		return s.ready(ctx)
	}
	return nil
}
func (*actionLifecycleStore) KafkaOfflineInstanceID() string {
	return "c805cb36-9f28-48b7-bb4c-851d0f272af2"
}
func (*actionLifecycleStore) RequiredOwnedActionPolicies(context.Context) ([]string, error) {
	return []string{strings.Repeat("a", 64)}, nil
}
func (s *actionLifecycleStore) ListOwnedActionPending(context.Context, string, int64) ([]messagedelivery.KafkaOnlinePending, error) {
	s.lists.Add(1)
	if s.recover != nil {
		return []messagedelivery.KafkaOnlinePending{{BranchID: "synthetic-owned-branch", Cursor: "synthetic-cursor", PolicySHA256: strings.Repeat("a", 64)}}, nil
	}
	return nil, nil
}
func (s *actionLifecycleStore) RecoverOwnedGroupNotification(ctx context.Context, id string, _ messagedelivery.OwnedGroupMessageCallbacks) (messagedelivery.Receipt, error) {
	if s.source != nil {
		return messagedelivery.Receipt{}, s.source(ctx, id)
	}
	return messagedelivery.Receipt{}, nil
}
func (s *actionLifecycleStore) RecoverOwnedActionOnline(ctx context.Context, _ string, _ messagedelivery.VisibilityReader, _ messagedelivery.Projection, _ messagedelivery.KafkaOnlineTransport) error {
	if s.recover != nil {
		return s.recover(ctx)
	}
	return nil
}
func (*actionLifecycleStore) RecoverOwnedActionOffline(context.Context, string, messagedelivery.VisibilityReader, messagedelivery.KafkaOfflineTransport) error {
	return nil
}

type actionLifecycleEffects struct{}

func (actionLifecycleEffects) InstanceID() string { return "2d6fd398-7b2e-40e4-b9b0-0be2642d6538" }
func (actionLifecycleEffects) RequiredOwnedRoleCallbackTransports(context.Context) ([]string, error) {
	return nil, nil
}
func (actionLifecycleEffects) ListOwnedRoleCallbacks(context.Context, string, int64) ([]groupeffects.OwnedRoleCallbackRef, error) {
	return nil, nil
}
func (actionLifecycleEffects) CheckReady(context.Context) error { return nil }
func (actionLifecycleEffects) RequiredOwnedProfiles(context.Context) ([]deliveryprofile.Group, error) {
	return nil, nil
}
func (actionLifecycleEffects) ListOwnedNotifications(context.Context, string, int64) ([]groupeffects.OwnedNotificationPending, error) {
	return nil, nil
}

type actionLifecycleOnline struct{}

func (actionLifecycleOnline) PolicyFingerprint() string { return strings.Repeat("a", 64) }
func (actionLifecycleOnline) Deliver(context.Context, messagedelivery.OnlineCall) (messagedelivery.OnlineReport, error) {
	return messagedelivery.OnlineReport{}, nil
}

type actionLifecycleProjection struct{}

func (actionLifecycleProjection) RaiseMaximum(context.Context, string, int64) error { return nil }
func (actionLifecycleProjection) RaiseSenderRead(context.Context, string, string, int64) error {
	return nil
}
func actionLifecycleWorker(t *testing.T, s *actionLifecycleStore, a *ownedWorkerAdapterFixture) *ManagedGroupActionWorker {
	t.Helper()
	worker, err := newManagedGroupActionWorker(s, actionLifecycleEffects{}, ownedWorkerReaderFixture{}, actionLifecycleProjection{}, []messagedelivery.KafkaOnlineTransport{actionLifecycleOnline{}}, []OwnedKafkaOfflineTransport{a}, map[string]*push.FrozenOwnedGroupMessageCallbacks{strings.Repeat("b", 64): {}})
	if err != nil {
		t.Fatal(err)
	}
	return worker
}
func stopActionLifecycle(t *testing.T, w *ManagedGroupActionWorker) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := w.Stop(ctx); err != nil {
		t.Fatal("owned action worker shutdown did not join", err)
	}
}

// These execute the real Start/Stop/run methods. Only storage/provider boundaries
// are synthetic; they do not claim Mongo, receiver business or vendor coverage.
func TestOwnedGroupActionStopCancelsConcurrentReadinessBeforeClosingPools(t *testing.T) {
	entered := make(chan struct{})
	returned := make(chan error, 1)
	store := &actionLifecycleStore{ready: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 15*time.Second {
			return messagedelivery.ErrInvalid
		}
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}}
	adapter := &ownedWorkerAdapterFixture{policy: strings.Repeat("a", 64)}
	worker := actionLifecycleWorker(t, store, adapter)
	t.Cleanup(func() { stopActionLifecycle(t, worker) })
	go func() { returned <- worker.Start(context.Background()) }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("actual readiness not entered")
	}
	stopActionLifecycle(t, worker)
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("Stop did not cancel the accepted Start", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not join after Stop")
	}
	if adapter.closed.Load() != 1 || store.lists.Load() != 0 {
		t.Fatal("readiness cancellation dispatched or closed owned pools more than once")
	}
	if err := worker.Start(context.Background()); !errors.Is(err, messagedelivery.ErrConflict) {
		t.Fatal("stopped worker admitted another Start", err)
	}
}

func TestOwnedGroupActionStopBeforeStartClosesAdmissionDuringBlockedPoolClose(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	defer resume()
	adapter := &ownedWorkerAdapterFixture{policy: strings.Repeat("a", 64), onClose: func() { close(entered); <-release }}
	store := &actionLifecycleStore{}
	worker := actionLifecycleWorker(t, store, adapter)
	t.Cleanup(func() { resume(); stopActionLifecycle(t, worker) })
	stopped := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		stopped <- worker.Stop(ctx)
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not reach owned Close")
	}
	started := make(chan error, 1)
	go func() { started <- worker.Start(context.Background()) }()
	select {
	case err := <-started:
		if !errors.Is(err, messagedelivery.ErrConflict) {
			t.Fatal("closing pools admitted Start", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Start waited behind pool Close instead of rejecting closed admission")
	}
	resume()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop failed to join Close")
	}
	stopActionLifecycle(t, worker)
	if adapter.closed.Load() != 1 || store.lists.Load() != 0 {
		t.Fatal("Stop before Start leaked or restarted owned worker")
	}
}

func TestOwnedGroupActionTimedOutStopRetainsJoinOwnershipAndNeverClosesLiveTransport(t *testing.T) {
	entered, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	resume := func() { once.Do(func() { close(release) }) }
	defer resume()
	store := &actionLifecycleStore{recover: func(context.Context) error {
		close(entered)
		<-release
		close(returned)
		return messagedelivery.ErrOnlineUnconfirmed
	}}
	adapter := &ownedWorkerAdapterFixture{policy: strings.Repeat("a", 64), onClose: func() {
		select {
		case <-returned:
		default:
			t.Error("pool closed before accepted recovery joined")
		}
	}}
	worker := actionLifecycleWorker(t, store, adapter)
	t.Cleanup(func() { resume(); stopActionLifecycle(t, worker) })
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("real worker loop did not enter recovery")
	}
	short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	err := worker.Stop(short)
	cancel()
	if !errors.Is(err, ErrKafkaOfflineShutdownPending) || adapter.closed.Load() != 0 {
		t.Fatal("timeout closed an in-flight owned pool or fabricated shutdown", err)
	}
	if err := worker.Start(context.Background()); !errors.Is(err, messagedelivery.ErrConflict) {
		t.Fatal("timed out Stop reopened Start admission", err)
	}
	resume()
	stopActionLifecycle(t, worker)
	stopActionLifecycle(t, worker)
	if adapter.closed.Load() != 1 {
		t.Fatal("repeated Stop repeated Close instead of joining same ownership")
	}
	summary, err := worker.DrainSummary()
	if err != nil || summary.InstanceID != store.KafkaOfflineInstanceID() || !summary.WorkerJoined || !summary.OwnedPoolsClosed {
		t.Fatal("drain did not bind original instance", err)
	}
}

type actionFairEffects struct {
	actionLifecycleEffects
	oldID, newID string
}

func (e actionFairEffects) ListOwnedNotifications(_ context.Context, after string, _ int64) ([]groupeffects.OwnedNotificationPending, error) {
	if after != "" {
		return nil, nil
	}
	return []groupeffects.OwnedNotificationPending{{ID: e.oldID, Cursor: e.oldID}, {ID: e.newID, Cursor: e.newID}}, nil
}
func TestOwnedGroupActionLegacyRowDoesNotStarveIndependentSource(t *testing.T) {
	const oldID = "1ca0daf4-49ef-4104-a3a0-f1c13cfe396e"
	const newID = "5ab6f8ef-df62-4e81-8cd7-9ee2ce92be76"
	accepted := make(chan struct{})
	var once sync.Once
	var old atomic.Int64
	store := &actionLifecycleStore{source: func(_ context.Context, id string) error {
		if id == oldID {
			old.Add(1)
			return groupeffects.ErrLegacyOwnedQuarantined
		}
		if id == newID {
			once.Do(func() { close(accepted) })
			return nil
		}
		return messagedelivery.ErrInvalid
	}}
	adapter := &ownedWorkerAdapterFixture{policy: strings.Repeat("a", 64)}
	worker, err := newManagedGroupActionWorker(store, actionFairEffects{oldID: oldID, newID: newID}, ownedWorkerReaderFixture{}, actionLifecycleProjection{}, []messagedelivery.KafkaOnlineTransport{actionLifecycleOnline{}}, []OwnedKafkaOfflineTransport{adapter}, map[string]*push.FrozenOwnedGroupMessageCallbacks{strings.Repeat("b", 64): {}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopActionLifecycle(t, worker) })
	if err := worker.Start(context.Background()); err != nil {
		t.Fatal("one incomplete source blocked real worker Start", err)
	}
	select {
	case <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("legacy quarantine starved independent source")
	}
	stopActionLifecycle(t, worker)
	if old.Load() == 0 || adapter.closed.Load() != 1 {
		t.Fatal("legacy source skipped without processing or owned pool leaked")
	}
}
