package group

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Uses the existing exclusive synthetic Mongo replica-set fixture, mandatory
// in CI. This proves actual RPC/transaction production, not gateway delivery.
func memberNoticeFixture(t *testing.T, monitor *event.CommandMonitor, configure func()) (context.Context, *groupServer, *groupauthority.Store, *mongo.Database) {
	t.Helper()
	ctx, service, authority, db, _ := groupStatusFixture(t, monitor, func() {
		policy := config.NotificationConf{IsSendMsg: true, ReliabilityLevel: constant.ReliableNotificationNoMsg}
		config.Config.Notification.GroupMemberMuted, config.Config.Notification.GroupMemberCancelMuted = policy, policy
		if configure != nil { configure() }
	})
	return ctx, service, authority, db
}

func memberMuteTime(t *testing.T, ctx context.Context, db *mongo.Database, uid string) time.Time {
	t.Helper()
	var row relation.GroupMemberModel
	if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": "g", "user_id": uid}).Decode(&row); err != nil { t.Fatal(err) }
	return row.MuteEndTime
}

func memberActionReceipt(t *testing.T, ctx context.Context, db *mongo.Database, action groupauthority.NoticeAction) actionReceipt {
	t.Helper()
	var row actionReceipt
	if err := db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"kind": groupeffects.Notification, "action": action}).Decode(&row); err != nil { t.Fatal(err) }
	return row
}

func TestMemberMuteRPCCommitsOriginalWireAndExactDefaultRecipients(t *testing.T) {
	ctx, service, _, db := memberNoticeFixture(t, nil, nil)
	if _, err := db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: "g", UserID: "unrelated", RoleLevel: constant.GroupOrdinaryUsers}); err != nil { t.Fatal(err) }
	if _, err := service.MuteGroupMember(statusActor(ctx, "admin"), &pbgroup.MuteGroupMemberReq{GroupID: "g", UserID: "member", MutedSeconds: 93}); err != nil { t.Fatal(err) }
	row := memberActionReceipt(t, ctx, db, groupauthority.NoticeMuteMember)
	if row.State != groupeffects.Pending || row.Command != row.OperationID || row.Producer != "group-action-v1" || len(row.Targets) != 2 { t.Fatal("business write lacks its durable configured split") }
	for _, branch := range row.Targets {
		if !reflect.DeepEqual(branch.UserIDs, []string{"admin", "member", "owner"}) { t.Fatal("member notice default recipients changed", branch.UserIDs) }
	}
	var message sdkws.MsgData
	if err := proto.Unmarshal(row.Payload, &message); err != nil { t.Fatal(err) }
	var elem sdkws.NotificationElem
	if err := json.Unmarshal(message.Content, &elem); err != nil { t.Fatal(err) }
	var tips sdkws.GroupMemberMutedTips
	if err := json.Unmarshal([]byte(elem.Detail), &tips); err != nil { t.Fatal(err) }
	if message.ContentType != constant.GroupMemberMutedNotification || message.SendID != "admin" || message.GroupID != "g" || tips.Group == nil || tips.Group.GroupID != "g" || tips.OpUser == nil || tips.OpUser.UserID != "admin" || tips.MutedUser == nil || tips.MutedUser.UserID != "member" || tips.MutedSeconds != 93 { t.Fatal("original SDK wire body changed") }
	until := memberMuteTime(t, ctx, db, "member")
	if !until.Equal(row.CreatedAt.Add(93*time.Second)) || tips.MutedUser.MuteEndTime != until.UnixMilli() { t.Fatal("notification and committed deadline differ") }
	if !memberMuteTime(t, ctx, db, "unrelated").IsZero() || storedGroupStatus(t, ctx, db) != constant.GroupOk { t.Fatal("mute widened its write scope") }
	if _, err := service.CancelMuteGroupMember(statusActor(ctx, "admin"), &pbgroup.CancelMuteGroupMemberReq{GroupID: "g", UserID: "member"}); err != nil { t.Fatal(err) }
	row = memberActionReceipt(t, ctx, db, groupauthority.NoticeUnmuteMember)
	if !memberMuteTime(t, ctx, db, "member").Equal(time.Unix(0, 0)) || effectCount(t, ctx, db) != 2 || len(row.Targets) != 2 { t.Fatal("unmute lost business state or its own obligation") }
	if err := proto.Unmarshal(row.Payload, &message); err != nil { t.Fatal(err) }
	if err := json.Unmarshal(message.Content, &elem); err != nil { t.Fatal(err) }
	var cancelled sdkws.GroupMemberCancelMutedTips
	if err := json.Unmarshal([]byte(elem.Detail), &cancelled); err != nil { t.Fatal(err) }
	if message.ContentType != constant.GroupMemberCancelMutedNotification || cancelled.OpUser == nil || cancelled.OpUser.UserID != "admin" || cancelled.MutedUser == nil || cancelled.MutedUser.UserID != "member" || cancelled.MutedUser.MuteEndTime != 0 { t.Fatal("unmute wire is not the legacy SDK contract") }
	for _, branch := range row.Targets { if !reflect.DeepEqual(branch.UserIDs, []string{"admin", "member", "owner"}) { t.Fatal("unmute recipient contract changed") } }
}

func TestMemberMuteRPCQueueAbortRollsBackMemberEffectAndCacheDuty(t *testing.T) {
	for _, cancelMute := range []bool{false, true} {
		t.Run(map[bool]string{false:"mute", true:"unmute"}[cancelMute], func(t *testing.T) {
			ctx, service, _, db := memberNoticeFixture(t, nil, nil)
			before := time.Now().UTC().Truncate(time.Millisecond).Add(time.Hour)
			if _, err := db.Collection("group_member").UpdateOne(ctx, bson.M{"group_id":"g", "user_id":"member"}, bson.M{"$set":bson.M{"mute_end_time":before}}); err != nil { t.Fatal(err) }
			sentinel := errors.New("synthetic member notice post-queue abort")
			service.notices.effects = &noticeQueueProbe{groupNoticeQueue: service.notices.effects, after: func(context.Context, *groupauthority.NoticeSnapshot, groupeffects.Frozen) error { return sentinel }}
			var err error
			if cancelMute { _, err = service.CancelMuteGroupMember(statusActor(ctx,"admin"), &pbgroup.CancelMuteGroupMemberReq{GroupID:"g", UserID:"member"}) } else { _, err = service.MuteGroupMember(statusActor(ctx,"admin"), &pbgroup.MuteGroupMemberReq{GroupID:"g", UserID:"member", MutedSeconds:12}) }
			if !errors.Is(err, sentinel) { t.Fatal("actual queue/write path was not exercised", err) }
			if !memberMuteTime(t,ctx,db,"member").Equal(before) || effectCount(t,ctx,db)!=0 { t.Fatal("aborted member/effect leaked") }
			n, err := db.Collection(groupauthority.InvalidationsCollection).CountDocuments(ctx,bson.M{})
			if err != nil || n != 0 { t.Fatal("aborted cache recovery duty leaked", err) }
			var group struct { Revision int64 `bson:"authz_revision"` }
			if err := db.Collection("group").FindOne(ctx,bson.M{"group_id":"g"}).Decode(&group); err != nil || group.Revision != 1 { t.Fatal("aborted authority fence leaked", err) }
		})
	}
}

func TestMemberMuteRPCRetryKeepsDeadlinePayloadAndMinimumWriter(t *testing.T) {
	ctx, service, _, db := memberNoticeFixture(t,nil,nil)
	var first *groupauthority.NoticeSnapshot
	var original groupeffects.Frozen
	var originalUntil time.Time
	calls := 0
	service.notices.effects = &noticeQueueProbe{groupNoticeQueue:service.notices.effects, after:func(tx context.Context, snapshot *groupauthority.NoticeSnapshot, f groupeffects.Frozen) error {
		calls++
		mute, ok := snapshot.MemberMute()
		if !ok || mute.UserID != "member" || mute.Seconds != 37 { t.Fatal("frozen member command missing") }
		if err := groupauthority.RequireMembers(tx,db,"g",[]string{"member"}); err != nil { t.Fatal("original member scope lost",err) }
		if err := groupauthority.RequireMembers(tx,db,"g",[]string{"owner"}); !errors.Is(err,groupauthority.ErrInvalidScope) { t.Fatal("readonly full roster granted owner write",err) }
		if calls == 1 {
			first, original, originalUntil = snapshot, f, mute.Until
			return mongo.CommandError{Code:112, Message:"synthetic member TX retry", Labels:[]string{"TransientTransactionError"}}
		}
		if first == snapshot || original.ID != f.ID || original.PayloadSHA256 != f.PayloadSHA256 || !originalUntil.Equal(mute.Until) { t.Fatal("retry extended duration, changed payload, or borrowed capability") }
		if _,_,err := first.NotificationRecipients(tx,db,uuid.NewString(),strings.Repeat("0",64),constant.GroupMemberMutedNotification,true); !errors.Is(err,groupauthority.ErrInvalidScope) { t.Fatal("abandoned snapshot escaped its attempt",err) }
		return nil
	}}
	if _,err := service.MuteGroupMember(statusActor(ctx,"admin"),&pbgroup.MuteGroupMemberReq{GroupID:"g",UserID:"member",MutedSeconds:37}); err != nil { t.Fatal(err) }
	if calls != 2 || effectCount(t,ctx,db) != 1 || !memberMuteTime(t,ctx,db,"member").Equal(originalUntil) { t.Fatal("retry failed or duplicated committed intent",calls) }
}

func TestMemberMuteRPCPreservesRoleHierarchyManagerAndMutedAdmin(t *testing.T) {
	for _, tc := range []struct { name,actor,target string; allowed,muted bool }{
		{"ordinary-denied","member","member",false,false},
		{"admin-owner-denied","admin","owner",false,false},
		{"admin-peer-denied","admin","admin",false,false},
		{"owner-admin-allowed","owner","admin",true,false},
		{"configured-manager-owner-allowed","synthetic-service","owner",true,false},
		{"muted-admin-member-allowed","admin","member",true,true},
	} {
		t.Run(tc.name,func(t *testing.T) {
			ctx,service,_,db := memberNoticeFixture(t,nil,nil)
			if tc.actor == "synthetic-service" { service.User.Client=&statusManagerUsers{} }
			if tc.muted { if _,err := db.Collection("group_member").UpdateOne(ctx,bson.M{"group_id":"g","user_id":"admin"},bson.M{"$set":bson.M{"mute_end_time":time.Now().UTC().Add(time.Hour)}}); err != nil { t.Fatal(err) } }
			_,err := service.MuteGroupMember(statusActor(ctx,tc.actor),&pbgroup.MuteGroupMemberReq{GroupID:"g",UserID:tc.target,MutedSeconds:5})
			if tc.allowed {
				if err != nil || effectCount(t,ctx,db) != 1 { t.Fatal("legal manager action rejected",err) }
			} else if !errs.ErrNoPermission.Is(err) || effectCount(t,ctx,db) != 0 || !memberMuteTime(t,ctx,db,tc.target).IsZero() { t.Fatal("role denial admitted a write",err) }
		})
	}
}

func TestMemberMuteRPCKnownCommitRetainsSuccessWhenProjectionUnavailable(t *testing.T) {
	for _, missing := range []bool{false,true} {
		t.Run(map[bool]string{false:"error",true:"missing"}[missing],func(t *testing.T) {
			ctx,service,_,db := memberNoticeFixture(t,nil,nil)
			service.invalidate=func(context.Context,string) error { return errors.New("synthetic cache down") }
			if missing { service.invalidate=nil }
			if _,err := service.MuteGroupMember(statusActor(ctx,"admin"),&pbgroup.MuteGroupMemberReq{GroupID:"g",UserID:"member",MutedSeconds:17}); err != nil { t.Fatal("committed write incorrectly invited replay",err) }
			if effectCount(t,ctx,db)!=1 || memberMuteTime(t,ctx,db,"member").IsZero() { t.Fatal("known commit lost member or duty") }
			n,err := db.Collection(groupauthority.InvalidationsCollection).CountDocuments(ctx,bson.M{})
			if err != nil || n != 1 { t.Fatal("projection recovery not retained",err) }
		})
	}
}

func TestMemberMuteRPCRejectsRetargetDuringDisplayHydration(t *testing.T) {
	ctx,service,_,db := memberNoticeFixture(t,nil,nil)
	if _,err := db.Collection("group_member").UpdateOne(ctx,bson.M{"group_id":"g","user_id":"member"},bson.M{"$set":bson.M{"nickname":"","face_url":""}}); err != nil { t.Fatal(err) }
	req:=&pbgroup.MuteGroupMemberReq{GroupID:"g",UserID:"member",MutedSeconds:19}
	calls:=0
	old:=service.Notification
	service.Notification=notification.NewGroupNotificationSender(service.db,nil,nil,func(_ context.Context,ids []string)([]notification.CommonUser,error) {
		calls++
		if !reflect.DeepEqual(ids,[]string{"member"}) { t.Fatal("display lookup was retargeted",ids) }
		req.GroupID,req.UserID,req.MutedSeconds="foreign","owner",999
		return []notification.CommonUser{&sdkws.UserInfo{UserID:"member",Nickname:"synthetic-display",FaceURL:"synthetic-avatar"}},nil
	})
	service.Notification.NotificationSender=old.NotificationSender
	if _,err := service.MuteGroupMember(statusActor(ctx,"admin"),req); !errs.ErrNoPermission.Is(err) { t.Fatal("mutated request was admitted",err) }
	if calls!=1 || effectCount(t,ctx,db)!=0 || !memberMuteTime(t,ctx,db,"member").IsZero() || !memberMuteTime(t,ctx,db,"owner").IsZero() { t.Fatal("display hydration retargeted or committed a write") }
}

type memberMuteCASBarrierKey struct{}
func TestMemberMuteRPCRevocationWinsBeforeDelayedMemberWrite(t *testing.T) {
	paused,release:=make(chan struct{}),make(chan struct{})
	var once,resumeOnce sync.Once
	resume:=func(){resumeOnce.Do(func(){close(release)})}
	monitor:=&event.CommandMonitor{Started:func(ctx context.Context,e *event.CommandStartedEvent){
		if ctx.Value(memberMuteCASBarrierKey{})!=true || e.CommandName!="update" { return }
		name,_:=e.Command.Lookup("update").StringValueOK();if name!="group" { return }
		once.Do(func(){close(paused);select{case <-release:case <-ctx.Done():}})
	}}
	ctx,service,_,db:=memberNoticeFixture(t,monitor,nil)
	finished,done:=make(chan error,1),make(chan struct{})
	t.Cleanup(func(){resume();select{case <-done:case <-ctx.Done():t.Error("member RPC goroutine shutdown unconfirmed")}})
	go func(){defer close(done);_,err:=service.MuteGroupMember(context.WithValue(statusActor(ctx,"admin"),memberMuteCASBarrierKey{},true),&pbgroup.MuteGroupMemberReq{GroupID:"g",UserID:"member",MutedSeconds:61});finished<-err}()
	select{case <-paused:case <-ctx.Done():t.Fatal("actual member RPC did not reach authority CAS")}
	role:=int32(constant.GroupOrdinaryUsers)
	if _,err:=service.SetGroupMemberInfo(statusActor(ctx,"owner"),&pbgroup.SetGroupMemberInfoReq{Members:[]*pbgroup.SetGroupMemberInfo{memberEdit("g","admin",&role,nil,nil)}});err!=nil{t.Fatal(err)}
	resume()
	select{case err:=<-finished:if !errs.ErrNoPermission.Is(err){t.Fatal("revoked actor changed member state",err)};case <-ctx.Done():t.Fatal("delayed member RPC did not finish")}
	if !memberMuteTime(t,ctx,db,"member").IsZero() || roleProtectedActionCount(t,ctx,db,"mute-member","unmute-member")!=0 {t.Fatal("revoked action leaked member/effect")}
	assertExactRoleRevocationDuty(t,ctx,db)
}

// This adapter tests the RPC's explicit unknown-outcome translation and proves
// it does not flush/replay. Shared Store wire-fault tests cover actual commit
// ambiguity; this fake is not evidence that an unknown transaction committed.
type memberUnconfirmedAuthority struct { calls int }
func (a *memberUnconfirmedAuthority) Mutation(context.Context, []groupauthority.Target, func(context.Context) error, func(context.Context) error) (*groupauthority.Commit,error) {
	a.calls++
	return nil,groupauthority.ErrCommitUnconfirmed
}
func TestMemberMuteRPCUnconfirmedCommitDoesNotReplayOrFlush(t *testing.T) {
	ctx,service,_,db:=memberNoticeFixture(t,nil,nil)
	a:=&memberUnconfirmedAuthority{}
	service.authority=a
	flushes:=0
	service.invalidate=func(context.Context,string)error{flushes++;return nil}
	_,err:=service.MuteGroupMember(statusActor(ctx,"admin"),&pbgroup.MuteGroupMemberReq{GroupID:"g",UserID:"member",MutedSeconds:11})
	if !errs.ErrInternalServer.Is(err) || !strings.Contains(err.Error(),"unconfirmed") || a.calls!=1 || flushes!=0 {t.Fatal("unknown outcome was treated as success or replayed",err,a.calls,flushes)}
	if !memberMuteTime(t,ctx,db,"member").IsZero() || effectCount(t,ctx,db)!=0 {t.Fatal("translation fixture unexpectedly wrote business/effects")}
}

func TestMemberMuteRPCDisplayLookupFailureDoesNotMutateOrQueue(t *testing.T) {
	ctx,service,_,db:=memberNoticeFixture(t,nil,nil)
	if _,err:=db.Collection("group_member").UpdateOne(ctx,bson.M{"group_id":"g","user_id":"member"},bson.M{"$set":bson.M{"nickname":"","face_url":""}});err!=nil{t.Fatal(err)}
	sentinel:=errors.New("synthetic target display unavailable")
	calls:=0
	old:=service.Notification
	service.Notification=notification.NewGroupNotificationSender(service.db,nil,nil,func(_ context.Context,ids []string)([]notification.CommonUser,error){
		calls++
		if !reflect.DeepEqual(ids,[]string{"member"}){t.Fatal("unexpected lookup target",ids)}
		return nil,sentinel
	})
	service.Notification.NotificationSender=old.NotificationSender
	if _,err:=service.MuteGroupMember(statusActor(ctx,"admin"),&pbgroup.MuteGroupMemberReq{GroupID:"g",UserID:"member",MutedSeconds:29});!errors.Is(err,sentinel){t.Fatal("failed display lookup was hidden",err)}
	if calls!=1 || effectCount(t,ctx,db)!=0 || !memberMuteTime(t,ctx,db,"member").IsZero(){t.Fatal("failed hydration committed member or effect")}
}
