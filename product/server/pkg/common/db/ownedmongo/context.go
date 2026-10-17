package ownedmongo

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
)

type ownerKey struct{}

var ErrMissingObserver = errors.New("original owned Mongo observer is required")

// Binding observes the caller's original resources. It creates no database,
// role, permission, namespace, Ready flag or cross-owner recovery capability.
func Bind(parent context.Context, observer *Observer) (context.Context, error) {
	if parent == nil || parent.Done() == nil || observer == nil {
		return nil, ErrMissingObserver
	}
	if previous := FromContext(parent); previous != nil {
		if previous != observer {
			return nil, ErrMissingObserver
		}
		return parent, nil
	}
	bound, err := ownedsource.Bind(parent, &observer.external)
	if err != nil {
		return nil, err
	}
	return context.WithValue(bound, ownerKey{}, observer), nil
}
func FromContext(ctx context.Context) *Observer {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(ownerKey{}).(*Observer)
	return value
}
