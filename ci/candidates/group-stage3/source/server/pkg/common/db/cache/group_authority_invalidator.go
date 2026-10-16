package cache

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/dtm-labs/rockscache"
	"github.com/redis/go-redis/v9"
)

// GroupAuthorityInvalidator owns a distinct Redis connection pool. It preserves
// the existing rockscache tag-delete contract, with real deadline-aware socket
// options rather than assuming the legacy shared client's ctx is honored.
type GroupAuthorityInvalidator struct {
	client   redis.UniversalClient
	cache    *rockscache.Client
	once     sync.Once
	closeErr error
}

func NewGroupAuthorityInvalidator(base redis.UniversalClient) (*GroupAuthorityInvalidator, error) {
	var bounded redis.UniversalClient
	switch original := base.(type) {
	case *redis.Client:
		if original == nil {
			return nil, errors.New("group cache client unavailable")
		}
		opts := *original.Options()
		opts.ContextTimeoutEnabled = true
		opts.DialTimeout = time.Second
		opts.ReadTimeout = time.Second
		opts.WriteTimeout = time.Second
		opts.PoolTimeout = time.Second
		opts.MaxRetries = -1
		bounded = redis.NewClient(&opts)
	case *redis.ClusterClient:
		if original == nil {
			return nil, errors.New("group cache client unavailable")
		}
		opts := *original.Options()
		opts.ContextTimeoutEnabled = true
		opts.DialTimeout = time.Second
		opts.ReadTimeout = time.Second
		opts.WriteTimeout = time.Second
		opts.PoolTimeout = time.Second
		opts.MaxRetries = -1
		opts.MaxRedirects = -1
		bounded = redis.NewClusterClient(&opts)
	default:
		return nil, errors.New("unsupported group cache client")
	}
	return &GroupAuthorityInvalidator{client: bounded, cache: rockscache.NewClient(bounded, GetDefaultOpt())}, nil
}
func (i *GroupAuthorityInvalidator) CheckReady(ctx context.Context) error {
	if i == nil || i.client == nil || ctx == nil {
		return errors.New("group cache client unavailable")
	}
	current, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return i.client.Ping(current).Err()
}
func (i *GroupAuthorityInvalidator) Apply(ctx context.Context, key string) error {
	if i == nil || i.cache == nil || ctx == nil || ctx.Err() != nil {
		return errors.New("group cache invalidation unavailable")
	}
	allowed := false
	for _, prefix := range []string{"GROUP_INFO:", "GROUP_MEMBER_IDS:", "GROUP_MEMBERS_HASH2:", "GROUP_MEMBER_INFO:", "JOIN_GROUPS_KEY:", "GROUP_MEMBER_NUM_CACHE:", "GROUP_ROLE_LEVEL_MEMBER_IDS:"} {
		if strings.HasPrefix(key, prefix) && len(key) > len(prefix) {
			allowed = true
			break
		}
	}
	if !allowed || len(key) > 1024 || strings.ContainsRune(key, 0) {
		return errors.New("invalid group cache namespace")
	}
	current, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return i.cache.TagAsDeleted2(current, key)
}
func (i *GroupAuthorityInvalidator) Close() error {
	if i == nil {
		return nil
	}
	i.once.Do(func() {
		if i.client != nil {
			i.closeErr = i.client.Close()
		}
	})
	return i.closeErr
}
