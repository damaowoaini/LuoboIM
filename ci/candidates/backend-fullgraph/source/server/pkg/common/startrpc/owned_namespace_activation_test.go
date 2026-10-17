//go:build linux || darwin

package startrpc

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"google.golang.org/grpc"
)

func TestOwnedRPCNamespaceAdmissionDefersActualWorkAndStopWins(t *testing.T) {
	t.Run("queued-work-is-not-started", func(t *testing.T) {
		w := newOwnedRPCWork()
		w.deferUntilNamespaceAdmission()
		ran := make(chan struct{})
		if !w.Run(func(ctx context.Context) { close(ran); <-ctx.Done() }) {
			t.Fatal("original work rejected during preparation")
		}
		w.mu.Lock()
		active, pending := w.active, len(w.namespacePending)
		w.mu.Unlock()
		if active != 0 || pending != 1 {
			t.Fatal("queued work was reported started")
		}
		select {
		case <-ran:
			t.Fatal("actual work ran before admission")
		default:
		}
		if err := w.activateAfterNamespaceAdmission(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-ran:
		case <-time.After(time.Second):
			t.Fatal("actual original work did not start")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := w.stop(ctx); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("stop-cancels-prepared-work", func(t *testing.T) {
		w := newOwnedRPCWork()
		w.deferUntilNamespaceAdmission()
		var calls atomic.Int32
		if !w.Run(func(context.Context) { calls.Add(1) }) {
			t.Fatal("prepare")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := w.stop(ctx); err != nil {
			t.Fatal(err)
		}
		if w.activateAfterNamespaceAdmission() == nil || calls.Load() != 0 {
			t.Fatal("stop admitted a queued writer")
		}
		w.mu.Lock()
		left := len(w.namespacePending)
		w.mu.Unlock()
		if left != 0 {
			t.Fatal("canceled pending entrypoints retained")
		}
	})
	t.Run("missing-known-publication-keeps-original-hook-closed", func(t *testing.T) {
		server := grpc.NewServer()
		defer server.Stop()
		scope := &ownedRPCScope{workers: newOwnedRPCWork()}
		ownedRPCScopes.Store(server, scope)
		defer ownedRPCScopes.Delete(server)
		defer scope.workers.cancel()
		state := &ownedNamespaceStartup{gate: &ownedcontrol.NamespaceRuntimeStartupGate{}}
		ownedNamespaceStartups.Store(server, state)
		defer ownedNamespaceStartups.Delete(server)
		calls := 0
		if err := OwnRPCNamespaceActivation(server, func() error { calls++; return nil }); err != nil {
			t.Fatal(err)
		}
		if activateOwnedNamespaceResources(context.Background(), server) == nil || calls != 0 {
			t.Fatal("bare gate opened a real source callback")
		}
	})
}
