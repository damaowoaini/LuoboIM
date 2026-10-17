package ownedcontrol

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/redis/go-redis/v9"
)

type RedisRevocation struct {
	BeforeSHA256           string
	AfterSHA256            string
	LegacyPermissionDenied bool
}

func redisUser(value any) (map[string]any, error) {
	list, ok := value.([]interface{})
	if !ok || len(list)%2 != 0 {
		return nil, ErrOwnedEvidence
	}
	out := map[string]any{}
	for i := 0; i < len(list); i += 2 {
		key, ok := list[i].(string)
		if !ok || out[key] != nil {
			return nil, ErrOwnedEvidence
		}
		out[key] = list[i+1]
	}
	return out, nil
}
func redisDisabled(row map[string]any) bool {
	flags, ok := row["flags"].([]interface{})
	if !ok {
		return false
	}
	off := false
	for _, value := range flags {
		flag, ok := value.(string)
		if !ok || flag == "on" {
			return false
		}
		off = off || flag == "off"
	}
	commands, ok := row["commands"].(string)
	if !ok || commands != "-@all" {
		return false
	}
	passwords, ok := row["passwords"].([]interface{})
	return off && ok && len(passwords) == 0
}

// RevokeOwnedRedisCredential mutates only a dedicated old service principal.
// The admin connection must have a distinct actual ACL identity; default-user
// or unauthenticated fixtures cannot stand in for credential exclusion.
// Invoke only under the real launcher hold after the old owned processes join.
func RevokeOwnedRedisCredential(ctx context.Context, admin, legacy redis.UniversalClient, adminUser, oldUser string) (RedisRevocation, error) {
	if ctx == nil || admin == nil || legacy == nil || adminUser == "" || adminUser == "default" || oldUser == adminUser || !strings.HasPrefix(oldUser, "owned_legacy_") {
		return RedisRevocation{}, ErrOwnedEvidence
	}
	who, err := admin.Do(ctx, "ACL", "WHOAMI").Text()
	if err != nil || who != adminUser {
		return RedisRevocation{}, ErrOwnedEvidence
	}
	before, err := admin.Do(ctx, "ACL", "GETUSER", oldUser).Result()
	if err != nil {
		return RedisRevocation{}, err
	}
	beforeRow, err := redisUser(before)
	if err != nil {
		return RedisRevocation{}, err
	}
	if !redisDisabled(beforeRow) {
		who, err = legacy.Do(ctx, "ACL", "WHOAMI").Text()
		if err != nil || who != oldUser {
			return RedisRevocation{}, ErrOwnedEvidence
		}
	}
	if err = admin.Do(ctx, "ACL", "SETUSER", oldUser, "reset", "off", "-@all", "resetpass", "resetkeys", "resetchannels").Err(); err != nil {
		return RedisRevocation{}, err
	}
	// Redis 'off' alone does not disconnect existing authenticated sockets.
	// Remove command permissions AND kill only that named old principal's sockets.
	if err = admin.Do(ctx, "CLIENT", "KILL", "USER", oldUser, "SKIPME", "yes").Err(); err != nil {
		return RedisRevocation{}, err
	}
	after, err := admin.Do(ctx, "ACL", "GETUSER", oldUser).Result()
	if err != nil {
		return RedisRevocation{}, err
	}
	row, err := redisUser(after)
	if err != nil || !redisDisabled(row) {
		return RedisRevocation{}, ErrOwnedEvidence
	}
	err = legacy.Ping(ctx).Err()
	if err == nil {
		return RedisRevocation{}, ErrOwnedEvidence
	}
	message := err.Error()
	if !strings.HasPrefix(message, "WRONGPASS ") && !strings.HasPrefix(message, "NOPERM ") && !strings.HasPrefix(message, "NOAUTH ") {
		return RedisRevocation{}, ErrOwnedEvidence
	}
	// Only hashes of private ACL replies survive; passwords/ACL bodies are never
	// put in a public observation. Network/timeout errors are not a successful deny.
	b, err := json.Marshal(before)
	if err != nil {
		return RedisRevocation{}, err
	}
	a, err := json.Marshal(after)
	if err != nil {
		return RedisRevocation{}, err
	}
	return RedisRevocation{sha(b), sha(a), true}, nil
}
