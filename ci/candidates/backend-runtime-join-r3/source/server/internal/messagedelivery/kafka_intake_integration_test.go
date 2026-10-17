package messagedelivery

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Reuse the existing exact loopback RS / nonce DB claim / drain / checked
// Drop+Disconnect fixture. No public accounts, Kafka or runtime DB are used.
func kafkaFixture(t *testing.T, monitor *event.CommandMonitor) (context.Context, *groupauthority.Store, *Store, *mongo.Database, relation.GroupMemberModelInterface) {
	t.Helper()
	ctx, authority, base, db, members := membershipFixture(t, monitor)
	s, err := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	return context.WithValue(ctx, constant.OpUserID, "owner"), authority, s, db, members
}
func kafkaFixtureRecord(t *testing.T, offset int64) KafkaRecord {
	t.Helper()
	e := membershipOrdinaryEnvelope(t)
	return KafkaRecord{Topic: "synthetic-owned-source", Partition: 0, Offset: offset, Key: []byte("sg_g"), Body: e.Branches[0].Payload, Actor: "owner", ContextSHA256: hash([]byte("synthetic exact named internal producer headers"))}
}

func kafkaPolicyFixture(t *testing.T, s *Store) (KafkaDeliveryPolicy, string) {
	t.Helper()
	p := KafkaDeliveryPolicy{Version: "ordinary-kafka-v1", Epoch: s.epoch, ActivationSHA256: s.activationSHA, ConfigurationSHA256: hash([]byte("synthetic configuration")), Discovery: "zookeeper", Vendor: "none"}
	digest, err := p.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	return p, digest
}

func TestKafkaIntakeMongoFrozenSourceReplayCannotRefreezeRosterOrRevivePlaintext(t *testing.T) {
	ctx, authority, s, db, members := kafkaFixture(t, nil)
	record := kafkaFixtureRecord(t, 1)
	snapshot, policy := kafkaPolicyFixture(t, s)
	first, err := s.AcceptKafkaRecord(ctx, record, policy, snapshot)
	if err != nil || len(first.Branches) != 1 || first.Branches[0].Seq != 1 {
		t.Fatal("actual intake did not commit shared sequence", err)
	}
	var duty deliveryDuty
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": first.Branches[0].ID}).Decode(&duty); err != nil || !reflect.DeepEqual(duty.Recipients, []string{"member", "owner"}) || duty.ProducerVersion != "ordinary-kafka-v1" || duty.PolicySHA != policy {
		t.Fatal("first authoritative roster/policy not frozen with receipt", err)
	}
	if _, err := s.readDuty(ctx, first.Branches[0].ID); !errors.Is(err, ErrUnprepared) {
		t.Fatal("old policy-blind recovery accepted new producer duty", err)
	}
	if _, _, err := removeAtBoundary(ctx, authority, s, db, members, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("msg").UpdateOne(ctx, bson.M{"doc_id": "sg_g:0"}, bson.M{"$set": bson.M{"msgs.0.msg": nil, "msgs.0.global_deleted": true}}); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewMembershipBoundStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	// Policy changes cannot retag or reject an already committed source.
	replay, err := restarted.AcceptKafkaRecord(ctx, record, hash([]byte("later runtime policy")))
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatal("replay consulted changed roster or invented another receipt", err)
	}
	resolved, err := restarted.ResolveKafkaRecord(ctx, record)
	if err != nil || !reflect.DeepEqual(resolved, first) {
		t.Fatal("same source read-only inspection changed the durable result", err)
	}
	missing := record
	missing.Offset++
	if _, err := restarted.ResolveKafkaRecord(ctx, missing); !errors.Is(err, ErrCommitUnconfirmed) {
		t.Fatal("receipt absence was declared a rollback/new source", err)
	}
	var doc table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}).Decode(&doc); err != nil || doc.Msg[0].Msg != nil || !doc.Msg[0].GlobalDeleted {
		t.Fatal("receipt replay revived deleted plaintext", err)
	}
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": first.Branches[0].ID}).Decode(&duty); err != nil || !reflect.DeepEqual(duty.Recipients, []string{"member", "owner"}) || duty.PolicySHA != policy {
		t.Fatal("replay rewrote frozen pre-removal recipients/policy", err)
	}
	for _, collection := range []string{Receipts, Effects} {
		filter := bson.M{"kind": "kafka-source-v1"}
		if collection == Effects {
			filter = bson.M{"producer_version": "ordinary-kafka-v1"}
		}
		if n, err := db.Collection(collection).CountDocuments(ctx, filter); err != nil || n != 1 {
			t.Fatal("replay duplicated source custody or downstream obligation", collection, n, err)
		}
	}
	var stored bson.Raw
	if err := db.Collection(Receipts).FindOne(ctx, bson.M{"kind": "kafka-source-v1"}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	fields, err := stored.Elements()
	if err != nil || len(fields) != 8 {
		t.Fatal("source receipt retained more than its minimal proof", err)
	}
	changed := record
	changed.Body = append(append([]byte(nil), record.Body...), 0)
	if _, err := restarted.AcceptKafkaRecord(ctx, changed, policy); !errors.Is(err, ErrConflict) {
		t.Fatal("changed bytes at same broker position became another source", err)
	}
}

type kafkaSequenceBarrierKey struct{}

func TestKafkaIntakeMongoOrdinaryAndStoredNotificationUseSameContendedCounter(t *testing.T) {
	arrived, release := make(chan struct{}, 2), make(chan struct{})
	var mu sync.Mutex
	seen := 0
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(kafkaSequenceBarrierKey{}) != true || e.CommandName != "update" {
			return
		}
		name, _ := e.Command.Lookup("update").StringValueOK()
		// Both real producers freeze the same group revision BEFORE allocating.
		// Rendezvous before that shared write; pausing its holder at sequence
		// update would prevent the second real transaction reaching its counter.
		if name != "group" {
			return
		}
		mu.Lock()
		seen++
		pause := seen <= 2
		mu.Unlock()
		if pause {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}}
	ctx, _, s, db, _ := kafkaFixture(t, monitor)
	work, cancel := context.WithCancel(context.WithValue(ctx, kafkaSequenceBarrierKey{}, true))
	defer cancel()
	ordinary := kafkaFixtureRecord(t, 2)
	snapshot, policy := kafkaPolicyFixture(t, s)
	// Both new Kafka sources carry this exact policy. Seal it before the
	// rendezvous, otherwise its first INSERT (not the allocator) serializes
	// the two transactions before either can reach the observed group write.
	if err := s.transaction(ctx, func(tx context.Context) error { return s.freezeKafkaPolicy(tx, snapshot, policy) }); err != nil {
		t.Fatal("actual frozen delivery policy", err)
	}
	notification := kafkaFixtureRecord(t, 8)
	var message sdkws.MsgData
	if proto.Unmarshal(notification.Body, &message) != nil {
		t.Fatal("notification fixture")
	}
	message.MsgFrom = constant.SysMsgType
	message.ContentType = constant.GroupInfoSetNotification
	message.Options = msgprocessor.NewOptions(msgprocessor.WithNotNotification(true), msgprocessor.WithHistory(true), msgprocessor.WithOfflinePush(false))
	var err error
	notification.Body, err = proto.MarshalOptions{Deterministic: true}.Marshal(&message)
	if err != nil {
		t.Fatal(err)
	}
	// The original GroupInfoSet single stored sg_ branch is retained. Its real
	// service actor must pass the ORIGINAL admin/authenticated enqueue gate.
	notificationContext := originManager(t, ctx)
	notification.Actor = "namespace-manager"
	permit, err := s.ReserveMessageNamespaces(notificationContext, &message)
	if err != nil || permit == nil || permit.Check(notificationContext, &message) != nil {
		t.Fatal("actual authorized system source reservation", err)
	}
	notificationContext = context.WithValue(work, constant.OpUserID, notification.Actor)
	type result struct {
		receipt Receipt
		err     error
	}
	results := make(chan result, 2)
	done := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		cancel()
		resume()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false)
			t.Error("owned Kafka allocator workers did not drain; retaining test DB")
		}
	})
	go func() {
		defer workers.Done()
		r, e := s.AcceptKafkaRecord(work, ordinary, policy, snapshot)
		results <- result{r, e}
	}()
	go func() {
		defer workers.Done()
		r, e := s.AcceptKafkaRecord(notificationContext, notification, policy, snapshot)
		results <- result{r, e}
	}()
	go func() { workers.Wait(); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(3 * time.Second):
			t.Fatal("both real source transactions did not reach the shared group revision write")
		}
	}
	resume()
	var sequences []int64
	for i := 0; i < 2; i++ {
		select {
		case r := <-results:
			if r.err != nil || len(r.receipt.Branches) != 1 {
				t.Fatal("contended source failed", r.err)
			}
			sequences = append(sequences, r.receipt.Branches[0].Seq)
		case <-ctx.Done():
			t.Fatal("allocator work did not finish")
		}
	}
	if sequences[0] == sequences[1] || sequences[0]+sequences[1] != 3 {
		t.Fatal("ordinary Kafka and stored notification allocated different sequence domains", sequences)
	}
	var current counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", "sg_g")}).Decode(&current); err != nil || current.Maximum != 2 {
		t.Fatal("shared maximum not committed", err)
	}
	var doc table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}).Decode(&doc); err != nil || doc.Msg[0].Msg == nil || doc.Msg[1].Msg == nil || doc.Msg[0].Msg.ServerMsgID == doc.Msg[1].Msg.ServerMsgID {
		t.Fatal("real stored slots collided", err)
	}
}

func TestKafkaIntakeMongoSilentNotificationKeepsSeqZeroAndNoHistoryCounter(t *testing.T) {
	ctx, s, db := ordinaryEphemeralMongoFixture(t)
	ctx = originManager(t, ctx)
	record := kafkaFixtureRecord(t, 3)
	record.Actor = "namespace-manager"
	var message sdkws.MsgData
	if proto.Unmarshal(record.Body, &message) != nil {
		t.Fatal("fixture")
	}
	message.MsgFrom = constant.SysMsgType
	message.ContentType = constant.GroupInfoSetNotification
	message.Options = msgprocessor.NewOptions(msgprocessor.WithNotNotification(false), msgprocessor.WithSendMsg(false), msgprocessor.WithHistory(false), msgprocessor.WithOfflinePush(false))
	var err error
	record.Body, err = proto.MarshalOptions{Deterministic: true}.Marshal(&message)
	if err != nil {
		t.Fatal(err)
	}
	permit, err := s.ReserveMessageNamespaces(ctx, &message)
	if err != nil || permit == nil || permit.Check(ctx, &message) != nil {
		t.Fatal("actual seeded pair reservation", err)
	}
	beforeHeads := ordinaryEphemeralHeads(t, ctx, s)
	snapshot, policy := kafkaPolicyFixture(t, s)
	r, err := s.AcceptKafkaRecord(ctx, record, policy, snapshot)
	if err != nil || len(r.Branches) != 1 || r.Branches[0].Persist || r.Branches[0].Seq != 0 || r.Branches[0].ConversationID != "n_g" {
		t.Fatal("silent notification was converted to visible positive-sequence history", err)
	}
	if n, err := db.Collection("msg").CountDocuments(ctx, bson.M{}); err != nil || n != 0 {
		t.Fatal("silent notification wrote history", n, err)
	}
	if afterHeads := ordinaryEphemeralHeads(t, ctx, s); !reflect.DeepEqual(beforeHeads, afterHeads) {
		t.Fatal("silent notification advanced a seeded pair history counter", beforeHeads, afterHeads)
	}
	if beforeHeads["sg_g"] != 0 || beforeHeads["n_g"] != 0 {
		t.Fatal("fresh authorized pair did not start at its source seed")
	}
	var raw bson.M
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": r.Branches[0].ID}).Decode(&raw); err != nil || raw["producer_version"] != "ordinary-kafka-v1" || raw["seq"] != int64(0) || raw["persist"] != false {
		t.Fatal("Seq0 transport duty not retained for still-required policy-aware worker", err)
	}
	// This is source custody/Seq0 compatibility ONLY. Automatic replay/push
	// is intentionally still rejected by the old worker; no delivery claim.
}

func TestKafkaIntakeMongoConcurrentMemberRemovalRejectsPreviouslyReadSender(t *testing.T) {
	paused, release := make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(kafkaSequenceBarrierKey{}) != true || e.CommandName != "update" {
			return
		}
		name, _ := e.Command.Lookup("update").StringValueOK()
		if name != "group" {
			return
		}
		pauseOnce.Do(func() {
			close(paused)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}}
	ctx, authority, s, db, members := kafkaFixture(t, monitor)
	record := kafkaFixtureRecord(t, 4)
	snapshot, policy := kafkaPolicyFixture(t, s)
	var message sdkws.MsgData
	if proto.Unmarshal(record.Body, &message) != nil {
		t.Fatal("fixture")
	}
	message.SendID = "member"
	record.Actor = "member"
	var err error
	record.Body, err = proto.MarshalOptions{Deterministic: true}.Marshal(&message)
	if err != nil {
		t.Fatal(err)
	}
	work, cancel := context.WithCancel(context.WithValue(context.WithValue(ctx, constant.OpUserID, "member"), kafkaSequenceBarrierKey{}, true))
	defer cancel()
	result, done := make(chan error, 1), make(chan struct{})
	t.Cleanup(func() {
		cancel()
		resume()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false)
			t.Error("owned stale-roster worker did not drain; retain DB")
		}
	})
	go func() {
		defer close(done)
		_, err := s.AcceptKafkaRecord(work, record, policy, snapshot)
		result <- err
	}()
	select {
	case <-paused:
	case <-time.After(3 * time.Second):
		t.Fatal("ordinary intake did not reach same-group revision CAS after reading sender")
	}
	if _, _, err := removeAtBoundary(ctx, authority, s, db, members, nil); err != nil {
		t.Fatal("actual authorized member removal failed", err)
	}
	resume()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("stale ordinary sender committed after member removal ACK")
		}
	case <-ctx.Done():
		t.Fatal("stale ordinary sender did not finish")
	}
	var current counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", "sg_g")}).Decode(&current); err != nil || current.Maximum != 0 {
		t.Fatal("stale-authority intake consumed a sequence", err)
	}
	if n, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"kind": "kafka-source-v1"}); err != nil || n != 0 {
		t.Fatal("stale sender received a durable source ACK", n, err)
	}
	if n, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"producer_version": "ordinary-kafka-v1"}); err != nil || n != 0 {
		t.Fatal("stale sender left a delivery obligation", n, err)
	}
}

func TestKafkaIntakeMongoCommittedReceiptMissingDependenciesNeverReallocates(t *testing.T) {
	for _, missing := range []string{"original-policy", "core-receipt"} {
		t.Run(missing, func(t *testing.T) {
			ctx, _, s, db, _ := kafkaFixture(t, nil)
			record := kafkaFixtureRecord(t, 5)
			snapshot, policy := kafkaPolicyFixture(t, s)
			first, err := s.AcceptKafkaRecord(ctx, record, policy, snapshot)
			if err != nil {
				t.Fatal(err)
			}
			target := key(s.epoch, "kafka-policy", policy)
			expected := ErrUnprepared
			if missing == "core-receipt" {
				target = first.ID
				expected = ErrConflict
			}
			removed, err := db.Collection(Receipts).DeleteOne(ctx, bson.M{"_id": target})
			if err != nil || removed.DeletedCount != 1 {
				t.Fatal("exact owned corruption fixture not applied", err)
			}
			if _, err := s.AcceptKafkaRecord(ctx, record, policy, snapshot); !errors.Is(err, expected) {
				t.Fatal("committed source with missing dependency fell through to a fresh intake", err)
			}
			var current counter
			if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", "sg_g")}).Decode(&current); err != nil || current.Maximum != 1 {
				t.Fatal("corrupt committed source allocated again", err)
			}
			if n, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"producer_version": "ordinary-kafka-v1"}); err != nil || n != 1 {
				t.Fatal("corrupt replay created another duty", n, err)
			}
		})
	}
}

func TestKafkaIntakeMongoClearStillRedactsPolicyAwareDutyWhileOldWorkerIsFenced(t *testing.T) {
	ctx, _, s, db, _ := kafkaFixture(t, nil)
	record := kafkaFixtureRecord(t, 6)
	snapshot, policy := kafkaPolicyFixture(t, s)
	first, err := s.AcceptKafkaRecord(ctx, record, policy, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.readDuty(ctx, first.Branches[0].ID); !errors.Is(err, ErrUnprepared) {
		t.Fatal("old worker admission was relaxed", err)
	}
	if err := s.MutateMessageVisibility(ctx, db, []table.PendingVisibilityChange{{ConversationID: "sg_g", Below: 2}}, func(tx context.Context) error {
		changed, err := db.Collection("msg").UpdateOne(tx, bson.M{"doc_id": "sg_g:0"}, bson.M{"$set": bson.M{"msgs.0.msg": nil, "msgs.0.global_deleted": true}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 {
			return ErrConflict
		}
		return nil
	}); err != nil {
		t.Fatal("producer policy version prevented authoritative clear/redaction", err)
	}
	var duty bson.M
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": first.Branches[0].ID}).Decode(&duty); err != nil || duty["payload"] != nil || duty["recipients"] != nil || duty["push_pending"] != false || duty["visibility_revoked"] != true {
		t.Fatal("committed clear retained a delayed plaintext obligation", err)
	}
	replay, err := s.AcceptKafkaRecord(ctx, record, policy)
	if err != nil || !reflect.DeepEqual(replay, first) {
		t.Fatal("redacted source no longer resolved its original minimal receipt", err)
	}
	var doc table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}).Decode(&doc); err != nil || doc.Msg[0].Msg != nil {
		t.Fatal("replay rebuilt clear history", err)
	}
}

func TestKafkaIntakeMongoFreezesVisibleAndSilentKickRecipientsInTheirOriginalBranches(t *testing.T) {
	// A kicked owner is no longer a generic Kafka roster member. Use the actual
	// typed admission -> removal -> ordered Queue source and its original cut,
	// not an unproved KickedUserList in a Sys DTO. Existing branch/roster oracle
	// below is unchanged, including visible history versus silent Seq0.
	ctx, s, db, _, frozen, _, _ := removalVisibilityFixtureConfigured(t, groupauthority.NoticeKick, false, nil, "removed", func(message *sdkws.MsgData, policy *groupeffects.ActionDeliveryPolicy) {
		message.AtUserIDList = []string{"owner"}
		message.Options = msgprocessor.NewOptions(msgprocessor.WithNotNotification(false), msgprocessor.WithSendMsg(true), msgprocessor.WithHistory(false), msgprocessor.WithOfflinePush(true), msgprocessor.WithUnreadCount(true))
		message.Content = kafkaTips(t, &sdkws.MemberKickedTips{Group: &sdkws.GroupInfo{GroupID: "g"}, KickedUserList: []*sdkws.GroupMemberFullInfo{{GroupID: "g", UserID: "removed"}}})
		policy.OfflineEnabled = true
	}, true)
	var r Receipt
	err := s.transaction(ctx, func(tx context.Context) error {
		input, err := s.ownedActionInput(tx, frozen.ID)
		if err != nil {
			return err
		}
		r, err = s.acceptOwnedAction(tx, input)
		return err
	})
	if err != nil || len(r.Branches) != 2 || r.Branches[0].ConversationID != "sg_g" || r.Branches[0].Seq != 1 || !r.Branches[0].Persist || r.Branches[1].ConversationID != "n_g" || r.Branches[1].Seq != 0 || r.Branches[1].Persist {
		t.Fatal("visible/silent branch storage contract changed", err)
	}
	for i, want := range [][]string{{"owner"}, {"member", "owner", "removed"}} {
		var duty deliveryDuty
		if err := db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": r.Branches[i].ID}).Decode(&duty); err != nil || !reflect.DeepEqual(duty.Recipients, want) {
			t.Fatal("first TX froze the wrong branch roster", i, err)
		}
	}
}
