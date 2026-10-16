package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"reflect"
	"testing"

	"github.com/redis/go-redis/v9"
)

// Only Redis transport is replaced. These checks call the actual LiveSource
// redisPage through Page, including its saved cursor and production budgets.
// Even an unexpected command cannot dial a real service.
type scanReplyFixture struct {
	keys   []string
	next   uint64
	scans  int
	counts []int64
	reads  []string
	body   string
}

func (s *scanReplyFixture) DialHook(redis.DialHook) redis.DialHook {
	return func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("no fixture network") }
}
func (s *scanReplyFixture) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		switch value := cmd.(type) {
		case *redis.ScanCmd:
			if value.Name() != "scan" {
				return ErrInvalid
			}
			args := cmd.Args()
			if len(args) != 6 {
				return ErrInvalid
			}
			count, ok := args[5].(int64)
			if !ok || count < 1 {
				return ErrInvalid
			}
			s.counts = append(s.counts, count)
			// The first reply may exceed COUNT. A later request must use the
			// exact returned cursor, not restart at0 while pending keys exist.
			if s.scans == 0 {
				value.SetVal(append([]string(nil), s.keys...), s.next)
			} else {
				if s.next == 0 || cmd.Args()[1] != s.next {
					return ErrInvalid
				}
				value.SetVal(nil, 0)
			}
			s.scans++
			return nil
		case *redis.Cmd:
			args := value.Args()
			if value.Name() != "eval_ro" || len(args) != 6 || args[1] != readKeyScript || args[2] != 1 {
				return ErrInvalid
			}
			key, ok := args[3].(string)
			if !ok {
				return ErrInvalid
			}
			s.reads = append(s.reads, key)
			value.SetVal([]interface{}{"ok", "string", int64(-1), s.body, "9"})
			return nil
		default:
			return ErrInvalid
		}
	}
}
func (*scanReplyFixture) ProcessPipelineHook(redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(context.Context, []redis.Cmder) error { return ErrInvalid }
}
func scanSource(t *testing.T, count int, next uint64, body string) (*LiveSource, *scanReplyFixture) {
	t.Helper()
	hook := &scanReplyFixture{next: next, body: body}
	for n := 0; n < count; n++ {
		hook.keys = append(hook.keys, fmt.Sprintf("MAX_SEQ:synthetic-%04d", n))
	}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1})
	rdb.AddHook(hook)
	t.Cleanup(func() {
		if rdb.Close() != nil {
			t.Error("synthetic client close failed")
		}
	})
	return &LiveSource{redis: rdb}, hook
}
func reloadPageCursor(t *testing.T, cursor PageCursor) PageCursor {
	t.Helper()
	raw, e := json.Marshal(cursor)
	if e != nil {
		t.Fatal(e)
	}
	var result PageCursor
	if DecodeStrict(raw, &result) != nil || !result.valid() {
		t.Fatal("checkpoint cursor cannot resume")
	}
	return result
}

func TestNamespaceChunkScanHintKeepsWholeBoundedReply(t *testing.T) {
	for _, test := range []struct {
		name  string
		count int
		next  uint64
	}{
		{"257-next-nonzero", 257, 73}, {"257-final-cursor", 257, 0}, {"maximum-pending", maxScanReplyKeys, 0}, {"oversized-reply", maxScanReplyKeys + 1, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			source, hook := scanSource(t, test.count, test.next, "synthetic-dump")
			limits := defaultPageLimits()
			cursor := PageCursor{Phase: "redis"}
			page, e := source.Page(context.Background(), cursor, limits)
			if test.count > maxScanReplyKeys {
				if !errors.Is(e, ErrLimit) || hook.scans != 1 || len(hook.reads) != 0 {
					t.Fatal("oversized reply acquired read/advance permission")
				}
				return
			}
			if e != nil || len(page.Redis) != limits.Rows || len(page.After.Pending) != test.count-limits.Rows || !reflect.DeepEqual(page.After.Pending, hook.keys[limits.Rows:]) || page.After.RedisCursor != test.next || len(hook.counts) != 1 || hook.counts[0] != int64(limits.Rows) {
				t.Fatal("COUNT hint truncated or rejected a legal reply")
			}
			committed := []string{}
			for n := 0; n < maxScanReplyKeys; n++ {
				for _, row := range page.Redis {
					committed = append(committed, row.Key)
				}
				cursor = reloadPageCursor(t, page.After)
				if len(cursor.Pending) == 0 {
					break
				}
				page, e = source.Page(context.Background(), cursor, limits)
				if e != nil || hook.scans != 1 || len(page.Redis) > limits.Rows {
					t.Fatal("pending keys restarted SCAN or exceeded page rows")
				}
			}
			if !reflect.DeepEqual(committed, hook.keys) || !reflect.DeepEqual(hook.reads, hook.keys) || hook.scans != 1 {
				t.Fatal("saved reply keys were lost, duplicated or reordered")
			}
			if test.next == 0 {
				if cursor.Prefix != 1 || cursor.RedisCursor != 0 || len(cursor.Pending) != 0 {
					t.Fatal("terminal cursor restarted instead of advancing prefix")
				}
			} else {
				if cursor.Prefix != 0 || cursor.RedisCursor != test.next {
					t.Fatal("nonzero continuation was not preserved")
				}
				last, e := source.Page(context.Background(), cursor, limits)
				if e != nil || hook.scans != 2 || last.After.Prefix != 1 || len(last.Redis) != 0 {
					t.Fatal("next cursor was not consumed after pending drained")
				}
			}
		})
	}
}

func TestNamespaceChunkScanByteBudgetPreservesPendingTail(t *testing.T) {
	source, hook := scanSource(t, 7, 0, "synthetic-body-needs-finite-space")
	limits := defaultPageLimits()
	perRow := len(hook.keys[0]) + len(hook.body) + 1
	limits.Bytes = perRow * 2
	cursor := PageCursor{Phase: "redis"}
	committed := []string{}
	for n := 0; n < 8 && cursor.Prefix == 0; n++ {
		page, e := source.Page(context.Background(), cursor, limits)
		if e != nil || len(page.Redis) < 1 || len(page.Redis) > 2 || hook.scans != 1 {
			t.Fatal("byte-bound pending page stalled or restarted SCAN")
		}
		for _, row := range page.Redis {
			committed = append(committed, row.Key)
		}
		if !reflect.DeepEqual(page.After.Pending, hook.keys[len(committed):]) && len(committed) < len(hook.keys) {
			t.Fatal("byte budget dropped a pending reply suffix")
		}
		cursor = reloadPageCursor(t, page.After)
	}
	if !reflect.DeepEqual(committed, hook.keys) || cursor.Prefix != 1 || len(cursor.Pending) != 0 {
		t.Fatal("byte budget lost or replayed committed rows")
	}
	// A candidate key may be read once to learn it does not fit, then read
	// again on the next page. Those are bounded read-only probes, not commits.
	if len(hook.reads) <= len(hook.keys) {
		t.Fatal("fixture did not actually exercise the byte-bound suffix")
	}
}
