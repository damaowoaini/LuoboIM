package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"go.mongodb.org/mongo-driver/event"
)

// This boundary fake owns no DB or permission. The test runs the production
// HTTP listener, admission, handlers, Serve/Shutdown and stop ordering; the real
// DB-backed scopes/worker subscription have separate integration controls.
type apiOwnerFixture struct {
	ready              chan struct{}
	mu                 sync.Mutex
	announced, drained []string
	unknown            []ownedcontrol.SourceObservation
}

func (o *apiOwnerFixture) Announce(steps []string) error {
	o.mu.Lock()
	o.announced = append([]string{}, steps...)
	o.mu.Unlock()
	close(o.ready)
	return nil
}
func (o *apiOwnerFixture) Drain(steps []string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.drained = append([]string{}, steps...)
	return nil
}
func (o *apiOwnerFixture) RecordUnconfirmed(v ownedcontrol.SourceObservation) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.unknown = append(o.unknown, v)
	return nil
}
func ownedAPIWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("owned API barrier did not complete")
	}
}
func ownedAPIResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(4 * time.Second):
		t.Fatal("owned API did not stop")
		return nil
	}
}

func TestOwnedAPIActualHTTPWaitsForInflightHandlerThenClosesOriginalResources(t *testing.T) {
	old := config.Config.Api.ListenIP
	config.Config.Api.ListenIP = "127.0.0.1"
	t.Cleanup(func() { config.Config.Api.ListenIP = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &apiOwnerFixture{ready: make(chan struct{})}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	var mu sync.Mutex
	var order []string
	record := func(name string) func(context.Context) error {
		return func(context.Context) error { mu.Lock(); order = append(order, name); mu.Unlock(); return nil }
	}
	assembly := &ownedAPIAssembly{source: &ownedMongoSource{}, router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(204) }), stopLogout: record("logout"), stopSensitive: record("sensitive"), closeValidationHTTP: record("http-validator"), closeRedis: record("redis"), closeMongo: record("mongo"), closeDiscovery: record("discovery")}
	result, finished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		result <- startOwnedAPI(ctx, 0, 0, owner, func(context.Context) (*ownedAPIAssembly, error) { return assembly, nil })
	}()
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-finished:
		case <-time.After(4 * time.Second):
			t.Error("owned HTTP cleanup did not join")
		}
	})
	ownedAPIWait(t, owner.ready)
	response := make(chan error, 1)
	client := &http.Client{Timeout: 3 * time.Second}
	go func() {
		deadline, tick := time.NewTimer(3*time.Second), time.NewTicker(time.Millisecond)
		defer deadline.Stop()
		defer tick.Stop()
		for {
			r, err := client.Get("http://" + assembly.address + "/actual-handler")
			if err != nil {
				response <- err
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			r.Body.Close()
			if r.StatusCode == 204 {
				response <- nil
				return
			}
			if r.StatusCode != 503 {
				response <- errors.New("original handler status changed")
				return
			}
			// Ready-file publication precedes admitting requests. Only a 503
			// from that real gate may be retried; admitted work executes once.
			select {
			case <-tick.C:
			case <-deadline.C:
				response <- errors.New("actual HTTP admission did not enable")
				return
			}
		}
	}()
	ownedAPIWait(t, entered)
	cancel()
	// The first HTTP handler itself is the barrier: no resource may close or
	// canonical drain appear while that original handler still owns its work.
	mu.Lock()
	prematureResource := len(order) != 0
	mu.Unlock()
	if prematureResource {
		t.Fatal("resource closed before handler release")
	}
	owner.mu.Lock()
	prematureDrain := owner.drained != nil
	owner.mu.Unlock()
	if prematureDrain {
		t.Fatal("premature drain")
	}
	releaseOnce.Do(func() { close(release) })
	if err := ownedAPIResult(t, response); err != nil {
		t.Fatal(err)
	}
	if err := ownedAPIResult(t, result); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(order, []string{"logout", "sensitive", "http-validator", "redis", "mongo", "discovery"}) {
		t.Fatal("actual resource stop ordering changed", order)
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if !reflect.DeepEqual(owner.drained, owner.announced) || len(owner.unknown) != 0 {
		t.Fatal("drain does not bind actual completed steps")
	}
}

func TestOwnedAPIStartCancellationClosesPartialActualHandlesWithoutAnnouncement(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &apiOwnerFixture{ready: make(chan struct{})}
	entered := make(chan struct{})
	var order []string
	stop := func(name string) func(context.Context) error {
		return func(context.Context) error { order = append(order, name); return nil }
	}
	result, finished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		result <- startOwnedAPI(ctx, 0, 0, owner, func(init context.Context) (*ownedAPIAssembly, error) {
			close(entered)
			<-init.Done()
			return &ownedAPIAssembly{closeRedis: stop("redis"), closeMongo: stop("mongo"), closeDiscovery: stop("discovery")}, init.Err()
		})
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(4 * time.Second):
			t.Error("partial startup cleanup did not join")
		}
	})
	ownedAPIWait(t, entered)
	cancel()
	if err := ownedAPIResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatal("actual startup cancellation was not retained", err)
	}
	if !reflect.DeepEqual(order, []string{"redis", "mongo", "discovery"}) {
		t.Fatal("partial handles were not closed exactly once", order)
	}
	if owner.announced != nil || owner.drained != nil {
		t.Fatal("partial start fabricated ready/drain")
	}
}

func TestOwnedAPIWorkerCloseFailureRetainsLaterPoolsAndRefusesDrain(t *testing.T) {
	old := config.Config.Api.ListenIP
	config.Config.Api.ListenIP = "127.0.0.1"
	t.Cleanup(func() { config.Config.Api.ListenIP = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &apiOwnerFixture{ready: make(chan struct{})}
	var mu sync.Mutex
	var order []string
	stop := func(name string) func(context.Context) error {
		return func(context.Context) error { mu.Lock(); defer mu.Unlock(); order = append(order, name); return nil }
	}
	assembly := &ownedAPIAssembly{source: &ownedMongoSource{}, router: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), stopLogout: func(context.Context) error { return context.DeadlineExceeded }, stopSensitive: stop("sensitive"), closeValidationHTTP: stop("http-validator"), closeRedis: stop("redis"), closeMongo: stop("mongo"), closeDiscovery: stop("discovery")}
	result, finished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		result <- startOwnedAPI(ctx, 0, 0, owner, func(context.Context) (*ownedAPIAssembly, error) { return assembly, nil })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(4 * time.Second):
			t.Error("failed-stop cleanup did not join")
		}
	})
	ownedAPIWait(t, owner.ready)
	cancel()
	if err := ownedAPIResult(t, result); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("close failure hidden", err)
	}
	if len(order) != 0 || owner.drained != nil {
		t.Fatal("unjoined worker allowed pool close or drain")
	}
}

func TestOwnedAPIUnconfirmedWriteClosesHandlesButRetainsFailureObservationWithoutDrain(t *testing.T) {
	old := config.Config.Api.ListenIP
	config.Config.Api.ListenIP = "127.0.0.1"
	t.Cleanup(func() { config.Config.Api.ListenIP = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &apiOwnerFixture{ready: make(chan struct{})}
	var order []string
	stop := func(name string) func(context.Context) error {
		return func(context.Context) error { order = append(order, name); return nil }
	}
	source := &ownedMongoSource{}
	monitor := source.monitor()
	monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "update", ConnectionID: "fixture", RequestID: 1})
	monitor.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "update", ConnectionID: "fixture", RequestID: 1}})
	assembly := &ownedAPIAssembly{source: source, router: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), stopLogout: stop("logout"), stopSensitive: stop("sensitive"), closeValidationHTTP: stop("http-validator"), closeRedis: stop("redis"), closeMongo: stop("mongo"), closeDiscovery: stop("discovery")}
	result, finished := make(chan error, 1), make(chan struct{})
	go func() {
		defer close(finished)
		result <- startOwnedAPI(ctx, 0, 0, owner, func(context.Context) (*ownedAPIAssembly, error) { return assembly, nil })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(4 * time.Second):
			t.Error("unconfirmed-source cleanup did not join")
		}
	})
	ownedAPIWait(t, owner.ready)
	cancel()
	if err := ownedAPIResult(t, result); !errors.Is(err, errOwnedAPI) {
		t.Fatal("unknown source reported successful shutdown", err)
	}
	if len(order) != 6 || owner.drained != nil || len(owner.unknown) != 1 || !owner.unknown[0].SourceOutcomeUnconfirmed {
		t.Fatal("unknown observation lost or false drain created")
	}
}
