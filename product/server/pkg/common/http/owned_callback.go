package http

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"io"
	"net/http"
	"sync"
	"time"
)

type ownedCallbackKey struct{}
type OwnedCallbackHTTP struct {
	client    *http.Client
	transport *http.Transport
	once      sync.Once
}

func NewOwnedCallbackHTTP() (*OwnedCallbackHTTP, error) {
	original, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("callback transport type is unconfirmed")
	}
	transport := original.Clone()
	return &OwnedCallbackHTTP{client: &http.Client{Transport: transport, Timeout: 15 * time.Second}, transport: transport}, nil
}
func (o *OwnedCallbackHTTP) Bind(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownedCallbackKey{}, o)
}
func (o *OwnedCallbackHTTP) Close() error {
	if o == nil || o.transport == nil {
		return errors.New("callback transport is absent")
	}
	o.once.Do(o.transport.CloseIdleConnections)
	return nil
}
func callbackClient(ctx context.Context) *http.Client {
	if o, ok := ctx.Value(ownedCallbackKey{}).(*OwnedCallbackHTTP); ok && o != nil {
		return o.client
	}
	return client
}

// Only the actual owned push assembly opts into this transport. It captures
// the original observer/lifetime once; request contexts cannot transfer owner.
func NewOwnedPushHTTP(parent context.Context, cancelOwner context.CancelFunc) (*OwnedCallbackHTTP, error) {
	owner := ownedsource.FromContext(parent)
	if parent == nil || parent.Done() == nil || owner == nil || cancelOwner == nil {
		return nil, errors.New("original push HTTP owner is required")
	}
	o, err := NewOwnedCallbackHTTP()
	if err != nil {
		return nil, err
	}
	o.client.Transport = &ownedPushRoundTripper{o.transport, parent, owner, cancelOwner}
	o.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return o, nil
}
func (o *OwnedCallbackHTTP) HTTPClient() *http.Client {
	if o == nil {
		return nil
	}
	return o.client
}

type ownedPushRoundTripper struct {
	next        http.RoundTripper
	parent      context.Context
	owner       *ownedsource.Observer
	cancelOwner context.CancelFunc
}

func (t *ownedPushRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.Context().Err() != nil || t.parent.Err() != nil {
		return nil, errors.New("original push request is not current")
	}
	ctx, cancel := context.WithCancel(req.Context())
	stop := context.AfterFunc(t.parent, cancel)
	finish := t.owner.Attempt(ownedsource.PushDelivery)
	response, err := t.next.RoundTrip(req.Clone(ctx))
	if err != nil || response == nil || response.Body == nil {
		finish(true)
		stop()
		cancel()
		t.cancelOwner()
		return response, errors.New("push HTTP outcome is unconfirmed")
	}
	body := &ownedPushBody{ReadCloser: response.Body, finish: finish, stop: stop, cancel: cancel, cancelOwner: t.cancelOwner, expected: response.ContentLength, bad: response.StatusCode < 200 || response.StatusCode >= 300}
	response.Body = body
	return response, nil
}

type ownedPushBody struct {
	io.ReadCloser
	finish              func(bool)
	stop                func() bool
	cancel, cancelOwner context.CancelFunc
	once                sync.Once
	mu                  sync.Mutex
	eof, bad            bool
	read, expected      int64
	closeErr            error
}

func (b *ownedPushBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	b.read += int64(n)
	if err == io.EOF {
		b.eof = true
	} else if err != nil {
		b.bad = true
	}
	b.mu.Unlock()
	return n, err
}
func (b *ownedPushBody) Close() error {
	b.once.Do(func() {
		b.closeErr = b.ReadCloser.Close()
		b.mu.Lock()
		unknown := b.bad || (!b.eof && (b.expected < 0 || b.read != b.expected)) || b.closeErr != nil
		b.mu.Unlock()
		b.finish(unknown)
		b.stop()
		b.cancel()
		if unknown {
			b.cancelOwner()
		}
	})
	return b.closeErr
}
