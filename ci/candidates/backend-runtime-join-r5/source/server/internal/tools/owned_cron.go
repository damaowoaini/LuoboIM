package tools

import (
	"context"
	"errors"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"github.com/redis/go-redis/v9"
	"github.com/robfig/cron/v3"
)

var errOwnedCron = errors.New("owned cron lifecycle is unconfirmed")

var ownedCronSteps = []string{"cron-admission", "cron-jobs", "kafka-producers", "redis", "mongo", "discovery"}

type ownedCronIdentity interface {
	CheckDatabase(string) error
	Announce([]string) error
	Drain([]string) error
}

type ownedCronResources struct {
	locker                                                 redis.UniversalClient
	jobs                                                   [2]func(context.Context) error
	closeProducers, closeRedis, closeMongo, closeDiscovery func(context.Context) error
}

type ownedCronFactory func(context.Context, func(string) error) (*ownedCronResources, error)

// The ordinary startup remains unchanged. Only the original explicit private
// bootstrap can select this owner, its actual database and its private receipts.
func StartOwnedTask() error {
	lifetime, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer cancel()
	identity, err := startrpc.LoadOwnedServerIdentity("crontask")
	if err != nil {
		return err
	}
	source := &ownedmongo.Observer{}
	if err := identity.BindSourceObserver(source); err != nil {
		return err
	}
	bound, err := ownedmongo.Bind(lifetime, source)
	if err != nil {
		return err
	}
	return runOwnedCron(bound, identity, newOwnedCronResources,
		[2]string{config.Config.ChatRecordsClearTime, config.Config.MsgDestructTime}, config.Config.EnableCronLocker)
}

// This owns the actual cron scheduler. Stop closes its admission and joins its
// original jobs before any producer, storage pool or RPC discovery is closed.
func runOwnedCron(lifetime context.Context, identity ownedCronIdentity, build ownedCronFactory, schedules [2]string, locking bool) (resultErr error) {
	if lifetime == nil || lifetime.Done() == nil || lifetime.Err() != nil || identity == nil || build == nil {
		return errOwnedCron
	}
	work, cancel := context.WithCancel(lifetime)
	defer cancel()
	var resources *ownedCronResources
	var scheduler *cron.Cron
	var mu sync.Mutex
	stopping := false
	var jobErr error
	jobFailure := make(chan error, 1)
	fail := func(err error) {
		mu.Lock()
		if jobErr == nil {
			jobErr = err
		}
		mu.Unlock()
		select {
		case jobFailure <- err:
		default:
		}
	}
	var stopOnce sync.Once
	var stopErr error
	var completed []string
	stop := func() error {
		stopOnce.Do(func() {
			join, done := context.WithTimeout(context.Background(), 30*time.Second)
			defer done()
			mu.Lock()
			stopping = true
			mu.Unlock()
			if scheduler != nil {
				joined := scheduler.Stop()
				completed = append(completed, "cron-admission")
				cancel()
				select {
				case <-joined.Done():
					completed = append(completed, "cron-jobs")
				case <-join.Done():
					stopErr = errors.Join(errOwnedCron, join.Err())
					return
				}
			} else {
				cancel()
			}
			if resources == nil {
				return
			}
			for _, item := range []struct {
				name  string
				close func(context.Context) error
			}{
				{"kafka-producers", resources.closeProducers}, {"redis", resources.closeRedis},
				{"mongo", resources.closeMongo}, {"discovery", resources.closeDiscovery},
			} {
				if item.close == nil {
					continue
				} // only actual partial handles exist on failed startup
				if err := join.Err(); err != nil {
					stopErr = errors.Join(errOwnedCron, err)
					return
				}
				if err := item.close(join); err != nil {
					stopErr = errors.Join(errOwnedCron, err)
					return
				}
				if err := join.Err(); err != nil {
					stopErr = errors.Join(errOwnedCron, err)
					return
				}
				completed = append(completed, item.name)
			}
		})
		return stopErr
	}
	defer func() { resultErr = errors.Join(resultErr, stop()) }()
	var err error
	resources, err = build(work, identity.CheckDatabase)
	if err != nil {
		return err
	}
	if resources == nil || resources.jobs[0] == nil || resources.jobs[1] == nil ||
		resources.closeProducers == nil || resources.closeRedis == nil || resources.closeMongo == nil || resources.closeDiscovery == nil ||
		(locking && resources.locker == nil) || work.Err() != nil {
		return errOwnedCron
	}
	scheduler = cron.New()
	keys := [2]string{"cron_clear_msg_and_fix_seq", "cron_conversations_destruct_msgs"}
	for index, schedule := range schedules {
		job, key := resources.jobs[index], keys[index]
		_, err = scheduler.AddFunc(schedule, func() {
			mu.Lock()
			rejected := stopping || work.Err() != nil
			mu.Unlock()
			if rejected {
				return
			}
			defer func() {
				if recover() != nil {
					fail(errOwnedCron)
				}
			}()
			if locking {
				ok, err := resources.locker.SetNX(work, key, "used", 5*time.Second).Result()
				if err != nil || !ok {
					return
				} // preserve the existing non-renewing lock policy
			}
			if err := job(work); err != nil {
				fail(err)
			}
		})
		if err != nil {
			return err
		}
	}
	if work.Err() != nil {
		return errOwnedCron
	}
	scheduler.Start()
	if err := identity.Announce(append([]string{}, ownedCronSteps...)); err != nil {
		return err
	}
	select {
	case <-lifetime.Done():
	case resultErr = <-jobFailure:
	}
	if err := stop(); err != nil {
		return errors.Join(resultErr, err)
	}
	mu.Lock()
	failedJob := jobErr
	mu.Unlock()
	resultErr = errors.Join(resultErr, failedJob)
	if resultErr != nil {
		return resultErr
	}
	// The shared identity reads the very same observer after actual clients close.
	// An unknown Mongo outcome writes failure custody and cannot create a drain.
	return identity.Drain(append([]string{}, completed...))
}
