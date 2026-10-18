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

// redisACLFields accepts only the two actual GETUSER protocol objects. The
// explicit seen-key check must also reject a duplicate whose first value is nil.
func redisACLFields(value any, required []string) (map[string]any, error) {
	out := make(map[string]any, len(required))
	add := func(rawKey, value any) error {
		key, ok := rawKey.(string)
		if !ok {
			return ErrOwnedEvidence
		}
		if _, exists := out[key]; exists {
			return ErrOwnedEvidence
		}
		known := false
		for _, field := range required {
			known = known || key == field
		}
		if !known {
			return ErrOwnedEvidence
		}
		out[key] = value
		return nil
	}
	switch reply := value.(type) {
	case []interface{}: // RESP2 alternating field/value array.
		if len(reply) != 2*len(required) {
			return nil, ErrOwnedEvidence
		}
		for i := 0; i < len(reply); i += 2 {
			if err := add(reply[i], reply[i+1]); err != nil {
				return nil, err
			}
		}
	case map[interface{}]interface{}: // RESP3 map returned by Cmd.ReadReply.
		if len(reply) != len(required) {
			return nil, ErrOwnedEvidence
		}
		for key, item := range reply {
			if err := add(key, item); err != nil {
				return nil, err
			}
		}
	default:
		return nil, ErrOwnedEvidence
	}
	if len(out) != len(required) {
		return nil, ErrOwnedEvidence
	}
	return out, nil
}

// Normalize the complete fixed GETUSER contract, including selectors, before
// hashing either private ACL reply. No raw RESP3 map is passed to encoding/json.
func redisUser(value any) (map[string]any, error) {
	out, err := redisACLFields(value, []string{"flags", "passwords", "commands", "keys", "channels", "selectors"})
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"flags", "passwords"} {
		items, ok := out[field].([]interface{})
		if !ok || items == nil {
			return nil, ErrOwnedEvidence
		}
		copyItems := make([]interface{}, len(items))
		for i, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, ErrOwnedEvidence
			}
			copyItems[i] = text
		}
		out[field] = copyItems
	}
	for _, field := range []string{"commands", "keys", "channels"} {
		if _, ok := out[field].(string); !ok {
			return nil, ErrOwnedEvidence
		}
	}
	selectors, ok := out["selectors"].([]interface{})
	if !ok || selectors == nil {
		return nil, ErrOwnedEvidence
	}
	normalized := make([]interface{}, len(selectors))
	for i, value := range selectors {
		selector, err := redisACLFields(value, []string{"commands", "keys", "channels"})
		if err != nil {
			return nil, err
		}
		for _, field := range []string{"commands", "keys", "channels"} {
			if _, ok := selector[field].(string); !ok {
				return nil, ErrOwnedEvidence
			}
		}
		normalized[i] = selector
	}
	out["selectors"] = normalized
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
	selectors, ok := row["selectors"].([]interface{})
	if !ok || selectors == nil || len(selectors) != 0 {
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
	b, err := json.Marshal(beforeRow)
	if err != nil {
		return RedisRevocation{}, err
	}
	a, err := json.Marshal(row)
	if err != nil {
		return RedisRevocation{}, err
	}
	return RedisRevocation{sha(b), sha(a), true}, nil
}
