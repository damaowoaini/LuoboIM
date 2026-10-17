package cache

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/redis/go-redis/v9"
)

// A nil context preserves the original singleton path. Explicit source-owned
// RPC lifetimes reuse the already existing independent bounded constructor;
// they never take ownership of another caller's singleton or mutate that API.
func NewProcessRuntimeRedis(owned context.Context) (redis.UniversalClient, error) {
	if owned == nil {
		return NewRedis()
	}
	if ownedsource.FromContext(owned) == nil {
		return nil, ErrOwnedRedis
	}
	return NewGroupRuntimeRedis(owned)
}
