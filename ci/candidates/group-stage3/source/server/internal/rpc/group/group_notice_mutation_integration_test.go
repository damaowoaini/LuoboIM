package group

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	pbuser "github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// These fixtures reuse the exact loopback/fresh nonce DB/exclusive claim/drop
// and cleanup error checks in frozen groupRPCMongo. No live group or Redis is
// used. Fake cache errors below prove durable obligation retention, not actual
// Redis recovery, gateway delivery or an operational worker lifecycle.
func groupStatusFixture(t *testing.T, monitor *event.CommandMonitor, configure func()) (context.Context, *groupServer, *groupauthority.Store, *mongo.Database, *groupeffects.Store) {
	t.Helper()
	memberEditSetup(t)
	config.Config.Callback.CallbackBeforeSendGroupMsg.Enable = false
	config.Config.Callback.CallbackAfterSendGroupMsg.Enable = false
	config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable = false
	config.Config.Callback.CallbackOnlinePush.Enable = false
	config.Config.Callback.CallbackOfflinePush.Enable = false
	policy := config.NotificationConf{IsSendMsg: true, ReliabilityLevel: constant.ReliableNotificationNoMsg}
	config.Config.Notification.GroupMuted, config.Config.Notification.GroupCancelMuted = policy, policy
	if configure != nil { configure() }
	ctx, authority, db := groupRPCMongo(t, monitor)
	if err := db.CreateCollection(ctx, groupeffects.Collection); err != nil { t.Fatal(err) }
	if _, err := db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); err != nil { t.Fatal(err) }
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", "status": int32(constant.GroupOk), "group_type": int32(constant.WorkingGroup), groupauthority.RevisionField: int64(1)}); err != nil { t.Fatal(err) }
	for uid, role := range map[string]int32{"owner": constant.GroupOwner, "admin": constant.GroupAdmin, "member": constant.GroupOrdinaryUsers} {
		if _, err := db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: "g", UserID: uid, RoleLevel: role, Nickname: uid, FaceURL: "synthetic-avatar", JoinTime: time.Unix(1, 0)}); err != nil { t.Fatal(err) }
	}
	service, dispatches := realAuthorityRPC(t, ctx, authority, db)
	if dispatches.Load() != 0 { t.Fatal("fixture sent a legacy notification") }
	runtime, err := newGroupNoticeRuntime(db, uuid.NewString())
	if err != nil { t.Fatal(err) }
	service.notices = runtime
	store := runtime.effects.(*groupeffects.Store)
	if err := store.CheckReady(ctx); err != nil { t.Fatal(err) }
	return ctx, service, authority, db, store
}
func statusActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, constant.OpUserID, actor)
}
func storedGroupStatus(t *testing.T, ctx context.Context, db *mongo.Database) int32 {
	t.Helper()
	var row struct { Status int32 `bson:"status"` }
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": "g"}).Decode(&row); err != nil { t.Fatal(err) }
	return row.Status
}
func effectCount(t *testing.T, ctx context.Context, db *mongo.Database) int64 {
	t.Helper()
	n, err := db.Collection(groupeffects.Collection).CountDocuments(ctx, bson.M{"kind": groupeffects.Notification})
	if err != nil { t.Fatal(err) }
	return n
}
type actionReceipt struct {
	groupeffects.Receipt `bson:",inline"`
	Targets []groupeffects.NotificationTargets `bson:"delivery_targets"`
	Policy groupeffects.ActionDeliveryPolicy `bson:"action_policy"`
	Producer string `bson:"producer_version"`
	Command string `bson:"command_id"`
}
func readActionReceipt(t *testing.T, ctx context.Context, db *mongo.Database) actionReceipt {
	t.Helper()
	var row actionReceipt
	if err := db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"kind": groupeffects.Notification}).Decode(&row); err != nil { t.Fatal(err) }
	return row
}

func TestGroupStatusRPCBusinessAndFrozenNotificationCommitTogether(t *testing.T) {
	ctx, service, _, db, store := groupStatusFixture(t, nil, nil)
	if _, err := service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"}); err != nil { t.Fatal(err) }
	if storedGroupStatus(t, ctx, db) != constant.GroupStatusMuted || effectCount(t, ctx, db) != 1 { t.Fatal("business/effect not jointly committed") }
	row := readActionReceipt(t, ctx, db)
	if row.State != groupeffects.Pending || row.Producer != "group-action-v1" || row.Command != row.OperationID { t.Fatal("missing durable command/effect") }
	for _, target := range row.Targets {
		if !reflect.DeepEqual(target.UserIDs, []string{"admin", "member", "owner"}) { t.Fatal("group-only writer did not retain exact full roster") }
	}
	if len(row.Targets) != 2 { t.Fatal("configured visible/silent split changed") }
	if _, err := store.ReadNotificationDelivery(ctx, row.ID); !errors.Is(err, groupeffects.ErrUnavailable) { t.Fatal("old policy-oblivious receiver consumed an action duty", err) }
	if _, err := service.CancelMuteGroup(statusActor(ctx, "admin"), &pbgroup.CancelMuteGroupReq{GroupID: "g"}); err != nil { t.Fatal(err) }
	if storedGroupStatus(t, ctx, db) != constant.GroupOk || effectCount(t, ctx, db) != 2 { t.Fatal("unmute lost its own committed obligation") }
}

func TestGroupStatusRPCKnownCommitCacheFailureReturnsSuccessAndRetainsDuty(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, nil)
	service.invalidate = func(context.Context, string) error { return errors.New("synthetic cache unavailable") }
	_, err := service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"})
	if err != nil { t.Fatal("known committed mutation must retain ordinary success ACK", err) }
	if storedGroupStatus(t, ctx, db) != constant.GroupStatusMuted || effectCount(t, ctx, db) != 1 { t.Fatal("cache error dropped committed business/effect") }
	n, err := db.Collection(groupauthority.InvalidationsCollection).CountDocuments(ctx, bson.M{})
	if err != nil || n != 1 { t.Fatal("cache recovery obligation missing", err) }
}

type noticeQueueProbe struct {
	groupNoticeQueue
	after func(context.Context, *groupauthority.NoticeSnapshot, groupeffects.Frozen) error
}
func (p *noticeQueueProbe) QueueActionNotification(ctx context.Context, snapshot *groupauthority.NoticeSnapshot, message *sdkws.MsgData, policy groupeffects.ActionDeliveryPolicy) (groupeffects.Frozen, error) {
	f, err := p.groupNoticeQueue.QueueActionNotification(ctx, snapshot, message, policy)
	if err == nil && p.after != nil { err = p.after(ctx, snapshot, f) }
	return f, err
}

func TestGroupStatusRPCQueueFailureAbortsStatusAndAllObligations(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, nil)
	sentinel := errors.New("synthetic post-queue abort")
	service.notices.effects = &noticeQueueProbe{groupNoticeQueue: service.notices.effects, after: func(context.Context, *groupauthority.NoticeSnapshot, groupeffects.Frozen) error { return sentinel }}
	if _, err := service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"}); !errors.Is(err, sentinel) { t.Fatal(err) }
	if storedGroupStatus(t, ctx, db) != constant.GroupOk || effectCount(t, ctx, db) != 0 { t.Fatal("aborted status/effect leaked") }
	n, err := db.Collection(groupauthority.InvalidationsCollection).CountDocuments(ctx, bson.M{})
	if err != nil || n != 0 { t.Fatal("aborted cache duty leaked", err) }
}

func TestGroupStatusRPCRejectsOrdinaryAndNeverReactivatesDismissed(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, nil)
	if _, err := service.MuteGroup(statusActor(ctx, "member"), &pbgroup.MuteGroupReq{GroupID: "g"}); !errs.ErrNoPermission.Is(err) { t.Fatal("ordinary user admitted", err) }
	if _, err := db.Collection("group").UpdateOne(ctx, bson.M{"group_id": "g"}, bson.M{"$set": bson.M{"status": int32(constant.GroupStatusDismissed)}}); err != nil { t.Fatal(err) }
	for _, unmute := range []bool{false, true} {
		var err error
		if unmute { _, err = service.CancelMuteGroup(statusActor(ctx, "admin"), &pbgroup.CancelMuteGroupReq{GroupID: "g"}) } else { _, err = service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"}) }
		if err == nil || storedGroupStatus(t, ctx, db) != constant.GroupStatusDismissed || effectCount(t, ctx, db) != 0 { t.Fatal("terminal group was reactivated or emitted an effect", err) }
	}
}

func TestGroupStatusRPCRetryKeepsCommandButInvalidatesAttemptCapability(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, nil)
	var first *groupauthority.NoticeSnapshot
	var frozen groupeffects.Frozen
	var calls int
	service.notices.effects = &noticeQueueProbe{groupNoticeQueue: service.notices.effects, after: func(tx context.Context, snapshot *groupauthority.NoticeSnapshot, f groupeffects.Frozen) error {
		calls++
		if err := groupauthority.RequireMembers(tx, db, "g", []string{"owner"}); !errors.Is(err, groupauthority.ErrInvalidScope) { t.Fatal("notification widened member-write scope", err) }
		if calls == 1 {
			first, frozen = snapshot, f
			return mongo.CommandError{Code: 112, Message: "synthetic bounded retry", Labels: []string{"TransientTransactionError"}}
		}
		if first == snapshot || frozen.ID != f.ID || frozen.PayloadSHA256 != f.PayloadSHA256 { t.Fatal("retry changed durable intent or borrowed prior capability") }
		if _, _, err := first.NotificationRecipients(tx, db, uuid.NewString(), strings.Repeat("0", 64), constant.GroupMutedNotification, true); !errors.Is(err, groupauthority.ErrInvalidScope) { t.Fatal("abandoned capability accepted in new attempt", err) }
		return nil
	}}
	if _, err := service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"}); err != nil { t.Fatal(err) }
	if calls != 2 || effectCount(t, ctx, db) != 1 { t.Fatal("retry duplicated notification or failed to retry", calls) }
	if _, _, err := first.NotificationRecipients(statusActor(ctx, "admin"), db, uuid.NewString(), strings.Repeat("0", 64), constant.GroupMutedNotification, true); err == nil { t.Fatal("capability escaped transaction lifetime") }
}

func TestGroupStatusRPCFrozenPolicyPreservesLegalOfflineAndCallbacks(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, func() {
		config.Config.Notification.GroupMuted = config.NotificationConf{IsSendMsg: false, ReliabilityLevel: constant.UnreliableNotification, OfflinePush: config.POfflinePush{Enable: true}}
		config.Config.Callback.CallbackUrl = "https://synthetic.invalid/callback"
		config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable = true
		config.Config.Callback.CallbackOnlinePush.Enable = true
		config.Config.Callback.CallbackOfflinePush.Enable = true
	})
	// A changed global object cannot retarget a prepared runtime's duty.
	config.Config.Notification.GroupMuted = config.NotificationConf{IsSendMsg: true, ReliabilityLevel: constant.ReliableNotificationNoMsg}
	config.Config.Callback.CallbackOfflinePush.Enable = false
	if _, err := service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"}); err != nil { t.Fatal(err) }
	row := readActionReceipt(t, ctx, db)
	if !row.Policy.OfflineEnabled || row.Policy.BeforeGroupPushBinding == "" || row.Policy.OnlinePushBinding == "" || row.Policy.OfflinePushBinding == "" || len(row.Targets) != 1 { t.Fatal("legal enabled policy silently disabled") }
	var message sdkws.MsgData
	if err := proto.Unmarshal(row.Payload, &message); err != nil { t.Fatal(err) }
	opts := msgprocessor.Options(message.Options)
	if opts.IsHistory() || opts.IsSendMsg() || !opts.IsOfflinePush() { t.Fatal("legacy unreliable/silent/offline configuration changed") }
}

type statusCASBarrierKey struct{}
func TestGroupStatusRPCRevocationCommitWinsBeforeDelayedStatusWrite(t *testing.T) {
	paused, release := make(chan struct{}), make(chan struct{})
	var once, resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(release) }) }
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, event *event.CommandStartedEvent) {
		if ctx.Value(statusCASBarrierKey{}) != true || event.CommandName != "update" { return }
		name, _ := event.Command.Lookup("update").StringValueOK()
		if name != "group" { return }
		once.Do(func() { close(paused); select { case <-release: case <-ctx.Done(): } })
	}}
	ctx, service, _, db, _ := groupStatusFixture(t, monitor, nil)
	finished := make(chan error, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		resume()
		select { case <-done: case <-ctx.Done(): t.Error("actual RPC goroutine shutdown unconfirmed") }
	})
	go func() { defer close(done); _, err := service.MuteGroup(context.WithValue(statusActor(ctx, "admin"), statusCASBarrierKey{}, true), &pbgroup.MuteGroupReq{GroupID: "g"}); finished <- err }()
	select { case <-paused: case <-ctx.Done(): t.Fatal("actual RPC did not reach CAS barrier") }
	role := int32(constant.GroupOrdinaryUsers)
	_, err := service.SetGroupMemberInfo(statusActor(ctx, "owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "admin", &role, nil, nil)}})
	if err != nil { t.Fatal(err) }
	resume()
	select {
	case err := <-finished:
		if !errs.ErrNoPermission.Is(err) { t.Fatal("delayed RPC ignored committed revocation", err) }
	case <-ctx.Done(): t.Fatal("delayed RPC did not finish")
	}
	if storedGroupStatus(t, ctx, db) != constant.GroupOk || effectCount(t, ctx, db) != 0 { t.Fatal("revoked action leaked state/effect") }
}

type statusManagerUsers struct {
	pbuser.UserClient
	calls int
}
func (c *statusManagerUsers) GetDesignateUsers(_ context.Context, req *pbuser.GetDesignateUsersReq, _ ...grpc.CallOption) (*pbuser.GetDesignateUsersResp, error) {
	c.calls++
	if !reflect.DeepEqual(req.UserIDs, []string{"synthetic-service"}) { return nil, errs.ErrArgs }
	return &pbuser.GetDesignateUsersResp{UsersInfo: []*sdkws.UserInfo{{UserID: "synthetic-service", Nickname: "synthetic-manager", FaceURL: "synthetic-avatar"}}}, nil
}

func TestGroupStatusRPCKeepsConfiguredManagerAndMutedAdminLegal(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, nil)
	users := &statusManagerUsers{}
	service.User.Client = users
	if _, err := service.MuteGroup(statusActor(ctx, "synthetic-service"), &pbgroup.MuteGroupReq{GroupID: "g"}); err != nil { t.Fatal("configured manager rejected", err) }
	if users.calls != 1 { t.Fatal("manager hydration was retried or omitted", users.calls) }
	if _, err := db.Collection("group_member").UpdateOne(ctx, bson.M{"group_id": "g", "user_id": "admin"}, bson.M{"$set": bson.M{"mute_end_time": time.Now().UTC().Add(time.Hour)}}); err != nil { t.Fatal(err) }
	if _, err := service.CancelMuteGroup(statusActor(ctx, "admin"), &pbgroup.CancelMuteGroupReq{GroupID: "g"}); err != nil { t.Fatal("muted administrator lost management rights", err) }
	if storedGroupStatus(t, ctx, db) != constant.GroupOk || effectCount(t, ctx, db) != 2 { t.Fatal("legal manager/admin effects missing") }
}

func TestGroupStatusRPCOverfullRosterAbortsBeforeBusinessWrite(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, nil)
	rows := make([]any, 49998) // Three seeded members + these = 50001.
	for i := range rows { rows[i] = bson.M{"group_id": "g", "user_id": fmt.Sprintf("synthetic-overflow-%05d", i), "role_level": int32(constant.GroupOrdinaryUsers)} }
	if _, err := db.Collection("group_member").InsertMany(ctx, rows); err != nil { t.Fatal(err) }
	if _, err := service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"}); !errors.Is(err, groupauthority.ErrInvalidScope) { t.Fatal("overflow roster was truncated/accepted", err) }
	if storedGroupStatus(t, ctx, db) != constant.GroupOk || effectCount(t, ctx, db) != 0 { t.Fatal("overflow committed business/effects") }
	var row struct { Revision int64 `bson:"authz_revision"` }
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": "g"}).Decode(&row); err != nil || row.Revision != 1 { t.Fatal("overflow did not abort even the revision fence", err) }
}

// These two cases exercise ONLY the new readonly capability with real models;
// actual Kick/Quit RPCs and their cross-service floor receipts are not wired by
// slice1. This test must never be reported as those RPCs having been delivered.
func TestGroupNoticeRemovedSelfRecipientDoesNotGrantRosterWrites(t *testing.T) {
	for _, action := range []groupauthority.NoticeAction{groupauthority.NoticeKick, groupauthority.NoticeQuit} {
		t.Run(string(action), func(t *testing.T) {
			ctx, service, authority, db, _ := groupStatusFixture(t, nil, nil)
			actor := "owner"
			contentType := int32(constant.MemberKickedNotification)
			if action == groupauthority.NoticeQuit { actor, contentType = "member", int32(constant.MemberQuitNotification) }
			commandCtx, _, err := groupauthority.NewNoticeCommand(statusActor(ctx, actor), service.notices.epoch, action, "g")
			if err != nil { t.Fatal(err) }
			var snapshot *groupauthority.NoticeSnapshot
			_, err = authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: "g", Members: []string{"member"}}}, func(tx context.Context) error {
				var err error
				snapshot, err = groupauthority.BeginNotice(tx, db, "g")
				return err
			}, func(tx context.Context) error {
				if err := service.db.DeleteGroupMember(tx, "g", []string{"member"}); err != nil { return err }
				if err := snapshot.Finish(tx, db); err != nil { return err }
				ids, _, err := snapshot.NotificationRecipients(tx, db, uuid.NewString(), strings.Repeat("0", 64), contentType, true)
				if err != nil || !reflect.DeepEqual(ids, []string{"admin", "member", "owner"}) { t.Fatal("silent notification dropped removed self", err) }
				ids, _, err = snapshot.NotificationRecipients(tx, db, uuid.NewString(), strings.Repeat("0", 64), contentType, false)
				if err != nil || !reflect.DeepEqual(ids, []string{"admin", "owner"}) { t.Fatal("visible copy disclosed beyond existing admin policy", err) }
				if err := groupauthority.RequireMembers(tx, db, "g", []string{"owner"}); !errors.Is(err, groupauthority.ErrInvalidScope) { t.Fatal("readonly recipients became writable", err) }
				if err := groupauthority.RequireAllMembers(tx, db, "g"); !errors.Is(err, groupauthority.ErrInvalidScope) { t.Fatal("readonly roster became whole-group write authority", err) }
				return nil
			})
			if err != nil { t.Fatal(err) }
		})
	}
}
