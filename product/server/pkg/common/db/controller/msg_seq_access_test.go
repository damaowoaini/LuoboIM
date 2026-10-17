package controller

import (
	"context"
	"errors"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/mongo"
	"reflect"
	"testing"
)

type exactSeqCacheFixture struct {
	cache.MsgModel
	asked []int64
}

func (*exactSeqCacheFixture) GetConversationUserMinSeq(context.Context, string, string) (int64, error) {
	return 50, nil
}
func (*exactSeqCacheFixture) GetMinSeq(context.Context, string) (int64, error) { return 1, nil }
func (*exactSeqCacheFixture) GetMaxSeq(context.Context, string) (int64, error) { return 100, nil }
func (*exactSeqCacheFixture) GetUserDelList(context.Context, string, string) ([]int64, error) {
	return []int64{50}, nil
}
func (c *exactSeqCacheFixture) GetMessagesBySeq(_ context.Context, _ string, seqs []int64) ([]*sdkws.MsgData, []int64, error) {
	c.asked = seqs
	result := []*sdkws.MsgData{}
	for _, seq := range seqs {
		result = append(result, &sdkws.MsgData{Seq: seq})
	}
	return result, nil, nil
}
func TestExactSequenceReadRespectsUserFloorAndCachedDeletion(t *testing.T) {
	cache := &exactSeqCacheFixture{}
	db := &commonMsgDatabase{cache: cache, msgDocDatabase: &summaryMongoFixture{}}
	min, max, messages, err := db.GetMsgBySeqs(context.Background(), "user", "sg_group", []int64{1, 49, 50, 100, 101})
	if err != nil || min != 50 || max != 100 || !reflect.DeepEqual(cache.asked, []int64{50, 100}) || len(messages) != 1 || messages[0].Seq != 100 {
		t.Fatal("exact sequence read crossed visible floor/deletion boundary")
	}
}

type summaryCacheFixture struct {
	cache.MsgModel
	fail bool
}

func (c summaryCacheFixture) GetMessagesBySeq(_ context.Context, id string, _ []int64) ([]*sdkws.MsgData, []int64, error) {
	if c.fail {
		return nil, nil, errors.New("cache unavailable")
	}
	if id == "cached" {
		return []*sdkws.MsgData{{Seq: 1, KeyVersion: 7, Content: []byte{255, 0, 128}}}, nil, nil
	}
	return nil, []int64{1}, nil
}

type summaryMongoFixture struct {
	ut.MsgDocModelInterface
	calls int
}

func (*summaryMongoFixture) ReadMessageVisibility(context.Context, []ut.MessageVisibilityFloor) ([]ut.MessageVisibilityFloor, error) {
	return nil, nil
}

func (*summaryMongoFixture) HiddenMessageSequences(context.Context, string, string, []int64) ([]int64, error) {
	return nil, nil
}

func (m *summaryMongoFixture) FindOneByDocID(_ context.Context, id string) (*ut.MsgDocModel, error) {
	m.calls++
	if id == "persisted:0" {
		return &ut.MsgDocModel{Msg: []*ut.MsgInfoModel{{Msg: &ut.MsgDataModel{Seq: 1, KeyVersion: 9}}}}, nil
	}
	return nil, mongo.ErrNoDocuments
}
func TestConversationSummaryReadsLatestCacheAndHandlesAbsentSlots(t *testing.T) {
	mongoStore := &summaryMongoFixture{}
	db := &commonMsgDatabase{cache: summaryCacheFixture{}, msgDocDatabase: mongoStore}
	messages, err := db.FindOneByDocIDs(context.Background(), []string{"cached", "persisted", "empty", "missing"}, map[string]int64{"cached": 1, "persisted": 1, "empty": 0, "missing": 1})
	if err != nil || len(messages) != 2 || messages["cached"].KeyVersion != 7 || messages["persisted"].KeyVersion != 9 || mongoStore.calls != 2 {
		t.Fatal("latest cache/persisted/empty summary regression", err)
	}
	db.cache = summaryCacheFixture{fail: true}
	if _, err := db.FindOneByDocIDs(context.Background(), []string{"cached"}, map[string]int64{"cached": 1}); err == nil {
		t.Fatal("cache failure masked as empty summary")
	}
}
