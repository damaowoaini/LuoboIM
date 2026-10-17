//go:build js && wasm

package wasm_wrapper

import (
	"errors"
	"sync"
	"syscall/js"
	"testing"
	"time"

	"github.com/openimsdk/openim-sdk-core/v3/open_im_sdk_callback"
	"github.com/openimsdk/openim-sdk-core/v3/wasm/bufferplan"
)

type bufferTestCallback struct {
	mu                         sync.Mutex
	success, failure, progress int
}

func (b *bufferTestCallback) OnSuccess(string)      { b.mu.Lock(); b.success++; b.mu.Unlock() }
func (b *bufferTestCallback) OnError(int32, string) { b.mu.Lock(); b.failure++; b.mu.Unlock() }
func (b *bufferTestCallback) OnProgress(int)        { b.mu.Lock(); b.progress++; b.mu.Unlock() }
func testPlan(t *testing.T) bufferplan.Plan {
	t.Helper()
	plan, err := bufferplan.Prepare(`{"clientMsgID":"video-a","contentType":104,"videoElem":{"videoUUID":"v/file.mp4","snapshotUUID":"s/file.jpg"}}`, "peer-a", "", [2]int64{4, 3})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
func waitBufferRelease(t *testing.T, done <-chan bool) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("release did not settle")
	}
}

func TestBufferVideoAcknowledgesPairBeforeOriginalDispatchAndSettlesOnce(t *testing.T) {
	InvalidateBufferSession()
	plan := testPlan(t)
	callback := &bufferTestCallback{}
	done := make(chan bool, 1)
	registered, dispatched := false, 0
	deps := bufferDependencies{current: func() bool { return true }, registered: func(*bufferLease) bool { return registered },
		register: func(_ *bufferLease, p bufferplan.Plan, _ [2]js.Value) error {
			if len(p.Files) != 2 {
				t.Fatal("pair was split")
			}
			registered = true
			return nil
		},
		release: func(l *bufferLease) { forgetBuffer(l); done <- true },
		dispatch: func(cb open_im_sdk_callback.SendMsgCallBack, op, raw, recv, group, push string, current func() bool) {
			if !registered || !current() || recv != "peer-a" || group != "" || op != "op-a" || raw != plan.JSON {
				t.Fatal("dispatch preceded exact pair ACK")
			}
			dispatched++
			cb.OnProgress(40)
			cb.OnSuccess(`{"clientMsgID":"video-a"}`)
			cb.OnError(1, "late")
		}}
	runBuffer(plan, bufferEpoch(), "op-a", "peer-a", "", "{}", [2]js.Value{}, callback, deps)
	waitBufferRelease(t, done)
	if dispatched != 1 || callback.success != 1 || callback.failure != 0 || callback.progress != 1 {
		t.Fatal(dispatched, callback)
	}
}

func TestBufferRegistrationFailureOrRetiredAckNeverDispatches(t *testing.T) {
	for _, mode := range []string{"failed-map", "owner-ABA", "lost-worker"} {
		t.Run(mode, func(t *testing.T) {
			InvalidateBufferSession()
			epoch := bufferEpoch()
			plan := testPlan(t)
			callback := &bufferTestCallback{}
			done := make(chan bool, 1)
			worker := true
			sent := 0
			deps := bufferDependencies{current: func() bool { return bufferEpoch() == epoch }, registered: func(*bufferLease) bool { return worker },
				register: func(*bufferLease, bufferplan.Plan, [2]js.Value) error {
					if mode == "failed-map" {
						return errors.New("map ACK")
					}
					if mode == "owner-ABA" {
						InvalidateBufferSession()
						InvalidateBufferSession()
					}
					if mode == "lost-worker" {
						worker = false
					}
					return nil
				},
				release: func(l *bufferLease) { forgetBuffer(l); done <- true },
				dispatch: func(open_im_sdk_callback.SendMsgCallBack, string, string, string, string, string, func() bool) {
					sent++
				}}
			runBuffer(plan, epoch, "op-a", "peer-a", "", "{}", [2]js.Value{}, callback, deps)
			waitBufferRelease(t, done)
			if sent != 0 || callback.failure != 1 || callback.success != 0 {
				t.Fatal(mode, sent, callback)
			}
		})
	}
}

func TestBufferDelayedDispatchKeepsTheOriginalSessionAndMapGuard(t *testing.T) {
	InvalidateBufferSession()
	epoch := bufferEpoch()
	plan := testPlan(t)
	callback := &bufferTestCallback{}
	done := make(chan bool, 1)
	var lateCurrent func() bool
	var lateCallback open_im_sdk_callback.SendMsgCallBack
	deps := bufferDependencies{current: func() bool { return bufferEpoch() == epoch }, registered: func(*bufferLease) bool { return true },
		register: func(*bufferLease, bufferplan.Plan, [2]js.Value) error { return nil },
		release:  func(l *bufferLease) { forgetBuffer(l); done <- true },
		dispatch: func(cb open_im_sdk_callback.SendMsgCallBack, _ string, _ string, _ string, _ string, _ string, current func() bool) {
			lateCurrent = current
			lateCallback = cb
		}}
	runBuffer(plan, epoch, "op-a", "peer-a", "", "{}", [2]js.Value{}, callback, deps)
	if lateCurrent == nil || !lateCurrent() {
		t.Fatal("original dispatch guard missing")
	}
	InvalidateBufferSession()
	InvalidateBufferSession()
	if lateCurrent() {
		t.Fatal("queued SDK operation borrowed a replacement session")
	}
	lateCallback.OnProgress(10)
	lateCallback.OnError(1, "retired before physical send")
	waitBufferRelease(t, done)
	if callback.progress != 0 || callback.failure != 1 {
		t.Fatal(callback)
	}
}

func TestBufferActiveUUIDRejectsConcurrentByBufferAndByFileWithoutReplacingIt(t *testing.T) {
	InvalidateBufferSession()
	plan := testPlan(t)
	lease, err := claimBuffer(bufferEpoch(), plan)
	if err != nil {
		t.Fatal(err)
	}
	if !bufferMessageConflict(plan.JSON) {
		t.Fatal("ByFile collision was admitted")
	}
	other := plan
	other.Message.ClientMsgID = "other-client"
	if _, err = claimBuffer(bufferEpoch(), other); err == nil {
		t.Fatal("concurrent UUID replaced")
	}
	forgetBuffer(lease)
	if bufferMessageConflict(plan.JSON) {
		t.Fatal("ordinary ByFile path retained a completed lease")
	}
	if _, err = claimBuffer(bufferEpoch(), other); err != nil {
		t.Fatal("explicit new operation could not claim released UUID", err)
	}
	InvalidateBufferSession()
}

func TestBufferLateReleaseCannotRemoveNewSessionLeaseAndOldProgressIsSuppressed(t *testing.T) {
	InvalidateBufferSession()
	plan := testPlan(t)
	old, err := claimBuffer(bufferEpoch(), plan)
	if err != nil {
		t.Fatal(err)
	}
	InvalidateBufferSession()
	fresh, err := claimBuffer(bufferEpoch(), plan)
	if err != nil {
		t.Fatal(err)
	}
	forgetBuffer(old)
	if !bufferMessageConflict(plan.JSON) {
		t.Fatal("old release erased new owner lease")
	}
	callback := &bufferTestCallback{}
	cb := &bufferCallbacks{SendMsgCallBack: callback, current: func() bool { return false }, finished: func() {}}
	cb.OnProgress(90)
	cb.OnError(1, "old")
	cb.OnSuccess("late")
	if callback.progress != 0 || callback.failure != 1 || callback.success != 0 {
		t.Fatal(callback)
	}
	forgetBuffer(fresh)
}

func TestOrdinaryEarliestClaimBlocksBufferBeforeRegistrationAndDoesNotAcceptAnUnopenedTicket(t *testing.T) {
	InvalidateBufferSession()
	plan := testPlan(t)
	calls := 0
	claim := js.FuncOf(func(_ js.Value, args []js.Value) interface{} {
		calls++
		if args[2].Length() != 2 {
			t.Fatal("missing exact UUID pair")
		}
		return true
	})
	old := js.Global().Get("wasmBufferFileMapClaim")
	js.Global().Set("wasmBufferFileMapClaim", claim)
	defer func() { js.Global().Set("wasmBufferFileMapClaim", old); claim.Release() }()
	ordinary, err := claimMessageInvocation(plan.JSON, bufferEpoch())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = claimMessageInvocation(plan.JSON, bufferEpoch()); err == nil {
		t.Fatal("buffer overtook ordinary before registration")
	}
	if calls != 1 {
		t.Fatal("conflicting operation reached the JS map", calls)
	}
	forgetBuffer(ordinary)
	fresh, err := claimMessageInvocation(plan.JSON, bufferEpoch())
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatal("explicit next operation did not claim")
	}
	forgetBuffer(fresh)
}
