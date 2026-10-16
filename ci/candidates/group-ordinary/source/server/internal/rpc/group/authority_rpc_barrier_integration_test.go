package group

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mongotx"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
)

// Only notification assembly in this fixture bypasses Redis. The production
// controller, three real Mongo models, role DAO, group revision and TX are real.
// This proves the RPC authorization barrier, NOT Redis fault recovery/delivery.
type rpcNotificationReadDB struct {
	controller.GroupDatabase
	groups  relation.GroupModelInterface
	members relation.GroupMemberModelInterface
}

func (d *rpcNotificationReadDB) TakeGroup(ctx context.Context, gid string) (*relation.GroupModel, error) {
	return d.groups.Take(ctx, gid)
}
func (d *rpcNotificationReadDB) TakeGroupMember(ctx context.Context, gid, uid string) (*relation.GroupMemberModel, error) {
	return d.members.Take(ctx, gid, uid)
}
func (d *rpcNotificationReadDB) FindGroupMemberNum(ctx context.Context, gid string) (uint32, error) {
	n, e := d.members.TakeGroupMemberNum(ctx, gid)
	return uint32(n), e
}
func (d *rpcNotificationReadDB) GetGroupRoleLevelMemberIDs(ctx context.Context, gid string, role int32) ([]string, error) {
	return d.members.FindRoleLevelUserIDs(ctx, gid, role)
}
func (d *rpcNotificationReadDB) FindGroupMembers(ctx context.Context, gid string, ids []string) ([]*relation.GroupMemberModel, error) {
	return d.members.FindForPermission(ctx, gid, ids)
}

func realAuthorityRPC(t *testing.T, ctx context.Context, store *groupauthority.Store, db *mongo.Database) (*groupServer, *atomic.Int32) {
	t.Helper()
	groups, e := mgo.NewGroupMongoContext(ctx, db)
	if e != nil {
		t.Fatal(e)
	}
	members, e := mgo.NewGroupMemberContext(ctx, db)
	if e != nil {
		t.Fatal(e)
	}
	requests, e := mgo.NewGroupRequestMgoContext(ctx, db)
	if e != nil {
		t.Fatal(e)
	}
	database := controller.NewGroupDatabase(nil, groups, members, requests, mongotx.New(db.Client()), nil)
	notifications := &atomic.Int32{}
	view := &rpcNotificationReadDB{GroupDatabase: database, groups: groups, members: members}
	n := notification.NewGroupNotificationSender(view, nil, nil, func(context.Context, []string) ([]notification.CommonUser, error) {
		t.Error("seeded profiles unexpectedly required external user RPC")
		return nil, errs.ErrArgs
	})
	n.NotificationSender = rpcclient.NewNotificationSender(rpcclient.WithLocalSendMsg(func(_ context.Context, r *pbmsg.SendMsgReq) (*pbmsg.SendMsgResp, error) {
		if r.MsgData == nil || r.MsgData.MsgFrom != constant.SysMsgType {
			t.Error("malformed notification dispatch")
		}
		notifications.Add(1)
		return &pbmsg.SendMsgResp{ClientMsgID: r.GetMsgData().GetClientMsgID()}, nil
	}))
	service := &groupServer{db: database, authority: store, invalidate: func(context.Context, string) error { return nil }, Notification: n}
	attachRoleNoticeFixture(t, ctx, service, db)
	return service, notifications
}

type delayedRoleRPCKey struct{}

func TestGroupAuthorityRealRPCRevocationACKFencesDelayedMemberEdit(t *testing.T) {
	memberEditSetup(t)
	paused, release := make(chan struct{}), make(chan struct{})
	var once, resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(release) }) }
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(delayedRoleRPCKey{}) != true || e.CommandName != "update" {
			return
		}
		collection, _ := e.Command.Lookup("update").StringValueOK()
		if collection != "group" {
			return
		}
		once.Do(func() {
			close(paused)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}}
	ctx, store, db := groupRPCMongo(t, monitor)
	if _, e := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", "group_type": int32(constant.WorkingGroup), "status": int32(0), groupauthority.RevisionField: int64(1)}); e != nil {
		t.Fatal(e)
	}
	for user, role := range map[string]int32{"owner": 100, "admin": 60, "member": 20} {
		if _, e := db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: "g", UserID: user, RoleLevel: role, Nickname: user, FaceURL: "synthetic-avatar", JoinTime: time.Unix(1, 0)}); e != nil {
			t.Fatal(e)
		}
	}
	service, notifications := realAuthorityRPC(t, ctx, store, db)
	// Same signed in-process callback as the already-accepted role suite. It is
	// outside TX retries; this cloud-only test performs no real user/group I/O.
	callbacks := memberEditCallback(t, func() {}, map[string]any{})
	done, exited := make(chan error, 1), make(chan struct{})
	t.Cleanup(func() {
		resume()
		select {
		case <-exited:
		case <-ctx.Done():
			t.Error("delayed real RPC did not terminate")
		}
	})
	go func() {
		defer close(exited)
		current := context.WithValue(memberEditContext("admin"), delayedRoleRPCKey{}, true)
		bounded, cancel := context.WithDeadline(current, time.Now().Add(25*time.Second))
		defer cancel()
		_, err := service.SetGroupMemberInfo(bounded, &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{{GroupID: "g", UserID: "member", Nickname: wrapperspb.String("stale-intent")}}})
		done <- err
	}()
	select {
	case <-paused:
	case <-ctx.Done():
		t.Fatal("real RPC first revision CAS was not observed")
	}
	_, err := service.SetGroupMemberInfo(memberEditContext("owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{{GroupID: "g", UserID: "admin", RoleLevel: wrapperspb.Int32(20)}}})
	if err != nil {
		resume()
		t.Fatal("owner's real RPC revocation did not ACK", err)
	}
	resume()
	var delayed error
	select {
	case delayed = <-done:
	case <-ctx.Done():
		t.Fatal("delayed RPC incomplete")
	}
	var admin, member relation.GroupMemberModel
	if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": "g", "user_id": "admin"}).Decode(&admin); err != nil {
		t.Fatal(err)
	}
	if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": "g", "user_id": "member"}).Decode(&member); err != nil {
		t.Fatal(err)
	}
	if !errs.ErrNoPermission.Is(delayed) || admin.RoleLevel != 20 || member.Nickname != "member" {
		t.Fatalf("real RPC stale write escaped owner revocation: role=%d nicknameChanged=%t denied=%t", admin.RoleLevel, member.Nickname != "member", errs.ErrNoPermission.Is(delayed))
	}
	if callbacks.Load() != 2 {
		t.Fatalf("before callback repeated across retry: %d", callbacks.Load())
	}
	if notifications.Load() != 0 {
		t.Fatalf("durable RPC unexpectedly used legacy notification dispatch: %d", notifications.Load())
	}
	assertExactRoleRevocationDuty(t, ctx, db)
}

var rpcBatchAbort = errors.New("synthetic second writer abort")

type abortAfterFirstGroupWriter struct{ controller.GroupDatabase }

func (d *abortAfterFirstGroupWriter) UpdateGroupMembers(ctx context.Context, rows []*relation.BatchUpdateGroupMember) error {
	if len(rows) != 2 {
		return errors.New("unexpected synthetic batch shape")
	}
	if err := d.GroupDatabase.UpdateGroupMembers(ctx, rows[:1]); err != nil {
		return err
	}
	return rpcBatchAbort
}
func TestGroupAuthorityRealRPCBatchWriterFailureRollsBackBothGroupFences(t *testing.T) {
	memberEditSetup(t)
	ctx, store, db := groupRPCMongo(t, nil)
	for _, gid := range []string{"g", "h"} {
		if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": gid, "group_type": int32(constant.WorkingGroup), "status": int32(0), groupauthority.RevisionField: int64(1)}); err != nil {
			t.Fatal(err)
		}
		for uid, role := range map[string]int32{"owner": 100, "member": 20} {
			if _, err := db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: gid, UserID: uid, RoleLevel: role, Nickname: uid, FaceURL: "synthetic-avatar"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	service, notifications := realAuthorityRPC(t, ctx, store, db)
	service.db = &abortAfterFirstGroupWriter{GroupDatabase: service.db}
	_, err := service.SetGroupMemberInfo(memberEditContext("owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{
		{GroupID: "g", UserID: "member", Nickname: wrapperspb.String("rolled-back")},
		{GroupID: "h", UserID: "member", Nickname: wrapperspb.String("not-written")},
	}})
	if !errors.Is(err, rpcBatchAbort) {
		t.Fatal("expected bounded writer failure was changed", err)
	}
	for _, gid := range []string{"g", "h"} {
		var row struct {
			Revision int64 `bson:"authz_revision"`
		}
		if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Decode(&row); err != nil || row.Revision != 1 {
			t.Fatal("group fence survived abort", err)
		}
		var member relation.GroupMemberModel
		if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": gid, "user_id": "member"}).Decode(&member); err != nil || member.Nickname != "member" {
			t.Fatal("partial batch write survived abort", err)
		}
	}
	if count, err := db.Collection(groupauthority.InvalidationsCollection).CountDocuments(ctx, bson.M{}); err != nil || count != 0 {
		t.Fatal("aborted batch retained cache obligation", err)
	}
	if notifications.Load() != 0 {
		t.Fatal("aborted batch dispatched postcommit notification")
	}
}

func TestGroupAuthorityRequestAndMembershipHistoryShareTheSameCommit(t *testing.T) {
	memberEditSetup(t)
	for _, abort := range []bool{true, false} {
		name := "known-commit"
		if abort {
			name = "abort-after-history"
		}
		t.Run(name, func(t *testing.T) {
			ctx, store, db := groupRPCMongo(t, nil)
			if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", "status": int32(0), groupauthority.RevisionField: int64(1)}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: "g", UserID: "owner", RoleLevel: 100, Nickname: "owner", FaceURL: "synthetic-avatar"}); err != nil {
				t.Fatal(err)
			}
			service, _ := realAuthorityRPC(t, ctx, store, db)
			if _, err := db.Collection("group_request").InsertOne(ctx, bson.M{"group_id": "g", "user_id": "applicant", "handle_result": int32(0)}); err != nil {
				t.Fatal(err)
			}
			member := &relation.GroupMemberModel{GroupID: "g", UserID: "applicant", RoleLevel: 20, JoinTime: time.Unix(100, 0)}
			commit, err := store.Mutation(memberEditContext("owner"), []groupauthority.Target{{GroupID: "g", Members: []string{"applicant"}}}, func(current context.Context) error {
				return service.freshAdmin(current, "g", true)
			}, func(current context.Context) error {
				if err := service.db.HandlerGroupRequest(current, "g", "applicant", "fixture", constant.GroupResponseAgree, member); err != nil {
					return err
				}
				if abort {
					return rpcBatchAbort
				}
				return nil
			})
			if abort {
				if !errors.Is(err, rpcBatchAbort) || commit != nil {
					t.Fatal("abort reported a committed request", err)
				}
			} else if err != nil || commit == nil {
				t.Fatal("valid request/history commit failed", err)
			}
			var request relation.GroupRequestModel
			if err := db.Collection("group_request").FindOne(ctx, bson.M{"group_id": "g", "user_id": "applicant"}).Decode(&request); err != nil {
				t.Fatal(err)
			}
			expected := int64(1)
			if abort {
				expected = 0
			}
			for _, collection := range []string{"group_member", "group_membership_history"} {
				count, err := db.Collection(collection).CountDocuments(ctx, bson.M{"group_id": "g", "user_id": "applicant"})
				if err != nil || count != expected {
					t.Fatalf("request/member/history split: collection=%s expected=%d count=%d error=%v", collection, expected, count, err)
				}
			}
			if abort && request.HandleResult != 0 || !abort && request.HandleResult != constant.GroupResponseAgree {
				t.Fatal("request outcome split from membership/history")
			}
		})
	}
}
