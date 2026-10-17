package controller

import (
	"context"
	"errors"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"reflect"
	"testing"
)

type cacheStageFailure struct {
	cache.MsgModel
	fail      string
	failure   error
	order     []string
	storedSeq int64
}

func (f *cacheStageFailure) GetMaxSeq(context.Context, string) (int64, error) {
	f.order = append(f.order, "head")
	return 7, nil
}
func (f *cacheStageFailure) SetMessageToCache(_ context.Context, _ string, msgs []*sdkws.MsgData) (int, error) {
	f.order = append(f.order, "message")
	f.storedSeq = msgs[0].Seq
	if f.fail == "message" {
		return 0, f.failure
	}
	return len(msgs), nil
}
func (f *cacheStageFailure) SetMaxSeq(context.Context, string, int64) error {
	f.order = append(f.order, "max")
	if f.fail == "max" {
		return f.failure
	}
	return nil
}
func (f *cacheStageFailure) SetHasReadSeqs(context.Context, string, map[string]int64) error {
	f.order = append(f.order, "read")
	if f.fail == "read" {
		return f.failure
	}
	return nil
}
func TestActualMessageCacheStageFailureCannotBeOverwrittenByLaterAcknowledgement(t *testing.T) {
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_V1", "0")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH", "")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256", "")
	for _, stage := range []string{"message", "max", "read", "ack"} {
		t.Run(stage, func(t *testing.T) {
			sentinel := errors.New("synthetic pre-I/O cache refusal")
			storage := &cacheStageFailure{fail: stage, failure: sentinel}
			database := &commonMsgDatabase{cache: storage}
			message := &sdkws.MsgData{SendID: "synthetic-sender", ClientMsgID: "synthetic-cache-stage"}
			previous, isNew, err := database.BatchInsertChat2Cache(context.Background(), "si_synthetic-cache", []*sdkws.MsgData{message})
			expected := []string{"head", "message", "max", "read"}
			if stage == "message" {
				expected = expected[:2]
			} else if stage == "max" {
				expected = expected[:3]
			}
			if !reflect.DeepEqual(storage.order, expected) || previous != 7 || isNew || storage.storedSeq != 8 {
				t.Fatal("actual cache stage order/sequence contract changed", storage.order)
			}
			if stage == "ack" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, sentinel) {
				t.Fatal("original stage failure was hidden by another call", err)
			}
		})
	}
}
