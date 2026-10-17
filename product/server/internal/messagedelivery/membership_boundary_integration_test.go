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
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Dependency-slice coverage: actual scoped Mongo member model and shared
// allocator, NOT the still-unwired Kick/Quit RPCs or legacy reader migration.
// Existing sequenceMongo enforces exact ephemeral RS, nonce DB claim/drop and
// retains the DB if an owned concurrent test operation cannot be drained.
func membershipFixture(t *testing.T, monitor *event.CommandMonitor) (context.Context, *groupauthority.Store, *Store, *mongo.Database, relation.GroupMemberModelInterface) {
	t.Helper()
	ctx, authority, store, db := sequenceMongo(t, monitor)
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", "status": int32(constant.GroupOk), "group_type": int32(constant.WorkingGroup), groupauthority.RevisionField: int64(1)}); err != nil {
		t.Fatal(err)
	}
	for uid, role := range map[string]int32{"owner": constant.GroupOwner, "member": constant.GroupOrdinaryUsers} {
		if _, err := db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: "g", UserID: uid, RoleLevel: role, JoinTime: time.Unix(1, 0).UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	members, err := mgo.NewGroupMemberContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic operator preparation in this exclusively claimed fresh DB only.
	// No production preparation API or implicit missing-owner adoption exists.
	for _, name := range []string{Sequences, Receipts} {
		if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": MembershipSchemaID, "version": int32(1), "epoch": store.epoch, "activation_sha256": store.activationSHA, "legacy_message_writers_retired": true, "legacy_group_writers_retired": true, "owner_bounds_seeded": true, "readers_bound": true}); err != nil {
			t.Fatal(err)
		}
	}
	prepareMembershipNamespaceFixture(t, ctx, authority, store, db, "g", []string{"member", "owner"}, 0, 0)
	jointEffects(t, ctx, db)
	if err := store.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, authority, store, db, members
}

func removeAtBoundary(ctx context.Context, authority *groupauthority.Store, store *Store, db *mongo.Database, members relation.GroupMemberModelInterface, after func(context.Context) error) (MemberRemovalReceipt, *groupauthority.MemberRemovalScope, error) {
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	ctx, _, err := groupauthority.NewNoticeCommand(ctx, store.epoch, groupauthority.NoticeKick, "g")
	if err != nil {
		return MemberRemovalReceipt{}, nil, err
	}
	var snapshot *groupauthority.NoticeSnapshot
	var cap *groupauthority.MemberRemovalScope
	var receipt MemberRemovalReceipt
	_, err = authority.Mutation(ctx, []groupauthority.Target{{GroupID: "g", Members: []string{"member"}}}, func(tx context.Context) error {
		var err error
		snapshot, err = groupauthority.BeginNotice(tx, db, "g")
		return err
	}, func(tx context.Context) error {
		if err := members.Delete(tx, "g", []string{"member"}); err != nil {
			return err
		}
		if err := snapshot.Finish(tx, db); err != nil {
			return err
		}
		var err error
		cap, err = groupauthority.CompletedMemberRemoval(tx, db, "g")
		if err != nil {
			return err
		}
		receipt, err = store.RecordMemberRemoval(tx, cap)
		if err != nil {
			return err
		}
		if err := groupauthority.RequireMembers(tx, db, "g", []string{"owner"}); !errors.Is(err, groupauthority.ErrInvalidScope) {
			return errors.New("removal receipt widened original member writer scope")
		}
		if after != nil {
			return after(tx)
		}
		return nil
	})
	return receipt, cap, err
}

func TestMembershipRemovalAtomicallyClosesEmptyWindowAndResolvesAfterRestart(t *testing.T) {
	ctx, authority, store, db, members := membershipFixture(t, nil)
	receipt, cap, err := removeAtBoundary(ctx, authority, store, db, members, nil)
	if err != nil {
		t.Fatal(err)
	}
	window, err := store.ReadMembershipWindow(ctx, "sg_g", "member")
	if err != nil || !window.Closed || window.Upper != 0 || window.AllowsSequence(1, 0) || window.LastCommand != receipt.CommandID {
		t.Fatal("empty exit boundary became legacy unbounded zero", err)
	}
	if n, err := db.Collection("group_member").CountDocuments(ctx, bson.M{"group_id": "g", "user_id": "member"}); err != nil || n != 0 {
		t.Fatal("receipt committed without actual member model deletion", err)
	}
	if n, err := db.Collection("group_membership_history").CountDocuments(ctx, bson.M{"group_id": "g", "user_id": "member", "last_left_at": bson.M{"$type": "date"}}); err != nil || n != 1 {
		t.Fatal("member history was not committed with the owner bound", err)
	}
	owner, err := store.ReadMembershipWindow(ctx, "sg_g", "owner")
	if err != nil || owner.Closed || owner.Revision != 1 {
		t.Fatal("readonly notification roster widened owner history write", err)
	}
	if _, err := cap.Inspect(context.WithValue(ctx, constant.OpUserID, "owner"), db); !errors.Is(err, groupauthority.ErrInvalidScope) {
		t.Fatal("write capability escaped committed attempt", err)
	}
	restarted, err := NewStore(db, store.epoch, store.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := restarted.ResolveMemberRemoval(ctx, receipt.CommandID)
	if err != nil || !reflect.DeepEqual(resolved, receipt) {
		t.Fatal("same command inspection did not retain minimal boundary receipt", err)
	}
	if _, err := restarted.ResolveMemberRemoval(ctx, uuid.NewString()); !errors.Is(err, ErrCommitUnconfirmed) {
		t.Fatal("receipt absence was misreported as proven rollback", err)
	}
}

func TestMembershipRemovalAbortRollsBackMemberCounterWindowAndReceipt(t *testing.T) {
	ctx, authority, store, db, members := membershipFixture(t, nil)
	sentinel := errors.New("synthetic failure after member boundary receipt")
	_, _, err := removeAtBoundary(ctx, authority, store, db, members, func(context.Context) error { return sentinel })
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	window, err := store.ReadMembershipWindow(ctx, "sg_g", "member")
	if err != nil || window.Closed || window.Revision != 1 {
		t.Fatal("aborted transaction left an owner bound", err)
	}
	if n, err := db.Collection("group_member").CountDocuments(ctx, bson.M{"group_id": "g", "user_id": "member"}); err != nil || n != 1 {
		t.Fatal("member delete escaped aborted transaction", err)
	}
	if n, err := db.Collection("group_membership_history").CountDocuments(ctx, bson.M{"group_id": "g", "user_id": "member"}); err != nil || n != 0 {
		t.Fatal("aborted membership history escaped transaction", err)
	}
	var current counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(store.epoch, "sequence", "sg_g")}).Decode(&current); err != nil || current.Maximum != 0 || current.Revision != 1 {
		t.Fatal("aborted receipt advanced shared allocator", err)
	}
	if n, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"kind": "membership-removal-pair-v2"}); err != nil || n != 0 {
		t.Fatal("aborted receipt survived", err)
	}
}

func TestMembershipRemovalNeverGuessesMissingSchemaOwnerOrMalformedWindow(t *testing.T) {
	for _, missing := range []string{"schema", "owner", "closed-field"} {
		t.Run(missing, func(t *testing.T) {
			ctx, authority, store, db, members := membershipFixture(t, nil)
			var err error
			switch missing {
			case "schema":
				_, err = db.Collection(Sequences).DeleteOne(ctx, bson.M{"_id": MembershipSchemaID})
			case "owner":
				_, err = db.Collection(Sequences).DeleteOne(ctx, bson.M{"_id": key(store.epoch, "membership-window", "sg_g\x00member")})
			case "closed-field":
				_, err = db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": key(store.epoch, "membership-window", "sg_g\x00member")}, bson.M{"$unset": bson.M{"closed": ""}})
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := removeAtBoundary(ctx, authority, store, db, members, nil); err == nil {
				t.Fatal("unknown legacy scope was adopted as a new open bound")
			}
			if n, err := db.Collection("group_member").CountDocuments(ctx, bson.M{"group_id": "g", "user_id": "member"}); err != nil || n != 1 {
				t.Fatal("unprepared boundary committed member delete", err)
			}
			if n, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"kind": "membership-removal-pair-v2"}); err != nil || n != 0 {
				t.Fatal("unprepared boundary emitted receipt", err)
			}
		})
	}
}

type memberBoundaryBarrierKey struct{}
type memberBoundaryRemovalAttemptKey struct{}

func membershipOrdinaryEnvelope(t *testing.T) Envelope {
	t.Helper()
	message := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", ServerMsgID: uuid.NewString(), ClientMsgID: uuid.NewString(), ContentType: constant.Text, MsgFrom: constant.UserMsgType, SessionType: constant.SuperGroupChatType, CreateTime: 2000, SendTime: 2000, Content: []byte("synthetic membership boundary body"), Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(true), msgprocessor.WithHistory(true))}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	return Envelope{ID: uuid.NewString(), Branches: []Branch{{ID: uuid.NewString(), ConversationID: "sg_g", Payload: body, Recipients: []string{"member", "owner"}, Persist: true}}}
}

func TestMembershipRemovalSerializesWithTheActualOrdinarySharedAllocator(t *testing.T) {
	paused, release, removing, removalRelease := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce, removalOnce, removalReleaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	resumeRemoval := func() { removalReleaseOnce.Do(func() { close(removalRelease) }) }
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, event *event.CommandStartedEvent) {
		if event.CommandName != "update" {
			return
		}
		name, _ := event.Command.Lookup("update").StringValueOK()
		if ctx.Value(memberBoundaryRemovalAttemptKey{}) == true && name == "group" {
			// Preserve the real first transaction snapshot and its exact update,
			// but do not send the contending write until ordinary's known commit.
			// Otherwise all production retries can be exhausted in that interval.
			removalOnce.Do(func() {
				close(removing)
				select {
				case <-removalRelease:
				case <-ctx.Done():
				}
			})
		}
		if ctx.Value(memberBoundaryBarrierKey{}) != true || name != Sequences {
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
	ctx, authority, store, db, members := membershipFixture(t, monitor)
	managed, err := NewMembershipBoundStore(db, store.epoch, store.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	owner, cancel := context.WithCancel(context.WithValue(ctx, constant.OpUserID, "owner"))
	defer cancel()
	policy, policySHA := kafkaPolicyFixture(t, managed)
	recordFor := func(offset int64, input Envelope) KafkaRecord {
		record := kafkaFixtureRecord(t, offset)
		record.Body = append([]byte(nil), input.Branches[0].Payload...)
		var message sdkws.MsgData
		if proto.Unmarshal(record.Body, &message) != nil {
			t.Fatal("actual ordinary source fixture")
		}
		permit, err := managed.ReserveMessageNamespaces(owner, &message)
		if err != nil || permit == nil || permit.Check(owner, &message) != nil {
			t.Fatal("actual ordinary pre-enqueue reservation", err)
		}
		return record
	}
	first := membershipOrdinaryEnvelope(t)
	firstRecord := recordFor(20, first)
	type outcome struct {
		receipt MemberRemovalReceipt
		err     error
	}
	result := make(chan outcome, 1)
	firstResult := make(chan error, 1)
	done := make(chan struct{})
	var workers sync.WaitGroup
	workers.Add(2)
	t.Cleanup(func() {
		cancel()
		resume()
		resumeRemoval()
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		select {
		case <-done:
		case <-end.Done():
			ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false)
			t.Error("owned boundary worker drain unconfirmed; retain DB")
		}
	})
	// Real Kafka first freezes the roster (group revision) and then pauses at
	// its actual shared counter CAS. Starting removal first and holding that
	// same group write would prevent this real producer from ever getting here.
	go func() {
		defer workers.Done()
		r, err := managed.AcceptKafkaRecord(context.WithValue(owner, memberBoundaryBarrierKey{}, true), firstRecord, policySHA, policy)
		if err == nil && (len(r.Branches) != 1 || r.Branches[0].Seq != 1) {
			err = errors.New("first real ordinary source did not allocate sequence one")
		}
		firstResult <- err
	}()
	go func() {
		defer workers.Done()
		select {
		case <-paused:
		case <-owner.Done():
			result <- outcome{err: owner.Err()}
			return
		}
		r, _, err := removeAtBoundary(context.WithValue(owner, memberBoundaryRemovalAttemptKey{}, true), authority, store, db, members, nil)
		result <- outcome{r, err}
	}()
	go func() { workers.Wait(); close(done) }()
	select {
	case <-removing:
	case <-ctx.Done():
		t.Fatal("actual removal did not contend with the real ordinary group revision")
	}
	resume()
	select {
	case err := <-firstResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual ordinary transaction did not settle")
	}
	// firstResult is sent only after AcceptKafkaRecord returns a known durable
	// receipt. Now release the stale real removal attempt; its actual retry
	// must observe the committed shared head and preserve the oracle below.
	resumeRemoval()
	select {
	case r := <-result:
		if r.err != nil || r.receipt.Upper != 1 {
			t.Fatal("removal did not retry against the committed ordinary seq", r.err)
		}
	case <-ctx.Done():
		t.Fatal("member removal did not settle")
	}
	second := membershipOrdinaryEnvelope(t)
	second.Branches[0].Recipients = []string{"owner"} // current sender-authorized roster; removed owner is tested by its frozen window below.
	secondReceipt, err := managed.AcceptKafkaRecord(owner, recordFor(21, second), policySHA, policy)
	if err != nil || len(secondReceipt.Branches) != 1 {
		t.Fatal("actual later ordinary source", err)
	}
	var duty deliveryDuty
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": secondReceipt.Branches[0].ID}).Decode(&duty); err != nil || !reflect.DeepEqual(duty.Recipients, second.Branches[0].Recipients) {
		t.Fatal("actual post-removal sender-authorized roster", err)
	}
	window, err := store.ReadMembershipWindow(ctx, "sg_g", "member")
	if err != nil || !window.AllowsSequence(1, 0) || window.AllowsSequence(2, 0) {
		t.Fatal("post-removal allocation escaped the frozen owner upper bound", err)
	}
	var current counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(store.epoch, "sequence", "sg_g")}).Decode(&current); err != nil || current.Maximum != 2 {
		t.Fatal("a second independent sequence namespace was used", err)
	}
}
