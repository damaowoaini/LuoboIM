package cache

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/redis/go-redis/v9"
	"io"
	"net"
	"testing"
)

type ownedRedisReplyError string

func (e ownedRedisReplyError) Error() string { return string(e) }
func (ownedRedisReplyError) RedisError()     {}
func TestOwnedRedisPhysicalWriteUnknownCannotBeClearedOrMoveToLaterOwner(t *testing.T) {
	for _, kind := range []string{"pre-io-error", "lost-ack", "ack", "mixed-pipeline"} {
		t.Run(kind, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			original, later := &ownedsource.Observer{}, &ownedsource.Observer{}
			ctx, err := ownedsource.Bind(parent, later)
			if err != nil {
				t.Fatal(err)
			}
			hook := &ownedRedisSource{owner: original, lifetime: parent}
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			conn := &ownedRedisConn{left, hook}
			joined := make(chan struct{})
			if kind != "pre-io-error" {
				go func() { defer close(joined); var one [1]byte; _, _ = io.ReadFull(right, one[:]) }()
			} else {
				close(joined)
			}
			write := func() {
				if _, err := conn.Write([]byte{'x'}); err != nil {
					t.Error(err)
				}
			}
			var outcome error
			if kind == "mixed-pipeline" {
				first, second := redis.NewIntCmd(ctx, "incr", "private-unretained-key"), redis.NewIntCmd(ctx, "incr", "other-private-key")
				outcome = hook.ProcessPipelineHook(func(context.Context, []redis.Cmder) error {
					write()
					first.SetErr(ownedRedisReplyError("WRONGTYPE"))
					second.SetErr(io.ErrUnexpectedEOF)
					return first.Err()
				})(ctx, []redis.Cmder{first, second})
			} else {
				outcome = hook.ProcessHook(func(context.Context, redis.Cmder) error {
					if kind == "pre-io-error" {
						return context.DeadlineExceeded
					}
					write()
					if kind == "ack" {
						return nil
					}
					return io.ErrUnexpectedEOF
				})(ctx, redis.NewIntCmd(ctx, "incr", "private-unretained-key"))
			}
			<-joined
			wantUnknown := kind == "lost-ack" || kind == "mixed-pipeline"
			if (outcome == nil) != (kind == "ack") || original.Snapshot().Unconfirmed() != wantUnknown || later.Snapshot().RedisMutation.Started != 0 {
				t.Fatal("I/O/response/original owner outcome differs")
			}
			original.Attempt(ownedsource.RedisMutation)(false)
			if original.Snapshot().Unconfirmed() != wantUnknown {
				t.Fatal("later ACK erased unknown")
			}
		})
	}
	parent, cancel := context.WithCancel(context.Background())
	cancel()
	hook := &ownedRedisSource{owner: &ownedsource.Observer{}, lifetime: parent}
	calls := 0
	err := hook.ProcessHook(func(context.Context, redis.Cmder) error { calls++; return nil })(parent, redis.NewIntCmd(parent, "incr", "not-dispatched"))
	if err == nil || calls != 0 || hook.owner.Snapshot().Unconfirmed() {
		t.Fatal("canceled pre-dispatch entered I/O or invented unknown")
	}
	if client, err := NewOwnedSourceRedis(context.Background()); !errors.Is(err, ErrOwnedRedis) || client != nil {
		t.Fatal("missing owner opened source pool")
	}
}

func TestOwnedRedisFirstLostACKRetiresOnlyOriginalPoolBeforeNextMutation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	pool, cancelPool := context.WithCancel(parent)
	defer cancelPool()
	original, later := &ownedsource.Observer{}, &ownedsource.Observer{}
	caller, err := ownedsource.Bind(parent, later)
	if err != nil {
		t.Fatal(err)
	}
	hook := &ownedRedisSource{owner: original, lifetime: pool, cancelPool: cancelPool}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	wire := &ownedRedisConn{left, hook}
	joined := make(chan struct{})
	go func() { defer close(joined); var body [1]byte; _, _ = io.ReadFull(right, body[:]) }()
	first := redis.NewIntCmd(caller, "incr", "synthetic-sequence")
	err = hook.ProcessHook(func(context.Context, redis.Cmder) error {
		if _, err := wire.Write([]byte{'x'}); err != nil {
			return err
		}
		return io.ErrUnexpectedEOF
	})(caller, first)
	<-joined
	if !errors.Is(err, ErrOwnedRedis) || pool.Err() == nil || parent.Err() != nil || !original.Snapshot().Unconfirmed() {
		t.Fatal("actual lost ACK did not retire its own pool only")
	}
	calls := 0
	err = hook.ProcessHook(func(context.Context, redis.Cmder) error { calls++; return nil })(caller, redis.NewIntCmd(caller, "incr", "same-original-sequence"))
	if !errors.Is(err, ErrOwnedRedis) || calls != 0 || later.Snapshot().RedisMutation.Started != 0 || original.Snapshot().RedisMutation.Started != 1 {
		t.Fatal("next original command replayed or adopted a later caller")
	}
	original.Attempt(ownedsource.RedisMutation)(false)
	if !original.Snapshot().Unconfirmed() {
		t.Fatal("later ACK erased original pool uncertainty")
	}
}
