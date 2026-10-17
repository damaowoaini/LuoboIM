package sensitiveword

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/OpenIMSDK/tools/log"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/redis/go-redis/v9"
)

var ErrOwnedChecker = errors.New("owned sensitive-word worker is unconfirmed")

// OwnedChecker owns the two real legacy obligations, with the same reload and
// notification channel. It deliberately uses synchronous ReceiveMessage, not
// PubSub.Channel's additional hidden goroutines. No shared pool is closed here.
type OwnedChecker struct {
	startMu          sync.Mutex
	started, closing bool
	*SensitiveWordChecker
	ctx      context.Context
	cancel   context.CancelFunc
	sub      *redis.PubSub
	wg       sync.WaitGroup
	once     sync.Once
	done     chan struct{}
	closeErr error
}

func NewOwnedSensitiveWordChecker(parent context.Context, db relation.SensitiveWordModelInterface, rdb redis.UniversalClient) (*OwnedChecker, error) {
	if parent == nil || parent.Done() == nil || db == nil || rdb == nil {
		return nil, ErrOwnedChecker
	}
	ctx, cancel := context.WithCancel(parent)
	c := &SensitiveWordChecker{db: db, rdb: rdb, words: make(map[string]bool), updateInterval: 5 * time.Minute}
	if err := c.reload(ctx); err != nil {
		cancel()
		return nil, err
	}
	sub := rdb.Subscribe(ctx, sensitiveWordReloadChannel)
	// PubSub Receive(0) may block in a read with no deadline. Bind the exact
	// initial handle to cancellation as well as a finite handshake read, and join
	// that Close callback before returning a failed/partially initialized owner.
	closeDone := make(chan struct{})
	var cancelCloseErr error
	stopClose := context.AfterFunc(ctx, func() { cancelCloseErr = sub.Close(); close(closeDone) })
	_, err := sub.ReceiveTimeout(ctx, 5*time.Second)
	if !stopClose() {
		<-closeDone
		cancel()
		return nil, errors.Join(ErrOwnedChecker, err, ctx.Err(), cancelCloseErr)
	}
	if err != nil {
		cancel()
		return nil, errors.Join(err, sub.Close())
	}
	owned := &OwnedChecker{SensitiveWordChecker: c, ctx: ctx, cancel: cancel, sub: sub, done: make(chan struct{})}
	if ownedcontrol.NamespaceRuntimeStartupFromContext(parent) == nil {
		if err := owned.StartPrepared(); err != nil {
			_ = sub.Close()
			cancel()
			return nil, err
		}
	}
	return owned, nil
}

func (c *OwnedChecker) reloadOwned() {
	ctx, cancel := context.WithTimeout(c.ctx, 20*time.Second)
	defer cancel()
	if err := c.reload(ctx); err != nil && c.ctx.Err() == nil {
		log.ZWarn(ctx, "Sensitive-word reload remains pending", nil)
	}
}
func (c *OwnedChecker) autoReloadOwned() {
	defer c.wg.Done()
	ticker := time.NewTicker(c.updateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.reloadOwned()
		}
	}
}
func (c *OwnedChecker) subscribeOwned() {
	defer c.wg.Done()
	for {
		_, err := c.sub.ReceiveMessage(c.ctx)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			// Same pubsub reconnects; no second owner or replacement pool is made.
			timer := time.NewTimer(2 * time.Second)
			select {
			case <-c.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		c.reloadOwned()
	}
}

func (c *OwnedChecker) Stop(ctx context.Context) error {
	if c == nil || ctx == nil {
		return ErrOwnedChecker
	}
	c.once.Do(func() {
		c.startMu.Lock()
		c.closing = true
		c.cancel()
		c.startMu.Unlock()
		go func() {
			c.closeErr = c.sub.Close()
			c.wg.Wait()
			close(c.done)
		}()
	})
	select {
	case <-c.done:
		return c.closeErr
	case <-ctx.Done():
		return errors.Join(ErrOwnedChecker, ctx.Err())
	}
}

// Called by the original API owner only after its exact whole-graph admission.
// The old constructor still starts these same workers immediately when there
// is no private staged context. Stop and Start share admission under one lock.
func (c *OwnedChecker) StartPrepared() error {
	if c == nil {
		return ErrOwnedChecker
	}
	c.startMu.Lock()
	defer c.startMu.Unlock()
	if c.closing || c.ctx.Err() != nil {
		return ErrOwnedChecker
	}
	if c.started {
		return nil
	}
	c.started = true
	c.wg.Add(2)
	go c.autoReloadOwned()
	go c.subscribeOwned()
	return nil
}
