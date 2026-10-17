package controller

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
)

type upperReply struct { closed bool; upper int64; err error }
type windowReaderFixture struct { replies []upperReply; calls int; owner, cid string }
func (f *windowReaderFixture) ReadGroupHistoryUpper(_ context.Context, cid, owner string) (bool, int64, error) {
	f.owner, f.cid = owner, cid
	i := min(f.calls, len(f.replies)-1)
	f.calls++
	r := f.replies[i]
	return r.closed, r.upper, r.err
}
type windowCacheFixture struct { cache.MsgModel; asked []int64 }
func (*windowCacheFixture) GetConversationUserMinSeq(context.Context, string, string) (int64, error) { return 50, nil }
func (*windowCacheFixture) GetMinSeq(context.Context, string) (int64, error) { return 1, nil }
func (*windowCacheFixture) GetMaxSeq(context.Context, string) (int64, error) { return 100, nil }
func (*windowCacheFixture) GetUserDelList(context.Context, string, string) ([]int64, error) { return nil, nil }
func (*windowCacheFixture) GetClientMessageSeqs(context.Context, string, []string) (map[string]int64, error) { return map[string]int64{"source":100}, nil }
func (f *windowCacheFixture) GetMessagesBySeq(_ context.Context, _ string, seqs []int64) ([]*sdkws.MsgData, []int64, error) {
	f.asked = append([]int64(nil), seqs...)
	result := make([]*sdkws.MsgData, 0, len(seqs))
	for _, seq := range seqs { result = append(result, &sdkws.MsgData{Seq:seq, ClientMsgID:"source"}) }
	return result, nil, nil
}

func TestGroupHistoryClosedZeroReturnsEmptyBeforeCacheBodyRead(t *testing.T) {
	for _, rangeRead := range []bool{false,true} {
		t.Run(map[bool]string{false:"exact",true:"range"}[rangeRead],func(t *testing.T) {
			c := &windowCacheFixture{}
			db := &commonMsgDatabase{cache:c, msgDocDatabase:&summaryMongoFixture{}, groupHistory:&windowReaderFixture{replies:[]upperReply{{closed:true}}}}
			var messages []*sdkws.MsgData
			var maximum int64
			var err error
			if rangeRead { _,maximum,messages,err=db.GetMsgBySeqsRange(context.Background(),"owner","sg_g",50,100,51,0) } else { _,maximum,messages,err=db.GetMsgBySeqs(context.Background(),"owner","sg_g",[]int64{50,100}) }
			if err!=nil || maximum!=0 || len(messages)!=0 || c.asked!=nil { t.Fatal("closed zero became legacy unlimited or read a body",err) }
		})
	}
}

func TestGroupHistoryFinalAuthorityReadClipsCachedBodyAndMaximum(t *testing.T) {
	c := &windowCacheFixture{}
	r := &windowReaderFixture{replies:[]upperReply{{},{closed:true,upper:60}}}
	db := &commonMsgDatabase{cache:c,msgDocDatabase:&summaryMongoFixture{},groupHistory:r}
	minimum,maximum,messages,err := db.GetMsgBySeqs(context.Background(),"owner","sg_g",[]int64{49,50,60,100})
	if err!=nil || minimum!=50 || maximum!=60 || len(messages)!=2 || messages[0].Seq!=50 || messages[1].Seq!=60 || !reflect.DeepEqual(c.asked,[]int64{50,60,100}) || r.calls!=2 || r.owner!="owner" || r.cid!="sg_g" { t.Fatal("final upper authority/lower floor/owner binding lost",err) }
}

func TestGroupHistoryRangePreservesLegacyNonzeroMaximumAndOwnerLowerFloor(t *testing.T) {
	c := &windowCacheFixture{}
	db := &commonMsgDatabase{cache:c,msgDocDatabase:&summaryMongoFixture{},groupHistory:&windowReaderFixture{replies:[]upperReply{{closed:true,upper:80}}}}
	minimum,maximum,messages,err := db.GetMsgBySeqsRange(context.Background(),"owner","sg_g",40,100,100,70)
	if err!=nil || minimum!=50 || maximum!=70 || len(messages)!=21 || messages[0].Seq!=50 || messages[20].Seq!=70 { t.Fatal("new window replaced an existing lower or legacy upper restriction",err) }
}

func TestGroupHistoryIdentityAndSummaryUseTheSameFinalWindow(t *testing.T) {
	c := &windowCacheFixture{}
	db := &commonMsgDatabase{cache:c,msgDocDatabase:&summaryMongoFixture{},groupHistory:&windowReaderFixture{replies:[]upperReply{{},{closed:true,upper:60}}}}
	if _,err:=db.GetMsgByClientMsgIDs(context.Background(),"owner","sg_g",[]string{"source"});err==nil { t.Fatal("exact client identity bypassed owner upper history window") }
	r := &windowReaderFixture{replies:[]upperReply{{closed:true,upper:60}}}
	db.groupHistory=r
	ctx:=context.WithValue(context.Background(),constant.OpUserID,"owner")
	got,err:=db.FindOneByDocIDs(ctx,[]string{"sg_g"},map[string]int64{"sg_g":100})
	if err!=nil || len(got)!=0 || r.owner!="owner" { t.Fatal("conversation summary bypassed the final owner window",err) }
}

func TestGroupHistoryMissingOrFailedAuthorityNeverReturnsCachedBody(t *testing.T) {
	for _, replies := range [][]upperReply{{{err:errors.New("synthetic unprepared owner")}},{{},{err:errors.New("synthetic final catalog failure")}},{{upper:3}}} {
		c:=&windowCacheFixture{}
		db:=&commonMsgDatabase{cache:c,msgDocDatabase:&summaryMongoFixture{},groupHistory:&windowReaderFixture{replies:replies}}
		if _,_,body,err:=db.GetMsgBySeqs(context.Background(),"owner","sg_g",[]int64{100});err==nil || len(body)!=0 { t.Fatal("authority error became cached history") }
	}
}

func TestGroupHistoryLegacyAndNotificationCIDDoNotBorrowGroupWindow(t *testing.T) {
	for _, cid:=range []string{"n_g","si_pair"} {
		c:=&windowCacheFixture{}
		r:=&windowReaderFixture{replies:[]upperReply{{err:errors.New("must not be queried")}}}
		db:=&commonMsgDatabase{cache:c,msgDocDatabase:&summaryMongoFixture{},groupHistory:r}
		_,maximum,body,err:=db.GetMsgBySeqs(context.Background(),"owner",cid,[]int64{100})
		if err!=nil || maximum!=100 || len(body)!=1 || r.calls!=0 { t.Fatal("non-group CID borrowed a group history window",err) }
	}
	db:=&commonMsgDatabase{cache:&windowCacheFixture{},msgDocDatabase:&summaryMongoFixture{}}
	if _,_,body,err:=db.GetMsgBySeqs(context.Background(),"owner","sg_g",[]int64{100});err!=nil || len(body)!=1 { t.Fatal("explicit legacy mode changed",err) }
}
