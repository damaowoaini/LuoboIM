package messagedelivery

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type ownedProjection struct {
	projection          *RedisProjection
	client              *redis.Client
	cid, sender         string
	maxValue, readValue int64
}

func (p *ownedProjection) RaiseMaximum(ctx context.Context, cid string, v int64) error {
	if cid != p.cid {
		return ErrInvalid
	}
	if err := p.projection.RaiseMaximum(ctx, cid, v); err != nil {
		return err
	}
	p.maxValue = max(p.maxValue, v)
	return nil
}
func (p *ownedProjection) RaiseSenderRead(ctx context.Context, uid, cid string, v int64) error {
	if cid != p.cid || uid != p.sender {
		return ErrInvalid
	}
	if err := p.projection.RaiseSenderRead(ctx, uid, cid, v); err != nil {
		return err
	}
	p.readValue = max(p.readValue, v)
	return nil
}

func projectionRedis(t *testing.T) *ownedProjection {
	t.Helper()
	raw := os.Getenv("LUOBO_RETENTION_TEST_REDIS")
	if raw == "" {
		t.Skip("explicit ephemeral Redis fixture not selected")
	}
	if raw != "redis://127.0.0.1:36379/15" {
		t.Fatal("exact task Redis15 endpoint required")
	}
	options, err := redis.ParseURL(raw)
	if err != nil || options.Addr != "127.0.0.1:36379" || options.DB != 15 || options.Username != "" || options.Password != "" || options.TLSConfig != nil {
		t.Fatal("task Redis15 fixture configuration invalid")
	}
	options.ContextTimeoutEnabled = true
	options.DialTimeout = time.Second
	options.ReadTimeout = time.Second
	options.WriteTimeout = time.Second
	options.PoolTimeout = time.Second
	options.MaxRetries = -1
	client := redis.NewClient(options)
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error("task Redis close unconfirmed", err)
		}
	})
	sender, group, nonce := uuid.NewString(), uuid.NewString(), uuid.NewString()
	cid := "sg_" + group
	keys := []string{"luobo_message_projection_claim_" + group, "MAX_SEQ:" + cid, "HAS_READ_SEQ:" + sender + ":" + cid}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	claimed, err := client.Eval(ctx, `if redis.call('EXISTS',KEYS[1],KEYS[2],KEYS[3])~=0 then return 0 end
redis.call('SET',KEYS[1],ARGV[1]);redis.call('SET',KEYS[2],'0');redis.call('SET',KEYS[3],'0');return 1`, keys, nonce).Int64()
	if err != nil || claimed != 1 {
		t.Fatal("exact nonce projection keys claim unconfirmed; no cleanup authorized")
	}
	projection, err := NewRedisProjection(client)
	if err != nil {
		t.Fatal(err)
	}
	owned := &ownedProjection{projection: projection, client: client, cid: cid, sender: sender}
	t.Cleanup(func() {
		end, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		removed, err := client.Eval(end, `for i=1,3 do if redis.call('TYPE',KEYS[i]).ok~='string' then return -1 end end
if redis.call('GET',KEYS[1])~=ARGV[1] or redis.call('GET',KEYS[2])~=ARGV[2] or redis.call('GET',KEYS[3])~=ARGV[3] then return -2 end
return redis.call('DEL',KEYS[1],KEYS[2],KEYS[3])`, keys, nonce, strconv.FormatInt(owned.maxValue, 10), strconv.FormatInt(owned.readValue, 10)).Int64()
		if err != nil || removed != 3 {
			t.Error("exact claimed projection keys cleanup unconfirmed")
			return
		}
		remaining, err := client.Exists(end, keys...).Result()
		if err != nil || remaining != 0 {
			t.Error("exact projection keys absence unconfirmed")
		}
	})
	return owned
}
