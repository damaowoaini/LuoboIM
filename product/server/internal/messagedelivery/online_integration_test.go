package messagedelivery

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"google.golang.org/protobuf/proto"
)

type partialProjectionFailure struct{ real *ownedProjection }

func (p partialProjectionFailure) RaiseMaximum(ctx context.Context, cid string, v int64) error {
	return p.real.RaiseMaximum(ctx, cid, v)
}
func (p partialProjectionFailure) RaiseSenderRead(context.Context, string, string, int64) error {
	return errors.New("synthetic boundary failure after real Redis max ACK")
}

// This transport intentionally models the external boundary, not the actual
// gateway or SDK implementation. Concrete gateway wire interpretation has its
// separate bufconn tests; actual SDK seq suppression has separate overlay tests.
type recordingOnline struct {
	calls     []OnlineCall
	lostFirst bool
	logical   map[string]bool
}

func (r *recordingOnline) Deliver(_ context.Context, call OnlineCall) (OnlineReport, error) {
	copyCall := call
	copyCall.Message = proto.Clone(call.Message).(*sdkws.MsgData)
	copyCall.Recipients = append([]string(nil), call.Recipients...)
	r.calls = append(r.calls, copyCall)
	report := OnlineReport{DispatchStarted: true}
	for _, uid := range call.Recipients {
		r.logical[uid+"\x00"+call.ConversationID+"\x00"+call.Message.ClientMsgID] = true
		report.ACKs = append(report.ACKs, OnlineACK{UserID: uid, ResponseSHA256: hash([]byte("synthetic matching gateway ACK" + uid))})
	}
	if r.lostFirst && len(r.calls) == 1 {
		return OnlineReport{DispatchStarted: true}, errors.New("synthetic gateway response lost after applying")
	}
	return report, nil
}

func TestMessageDeliveryMongoRedisLateOnlineRecoveryKeepsFrozenRecipientAndOriginalTime(t *testing.T) {
	ctx, _, s, db := sequenceMongo(t, nil)
	redis := projectionRedis(t)
	if err := s.PrepareSequence(ctx, redis.cid, Seed{MongoMax: 0, RedisMax: 0, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic raw known empty CID"))}); err != nil {
		t.Fatal(err)
	}
	input := sequenceFixture(t)
	var m sdkws.MsgData
	if err := proto.Unmarshal(input.Branches[0].Payload, &m); err != nil {
		t.Fatal(err)
	}
	m.GroupID = strings.TrimPrefix(redis.cid, "sg_")
	m.RecvID = m.GroupID
	m.SendID = redis.sender
	m.CreateTime = time.Now().Add(-31 * time.Second).UnixMilli()
	m.SendTime = m.CreateTime
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(&m)
	if err != nil {
		t.Fatal(err)
	}
	input.Branches[0].ConversationID = redis.cid
	input.Branches[0].Payload = body
	input.Branches[0].Recipients = []string{"frozen-recipient"}
	if _, err := s.AcceptCommitted(ctx, input); err != nil {
		t.Fatal(err)
	}
	transport := &recordingOnline{lostFirst: true, logical: map[string]bool{}}
	reader := controller.NewReadOnlyMessageReader(redis.client, db)
	if err := s.Project(ctx, input.Branches[0].ID, partialProjectionFailure{redis}); err == nil {
		t.Fatal("partial projection error was swallowed")
	}
	if err := s.RecoverOnline(ctx, input.Branches[0].ID, reader, transport); !errors.Is(err, ErrUnprepared) || len(transport.calls) != 0 {
		t.Fatal("online sent before required max/read projection finished", err)
	}
	restarted, err := NewStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Project(ctx, input.Branches[0].ID, redis); err != nil {
		t.Fatal(err)
	}
	// A later cache/group/member/user list must not expand the committed roster.
	input.Branches[0].Recipients[0] = "foreign-later-roster"
	if err := restarted.RecoverOnline(ctx, input.Branches[0].ID, reader, transport); !errors.Is(err, ErrOnlineUnconfirmed) {
		t.Fatal("lost online ACK was called complete", err)
	}
	restartedAgain, err := NewStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := restartedAgain.RecoverOnline(ctx, input.Branches[0].ID, reader, transport); err != nil {
		t.Fatal(err)
	}
	if len(transport.calls) != 2 || len(transport.logical) != 1 {
		t.Fatal("same reliable event changed logical identity")
	}
	for _, call := range transport.calls {
		if len(call.Recipients) != 1 || call.Recipients[0] != "frozen-recipient" || call.Message.SendTime != m.SendTime || call.Message.Seq != 1 || call.Message.ClientMsgID != m.ClientMsgID || time.Now().UnixMilli()-call.Message.SendTime < 10000 {
			t.Fatal("late recovery changed time/source/seq/recipient or used old age drop")
		}
	}
	if err := restartedAgain.RecoverOnline(ctx, input.Branches[0].ID, reader, transport); err != nil || len(transport.calls) != 2 {
		t.Fatal("known online ACK replayed", err)
	}
	maxValue, err := redis.client.Get(ctx, "MAX_SEQ:"+redis.cid).Int64()
	if err != nil || maxValue != 1 {
		t.Fatal("real Redis max projection missing", err)
	}
	readValue, err := redis.client.Get(ctx, "HAS_READ_SEQ:"+redis.sender+":"+redis.cid).Int64()
	if err != nil || readValue != 1 {
		t.Fatal("real sender-read projection missing", err)
	}
}

func TestMessageDeliveryRedisMaxProjectionPreservesInt64AndNeverLowers(t *testing.T) {
	redis := projectionRedis(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, v := range []int64{9007199254740993, 9007199254740992, math.MaxInt64, math.MaxInt64 - 1} {
		if err := redis.RaiseMaximum(ctx, redis.cid, v); err != nil {
			t.Fatal(err)
		}
	}
	got, err := redis.client.Get(ctx, "MAX_SEQ:"+redis.cid).Int64()
	if err != nil || got != math.MaxInt64 {
		t.Fatal("projection lost integer precision or lowered committed max", err)
	}
}
