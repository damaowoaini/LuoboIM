package cache

import (
	"context"
	"errors"
	"github.com/redis/go-redis/v9"
	"testing"
)

type deleteListBoundary struct {
	fail                       string
	failure                    error
	members, removals, deletes int
}

func (h *deleteListBoundary) DialHook(next redis.DialHook) redis.DialHook { return next }
func (h *deleteListBoundary) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return next
}
func (h *deleteListBoundary) ProcessHook(redis.ProcessHook) redis.ProcessHook {
	return func(_ context.Context, cmd redis.Cmder) error {
		switch cmd.Name() {
		case "smembers":
			h.members++
			if h.fail == "members" {
				cmd.SetErr(h.failure)
				return h.failure
			}
			cmd.(*redis.StringSliceCmd).SetVal([]string{"synthetic-a", "synthetic-b"})
		case "srem":
			h.removals++
			if h.fail == "removal" && h.removals == 1 {
				cmd.SetErr(h.failure)
				return h.failure
			}
			cmd.(*redis.IntCmd).SetVal(1)
		case "del":
			h.deletes++
			if h.fail == "delete" {
				cmd.SetErr(h.failure)
				return h.failure
			}
			cmd.(*redis.IntCmd).SetVal(1)
		default:
			return errors.New("unexpected deletion cleanup dispatch")
		}
		return nil
	}
}
func TestActualDeleteListCleanupReturnsFailureWithoutDiscardingRemainingUserObligations(t *testing.T) {
	for _, stage := range []string{"members", "removal", "delete", "ack"} {
		t.Run(stage, func(t *testing.T) {
			boundary := &deleteListBoundary{fail: stage, failure: errors.New("synthetic pre-I/O cleanup refusal")}
			// Replace only the Redis process boundary; the actual cache cleanup method
			// must retain its complete multi-user behavior and return its real error.
			client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
			defer client.Close()
			client.AddHook(boundary)
			actual := &msgCache{rdb: client}
			err := actual.DelUserDeleteMsgsList(context.Background(), "si_synthetic-cleanup", []int64{1})
			if stage == "ack" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, boundary.failure) {
				t.Fatal("actual cleanup failure disappeared", err)
			}
			if boundary.members != 1 {
				t.Fatal("original member lookup count changed")
			}
			if stage == "members" {
				if boundary.removals != 0 || boundary.deletes != 0 {
					t.Fatal("failed lookup still removed data")
				}
			} else {
				if boundary.removals != 2 {
					t.Fatal("one failed user silently skipped another user cleanup")
				}
				expected := 1
				if stage == "removal" {
					expected = 0
				}
				if boundary.deletes != expected {
					t.Fatal("failed per-user cleanup deleted its retained original list")
				}
			}
		})
	}
}
