package api

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

// Exercises the original listener/Serve/shutdown function. The assembly seam
// below supplies no DB, signed permit or production readiness evidence.
func TestOwnedAPINamespaceAdmissionPrecedesActualWorkerAndServe(t *testing.T) {
	for _, mode := range []string{"release-original-resources", "cancel-while-prepared"} {
		t.Run(mode, func(t *testing.T) {
			old := config.Config.Api.ListenIP
			config.Config.Api.ListenIP = "127.0.0.1"
			defer func() { config.Config.Api.ListenIP = old }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owner := &apiOwnerFixture{ready: make(chan struct{})}
			entered, release := make(chan struct{}), make(chan struct{})
			var activated, handled, closes atomic.Int32
			closeResource := func(context.Context) error { closes.Add(1); return nil }
			assembly := &ownedAPIAssembly{source: &ownedMongoSource{}, router: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { handled.Add(1) }), stopLogout: closeResource, stopSensitive: closeResource, closeValidationHTTP: closeResource, closeRedis: closeResource, closeMongo: closeResource, closeDiscovery: closeResource, activate: func() error { activated.Add(1); return nil }, awaitNamespace: func(call context.Context, steps []string) error {
				if len(steps) != 9 {
					return errOwnedAPI
				}
				close(entered)
				select {
				case <-release:
					return nil
				case <-call.Done():
					return call.Err()
				}
			}}
			result := make(chan error, 1)
			go func() {
				result <- startOwnedAPI(ctx, 0, 0, owner, func(context.Context) (*ownedAPIAssembly, error) { return assembly, nil })
			}()
			ownedAPIWait(t, entered)
			if assembly.address == "" || activated.Load() != 0 || handled.Load() != 0 {
				t.Fatal("prepared stage ran original business work")
			}
			select {
			case <-owner.ready:
				t.Fatal("preparation announced a serving owner")
			default:
			}
			if mode == "release-original-resources" {
				close(release)
				ownedAPIWait(t, owner.ready)
				if activated.Load() != 1 {
					t.Fatal("original workers were not started exactly once")
				}
				cancel()
				if err := ownedAPIResult(t, result); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
				if err := ownedAPIResult(t, result); err == nil {
					t.Fatal("canceled admission reported success")
				}
				if activated.Load() != 0 {
					t.Fatal("late activation after cancellation")
				}
			}
			if closes.Load() != 6 {
				t.Fatal("original partial or running resource owner did not close once")
			}
			owner.mu.Lock()
			announced, drained := len(owner.announced), len(owner.drained)
			owner.mu.Unlock()
			if mode == "cancel-while-prepared" && (announced != 0 || drained != 0) {
				t.Fatal("prepared cleanup was falsely certified serving/drained")
			}
		})
	}
}
