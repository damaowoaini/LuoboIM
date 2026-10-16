package cache

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
)

// Update only fields still present at the atomic decision. Never replay a stale
// HGETALL snapshot over a concurrent exact-session HDEL.
const kickOtherTokensScript = `
local current=redis.call('HGET',KEYS[1],ARGV[1])
if not current then return 0 end
if tonumber(current) ~= tonumber(ARGV[2]) then return -1 end
local tokens=redis.call('HKEYS',KEYS[1])
for _,token in ipairs(tokens) do
 if token ~= ARGV[1] then redis.call('HSET',KEYS[1],token,ARGV[3]) end
end
return 1`

func (c *msgCache) KickOtherTokensByUidPid(ctx context.Context, user string, platform int, current string) error {
	if user == "" || current == "" || constant.PlatformIDToName(platform) == "" {
		return errs.ErrArgs.Wrap("invalid session")
	}
	key := uidPidToken + user + ":" + constant.PlatformIDToName(platform)
	value, err := c.rdb.Eval(ctx, kickOtherTokensScript, []string{key}, current, constant.NormalToken, constant.KickedToken).Int()
	if err != nil {
		return errs.Wrap(err)
	}
	if value != 1 {
		return errs.ErrTokenNotExist.Wrap("session was revoked before registration")
	}
	return nil
}
