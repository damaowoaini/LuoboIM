package s3

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
	"sync"

	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
)

var _ http.RoundTripper = (*OwnedTransport)(nil)

var ErrOwnedObjectLifecycle = errors.New("owned object-storage operation is unconfirmed")

type objectOperationKey struct{}
type objectOperation struct {
	owner        *OwnedTransport
	dispatched   bool
	requests     map[[32]byte]struct{}
	unconfirmed  bool
	finishSource func(bool)
}

// OwnedTransport owns a PRIVATE cloned pool. It retains no URL, headers, body,
// credentials, request pointer or arbitrary error text in source observations.
// Both wire completion and SDK-operation completion are joined before Close.
type OwnedTransport struct {
	parent        context.Context
	source        *ownedsource.Observer
	ctx           context.Context
	cancel        context.CancelFunc
	base          *http.Transport
	mu            sync.Mutex
	closing       bool
	unknown       bool
	operations    int
	closingBodies int
	closeFailed   bool
	active        map[*ownedObjectRequest]struct{}
	done          chan struct{}
	doneOnce      sync.Once
}
type ownedObjectRequest struct {
	owner        *OwnedTransport
	cancel       context.CancelFunc
	stop         func() bool
	once         sync.Once
	body         *ownedObjectBody
	mutation     bool
	operation    *objectOperation
	unconfirmed  bool
	finishSource func(bool)
}
type ownedObjectBody struct {
	io.ReadCloser
	request        *ownedObjectRequest
	complete       bool
	remaining      int64
	closeScheduled bool
	mu             sync.Mutex
}

func NewOwnedTransport(parent context.Context, template *http.Transport) (*OwnedTransport, error) {
	if parent == nil || parent.Done() == nil || parent.Err() != nil || template == nil || ownedsource.FromContext(parent) == nil {
		return nil, ErrOwnedObjectLifecycle
	}
	ctx, cancel := context.WithCancel(parent)
	return &OwnedTransport{parent: parent, source: ownedsource.FromContext(parent), ctx: ctx, cancel: cancel, base: template.Clone(), active: map[*ownedObjectRequest]struct{}{}, done: make(chan struct{})}, nil
}
func (t *OwnedTransport) BoundTo(parent context.Context) bool {
	return t != nil && parent != nil && ownedsource.FromContext(parent) != nil && ownedsource.FromContext(parent) == t.source && parent.Done() == t.parent.Done()
}
func objectMutation(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}
func (t *OwnedTransport) markUnknown() { t.mu.Lock(); t.unknown = true; t.mu.Unlock(); t.cancel() }
func (t *OwnedTransport) signalLocked() {
	if t.closing && len(t.active) == 0 && t.operations == 0 && t.closingBodies == 0 {
		t.doneOnce.Do(func() { close(t.done) })
	}
}
func (r *ownedObjectRequest) finish(unknown bool) {
	r.once.Do(func() {
		unknown = unknown || r.unconfirmed
		if r.mutation && r.operation == nil {
			r.finishSource(unknown)
		}
		r.stop()
		r.cancel()
		r.owner.mu.Lock()
		delete(r.owner.active, r)
		if unknown && r.mutation {
			r.owner.unknown = true
			if r.operation != nil {
				r.operation.unconfirmed = true
			}
		}
		r.owner.signalLocked()
		r.owner.mu.Unlock()
		if unknown && r.mutation {
			r.owner.cancel()
		}
	})
}
func (t *OwnedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t == nil || req == nil {
		return nil, ErrOwnedObjectLifecycle
	}
	mutation := objectMutation(req.Method)
	t.mu.Lock()
	if t.closing || t.ctx.Err() != nil || len(t.active) >= 1024 {
		t.mu.Unlock()
		return nil, ErrOwnedObjectLifecycle
	}
	// Multipart init/individual parts/complete have different exact targets.
	// Reject only a repeated physical mutation of the SAME target in this SDK
	// operation, even after a parsed 200 error. Keep only a transient SHA; no
	// URL/query/body is retained in the original source observer or receipts.
	op, _ := req.Context().Value(objectOperationKey{}).(*objectOperation)
	if mutation && op != nil {
		if op.owner != t {
			t.mu.Unlock()
			return nil, ErrOwnedObjectLifecycle
		}
		target := sha256.Sum256([]byte(req.Method + "\x00" + req.URL.String()))
		if _, repeated := op.requests[target]; repeated {
			op.unconfirmed = true
			t.mu.Unlock()
			t.markUnknown()
			return nil, ErrOwnedObjectLifecycle
		}
		op.requests[target] = struct{}{}
		if !op.dispatched {
			op.dispatched = true
			op.finishSource = t.source.Attempt(ownedsource.ObjectStorageMutation)
		}
	}
	ctx, cancel := context.WithCancel(req.Context())
	r := &ownedObjectRequest{owner: t, cancel: cancel, mutation: mutation, operation: op}
	r.stop = context.AfterFunc(t.ctx, cancel)
	if mutation && op == nil {
		r.finishSource = t.source.Attempt(ownedsource.ObjectStorageMutation)
	}
	t.active[r] = struct{}{}
	t.mu.Unlock()
	response, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		r.finish(true)
		return nil, err
	}
	if response == nil {
		r.finish(true)
		return nil, ErrOwnedObjectLifecycle
	}
	r.unconfirmed = mutation && (response.StatusCode < 200 || response.StatusCode >= 300)
	if response.Body == nil || response.Body == http.NoBody || response.ContentLength == 0 {
		r.finish(false)
		return response, nil
	}
	body := &ownedObjectBody{ReadCloser: response.Body, request: r, remaining: response.ContentLength}
	t.mu.Lock()
	r.body = body
	if t.closing {
		t.closeBodyLocked(body)
	}
	t.mu.Unlock()
	response.Body = body
	// A final non-success response is conservatively retained. It is never
	// promoted to an application ACK merely because its socket can be closed.
	if mutation && (response.StatusCode < 200 || response.StatusCode >= 300) {
		t.markUnknown()
	}
	return response, nil
}
func (b *ownedObjectBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	if b.remaining >= 0 {
		b.remaining -= int64(n)
	}
	b.complete = b.complete || err == io.EOF || b.remaining == 0
	complete := b.complete
	b.mu.Unlock()
	if err != nil || complete {
		b.request.finish(err != nil && err != io.EOF)
	}
	return n, err
}
func (b *ownedObjectBody) Close() error {
	err := b.ReadCloser.Close()
	b.mu.Lock()
	complete := b.complete
	b.mu.Unlock()
	b.request.finish(err != nil || !complete)
	return err
}

// Mutation observes the actual SDK result as well as its actual wire attempt.
// This catches malformed successful HTTP replies rejected by the real SDK.
// Pre-dispatch validation errors do not fabricate an unknown remote effect.
func (t *OwnedTransport) Mutation(ctx context.Context, fn func(context.Context) error) error {
	if t == nil || ctx == nil || fn == nil {
		return ErrOwnedObjectLifecycle
	}
	t.mu.Lock()
	if t.closing || t.ctx.Err() != nil {
		t.mu.Unlock()
		return ErrOwnedObjectLifecycle
	}
	t.operations++
	t.mu.Unlock()
	op := &objectOperation{owner: t, requests: map[[32]byte]struct{}{}}
	current, cancel := context.WithCancel(context.WithValue(ctx, objectOperationKey{}, op))
	stop := context.AfterFunc(t.ctx, cancel)
	defer func() {
		stop()
		cancel()
		t.mu.Lock()
		t.operations--
		t.signalLocked()
		t.mu.Unlock()
	}()
	err := fn(current)
	t.mu.Lock()
	unknown := op.unconfirmed || err != nil && op.dispatched
	finish := op.finishSource
	t.mu.Unlock()
	if finish != nil {
		finish(unknown)
	}
	if unknown {
		t.markUnknown()
	}
	return err
}

// Caller holds t.mu so a completed HTTP read cannot signal done before its
// cleanup goroutine is registered. An unreturned Close is never a joined body.
func (t *OwnedTransport) closeBodyLocked(body *ownedObjectBody) {
	if body.closeScheduled {
		return
	}
	body.closeScheduled = true
	t.closingBodies++
	go func() {
		err := body.Close()
		t.mu.Lock()
		if err != nil {
			t.closeFailed = true
		}
		t.closingBodies--
		t.signalLocked()
		t.mu.Unlock()
	}()
}
func (t *OwnedTransport) Close(ctx context.Context) error {
	if t == nil || ctx == nil {
		return ErrOwnedObjectLifecycle
	}
	t.mu.Lock()
	t.closing = true
	t.cancel()
	for r := range t.active {
		r.cancel()
		if r.body != nil {
			t.closeBodyLocked(r.body)
		}
	}
	t.signalLocked()
	t.mu.Unlock()
	t.base.CloseIdleConnections()
	select {
	case <-t.done:
		t.base.CloseIdleConnections()
		t.mu.Lock()
		failed := t.closeFailed
		t.mu.Unlock()
		if failed {
			return ErrOwnedObjectLifecycle
		}
		return nil
	case <-ctx.Done():
		return errors.Join(ErrOwnedObjectLifecycle, ctx.Err())
	}
}

// Snapshot is local liveness only. The shared original-owner source observer
// retains sticky uncertainty; a local close never clears or confirms it.
func (t *OwnedTransport) Snapshot() (active, operations, closingBodies int, unconfirmed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.active), t.operations, t.closingBodies, t.unknown
}
