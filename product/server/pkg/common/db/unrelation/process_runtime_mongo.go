package unrelation

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
)

// The nil-context legacy constructor is untouched. An explicit owned lifetime
// reuses the existing finite managed attempt and returns its actual client even
// on partial initialization failure so the original owner can close it.
func NewProcessRuntimeMongo(owned context.Context) (*Mongo, error) {
	if owned == nil {
		return NewMongo()
	}
	if ownedmongo.FromContext(owned) == nil {
		return nil, ownedmongo.ErrMissingObserver
	}
	return NewManagedMongo(owned)
}
