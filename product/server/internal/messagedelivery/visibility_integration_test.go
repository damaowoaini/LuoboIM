package messagedelivery

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Only scalar max/read projection is faked here. The source slot, floor,
// receipt, duty and all visibility mutations are in the real isolated Mongo
// replica set. No gateway/Redis/physical remote erasure claim is made.
type discardProjection struct{}

func (discardProjection) RaiseMaximum(context.Context, string, int64) error            { return nil }
func (discardProjection) RaiseSenderRead(context.Context, string, string, int64) error { return nil }

type staleBodyReader struct {
	message     *sdkws.MsgData
	beforeReply func() error
	calls       int
}

func (r *staleBodyReader) GetMsgBySeqs(_ context.Context, _ string, _ string, _ []int64) (int64, int64, []*sdkws.MsgData, error) {
	r.calls++
	if r.beforeReply != nil {
		fn := r.beforeReply
		r.beforeReply = nil
		if err := fn(); err != nil {
			return 0, 0, nil, err
		}
	}
	return 1, 1, []*sdkws.MsgData{proto.Clone(r.message).(*sdkws.MsgData)}, nil
}
func prepareVisibility(t *testing.T) (context.Context, *Store, *mongo.Database, Envelope, table.MsgDocModelInterface, *staleBodyReader) {
	t.Helper()
	ctx, _, s, db := sequenceMongo(t, nil)
	e := sequenceFixture(t)
	e.Branches[0].Recipients = []string{"a"}
	if err := s.PrepareSequence(ctx, "sg_g", Seed{MongoMax: 0, RedisMax: 0, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic known empty visibility source"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptCommitted(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := s.Project(ctx, e.Branches[0].ID, discardProjection{}); err != nil {
		t.Fatal(err)
	}
	driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, s)
	if err != nil {
		t.Fatal(err)
	}
	var m sdkws.MsgData
	if err := proto.Unmarshal(e.Branches[0].Payload, &m); err != nil {
		t.Fatal(err)
	}
	m.Seq = 1
	return ctx, s, db, e, driver, &staleBodyReader{message: &m}
}
func assertRetiredDuty(t *testing.T, ctx context.Context, s *Store, e Envelope) {
	t.Helper()
	d, err := s.readDuty(ctx, e.Branches[0].ID)
	if err != nil || len(d.Payload) != 0 || len(d.Recipients) != 0 || d.OnlinePending || d.OfflinePending || d.PushPending || !d.Hidden {
		t.Fatal("pending plaintext/recipient duty survived visibility commit", err)
	}
}
func sourceBytes(t *testing.T, ctx context.Context, db *mongo.Database) []byte {
	t.Helper()
	var row bson.Raw
	err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), row...)
}

func TestDeliveryMongoVisibilityWriterRetiresDelayedPlaintext(t *testing.T) {
	cases := []struct {
		name  string
		write func(context.Context, table.MsgDocModelInterface) error
	}{
		{"physical-slot", func(ctx context.Context, m table.MsgDocModelInterface) error {
			return m.DeleteMsgsInOneDocByIndex(ctx, "sg_g:0", []int{0})
		}},
		{"physical-document", func(ctx context.Context, m table.MsgDocModelInterface) error {
			return m.DeleteDocs(ctx, []string{"sg_g:0"})
		}},
		{"global-clear", func(ctx context.Context, m table.MsgDocModelInterface) error {
			return m.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "sg_g", MinSeq: 2}})
		}},
		{"owner-clear", func(ctx context.Context, m table.MsgDocModelInterface) error {
			return m.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "sg_g", UserID: "a", MinSeq: 2}})
		}},
		{"personal-delete", func(ctx context.Context, m table.MsgDocModelInterface) error {
			_, err := m.PushUnique(ctx, "sg_g:0", 0, "del_list", []string{"a"})
			return err
		}},
		{"revoke", func(ctx context.Context, m table.MsgDocModelInterface) error {
			_, err := m.UpdateMsg(ctx, "sg_g:0", 0, "revoke", &table.RevokeModel{UserID: "sender", Time: 3})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, s, db, e, driver, reader := prepareVisibility(t)
			if err := tc.write(ctx, driver); err != nil {
				t.Fatal("actual visibility writer failed", err)
			}
			assertRetiredDuty(t, ctx, s, e) // Assert immediately, before a recovery scan.
			afterDeletion := sourceBytes(t, ctx, db)
			restarted, err := NewStore(db, s.epoch, s.activationSHA)
			if err != nil {
				t.Fatal(err)
			}
			transport := &recordingOnline{logical: map[string]bool{}}
			if err := restarted.RecoverOnline(ctx, e.Branches[0].ID, reader, transport); err != nil || len(transport.calls) != 0 || reader.calls != 0 {
				t.Fatal("restart redispatched deleted old plaintext", err)
			}
			old, err := restarted.Resolve(ctx, e)
			if err != nil || old.Branches[0].Seq != 1 {
				t.Fatal("minimal receipt was lost with body", err)
			}
			replay, err := restarted.AcceptCommitted(ctx, e)
			if err != nil || replay.Branches[0].Seq != 1 {
				t.Fatal("same accepted intent did not resolve", err)
			}
			if !bytes.Equal(afterDeletion, sourceBytes(t, ctx, db)) {
				t.Fatal("accepted receipt replay rebuilt deleted source")
			}
			assertRetiredDuty(t, ctx, restarted, e)
		})
	}
}

func TestDeliveryMongoDeleteBetweenCachedReadAndFinalAdmissionSendsNothing(t *testing.T) {
	ctx, s, db, e, driver, reader := prepareVisibility(t)
	// Deterministic barrier: the caller already holds a stale body, then the
	// actual DAO commits its tombstone+duty erase before returning that body.
	reader.beforeReply = func() error { return driver.DeleteMsgsInOneDocByIndex(ctx, "sg_g:0", []int{0}) }
	transport := &recordingOnline{logical: map[string]bool{}}
	if err := s.RecoverOnline(ctx, e.Branches[0].ID, reader, transport); !errors.Is(err, ErrConflict) || reader.calls != 1 || len(transport.calls) != 0 {
		t.Fatal("final claim admitted a body erased during cache read", err)
	}
	assertRetiredDuty(t, ctx, s, e)
	restarted, err := NewStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.RecoverOnline(ctx, e.Branches[0].ID, reader, transport); err != nil || len(transport.calls) != 0 {
		t.Fatal("restart revived erased duty", err)
	}
}

func TestDeliveryMongoOwnerRestrictionPreservesOtherFrozenRecipient(t *testing.T) {
	ctx, _, s, db := sequenceMongo(t, nil)
	e := sequenceFixture(t)
	if err := s.PrepareSequence(ctx, "sg_g", Seed{MongoMax: 0, RedisMax: 0, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic owner isolation"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptCommitted(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := s.Project(ctx, e.Branches[0].ID, discardProjection{}); err != nil {
		t.Fatal(err)
	}
	driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "sg_g", UserID: "a", MinSeq: 2}}); err != nil {
		t.Fatal(err)
	}
	d, err := s.readDuty(ctx, e.Branches[0].ID)
	if err != nil || len(d.Payload) == 0 || d.Hidden {
		t.Fatal("personal restriction erased peer's allowed duty", err)
	}
	var m sdkws.MsgData
	if err := proto.Unmarshal(e.Branches[0].Payload, &m); err != nil {
		t.Fatal(err)
	}
	m.Seq = 1
	reader := &staleBodyReader{message: &m}
	transport := &recordingOnline{logical: map[string]bool{}}
	if err := s.RecoverOnline(ctx, e.Branches[0].ID, reader, transport); err != nil {
		t.Fatal(err)
	}
	if len(transport.calls) != 1 || len(transport.calls[0].Recipients) != 1 || transport.calls[0].Recipients[0] != "z" {
		t.Fatal("owner clear re-exposed owner or expanded peer roster")
	}
	d, err = s.readDuty(ctx, e.Branches[0].ID)
	if err != nil || d.State != "done" || len(d.Payload) != 0 {
		t.Fatal("finished duty retained plaintext", err)
	}
}

func TestDeliveryMongoActualRetentionCommitRetiresOnlyOwnerDuty(t *testing.T) {
	ctx, s, db, e, _, reader := prepareVisibility(t)
	for _, name := range []string{"conversation", retention.OutboxCollection} {
		if err := db.CreateCollection(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	policy := retention.Policy{Owner: "a", Conversation: "sg_g", Group: "g", ConversationType: 3, Enabled: true, Duration: 30, Revision: 1, Prepared: true}
	if _, err := db.Collection("conversation").InsertOne(ctx, policy); err != nil {
		t.Fatal(err)
	}
	store := retention.NewMongoStore(db)
	store.Now = func() time.Time { return now }
	store.PendingDelivery = s
	store.LegacyFloors = func(context.Context, string, string) (int64, int64, error) { return 0, 0, nil } // Exact fresh DB has no legacy Redis floor.
	floor, err := store.Floor(ctx, policy)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := retention.BuildPlan(ctx, store, policy, floor, now, 1)
	if err != nil || plan.ToFloor != 2 {
		t.Fatal("synthetic expired prefix not planned", err)
	}
	if err := store.Commit(ctx, plan); err != nil {
		t.Fatal("actual retention commit failed", err)
	}
	assertRetiredDuty(t, ctx, s, e)
	count, err := db.Collection(retention.OutboxCollection).CountDocuments(ctx, bson.M{})
	if err != nil || count != 1 {
		t.Fatal("retention outbox not atomic with duty erase", err)
	}
	restarted, err := NewStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	transport := &recordingOnline{logical: map[string]bool{}}
	if err := restarted.RecoverOnline(ctx, e.Branches[0].ID, reader, transport); err != nil || len(transport.calls) != 0 {
		t.Fatal("retention restart pushed expired body", err)
	}
}

func TestDeliveryMongoUnpreparedRedactionRollsBackSourceDeletion(t *testing.T) {
	ctx, s, db, _, driver, _ := prepareVisibility(t)
	before := sourceBytes(t, ctx, db)
	if _, err := db.Collection(Effects).UpdateOne(ctx, bson.M{"_id": SchemaID}, bson.M{"$set": bson.M{"activation_sha256": hash([]byte("foreign activation"))}}); err != nil {
		t.Fatal(err)
	}
	if err := driver.DeleteMsgsInOneDocByIndex(ctx, "sg_g:0", []int{0}); err == nil {
		t.Fatal("unprepared writer silently degraded to raw deletion")
	}
	if !bytes.Equal(before, sourceBytes(t, ctx, db)) {
		t.Fatal("source changed without pending redaction authority")
	}
	// Restore only this random test DB's known synthetic sentinel for cleanup;
	// no runtime migration or production schema is touched.
	if _, err := db.Collection(Effects).UpdateOne(ctx, bson.M{"_id": SchemaID}, bson.M{"$set": bson.M{"activation_sha256": s.activationSHA}}); err != nil {
		t.Fatal(err)
	}
}

func TestDeliveryMongoExistingGlobalCutoffPreventsInitialPlaintextPersistence(t *testing.T) {
	ctx, _, s, db := sequenceMongo(t, nil)
	e := sequenceFixture(t)
	if err := s.PrepareSequence(ctx, "sg_g", Seed{MongoMax: 0, RedisMax: 0, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic known counter baseline"))}); err != nil {
		t.Fatal(err)
	}
	driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "sg_g", MinSeq: 2}}); err != nil {
		t.Fatal(err)
	}
	receipt, err := s.AcceptCommitted(ctx, e)
	if err != nil || !receipt.Branches[0].Hidden || receipt.Branches[0].Seq != 1 {
		t.Fatal("preexisting global cutoff was ignored at acceptance", err)
	}
	count, err := db.Collection("msg").CountDocuments(ctx, bson.M{"doc_id": "sg_g:0"})
	if err != nil || count != 0 {
		t.Fatal("hidden late input re-created plaintext source", err)
	}
	assertRetiredDuty(t, ctx, s, e)
}
