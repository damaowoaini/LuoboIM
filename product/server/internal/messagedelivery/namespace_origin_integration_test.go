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
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

// Reuses the owned nonce DB/checked primary/claim/drop/disconnect fixture.
// The synthetic inventory covers only its init/left/right rows. It is NOT a
// production migration producer or proof that a real deployment is drained.
func originMongoFixture(t *testing.T, monitor *event.CommandMonitor, dialer options.ContextDialer) (context.Context, *groupauthority.Store, *Store, *mongo.Database, relation.GroupModelInterface, relation.GroupMemberModelInterface) {
	t.Helper()
	ctx, authority, store, db := ownedReceiverMongo(t, monitor, dialer)
	groups, err := mgo.NewGroupMongoContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	members, err := mgo.NewGroupMemberContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	cut := NamespaceCutover{ID: NamespaceCutoverID, Kind: "immutable-namespace-cutover-v1", Database: db.Name(), Epoch: store.epoch, ActivationSHA256: store.activationSHA, MigrationID: uuid.NewString(), State: "all-inventoried-namespaces-permanently-claimed", ManifestSHA256: hash([]byte("synthetic init,left,right inventory")), MongoInventorySHA256: hash([]byte("synthetic raw Mongo inventory")), RedisInventorySHA256: hash([]byte("synthetic absent Redis fixture")), DrainSHA256: hash([]byte("synthetic isolated no old writer")), OldBinaryFenceSHA256: hash([]byte("synthetic fixture only")), NamespaceCount: 9, ClaimedCount: 9, RegisteredAt: time.Now().UTC().Truncate(time.Millisecond)}
	for _, gid := range []string{"init", "left", "right"} {
		normal, notice, err := groupNamespacePair(gid)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range claimRows(groupauthority.NewGroupCreationInfo{ID: uuid.NewString(), Epoch: store.epoch, ActivationSHA256: store.activationSHA, GroupID: gid, InputSHA256: hash([]byte("synthetic old " + gid)), CreatedAt: cut.RegisteredAt}, normal, notice, hash([]byte("synthetic completed inventory"))) {
			if _, err := db.Collection(Sequences).InsertOne(ctx, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Collection(Sequences).InsertOne(ctx, cut); err != nil {
		t.Fatal(err)
	}
	return ctx, authority, store, db, groups, members
}

func originManager(t *testing.T, ctx context.Context) context.Context {
	t.Helper()
	oldManagers, oldAdmins := config.Config.Manager.UserID, config.Config.IMAdmin.UserID
	config.Config.Manager.UserID, config.Config.IMAdmin.UserID = []string{"namespace-manager"}, nil
	t.Cleanup(func() { config.Config.Manager.UserID, config.Config.IMAdmin.UserID = oldManagers, oldAdmins })
	return context.WithValue(ctx, constant.OpUserID, "namespace-manager")
}

func originEnvelope(t *testing.T, message *sdkws.MsgData, recipients ...string) Envelope {
	t.Helper()
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return Envelope{ID: uuid.NewString(), Branches: []Branch{{ID: uuid.NewString(), ConversationID: msgprocessor.GetConversationIDByMsg(message), Payload: body, Recipients: recipients, Persist: msgprocessor.Options(message.Options).IsHistory()}}}
}

func TestMessageNamespaceMongoFirstClaimControlsGroupAndDirectNotification(t *testing.T) {
	for _, first := range []string{"group", "direct"} {
		t.Run(first, func(t *testing.T) {
			ctx, authority, store, db, groups, members := originMongoFixture(t, nil, nil)
			manager := originManager(t, ctx)
			direct := namespaceMessage("collision", "peer", constant.NotificationChatType, false, false)
			if first == "group" {
				if _, _, err := applyNewGroupSeed(ctx, authority, store, groups, members, "collision_peer", nil); err != nil {
					t.Fatal(err)
				}
				if permit, err := store.ReserveMessageNamespaces(manager, direct); !errors.Is(err, ErrConflict) || permit != nil {
					t.Fatal("direct n_ alias acquired an enqueue grant over a group", err)
				}
				if n, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"kind": "namespace-enqueue-v1"}); err != nil || n != 0 {
					t.Fatal("rejected direct alias left an enqueue receipt", err)
				}
			} else {
				permit, err := store.ReserveMessageNamespaces(manager, direct)
				if err != nil || permit == nil || permit.Check(manager, direct) != nil {
					t.Fatal("fresh direct notification did not commit namespace before enqueue", err)
				}
				if _, _, err := applyNewGroupSeed(ctx, authority, store, groups, members, "collision_peer", nil); !errors.Is(err, ErrConflict) {
					t.Fatal("new group reused an owned direct n_ namespace", err)
				}
				for _, collection := range []string{"group", "group_member", "group_membership_history"} {
					if n, err := db.Collection(collection).CountDocuments(ctx, bson.M{"group_id": "collision_peer"}); err != nil || n != 0 {
						t.Fatal("collision committed a partial group or owner window", err)
					}
				}
				if n, err := db.Collection(Sequences).CountDocuments(ctx, bson.M{"_id": key(store.epoch, "sequence", "sg_collision_peer")}); err != nil || n != 0 {
					t.Fatal("collision leaked the losing normal counter", err)
				}
			}
		})
	}
}

type namespaceRaceGateKey struct{}

func TestMessageNamespaceMongoConcurrentCreateAndDirectReserveHaveOnePermanentWinner(t *testing.T) {
	arrived, release := make(chan struct{}, 2), make(chan struct{})
	var mu sync.Mutex
	seen := map[string]bool{}
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, event *event.CommandStartedEvent) {
		label, _ := ctx.Value(namespaceRaceGateKey{}).(string)
		if label == "" || event.CommandName != "find" {
			return
		}
		filter, ok := event.Command.Lookup("filter").DocumentOK()
		if !ok {
			return
		}
		id, _ := filter.Lookup("_id").StringValueOK()
		if id != namespaceClaimID("n_race_peer") {
			return
		}
		mu.Lock()
		first := !seen[label]
		seen[label] = true
		mu.Unlock()
		if first {
			arrived <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}}
	ctx, authority, store, db, groups, members := originMongoFixture(t, monitor, nil)
	originManager(t, ctx)
	current, cancel := context.WithCancel(ctx)
	var open sync.Once
	resume := func() { open.Do(func() { close(release) }) }
	type outcome struct {
		group  bool
		permit *NamespaceEnqueuePermit
		err    error
	}
	done := make(chan outcome, 2)
	go func() {
		_, _, err := applyNewGroupSeed(context.WithValue(current, namespaceRaceGateKey{}, "group"), authority, store, groups, members, "race_peer", nil)
		done <- outcome{group: true, err: err}
	}()
	go func() {
		marked := context.WithValue(context.WithValue(current, namespaceRaceGateKey{}, "direct"), constant.OpUserID, "namespace-manager")
		permit, err := store.ReserveMessageNamespaces(marked, namespaceMessage("race", "peer", constant.NotificationChatType, false, false))
		done <- outcome{permit: permit, err: err}
	}()
	drained := 0
	t.Cleanup(func() {
		cancel()
		resume()
		for drained < 2 {
			select {
			case <-done:
				drained++
			case <-time.After(3 * time.Second):
				ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false)
				t.Error("origin race did not drain; owned DB retained")
				return
			}
		}
	})
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(3 * time.Second):
			t.Fatal("both actual transactions did not reach the same permanent-key barrier")
		}
	}
	resume()
	winner := ""
	for i := 0; i < 2; i++ {
		select {
		case result := <-done:
			drained++
			if result.err == nil {
				if winner != "" {
					t.Fatal("both incompatible origins committed")
				}
				if result.group {
					winner = "group"
				} else {
					if result.permit == nil {
						t.Fatal("known direct commit did not grant enqueue")
					}
					winner = "direct"
				}
			} else if result.permit != nil {
				t.Fatal("losing/unknown transaction granted enqueue", result.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("origin conflict did not settle")
		}
	}
	if winner == "" {
		t.Fatal("neither compatible positive control committed")
	}
	origin, _, err := store.namespaceOwner(ctx, "n_race_peer")
	if err != nil {
		t.Fatal(err)
	}
	groupRows, err := db.Collection("group").CountDocuments(ctx, bson.M{"group_id": "race_peer"})
	if err != nil {
		t.Fatal(err)
	}
	enqueueRows, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"kind": "namespace-enqueue-v1"})
	if err != nil {
		t.Fatal(err)
	}
	seedRows, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"kind": "new-group-seed-pair-v1"})
	if err != nil {
		t.Fatal(err)
	}
	if winner == "group" {
		if origin.Kind != "group" || groupRows != 1 || enqueueRows != 0 || seedRows != 1 {
			t.Fatal("group winner left incompatible side effects")
		}
	} else if origin.Kind != "server-notification" || groupRows != 0 || enqueueRows != 1 || seedRows != 0 {
		t.Fatal("direct winner left incompatible side effects")
	}
}

func TestMessageNamespaceMongoExistingClaimCannotResetHeadOrAdoptTupleAlias(t *testing.T) {
	ctx, _, store, db, _, _ := originMongoFixture(t, nil, nil)
	actor := context.WithValue(ctx, constant.OpUserID, "a_b")
	message := namespaceMessage("a_b", "c", constant.SingleChatType, true, true)
	if permit, err := store.ReserveMessageNamespaces(actor, message); err != nil || permit == nil {
		t.Fatal(err)
	}
	input := originEnvelope(t, message, "a_b", "c")
	first, err := store.AcceptCommitted(actor, input)
	if err != nil || first.Branches[0].Seq != 1 {
		t.Fatal("authorized direct input did not use the reserved counter", err)
	}
	if permit, err := store.ReserveMessageNamespaces(actor, message); err != nil || permit == nil {
		t.Fatal("same reservation did not inspect known commit", err)
	}
	var counter counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(store.epoch, "sequence", "si_a_b_c")}).Decode(&counter); err != nil || counter.Maximum != 1 || counter.SeedMaximum != 0 {
		t.Fatal("reservation reset an advanced head", err)
	}
	changed := namespaceMessage("a", "b_c", constant.SingleChatType, true, true)
	changed.ServerMsgID = "synthetic-other-namespace-message"
	if permit, err := store.ReserveMessageNamespaces(context.WithValue(ctx, constant.OpUserID, "a"), changed); !errors.Is(err, ErrConflict) || permit != nil {
		t.Fatal("same-CID distinct exact UID tuple obtained a grant", err)
	}
	forged := originEnvelope(t, message, "foreign")
	if _, err := store.AcceptCommitted(actor, forged); !errors.Is(err, ErrConflict) {
		t.Fatal("namespace ownership became arbitrary recipient authority", err)
	}
	if n, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}}); err != nil || n != 1 {
		t.Fatal("rejected origin/recipient created another duty", err)
	}
}

func TestMessageNamespaceMongoSilentSeqZeroNeedsOriginAndPrivateInputProof(t *testing.T) {
	ctx, _, store, db, _, _ := originMongoFixture(t, nil, nil)
	actor := context.WithValue(ctx, constant.OpUserID, "a")
	message := namespaceMessage("a", "b", constant.SingleChatType, false, false)
	input := originEnvelope(t, message, "a", "b")
	if _, err := store.AcceptCommitted(actor, input); !errors.Is(err, ErrUnprepared) {
		t.Fatal("silent branch bypassed missing permanent origin", err)
	}
	if n, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}}); err != nil || n != 0 {
		t.Fatal("denied Seq0 created a duty", err)
	}
	if permit, err := store.ReserveMessageNamespaces(actor, message); err != nil || permit == nil {
		t.Fatal(err)
	}
	if err := store.transaction(ctx, func(tx context.Context) error { _, err := store.Accept(tx, input); return err }); !errors.Is(err, ErrUnprepared) {
		t.Fatal("strong TX alone forged a trusted input adapter", err)
	}
	accepted, err := store.AcceptCommitted(actor, input)
	if err != nil || accepted.Branches[0].Persist || accepted.Branches[0].Seq != 0 {
		t.Fatal("legitimate silent branch gained positive Seq/history", err)
	}
	var duty deliveryDuty
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": input.Branches[0].ID}).Decode(&duty); err != nil || duty.NamespaceOriginKind != "single" || !validSHA(duty.NamespaceOriginSHA256) || duty.Seq != 0 {
		t.Fatal("silent duty did not retain minimal typed origin", err)
	}
	if err := store.transaction(ctx, func(tx context.Context) error {
		return store.InitializeSequence(tx, "si_unproven", Seed{MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("caller supplied"))})
	}); !errors.Is(err, ErrUnprepared) {
		t.Fatal("caller maxima minted a permanent namespace seed", err)
	}
}

type originProjection struct{ calls int }

func (p *originProjection) RaiseMaximum(context.Context, string, int64) error { p.calls++; return nil }
func (p *originProjection) RaiseSenderRead(context.Context, string, string, int64) error {
	p.calls++
	return nil
}

func TestMessageNamespaceMongoProjectionRejectsMissingOrWrongRetainedOrigin(t *testing.T) {
	ctx, _, store, db, _, _ := originMongoFixture(t, nil, nil)
	actor := context.WithValue(ctx, constant.OpUserID, "a")
	message := namespaceMessage("a", "b", constant.SingleChatType, true, true)
	if permit, err := store.ReserveMessageNamespaces(actor, message); err != nil || permit == nil {
		t.Fatal(err)
	}
	input := originEnvelope(t, message, "a", "b")
	if _, err := store.AcceptCommitted(actor, input); err != nil {
		t.Fatal(err)
	}
	projection := &originProjection{}
	if err := store.Project(ctx, input.Branches[0].ID, projection); err != nil || projection.calls != 2 {
		t.Fatal("valid retained origin blocked normal projection", err)
	}
	for _, mutation := range []bson.M{{"$unset": bson.M{"namespace_origin_kind": "", "namespace_origin_sha256": ""}}, {"$set": bson.M{"namespace_origin_kind": "group", "namespace_origin_sha256": hash([]byte("foreign origin"))}}} {
		result, err := db.Collection(Effects).UpdateOne(ctx, bson.M{"branch_id": input.Branches[0].ID}, mutation)
		if err != nil || result.MatchedCount != 1 {
			t.Fatal("synthetic retained-origin corruption missed its exact duty", err)
		}
		if err := store.Project(ctx, input.Branches[0].ID, projection); err == nil || projection.calls != 2 {
			t.Fatal("legacy/foreign origin reached cache projection", err)
		}
		// Inspection/redaction must remain possible without fresh origin capture.
		if _, err := store.readDutyRecord(ctx, input.Branches[0].ID); err != nil {
			t.Fatal("missing origin prevented privacy inspection", err)
		}
	}
}

func TestMessageNamespaceMongoUnknownReservationCommitNeverGrantsEnqueue(t *testing.T) {
	drop := &commitReplyDropper{requests: map[int32]bool{}, all: true}
	ctx, _, store, db, _, _ := originMongoFixture(t, drop.monitor(), drop)
	actor := context.WithValue(context.WithValue(ctx, constant.OpUserID, "a"), deliveryDropCommitKey{}, true)
	message := namespaceMessage("a", "b", constant.SingleChatType, true, true)
	permit, err := store.ReserveMessageNamespaces(actor, message)
	if !errors.Is(err, ErrCommitUnconfirmed) || permit != nil || drop.dropped.Load() < 1 {
		t.Fatal("unknown commit returned an enqueue capability or fault missed real commit", err)
	}
	drop.mu.Lock()
	facts := append([]deliveryWireFact(nil), drop.facts...)
	drop.mu.Unlock()
	if len(facts) < 1 {
		t.Fatal("missing actual commit wire")
	}
	for _, fact := range facts {
		if fact.command != "commitTransaction" || !fact.transaction || fact.txn != facts[0].txn || fact.lsidSHA != facts[0].lsidSHA {
			t.Fatal("unknown result replayed business transaction")
		}
	}
	for _, item := range []struct {
		collection, kind string
		count            int64
	}{{Sequences, "permanent-message-origin-v1", 1}, {Receipts, "namespace-enqueue-v1", 1}} {
		if n, err := db.Collection(item.collection).CountDocuments(ctx, bson.M{"kind": item.kind}); err != nil || n != item.count {
			t.Fatal("unknown commit duplicated or lost its minimal namespace receipt", err)
		}
	}
	if n, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}}); err != nil || n != 0 {
		t.Fatal("namespace reservation pretended to be a message delivery", err)
	}
	var counter counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(store.epoch, "sequence", "si_a_b")}).Decode(&counter); err != nil || counter.Maximum != 0 {
		t.Fatal("namespace reservation allocated a message", err)
	}
	restarted, err := NewStore(db, store.epoch, store.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := restarted.ReserveMessageNamespaces(context.WithValue(ctx, constant.OpUserID, "a"), message)
	if err != nil || resolved == nil {
		t.Fatal("explicit exact reservation inspection could not resolve known commit", err)
	}
	if !reflect.DeepEqual(message.Content, []byte("synthetic namespace payload")) {
		t.Fatal("reservation mutated caller message")
	}
}
