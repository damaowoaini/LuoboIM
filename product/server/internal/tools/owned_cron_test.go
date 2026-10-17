package tools

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"reflect"
	"sync"
	"testing"
	"time"
)

// These controls use the actual cron scheduler and original job context. The
// resource callbacks are explicit close-boundary fixtures, not DB/ACK evidence.
type ownedCronIdentityFixture struct {
	mu                 sync.Mutex
	ready              chan struct{}
	database           string
	announced, drained []string
}

func (i *ownedCronIdentityFixture) CheckDatabase(name string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if name != "synthetic-original-cron" || (i.database != "" && i.database != name) {
		return errOwnedCron
	}
	i.database = name
	return nil
}
func (i *ownedCronIdentityFixture) Announce(steps []string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.database == "" || i.announced != nil || !reflect.DeepEqual(steps, ownedCronSteps) {
		return errOwnedCron
	}
	i.announced = append([]string{}, steps...)
	close(i.ready)
	return nil
}
func (i *ownedCronIdentityFixture) Drain(steps []string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if !reflect.DeepEqual(steps, i.announced) || i.drained != nil {
		return errOwnedCron
	}
	i.drained = append([]string{}, steps...)
	return nil
}

func TestOwnedCronActualSchedulerJoinsOriginalJobsBeforePoolsAndRefusesFailedDrain(t *testing.T) {
	for _, variant := range []string{"joined", "producer-close-failure", "job-error-after-cancel", "partial-constructor-failure"} {
		t.Run(variant, func(t *testing.T) {
			identity := &ownedCronIdentityFixture{ready: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			entered, canceled, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var enterOnce, canceledOnce, releaseOnce sync.Once
			var mu sync.Mutex
			var closes []string
			failure := errors.New("synthetic original cron failure")
			builds := 0
			build := func(original context.Context, check func(string) error) (*ownedCronResources, error) {
				builds++
				if original == nil || original.Done() == nil {
					return nil, errOwnedCron
				}
				if err := check("synthetic-original-cron"); err != nil {
					return nil, err
				}
				closeFn := func(name string) func(context.Context) error {
					return func(context.Context) error {
						mu.Lock()
						closes = append(closes, name)
						mu.Unlock()
						if name == "kafka-producers" && variant == "producer-close-failure" {
							return failure
						}
						return nil
					}
				}
				resources := &ownedCronResources{closeProducers: closeFn("kafka-producers"), closeRedis: closeFn("redis"), closeMongo: closeFn("mongo"), closeDiscovery: closeFn("discovery")}
				if variant == "partial-constructor-failure" {
					return resources, failure
				}
				job := func(call context.Context) error {
					if call != original {
						return errors.New("job replaced the original owner context")
					}
					enterOnce.Do(func() { close(entered) })
					<-call.Done()
					canceledOnce.Do(func() { close(canceled) })
					<-release
					if variant == "job-error-after-cancel" {
						return failure
					}
					return nil
				}
				resources.jobs = [2]func(context.Context) error{job, job}
				return resources, nil
			}
			result := make(chan error, 1)
			t.Cleanup(func() {
				cancel()
				releaseOnce.Do(func() { close(release) })
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("actual cron owner did not join after releasing original jobs")
				}
			})
			go func() {
				defer close(finished)
				result <- runOwnedCron(ctx, identity, build, [2]string{"@every 1s", "@every 1s"}, false)
			}()
			if variant != "partial-constructor-failure" {
				select {
				case <-identity.ready:
				case <-time.After(5 * time.Second):
					t.Fatal("actual cron owner did not announce")
				}
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("real scheduler did not start original job")
				}
				cancel()
				select {
				case <-canceled:
				case <-time.After(3 * time.Second):
					t.Fatal("actual job context was not canceled")
				}
				mu.Lock()
				premature := len(closes) != 0
				mu.Unlock()
				identity.mu.Lock()
				drained := len(identity.drained) != 0
				identity.mu.Unlock()
				if premature || drained {
					t.Fatal("a pool closed or drain appeared before the actual scheduled job joined")
				}
			}
			releaseOnce.Do(func() { close(release) })
			var err error
			select {
			case err = <-result:
			case <-time.After(5 * time.Second):
				t.Fatal("actual stopped scheduler/closer did not return")
			}
			mu.Lock()
			actual := append([]string{}, closes...)
			mu.Unlock()
			identity.mu.Lock()
			announced := append([]string{}, identity.announced...)
			drained := append([]string{}, identity.drained...)
			identity.mu.Unlock()
			if builds != 1 {
				t.Fatal("a failed constructor was replayed")
			}
			if variant == "producer-close-failure" {
				if !errors.Is(err, failure) || !reflect.DeepEqual(actual, []string{"kafka-producers"}) || len(drained) != 0 {
					t.Fatal("failed producer close was hidden or later pools closed", err, actual)
				}
			} else {
				if !reflect.DeepEqual(actual, []string{"kafka-producers", "redis", "mongo", "discovery"}) {
					t.Fatal("actual resource close order changed", actual)
				}
				if variant == "joined" {
					if err != nil || !reflect.DeepEqual(drained, ownedCronSteps) {
						t.Fatal("actual cron join did not produce exact completion", err, drained)
					}
				} else if !errors.Is(err, failure) || len(drained) != 0 {
					t.Fatal("failed constructor/job produced a drain", err, drained)
				}
			}
			if variant == "partial-constructor-failure" && len(announced) != 0 {
				t.Fatal("partial construction announced ready")
			}
		})
	}
}

type ownedCronConversationFixture struct {
	controller.ConversationDatabase
	want  context.Context
	calls int
}

func (c *ownedCronConversationFixture) GetAllConversationIDsNumber(ctx context.Context) (int64, error) {
	if ctx != c.want {
		return 0, errors.New("actual cron query replaced the owner context")
	}
	c.calls++
	return 0, nil
}

func TestOwnedCronActualCleanupQueryKeepsOwnerContextAndStopsCanceledDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	query := &ownedCronConversationFixture{want: ctx}
	tool := &MsgTool{conversationDatabase: query}
	tool.allConversationClearMsgAndFixSeq(ctx)
	if query.calls != 1 {
		t.Fatal("actual cleanup did not use the original cancellable query context")
	}
	cancel()
	tool.allConversationClearMsgAndFixSeq(ctx)
	if query.calls != 1 {
		t.Fatal("a canceled cron job dispatched another actual query")
	}
}
