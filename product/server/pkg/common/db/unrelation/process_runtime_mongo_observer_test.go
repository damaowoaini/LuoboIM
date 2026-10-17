package unrelation

import (
	"context"
	"errors"
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
)

func TestOwnedProcessMongoRejectsUnobservedOwnerBeforeAnyClientConstruction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	client, err := NewProcessRuntimeMongo(ctx)
	if client != nil || !errors.Is(err, ownedmongo.ErrMissingObserver) {
		t.Fatal("explicit owned context constructed unobserved client", err)
	}
}
