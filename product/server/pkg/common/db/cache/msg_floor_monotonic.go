package cache

import (
	"context"
	"strconv"

	"github.com/OpenIMSDK/tools/errs"
)

// Redis Lua numbers cannot exactly compare int64 sequences above2^53. Compare
// validated nonnegative decimal strings by length/lexicographic order instead.
const advanceVisibilityFloorScript = `
local maximum = '9223372036854775807'
local function canonical(value)
 if not value or not string.match(value, '^%d+$') then return nil end
 value = string.gsub(value, '^0+', '')
 if value == '' then value = '0' end
 if #value > 19 or (#value == 19 and value > maximum) then return nil end
 return value
end
local nextValue = canonical(ARGV[1])
if not nextValue then return redis.error_reply('invalid visibility floor') end
local stored = redis.call('GET', KEYS[1])
if stored then
 local current = canonical(stored)
 if not current then return redis.error_reply('invalid stored visibility floor') end
 if #current > #nextValue or (#current == #nextValue and current >= nextValue) then return 0 end
end
redis.call('SET', KEYS[1], nextValue)
return 1
`

func (c *msgCache) advanceVisibilityFloor(ctx context.Context, key string, value int64) error {
	if value < 0 {
		return errs.ErrArgs.Wrap("invalid visibility floor")
	}
	return c.rdb.Eval(ctx, advanceVisibilityFloorScript, []string{key}, strconv.FormatInt(value, 10)).Err()
}

func (c *msgCache) advanceVisibilityFloors(ctx context.Context, values map[string]int64, key func(string) string) error {
	for _, value := range values {
		if value < 0 {
			return errs.ErrArgs.Wrap("invalid visibility floor")
		}
	}
	for id, value := range values {
		if err := c.advanceVisibilityFloor(ctx, key(id), value); err != nil {
			return err
		}
	}
	return nil
}
