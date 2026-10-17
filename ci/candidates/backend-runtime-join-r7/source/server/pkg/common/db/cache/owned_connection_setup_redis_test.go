package cache

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"github.com/redis/go-redis/v9"
)

// Only a bounded in-memory Redis protocol peer. The real, locked go-redis
// client performs initConn, clones the production hook, and handles the reply.
// This does not replace the existing real Redis constructor integration test.
type ownedRedisSetupWire struct {
	client      *redis.Client
	source      *ownedRedisSource
	parent      context.Context
	pool        context.Context
	left        net.Conn
	right       net.Conn
	done        chan error
	dials       atomic.Int32
	maintenance atomic.Int32
	identity    atomic.Int32
	increments  atomic.Int32
}

func readOwnedRedisSetupCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil || len(line) > 32 || !strings.HasSuffix(line, "\r\n") || !strings.HasPrefix(line, "*") {
		return nil, errors.New("bounded RESP command array required")
	}
	count, err := strconv.Atoi(line[1 : len(line)-2])
	if err != nil || count < 1 || count > 8 {
		return nil, errors.New("bounded command arity required")
	}
	result := make([]string, count)
	for i := range result {
		line, err = reader.ReadString('\n')
		if err != nil || len(line) > 32 || !strings.HasSuffix(line, "\r\n") || !strings.HasPrefix(line, "$") {
			return nil, errors.New("bounded RESP command argument required")
		}
		size, err := strconv.Atoi(line[1 : len(line)-2])
		if err != nil || size < 0 || size > 4096 {
			return nil, errors.New("bounded argument length required")
		}
		body := make([]byte, size+2)
		if _, err = io.ReadFull(reader, body); err != nil || string(body[size:]) != "\r\n" {
			return nil, errors.New("complete command argument required")
		}
		result[i] = string(body[:size])
	}
	return result, nil
}

func (f *ownedRedisSetupWire) serve(kind string) error {
	defer f.right.Close()
	if err := f.right.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(f.right, 4096)
	expect := func(want ...string) error {
		got, err := readOwnedRedisSetupCommand(reader)
		if err != nil {
			return err
		}
		if len(got) != len(want) {
			return errors.New("actual driver command arity differs")
		}
		for i := range want {
			if !strings.EqualFold(got[i], want[i]) {
				return errors.New("actual driver command differs")
			}
		}
		return nil
	}
	reply := func(value string) error { _, err := io.WriteString(f.right, value); return err }
	if err := expect("hello", "3"); err != nil {
		return err
	}
	if err := reply("%0\r\n"); err != nil {
		return err
	}
	if err := expect("client", "maint_notifications", "on", "moving-endpoint-type", "internal-ip"); err != nil {
		return err
	}
	f.maintenance.Add(1)
	if kind == "metadata-transport-failure" {
		return nil
	}
	// Redis 8.2 has no such subcommand. A Redis protocol error is the
	// driver's documented ModeAuto fallback; transport failures are not.
	if err := reply("-ERR unknown subcommand 'maint_notifications'\r\n"); err != nil {
		return err
	}
	for _, option := range []string{"lib-name", "lib-ver"} {
		args, err := readOwnedRedisSetupCommand(reader)
		if err != nil {
			return err
		}
		if len(args) != 4 || args[0] != "client" || args[1] != "setinfo" || !strings.EqualFold(args[2], option) || args[3] == "" {
			return errors.New("actual driver SETINFO differs")
		}
		f.identity.Add(1)
	}
	// Read the whole real pipeline before replying (net.Pipe has no kernel
	// buffer); do not replace go-redis's actual pipeline machinery.
	if err := reply("+OK\r\n+OK\r\n"); err != nil {
		return err
	}
	if err := expect("ping"); err != nil {
		return err
	}
	if err := reply("+PONG\r\n"); err != nil {
		return err
	}
	if err := expect("incr", "synthetic-setup-sequence"); err != nil {
		return err
	}
	f.increments.Add(1)
	if kind == "business-lost-ack" {
		return nil
	}
	return reply(":1\r\n")
}

func newOwnedRedisSetupWire(t *testing.T, kind string, observer *ownedsource.Observer) *ownedRedisSetupWire {
	t.Helper()
	parent, cancelParent := context.WithCancel(context.Background())
	pool, cancelPool := context.WithCancel(parent)
	left, right := net.Pipe()
	f := &ownedRedisSetupWire{parent: parent, pool: pool, left: left, right: right, done: make(chan error, 1)}
	f.source = &ownedRedisSource{owner: observer, lifetime: pool, cancelPool: cancelPool}
	f.client = redis.NewClient(&redis.Options{
		Addr: "127.0.0.1:36379", PoolSize: 1, MaxRetries: -1, DialerRetries: 1,
		ContextTimeoutEnabled: true, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, PoolTimeout: time.Second,
		Dialer: func(ctx context.Context, _, _ string) (net.Conn, error) {
			if ctx.Err() != nil || f.dials.Add(1) != 1 {
				return nil, errors.New("only original protocol connection allowed")
			}
			return left, nil
		},
	})
	f.client.AddHook(f.source)
	go func() { f.done <- f.serve(kind) }()
	t.Cleanup(func() {
		cancelPool()
		_ = f.client.Close()
		_ = left.Close()
		_ = right.Close()
		cancelParent()
		if err := <-f.done; err != nil {
			t.Error("actual driver protocol peer failed", err)
		}
	})
	return f
}

func TestOwnedRedisActualDriverKeepsConnectionSetupOutsideBusinessOutcome(t *testing.T) {
	for _, kind := range []string{"business-ack", "business-lost-ack", "metadata-transport-failure"} {
		t.Run(kind, func(t *testing.T) {
			original, later := &ownedsource.Observer{}, &ownedsource.Observer{}
			f := newOwnedRedisSetupWire(t, kind, original)
			caller, err := ownedsource.Bind(f.parent, later)
			if err != nil {
				t.Fatal(err)
			}
			err = f.client.Ping(caller).Err()
			if kind == "metadata-transport-failure" {
				if err == nil || f.maintenance.Load() != 1 || f.identity.Load() != 0 || f.increments.Load() != 0 || original.Snapshot().RedisMutation.Started != 0 || original.Snapshot().Unconfirmed() {
					t.Fatal("connection transport failure became business attempt or successful fallback")
				}
				return
			}
			if err != nil || f.maintenance.Load() != 1 || f.identity.Load() != 2 || f.dials.Load() != 1 || original.Snapshot().RedisMutation.Started != 0 || original.Snapshot().Unconfirmed() {
				t.Fatal("real driver Redis82 setup fallback failed or invented a business mutation", err)
			}
			value, err := f.client.Incr(caller, "synthetic-setup-sequence").Result()
			counts := original.Snapshot().RedisMutation
			if f.increments.Load() != 1 || counts.Started != 1 || counts.InFlight != 0 || later.Snapshot().RedisMutation.Started != 0 {
				t.Fatal("business dispatch count or original owner differs")
			}
			if kind == "business-ack" {
				if err != nil || value != 1 || counts.Succeeded != 1 || original.Snapshot().Unconfirmed() || f.pool.Err() != nil {
					t.Fatal("real business ACK did not remain the only observed mutation", err)
				}
				return
			}
			if !errors.Is(err, ErrOwnedRedis) || !counts.Unknown || counts.Unconfirmed != 1 || f.pool.Err() == nil || f.parent.Err() != nil {
				t.Fatal("actual business lost ACK failed to retire only original pool", err)
			}
			if err := f.client.Incr(caller, "synthetic-setup-sequence").Err(); !errors.Is(err, ErrOwnedRedis) || f.increments.Load() != 1 || f.dials.Load() != 1 || original.Snapshot().RedisMutation.Started != 1 {
				t.Fatal("retired original business mutation replayed or reconnected", err)
			}
			original.Attempt(ownedsource.RedisMutation)(false)
			if !original.Snapshot().Unconfirmed() || later.Snapshot().RedisMutation.Started != 0 {
				t.Fatal("later ACK or caller cleared original unknown")
			}
		})
	}
}

func TestOwnedRedisConnectionSetupDoesNotExemptOtherClientOrMixedWrites(t *testing.T) {
	for _, kind := range []string{"client-kill", "invalid-maint-shape", "mixed-pipeline"} {
		t.Run(kind, func(t *testing.T) {
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			pool, cancelPool := context.WithCancel(parent)
			defer cancelPool()
			original := &ownedsource.Observer{}
			hook := &ownedRedisSource{owner: original, lifetime: pool, cancelPool: cancelPool}
			left, right := net.Pipe()
			defer left.Close()
			defer right.Close()
			_ = left.SetWriteDeadline(time.Now().Add(time.Second))
			_ = right.SetReadDeadline(time.Now().Add(time.Second))
			done := make(chan error, 1)
			go func() { var byte [1]byte; _, err := io.ReadFull(right, byte[:]); done <- err }()
			wire := &ownedRedisConn{left, hook}
			writeThenLoseACK := func() error {
				if _, err := wire.Write([]byte{'x'}); err != nil {
					return fmt.Errorf("physical fixture write: %w", err)
				}
				return io.ErrUnexpectedEOF
			}
			var err error
			if kind == "mixed-pipeline" {
				cmds := []redis.Cmder{redis.NewStatusCmd(parent, "client", "setinfo", "LIB-NAME", "synthetic-driver"), redis.NewIntCmd(parent, "incr", "synthetic-owned-sequence")}
				err = hook.ProcessPipelineHook(func(context.Context, []redis.Cmder) error { return writeThenLoseACK() })(parent, cmds)
			} else {
				args := []interface{}{"client", "kill", "id", "12345"}
				if kind == "invalid-maint-shape" {
					args = []interface{}{"client", "maint_notifications", "on", "moving-endpoint-type", "unreviewed"}
				}
				err = hook.ProcessHook(func(context.Context, redis.Cmder) error { return writeThenLoseACK() })(parent, redis.NewStatusCmd(parent, args...))
			}
			if wireErr := <-done; wireErr != nil {
				t.Fatal(wireErr)
			}
			counts := original.Snapshot().RedisMutation
			if !errors.Is(err, ErrOwnedRedis) || counts.Started != 1 || counts.Unconfirmed != 1 || !counts.Unknown || pool.Err() == nil || parent.Err() != nil {
				t.Fatal("other CLIENT or mixed business write escaped original lost ACK gate", err)
			}
		})
	}
}
