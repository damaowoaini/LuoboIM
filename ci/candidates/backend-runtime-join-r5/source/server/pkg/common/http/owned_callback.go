package http

import (
	"context"
	"errors"
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
