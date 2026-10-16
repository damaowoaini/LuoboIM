package messagedelivery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

type sequenceBarrierKey struct{}

func TestMessageDeliveryMongoOrdinaryAndNotificationShareOneConcurrentSequence(t *testing.T) {
	var entered atomic.Int32
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(sequenceBarrierKey{}) != true || e.CommandName != "update" {
			return
		}
		name, _ := e.Command.Lookup("update").StringValueOK()
		if name != Sequences {
			return
		}
		if entered.Add(1) <= 2 {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}}
	ctx, authority, s, db := sequenceMongo(t, monitor)
	workerCtx, cancelWorkers := context.WithCancel(ctx)
	var workers sync.WaitGroup
	done := make(chan struct{})

	initializeSequence(t, ctx, authority, s, "sg_g", 40)
	model := table.MsgDocModel{}
	driver := unrelation.NewMsgMongoDriver(db)
	if err := driver.EnsureMessageDoc(ctx, model.GetDocID("sg_g", 40)); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.UpdateMsg(ctx, model.GetDocID("sg_g", 40), model.GetMsgIndex(40), "msg", &table.MsgDataModel{Seq: 40, ClientMsgID: "prior-message", Content: "prior history"}); err != nil {
		t.Fatal(err)
	}
	ordinary, notification := sequenceFixture(t), sequenceFixture(t)
	notification.ID = uuid.NewString()
	notification.Branches[0].ID = uuid.NewString()
	var m sdkws.MsgData
	if proto.Unmarshal(notification.Branches[0].Payload, &m) != nil {
		t.Fatal("fixture")
	}
	m.MsgFrom = constant.SysMsgType
	m.ContentType = constant.GroupInfoSetNotification
	m.ClientMsgID = "ga3:synthetic-frozen-effect"
	m.ServerMsgID = "stable-notification-server"
	m.Content = []byte("synthetic frozen notification")
	var err error
	notification.Branches[0].Payload, err = proto.MarshalOptions{Deterministic: true}.Marshal(&m)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		r Receipt
		e error
	}
	results := make(chan result, 2)
	workers.Add(2)
	for _, job := range []struct {
		fence string
		input Envelope
	}{{"left", ordinary}, {"right", notification}} {
		go func(fence string, input Envelope) {
			defer workers.Done()
			var r Receipt
			e := sequenceTxn(context.WithValue(workerCtx, sequenceBarrierKey{}, true), authority, fence, func(tx context.Context) error { var err error; r, err = s.Accept(tx, input); return err })
			results <- result{r, e}
		}(job.fence, job.input)
	}
	go func() { workers.Wait(); close(done) }()
	t.Cleanup(func() {
		cancelWorkers()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false)
			t.Error("concurrent sequence worker drain unconfirmed")
		}
	})
	ready := true
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(3 * time.Second):
			ready = false
		}
	}
	close(release)
	var receipts []Receipt
	var resultErrors []error
	for i := 0; i < 2; i++ {
		select {
		case result := <-results:
			if result.e != nil {
				resultErrors = append(resultErrors, result.e)
			}
			receipts = append(receipts, result.r)
		case <-ctx.Done():
			t.Fatal("concurrent receiver did not stop")
		}
	}
	if len(resultErrors) > 0 {
		t.Fatal("concurrent receipt failed", resultErrors)
	}
	if !ready || entered.Load() < 2 {
		t.Fatal("test did not establish two concurrent actual sequence updates")
	}
	a, b := receipts[0].Branches[0].Seq, receipts[1].Branches[0].Seq
	if a == b || a+b != 83 || min(a, b) != 41 {
		t.Fatal("ordinary and notification sequence collided or skipped history", a, b)
	}
	var counter counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", "sg_g")}).Decode(&counter); err != nil || counter.Maximum != 42 {
		t.Fatal("counter not committed", err)
	}
	var doc table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": model.GetDocID("sg_g", 40)}).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if doc.Msg[39].Msg.Content != "prior history" || doc.Msg[40].Msg == nil || doc.Msg[41].Msg == nil || doc.Msg[40].Msg.ClientMsgID == doc.Msg[41].Msg.ClientMsgID {
		t.Fatal("actual OpenIM history layout/identity changed")
	}
	for _, name := range []string{Receipts, Effects} {
		count, err := db.Collection(name).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}})
		if err != nil || count != 2 {
			t.Fatal("receipt and downstream duties split from persistence", name, err)
		}
	}
}

func TestMessageDeliveryMongoReplayCannotReviveDeletedHistoryOrDuplicateDuty(t *testing.T) {
	ctx, authority, s, db := sequenceMongo(t, nil)
	initializeSequence(t, ctx, authority, s, "sg_g", 0)
	input := sequenceFixture(t)
	var first Receipt
	if err := sequenceTxn(ctx, authority, "left", func(tx context.Context) error { var err error; first, err = s.Accept(tx, input); return err }); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("msg").UpdateOne(ctx, bson.M{"doc_id": "sg_g:0"}, bson.M{"$set": bson.M{"msgs.0.global_deleted": true, "msgs.0.msg": nil}}); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	var replay Receipt
	if err := sequenceTxn(ctx, authority, "right", func(tx context.Context) error { var err error; replay, err = restarted.Accept(tx, input); return err }); err != nil {
		t.Fatal(err)
	}
	if replay.ID != first.ID || replay.Branches[0].Seq != first.Branches[0].Seq {
		t.Fatal("restart replaced original receipt")
	}
	var row table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}).Decode(&row); err != nil || row.Msg[0].Msg != nil || !row.Msg[0].GlobalDeleted {
		t.Fatal("receipt replay restored removed plaintext", err)
	}
	count, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}})
	if err != nil || count != 1 {
		t.Fatal("receipt replay duplicated push/cache obligation", err)
	}
	input.Branches[0].Recipients = append(input.Branches[0].Recipients, "other")
	if err := sequenceTxn(ctx, authority, "right", func(tx context.Context) error { _, err := restarted.Accept(tx, input); return err }); !errors.Is(err, ErrConflict) {
		t.Fatal("same source changed frozen recipient", err)
	}
}

func TestMessageDeliveryMongoPriorGlobalTombstoneSuppressesPlaintextAndPush(t *testing.T) {
	ctx, authority, s, db := sequenceMongo(t, nil)
	initializeSequence(t, ctx, authority, s, "sg_g", 0)
	driver := unrelation.NewMsgMongoDriver(db)
	if err := driver.EnsureMessageDoc(ctx, "sg_g:0"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("msg").UpdateOne(ctx, bson.M{"doc_id": "sg_g:0"}, bson.M{"$set": bson.M{"msgs.0.global_deleted": true, "msgs.0.del_list": []string{"owner"}}}); err != nil {
		t.Fatal(err)
	}
	input := sequenceFixture(t)
	var accepted Receipt
	if err := sequenceTxn(ctx, authority, "left", func(tx context.Context) error { var err error; accepted, err = s.Accept(tx, input); return err }); err != nil {
		t.Fatal(err)
	}
	if !accepted.Branches[0].Hidden || accepted.Branches[0].Seq != 1 {
		t.Fatal("future tombstone was ignored")
	}
	var duty bson.M
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": input.Branches[0].ID}).Decode(&duty); err != nil {
		t.Fatal(err)
	}
	if duty["payload"] != nil || duty["state"] != "ready" || duty["max_pending"] != true || duty["push_pending"] != false || duty["cache_pending"] != false {
		t.Fatal("hidden message was retained for later cache/push")
	}
}

func TestMessageDeliveryMongoMissingCounterAndBadSeedNeverOverwriteLegacySlot(t *testing.T) {
	ctx, authority, s, db := sequenceMongo(t, nil)
	input := sequenceFixture(t)
	if err := sequenceTxn(ctx, authority, "left", func(tx context.Context) error { _, err := s.Accept(tx, input); return err }); !errors.Is(err, ErrUnprepared) {
		t.Fatal("missing migration seed silently fell back to Redis", err)
	}
	// Prove that the earlier failed unprepared attempt did not persist an
	// idempotency receipt, obligation, or sequence before testing the collision.
	for _, collection := range []string{Sequences, Receipts, Effects} {
		count, err := db.Collection(collection).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}})
		if err != nil || count != 0 {
			t.Fatal("unprepared attempt left durable state", collection, count, err)
		}
	}
	initializeSequence(t, ctx, authority, s, "sg_g", 0)
	driver := unrelation.NewMsgMongoDriver(db)
	if err := driver.EnsureMessageDoc(ctx, "sg_g:0"); err != nil {
		t.Fatal(err)
	}
	written, err := driver.UpdateMsg(ctx, "sg_g:0", 0, "msg", &table.MsgDataModel{Seq: 1, ClientMsgID: "legacy", Content: "must survive"})
	if err != nil {
		t.Fatal(err)
	}
	if written == nil || written.MatchedCount != 1 || written.ModifiedCount != 1 {
		t.Fatal("legacy fixture write was not acknowledged exactly once")
	}
	var prior table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}).Decode(&prior); err != nil {
		t.Fatal("legacy fixture prestate unavailable", err)
	}
	if len(prior.Msg) != 100 || prior.Msg[0] == nil || prior.Msg[0].Msg == nil || prior.Msg[0].GlobalDeleted ||
		prior.Msg[0].Msg.Seq != 1 || prior.Msg[0].Msg.ClientMsgID != "legacy" || prior.Msg[0].Msg.Content != "must survive" {
		t.Fatal("legacy fixture prestate differs from the real occupied slot")
	}
	legacyRows, err := db.Collection("msg").CountDocuments(ctx, bson.M{"doc_id": "sg_g:0"})
	if err != nil || legacyRows != 1 {
		t.Fatal("legacy fixture document identity is not unique", legacyRows, err)
	}
	// Diagnostic only: compare the production dotted-null predicate with an
	// explicit array-element read. Neither query changes data or expectations.
	dottedNull, err := db.Collection("msg").CountDocuments(ctx, bson.M{"doc_id": "sg_g:0", "msgs.0.msg": nil, "msgs.0.global_deleted": bson.M{"$ne": true}})
	if err != nil {
		t.Fatal("dotted slot diagnostic unavailable", err)
	}
	exactNull, err := db.Collection("msg").CountDocuments(ctx, bson.M{"doc_id": "sg_g:0", "$expr": bson.M{"$let": bson.M{
		"vars": bson.M{"slot": bson.M{"$arrayElemAt": bson.A{"$msgs", 0}}},
		"in":   bson.M{"$eq": bson.A{"$$slot.msg", nil}},
	}}})
	if err != nil {
		t.Fatal("exact slot diagnostic unavailable", err)
	}
	t.Logf("collision_prestate: acknowledged_legacy_slot=true document_rows=%d dotted_null_matches=%d exact_null_matches=%d", legacyRows, dottedNull, exactNull)

	attemptErr := sequenceTxn(ctx, authority, "left", func(tx context.Context) error { _, err := s.Accept(tx, input); return err })
	// Retain safe, fixed-shape poststate evidence even when the original
	// ErrConflict assertion terminates this case immediately afterwards.
	var observed table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}).Decode(&observed); err != nil {
		t.Fatal("collision poststate unavailable", err)
	}
	preserved := len(observed.Msg) == 100 && observed.Msg[0] != nil && observed.Msg[0].Msg != nil &&
		observed.Msg[0].Msg.Seq == 1 && observed.Msg[0].Msg.ClientMsgID == "legacy" && observed.Msg[0].Msg.Content == "must survive"
	var observedCounter counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", "sg_g")}).Decode(&observedCounter); err != nil {
		t.Fatal("collision sequence poststate unavailable", err)
	}
	t.Logf("collision_poststate: accept_success=%t expected_conflict=%t legacy_preserved=%t counter_maximum=%d", attemptErr == nil, errors.Is(attemptErr, ErrConflict), preserved, observedCounter.Maximum)
	for _, collection := range []string{Receipts, Effects} {
		count, err := db.Collection(collection).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}})
		if err != nil {
			t.Fatal("collision duty poststate unavailable", err)
		}
		t.Logf("collision_poststate: collection=%s durable_rows=%d", collection, count)
	}
	if err := attemptErr; !errors.Is(err, ErrConflict) {
		t.Fatal("bad migration seed overwrote prior message", err)
	}
	var c counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", "sg_g")}).Decode(&c); err != nil || c.Maximum != 0 {
		t.Fatal("failed persistence still advanced sequence", err)
	}
	for _, name := range []string{Receipts, Effects} {
		count, err := db.Collection(name).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}})
		if err != nil || count != 0 {
			t.Fatal("failed persistence committed partial duties", err)
		}
	}
	var legacy table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}, options.FindOne()).Decode(&legacy); err != nil || legacy.Msg[0].Msg.Content != "must survive" {
		t.Fatal("legacy body changed", err)
	}
}
