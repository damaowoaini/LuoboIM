package cache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dtm-labs/rockscache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/redis/go-redis/v9"
)

// This bounded RESP3 peer supplies explicit complete/null/error replies. The
// locked go-redis decoder, physical-write hook and rockscache.Fetch2 are real;
// it does NOT execute Lua or certify Redis/namespace/Ready integration.
type ownedNullAckWire struct {
	client       *redis.Client
	pool         *ownedRedisPool
	parent       context.Context
	owner        *ownedsource.Observer
	left, right  net.Conn
	done         chan error
	dials, evals atomic.Int32
}
type ownedNullJoinedTransport struct{}

func (ownedNullJoinedTransport) DialHook(next redis.DialHook) redis.DialHook { return next }
func (ownedNullJoinedTransport) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (ownedNullJoinedTransport) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		err := next(ctx, cmd)
		if cmd.Name() == "eval" && err == redis.Nil {
			return errors.Join(err, io.ErrUnexpectedEOF)
		}
		return err
	}
}
func newOwnedNullAckWire(t *testing.T, mode string) *ownedNullAckWire {
	t.Helper()
	parent, cancel := context.WithCancel(context.Background())
	owner := &ownedsource.Observer{}
	bound, err := ownedsource.Bind(parent, owner)
	if err != nil {
		t.Fatal("original owner unavailable")
	}
	left, right := net.Pipe()
	wire := &ownedNullAckWire{parent: parent, owner: owner, left: left, right: right, done: make(chan error, 1)}
	opts := redis.Options{Addr: "127.0.0.1:36379", PoolSize: 1, MaxRetries: -1, DialerRetries: 1, ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second, Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
		if ctx.Err() != nil || wire.dials.Add(1) != 1 {
			return nil, errors.New("only original protocol connection permitted")
		}
		return left, nil
	}}
	wire.client = redis.NewClient(&opts)
	wire.pool, err = newOwnedRedisPool(bound, wire.client, func() redis.UniversalClient { copy := opts; return redis.NewClient(&copy) })
	if err != nil {
		left.Close()
		right.Close()
		cancel()
		t.Fatal("original owned pool unavailable")
	}
	if mode == "joined-null-transport" {
		wire.client.AddHook(ownedNullJoinedTransport{})
	}
	go func() { wire.done <- wire.serve(mode) }()
	t.Cleanup(func() {
		_ = wire.pool.Close()
		_ = left.Close()
		_ = right.Close()
		cancel()
		select {
		case err := <-wire.done:
			if err != nil {
				t.Error("bounded protocol peer failed", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("bounded protocol peer did not join")
		}
	})
	return wire
}
func (w *ownedNullAckWire) serve(mode string) error {
	defer w.right.Close()
	if err := w.right.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(w.right, 4096)
	read := func() ([]string, error) { return readOwnedRedisSetupCommand(reader) }
	reply := func(raw string) error { _, err := io.WriteString(w.right, raw); return err }
	args, err := read()
	if err != nil || len(args) != 2 || args[0] != "hello" || args[1] != "3" {
		return errors.New("original HELLO differs")
	}
	if err = reply("%0\r\n"); err != nil {
		return err
	}
	args, err = read()
	if err != nil || len(args) != 5 || args[0] != "client" || args[1] != "maint_notifications" {
		return errors.New("original maintenance initialization differs")
	}
	if err = reply("-ERR unknown subcommand 'maint_notifications'\r\n"); err != nil {
		return err
	}
	for _, option := range []string{"lib-name", "lib-ver"} {
		args, err = read()
		if err != nil || len(args) != 4 || args[0] != "client" || args[1] != "setinfo" || !strings.EqualFold(args[2], option) {
			return errors.New("original identity initialization differs")
		}
	}
	if err = reply("+OK\r\n+OK\r\n"); err != nil {
		return err
	}
	cached := ""
	for {
		// At a frame boundary only an empty-buffer close is expected. Once any
		// byte exists, the original bounded parser must reject a partial frame.
		first, peekErr := reader.Peek(1)
		if len(first) == 0 && (errors.Is(peekErr, io.EOF) || errors.Is(peekErr, io.ErrClosedPipe)) {
			return nil
		}
		if peekErr != nil {
			return peekErr
		}
		args, err = read()
		if err != nil {
			return err
		}
		if len(args) < 4 || args[0] != "eval" {
			return errors.New("actual business EVAL required")
		}
		w.evals.Add(1)
		if strings.HasPrefix(mode, "pipeline-") {
			second, err := read()
			if err != nil || len(second) < 4 || second[0] != "eval" {
				return errors.New("complete actual pipeline required")
			}
			w.evals.Add(1)
			switch mode {
			case "pipeline-null":
				err = reply("_\r\n:7\r\n")
			case "pipeline-lua-error":
				err = reply("_\r\n-ERR synthetic Lua failure\r\n")
			case "pipeline-lost-tail":
				_ = reply("_\r\n")
				return nil
			default:
				return errors.New("closed pipeline fixture required")
			}
			if err != nil {
				return err
			}
			continue
		}
		if mode == "lost-null-reply" {
			return nil
		}
		if mode == "lua-error" {
			if err = reply("-ERR synthetic Lua failure\r\n"); err != nil {
				return err
			}
			continue
		}
		if mode == "joined-null-transport" || strings.Contains(args[1], "raw-null") {
			if err = reply("_\r\n"); err != nil {
				return err
			}
			continue
		}
		if strings.Contains(args[1], "luaGet") {
			if cached == "" {
				err = reply("*2\r\n_\r\n$6\r\nLOCKED\r\n")
			} else {
				err = reply(fmt.Sprintf("*2\r\n$%d\r\n%s\r\n_\r\n", len(cached), cached))
			}
		} else if strings.Contains(args[1], "luaSet") {
			if len(args) != 7 || args[3] != "synthetic-user-cache" {
				return errors.New("actual rockscache set shape differs")
			}
			cached = args[4]
			err = reply("_\r\n")
		} else {
			return errors.New("actual rockscache script required")
		}
		if err != nil {
			return err
		}
	}
}

func TestOwnedRedisNullACKPreservesRealRockscacheAndCompletePipelineWithoutClearingUnknown(t *testing.T) {
	t.Run("real-rockscache-cold-set-null-and-warm-hit-retain-original-owner", func(t *testing.T) {
		wire := newOwnedNullAckWire(t, "cache")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		client := rockscache.NewClient(wire.pool, GetDefaultOpt())
		calls := 0
		fetch := func() (string, error) { calls++; return "synthetic-user-value", nil }
		for n := 0; n < 2; n++ {
			value, err := client.Fetch2(ctx, "synthetic-user-cache", time.Minute, fetch)
			if err != nil || value != "synthetic-user-value" {
				t.Fatal("actual cold/warm Fetch2 failed")
			}
		}
		if calls != 1 || wire.evals.Load() != 3 || wire.dials.Load() != 1 || wire.pool.source.lifetime.Err() != nil || wire.parent.Err() != nil || wire.owner.Snapshot().Unconfirmed() {
			t.Fatal("acknowledged null retired pool, repeated fill or lost original ownership")
		}
		err := wire.client.Eval(ctx, "-- raw-null\nreturn nil", []string{"synthetic-user-cache"}).Err()
		if err != redis.Nil || wire.pool.source.lifetime.Err() != nil || wire.owner.Snapshot().Unconfirmed() {
			t.Fatal("original direct Nil sentinel or acknowledged source state was changed")
		}
		counts := wire.owner.Snapshot().RedisMutation
		if counts.Started != 4 || counts.Succeeded != 4 || counts.Unconfirmed != 0 || counts.InFlight != 0 {
			t.Fatal("real acknowledged writes were not accounted exactly")
		}
	})
	t.Run("complete-pipeline-null-and-value-preserve-all-original-results", func(t *testing.T) {
		wire := newOwnedNullAckWire(t, "pipeline-null")
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		pipe := wire.client.Pipeline()
		first := pipe.Eval(ctx, "return nil", []string{"synthetic-user-cache"})
		second := pipe.Eval(ctx, "return 7", []string{"synthetic-user-cache"})
		_, err := pipe.Exec(ctx)
		if err != redis.Nil || first.Err() != redis.Nil || second.Err() != nil || second.Val() != int64(7) || wire.evals.Load() != 2 || wire.dials.Load() != 1 || wire.pool.source.lifetime.Err() != nil || wire.owner.Snapshot().Unconfirmed() {
			t.Fatal("complete actual null pipeline lost acknowledgement or original result")
		}
		counts := wire.owner.Snapshot().RedisMutation
		if counts.Started != 1 || counts.Succeeded != 1 || counts.Unconfirmed != 0 || counts.InFlight != 0 {
			t.Fatal("complete actual pipeline was not one acknowledged observation")
		}
	})
	t.Run("mixed-lua-error-lost-tail-and-joined-null-never-clear-unknown-or-replay", func(t *testing.T) {
		for _, mode := range []string{"pipeline-lua-error", "pipeline-lost-tail", "lost-null-reply", "lua-error", "joined-null-transport"} {
			wire := newOwnedNullAckWire(t, mode)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			var err error
			if strings.HasPrefix(mode, "pipeline-") {
				pipe := wire.client.Pipeline()
				pipe.Eval(ctx, "return nil", []string{"synthetic-user-cache"})
				pipe.Eval(ctx, "return 7", []string{"synthetic-user-cache"})
				_, err = pipe.Exec(ctx)
			} else {
				err = wire.client.Eval(ctx, "-- raw-null\nreturn nil", []string{"synthetic-user-cache"}).Err()
			}
			cancel()
			want := int32(1)
			if strings.HasPrefix(mode, "pipeline-") {
				want = 2
			}
			if !errors.Is(err, ErrOwnedRedis) || wire.evals.Load() != want || wire.pool.source.lifetime.Err() == nil || wire.parent.Err() != nil || !wire.owner.Snapshot().Unconfirmed() {
				t.Fatal("actual mixed/partial/wrapped result did not retain original unknown")
			}
			wire.owner.Attempt(ownedsource.RedisMutation)(false)
			if !wire.owner.Snapshot().Unconfirmed() {
				t.Fatal("later acknowledged observation erased earlier unknown")
			}
			if next := wire.client.Eval(context.Background(), "-- raw-null\nreturn nil", []string{"synthetic-user-cache"}).Err(); !errors.Is(next, ErrOwnedRedis) || wire.evals.Load() != want || wire.dials.Load() != 1 {
				t.Fatal("retired pool replayed after unknown")
			}
		}
	})
}
