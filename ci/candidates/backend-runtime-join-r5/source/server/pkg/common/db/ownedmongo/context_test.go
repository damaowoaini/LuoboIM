package ownedmongo

import (
	"context"
	"testing"
)

func TestOwnedMongoBindingRetainsOriginalObserverAndActualLifetime(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, second := &Observer{}, &Observer{}
	if _, err := Bind(context.Background(), first); err == nil {
		t.Fatal("unowned noncancellable context accepted")
	}
	if _, err := Bind(parent, nil); err == nil {
		t.Fatal("missing observer accepted")
	}
	bound, err := Bind(parent, first)
	if err != nil || FromContext(bound) != first {
		t.Fatal("original observer not bound", err)
	}
	if _, err := Bind(bound, second); err == nil {
		t.Fatal("later observer erased original sticky state")
	}
	again, err := Bind(bound, first)
	if err != nil || again != bound {
		t.Fatal("same binding was silently replaced", err)
	}
	cancel()
	if bound.Err() != context.Canceled {
		t.Fatal("observer binding detached actual owner cancellation")
	}
}
