package tools

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestOwnedCronNamespaceAdmissionPrecedesActualScheduler(t *testing.T) {
	for _, mode := range []string{"release-original-job", "cancel-while-prepared"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			identity := &ownedCronIdentityFixture{ready: make(chan struct{})}
			prepared, release, entered := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var calls, closes atomic.Int32
			closeResource := func(context.Context) error { closes.Add(1); return nil }
			factory := func(original context.Context, check func(string) error) (*ownedCronResources, error) {
				if err := check("synthetic-original-cron"); err != nil {
					return nil, err
				}
				job := func(call context.Context) error {
					if call != original {
						return errOwnedCron
					}
					calls.Add(1)
					once.Do(func() { close(entered) })
					<-call.Done()
					return nil
				}
				return &ownedCronResources{jobs: [2]func(context.Context) error{job, job}, closeProducers: closeResource, closeRedis: closeResource, closeMongo: closeResource, closeDiscovery: closeResource, awaitNamespace: func(call context.Context) error {
					close(prepared)
					select {
					case <-release:
						return nil
					case <-call.Done():
						return call.Err()
					}
				}}, nil
			}
			result := make(chan error, 1)
			go func() { result <- runOwnedCron(ctx, identity, factory, [2]string{"@every 1s", "@every 1s"}, false) }()
			select {
			case <-prepared:
			case <-time.After(3 * time.Second):
				t.Fatal("actual preparation did not reach barrier")
			}
			if calls.Load() != 0 || closes.Load() != 0 {
				t.Fatal("prepared stage ran a job or closed its pool")
			}
			select {
			case <-identity.ready:
				t.Fatal("scheduler announced before admission")
			default:
			}
			if mode == "release-original-job" {
				close(release)
				select {
				case <-entered:
				case <-time.After(3 * time.Second):
					t.Fatal("real scheduler did not run original job")
				}
				cancel()
			} else {
				cancel()
			}
			select {
			case err := <-result:
				if mode == "release-original-job" && err != nil || mode == "cancel-while-prepared" && err == nil {
					t.Fatal("actual cancellation/start result lost", err)
				}
			case <-time.After(4 * time.Second):
				t.Fatal("original scheduler/resources did not join")
			}
			if closes.Load() != 4 {
				t.Fatal("original pools not joined exactly once")
			}
			identity.mu.Lock()
			announced, drained := len(identity.announced), len(identity.drained)
			identity.mu.Unlock()
			if mode == "cancel-while-prepared" && (calls.Load() != 0 || announced != 0 || drained != 0) {
				t.Fatal("prepared cleanup minted scheduler/drain evidence")
			}
		})
	}
}
