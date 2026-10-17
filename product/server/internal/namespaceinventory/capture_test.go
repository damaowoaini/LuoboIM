package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
)

func fixtureIdentity() Identity {
	return Identity{Binding: Binding{Tenant: "synthetic-tenant", Database: "synthetic_db", StorageInstance: "1cfad324-14cd-40bf-b688-2736144cbf32", ReplicaSet: "synthetic-rs", RedisRunID: strings.Repeat("a", 40), RedisDB: 15}, MongoProcess: strings.Repeat("b", 24), MongoTopology: 1, RedisReplication: strings.Repeat("c", 40)}
}

type scriptedSource struct {
	ids                 []Identity
	identityCalls, pass int
	mongo               [][]MongoRow
	redis               [][]RedisRow
}

func (s *scriptedSource) Identity(context.Context) (Identity, error) {
	at := s.identityCalls
	s.identityCalls++
	if len(s.ids) == 0 {
		return fixtureIdentity(), nil
	}
	if at >= len(s.ids) {
		at = len(s.ids) - 1
	}
	return s.ids[at], nil
}
func (s *scriptedSource) Mongo(ctx context.Context, visit func(MongoRow) error) error {
	s.pass++
	if len(s.mongo) == 0 {
		return nil
	}
	at := s.pass - 1
	if at >= len(s.mongo) {
		at = len(s.mongo) - 1
	}
	for _, r := range s.mongo[at] {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := visit(r); e != nil {
			return e
		}
	}
	return nil
}
func (s *scriptedSource) Redis(ctx context.Context, visit func(RedisRow) error) error {
	if len(s.redis) == 0 {
		return nil
	}
	at := s.pass - 1
	if at >= len(s.redis) {
		at = len(s.redis) - 1
	}
	for _, r := range s.redis[at] {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := visit(r); e != nil {
			return e
		}
	}
	return nil
}
func rawRow(t *testing.T, collection, key string, value interface{}) MongoRow {
	t.Helper()
	raw, e := bson.Marshal(value)
	if e != nil {
		t.Fatal("synthetic BSON encoding failed")
	}
	return MongoRow{Collection: collection, KeySHA: digest([]byte(key)), Raw: raw}
}
func captureFixture(t *testing.T, s Source) *Snapshot {
	t.Helper()
	snapshot, e := Capture(context.Background(), s, fixtureIdentity().Binding, DefaultLimits())
	if e != nil {
		t.Fatalf("capture failed: %v", e)
	}
	return snapshot
}

func TestInventoryPreservesCollisionAndErasedEvidence(t *testing.T) {
	normal := msgprocessor.NewOptions(msgprocessor.WithNotNotification(true))
	doc := table.MsgDocModel{DocID: "si_a_b_c:0", Msg: []*table.MsgInfoModel{
		{Msg: &table.MsgDataModel{SendID: "a_b", RecvID: "c", SessionType: constant.SingleChatType, Options: normal, Seq: 1}},
		{Msg: &table.MsgDataModel{SendID: "a", RecvID: "b_c", SessionType: constant.SingleChatType, Options: normal, Seq: 2}},
		{}, nil,
	}}
	notice := table.MsgDocModel{DocID: "n_a_b:0", Msg: []*table.MsgInfoModel{{Msg: &table.MsgDataModel{SendID: "a", RecvID: "b", SessionType: constant.NotificationChatType, Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(false)), Seq: 1}}}}
	if msgprocessor.Options(notice.Msg[0].Msg.Options).IsNotNotification() {
		t.Fatal("synthetic n_ fixture must explicitly select notification options")
	}
	rows := []MongoRow{rawRow(t, "msg", "single", doc), rawRow(t, "msg", "notice", notice), rawRow(t, "group", "group", bson.M{"group_id": "a_b"})}
	snapshot := captureFixture(t, &scriptedSource{mongo: [][]MongoRow{rows}})
	if len(snapshot.Mongo) != 3 || snapshot.State != State {
		t.Fatal("current inventory was dropped or promoted")
	}
	summary := snapshot.Public()
	if summary.CandidateCollisions != 2 || summary.ErasedSlots != 1 || summary.Reasons["historical-lifecycle-coverage-unverified"] != 1 {
		t.Fatal("ambiguous origins or erased evidence were lost")
	}
	for _, o := range snapshot.Observations {
		if o.RawHead != nil {
			t.Fatal("message slot maxima became a zero/authoritative head")
		}
	}
	encoded, e := json.Marshal(summary)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{"synthetic-tenant", "synthetic_db", "si_a_b_c", "n_a_b", "1cfad324"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("public summary contains a private identifier")
		}
	}
	// Even a direct caller cannot inject arbitrary private strings into the
	// public reason histogram by modifying the cached derived observations.
	snapshot.Observations[0].Reasons = []string{"do-not-print-this-private-value"}
	snapshot.Reasons = []string{"do-not-print-this-private-value"}
	encoded, _ = json.Marshal(snapshot.Public())
	if strings.Contains(string(encoded), "do-not-print-this-private-value") {
		t.Fatal("public reason injection")
	}
}

func TestInventoryRejectsChangeRestartAndLimits(t *testing.T) {
	row := rawRow(t, "group", "one", bson.M{"group_id": "g"})
	changed := rawRow(t, "group", "one", bson.M{"group_id": "changed"})
	id := fixtureIdentity()
	restart := id
	restart.MongoProcess = strings.Repeat("d", 24)
	redisRestart := id
	redisRestart.RedisReplication = strings.Repeat("e", 40)
	for _, tc := range []struct {
		name   string
		s      *scriptedSource
		limits Limits
		want   error
	}{
		{"changed-document", &scriptedSource{mongo: [][]MongoRow{{row}, {changed}}}, DefaultLimits(), ErrChanged},
		{"mongo-restart", &scriptedSource{ids: []Identity{id, restart}}, DefaultLimits(), ErrIdentity},
		{"redis-failover", &scriptedSource{ids: []Identity{id, redisRestart}}, DefaultLimits(), ErrIdentity},
		{"changed-redis-head", &scriptedSource{redis: [][]RedisRow{{{Key: "MAX_SEQ:g", Type: "string", Dump: []byte("one"), RawHead: "1", PTTL: -1}}, {{Key: "MAX_SEQ:g", Type: "string", Dump: []byte("two"), RawHead: "2", PTTL: -1}}}}, DefaultLimits(), ErrChanged},
		{"bounded-before-retention", &scriptedSource{mongo: [][]MongoRow{{row}}}, Limits{Rows: 1, Bytes: 1, EntryBytes: 1, RedisElements: 1, Timeout: DefaultLimits().Timeout}, ErrLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, e := Capture(context.Background(), tc.s, id.Binding, tc.limits)
			if !errors.Is(e, tc.want) || snapshot != nil {
				t.Fatal("unstable or oversized observation was accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if snapshot, e := Capture(ctx, &scriptedSource{mongo: [][]MongoRow{{row}}}, id.Binding, DefaultLimits()); e == nil || snapshot != nil {
		t.Fatal("cancelled capture succeeded")
	}
}

func TestInventoryCounterClassesAndMissingHeadNeverInferZero(t *testing.T) {
	base := bson.D{{Key: "_id", Value: "counter"}, {Key: "epoch", Value: "synthetic-epoch"}, {Key: "conversation_id", Value: "sg_g"}, {Key: "maximum", Value: int64(7)}, {Key: "revision", Value: int64(1)}, {Key: "seed_maximum", Value: int64(3)}, {Key: "seed_evidence_sha256", Value: strings.Repeat("f", 64)}}
	for _, tc := range []struct {
		name     string
		value    bson.D
		wantHead bool
	}{
		{"raw-counter", base, true},
		{"schema-with-maximum", append(append(bson.D{}, base...), bson.E{Key: "kind", Value: "schema"}), false},
		{"nonstring-kind", append(append(bson.D{}, base...), bson.E{Key: "kind", Value: int64(1)}), false},
		{"negative", func() bson.D { v := append(bson.D{}, base...); v[3].Value = int64(-1); return v }(), false},
		{"overflow", func() bson.D { v := append(bson.D{}, base...); v[3].Value = int64(math.MaxInt64); return v }(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := classify(rawRow(t, "message_delivery_sequences_v1", tc.name, tc.value))
			if (o.RawHead != nil) != tc.wantHead {
				t.Fatal("mixed row decoded as a head")
			}
			if o.RawHead != nil && *o.RawHead != 7 {
				t.Fatal("raw head changed")
			}
		})
	}
	snapshot := captureFixture(t, &scriptedSource{mongo: [][]MongoRow{{rawRow(t, "conversation", "owner", bson.M{"owner_user_id": "owner", "conversation_id": "si_owner_peer", "user_id": "peer", "conversation_type": int32(constant.SingleChatType)})}}})
	if snapshot.Public().Reasons["no-observed-raw-head-zero-not-inferred"] != 1 {
		t.Fatal("missing head was hidden or inferred zero")
	}
	duplicate := rawRow(t, "group", "duplicate", bson.D{{Key: "group_id", Value: "one"}, {Key: "group_id", Value: "two"}})
	if o := classify(duplicate); len(o.Origins) != 0 || len(o.Reasons) != 1 || o.Reasons[0] != "malformed-or-duplicate-BSON" {
		t.Fatal("duplicate BSON keys became origin evidence")
	}
}
