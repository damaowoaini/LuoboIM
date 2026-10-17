package cache

import (
	"context"
	"github.com/OpenIMSDK/tools/errs"
	"sort"
	"strconv"
	"time"

	"github.com/OpenIMSDK/protocol/encryption"
	"github.com/dtm-labs/rockscache"
	"github.com/redis/go-redis/v9"
)

const (
	EncryptionKeyPrefix = "CON_ENCRYPTION_KEY:"
	MaxVersionKeyField  = "MAX_VERSION"
)

type EncryptionCache interface {
	metaCache
	GetConversationEncryptionAllKey(ctx context.Context, conversationID string) ([]*encryption.VersionKey, error)
	GetConversationVersionKey(ctx context.Context, conversationID string, version int32) (string, error)
	GenConversationNextVersionKey(ctx context.Context, conversationID string, fn func() (string, error)) error
	GetConversationMaxVersionKey(ctx context.Context, conversationID string) (*encryption.VersionKey, error)
}
type EncryptionCacheRedis struct {
	metaCache
	expireTime time.Duration
	rdb        redis.UniversalClient
	rcClient   *rockscache.Client
}

func NewEncryptionCacheRedis(
	rdb redis.UniversalClient,
	options rockscache.Options,
) EncryptionCache {
	rcClient := rockscache.NewClient(rdb, options)
	return &EncryptionCacheRedis{
		metaCache: NewMetaCacheRedis(rcClient),
		rcClient:  rcClient,
		rdb:       rdb,
	}
}
func (e EncryptionCacheRedis) getConversationKey(conversationID string) string {
	return EncryptionKeyPrefix + conversationID
}
func (e EncryptionCacheRedis) GetConversationEncryptionAllKey(ctx context.Context, conversationID string) ([]*encryption.VersionKey, error) {
	result, err := e.rdb.HGetAll(ctx, e.getConversationKey(conversationID)).Result()
	if err != nil {
		return nil, err
	}
	var versionKeys []*encryption.VersionKey
	for version, key := range result {
		if version != MaxVersionKeyField {
			v, err := strconv.Atoi(version)
			if err != nil || v < 1 || int64(v) > 2147483647 {
				continue
			}
			versionKeys = append(versionKeys, &encryption.VersionKey{
				Version: int32(v),
				Key:     key,
			})
		}
	}
	sort.Slice(versionKeys, func(i, j int) bool { return versionKeys[i].Version < versionKeys[j].Version })
	return versionKeys, nil
}

func (e EncryptionCacheRedis) GetConversationVersionKey(ctx context.Context, conversationID string, version int32) (string, error) {
	result, err := e.rdb.HGet(ctx, e.getConversationKey(conversationID), strconv.Itoa(int(version))).Result()
	if err != nil {
		return "", err
	}
	return result, nil
}

const allocateEncryptionVersion = `
local maximum = redis.call('HGET',KEYS[1],'MAX_VERSION')
local version = 0
if maximum then
 version = tonumber(maximum)
 if not version or version < 0 or version % 1 ~= 0 then return redis.error_reply('invalid encryption key version metadata') end
end
-- Preserve every historical field even when legacy MAX_VERSION was stale.
for _,field in ipairs(redis.call('HKEYS',KEYS[1])) do
 local existing = tonumber(field)
 if existing and existing > version and existing % 1 == 0 then version = existing end
end
if version >= 2147483647 then return redis.error_reply('encryption key version exhausted') end
local nextVersion = tostring(version + 1)
redis.call('HSET',KEYS[1],nextVersion,ARGV[1],'MAX_VERSION',nextVersion,'ISSUED_AT:'..nextVersion,ARGV[2])
return version + 1
`

func (e EncryptionCacheRedis) GenConversationNextVersionKey(ctx context.Context, conversationID string, fn func() (string, error)) error {
	if conversationID == "" {
		return errs.ErrArgs.Wrap("conversationID is empty")
	}
	key, err := fn()
	if err != nil {
		return err
	}
	if len(key) != 32 {
		return errs.ErrArgs.Wrap("encryption key must contain 32 bytes")
	}
	return e.rdb.Eval(ctx, allocateEncryptionVersion, []string{e.getConversationKey(conversationID)}, key, time.Now().UTC().UnixMilli()).Err()
}

func (e EncryptionCacheRedis) GetConversationMaxVersionKey(ctx context.Context, conversationID string) (*encryption.VersionKey, error) {
	maxVersion, err := e.rdb.HGet(ctx, e.getConversationKey(conversationID), MaxVersionKeyField).Result()
	if err != nil {
		return nil, err
	}
	key, err := e.rdb.HGet(ctx, e.getConversationKey(conversationID), maxVersion).Result()
	if err != nil {
		return nil, err
	}
	version, err := strconv.Atoi(maxVersion)
	if err != nil {
		return nil, err
	}
	return &encryption.VersionKey{
		Version: int32(version),
		Key:     key,
	}, nil
}
