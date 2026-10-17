package push

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/dummy"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/fcm"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/getui"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/jpush"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	http2 "github.com/openimsdk/open-im-server/v3/pkg/common/http"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"sync"
)

func newOwnedOfflinePusher(ctx context.Context, cache cache.MsgModel, httpOwner *http2.OwnedCallbackHTTP, cancelOwner context.CancelFunc) (offlinepush.OfflinePusher, func(context.Context) error, error) {
	if ctx == nil || ownedsource.FromContext(ctx) == nil || httpOwner == nil || cancelOwner == nil {
		return nil, nil, errOwnedPush
	}
	var provider offlinepush.OfflinePusher
	switch config.Config.Push.Enable {
	case "getui":
		provider = getui.NewClient(cache)
	case "jpush":
		provider = jpush.NewClient()
	case "fcm":
		owned, err := fcm.NewOwnedClient(ctx, cache, httpOwner, cancelOwner)
		if err != nil {
			return nil, nil, err
		}
		provider = owned
	default:
		provider = dummy.NewClient()
	}
	if provider == nil {
		return nil, nil, errOwnedPush
	}
	// No provider background loop is invented. The actual wrapper closes its
	// synchronous invocation gate, joins original calls and releases its adapter;
	// its HTTP/OAuth sockets belong solely to callback-http, closed afterwards.
	wrapped := newOwnedProvider(provider, ownedsource.FromContext(ctx), cancelOwner)
	return wrapped, wrapped.Close, nil
}

type ownedProvider struct {
	mu       sync.Mutex
	original offlinepush.OfflinePusher
	owner    *ownedsource.Observer
	cancel   context.CancelFunc
	closing  bool
	active   int
	done     chan struct{}
}

func newOwnedProvider(original offlinepush.OfflinePusher, owner *ownedsource.Observer, cancel context.CancelFunc) *ownedProvider {
	return &ownedProvider{original: original, owner: owner, cancel: cancel, done: make(chan struct{})}
}
func (p *ownedProvider) Close(ctx context.Context) error {
	p.mu.Lock()
	if !p.closing {
		p.closing = true
		if p.active == 0 {
			close(p.done)
		}
	}
	p.mu.Unlock()
	select {
	case <-p.done:
		p.mu.Lock()
		p.original = nil
		p.mu.Unlock()
		return nil
	case <-ctx.Done():
		return errOwnedPush
	}
}
func (p *ownedProvider) Push(ctx context.Context, users []string, title, content string, opts *offlinepush.Opts) error {
	if ctx == nil || ctx.Err() != nil || opts == nil || opts.Signal == nil {
		return errOwnedPush
	}
	p.mu.Lock()
	if p.closing || p.original == nil {
		p.mu.Unlock()
		return errOwnedPush
	}
	original := p.original
	p.active++
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.active--
		if p.closing && p.active == 0 {
			close(p.done)
		}
		p.mu.Unlock()
	}()
	before := p.owner.Snapshot().PushDelivery.Started
	err := original.Push(ctx, users, title, content, opts)
	if err != nil && p.owner.Snapshot().PushDelivery.Started != before {
		// Covers a 200 body that is syntactically unreadable/SDK-rejected after an
		// actual physical attempt. A preflight-only error does not invent a send.
		p.owner.Attempt(ownedsource.PushDelivery)(true)
		p.cancel()
		return errors.Join(errOwnedPush, err)
	}
	return err
}
