package group

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
)

// Reuse the original owned RS/nonce/claim/cleanup fixture. This proves the
// new capability against the real scoped member DAO and active Mongo TX; it
// does not pretend to prove namespace migration, windows or provider delivery.
func TestMemberAdmissionCapabilityBindsActualScopedWriteAndCannotSurviveItsTransaction(t *testing.T) {
	for _, mode := range []string{"commit-new", "existing-member", "rollback", "wrong-actor", "widened-scope"} {
		t.Run(mode, func(t *testing.T) {
			ctx, authority, db := groupRPCMongo(t, nil)
			if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", "status": int32(constant.GroupOk), "group_type": int32(constant.WorkingGroup), groupauthority.RevisionField: int64(1)}); err != nil {
				t.Fatal(err)
			}
			owner := &relation.GroupMemberModel{GroupID: "g", UserID: "owner", RoleLevel: constant.GroupOwner, JoinTime: time.Unix(1, 0).UTC()}
			if _, err := db.Collection("group_member").InsertOne(ctx, owner); err != nil {
				t.Fatal(err)
			}
			members, err := mgo.NewGroupMemberContext(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			uid := "new-member"
			if mode == "existing-member" {
				uid = "owner"
			}
			actor := context.WithValue(ctx, constant.OpUserID, "owner")
			commandCtx, _, err := groupauthority.NewMemberAdmissionCommand(actor, uuid.NewString(), "g", []string{uid}, groupauthority.MemberAdmissionInvite)
			if err != nil {
				t.Fatal(err)
			}
			targets := []groupauthority.Target{{GroupID: "g", Members: []string{uid}}}
			if mode == "widened-scope" {
				targets[0].Members = append(targets[0].Members, "foreign")
			}
			var cap *groupauthority.MemberAdmissionScope
			var captured groupauthority.MemberAdmissionInfo
			abort := errors.New("synthetic stop before business commit")
			_, err = authority.Mutation(commandCtx, targets, func(tx context.Context) error {
				var err error
				cap, err = groupauthority.BeginMemberAdmission(tx, db, "g")
				return err
			}, func(tx context.Context) error {
				if mode != "existing-member" {
					if err := members.Create(tx, []*relation.GroupMemberModel{{GroupID: "g", UserID: uid, RoleLevel: constant.GroupOrdinaryUsers, JoinTime: time.Unix(2, 0).UTC()}}); err != nil {
						return err
					}
				}
				if err := cap.Complete(tx, db); err != nil {
					return err
				}
				var err error
				captured, err = cap.Inspect(tx, db)
				if err != nil {
					return err
				}
				if mode == "wrong-actor" {
					if _, err := cap.Inspect(context.WithValue(tx, constant.OpUserID, "foreign"), db); err == nil {
						return errors.New("foreign actor borrowed original capability")
					}
				}
				if mode == "rollback" {
					return abort
				}
				return nil
			})
			if mode == "widened-scope" {
				if err == nil {
					t.Fatal("widened writer scope accepted")
				}
				return
			}
			if mode == "rollback" {
				if !errors.Is(err, abort) {
					t.Fatal("original transaction failure lost", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			want := []string{uid}
			if mode == "existing-member" {
				want = nil
			}
			if !reflect.DeepEqual(captured.NewMembers, want) || captured.GroupRevision < 1 {
				t.Fatal("new/existing membership classification differs", captured)
			}
			if _, err := cap.Inspect(commandCtx, db); err == nil {
				t.Fatal("finished/abandoned Mongo scope renewed admission")
			}
			count, err := db.Collection("group_member").CountDocuments(ctx, bson.M{"group_id": "g", "user_id": uid})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "rollback" && count != 0 || mode != "rollback" && count != 1 {
				t.Fatal("actual member commit/rollback differs", count)
			}
		})
	}
}

// Real same-TX queue + predecessor records, not a fake queue accepting any
// revision. Receiver/namespace delivery remains a separate integration gate.
func TestMembershipNotificationAndAfterCallbackUseOriginalReceiptOrderAtomically(t *testing.T) {
	for _, admission := range []bool{false, true} {
		name := "removal"
		if admission {
			name = "admission"
		}
		for _, rollback := range []bool{false, true} {
			mode := "commit"
			if rollback {
				mode = "rollback"
			}
			t.Run(name+"-"+mode, func(t *testing.T) {
				ctx, s, authority, db, store := groupStatusFixture(t, nil, func() {
					config.Config.Callback.CallbackUrl = "https://callback.fixture.invalid"
					config.Config.Callback.CallbackKillGroupMember = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
					config.Config.Callback.CallbackAfterJoinGroup = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
					policy := config.NotificationConf{IsSendMsg: true, ReliabilityLevel: constant.ReliableNotificationNoMsg}
					config.Config.Notification.MemberKicked, config.Config.Notification.MemberEnter = policy, policy
				})
				q, ok := s.notices.effects.(membershipEffectQueue)
				if !ok {
					t.Fatal("actual effect store is not the membership producer")
				}
				members, err := mgo.NewGroupMemberContext(ctx, db)
				if err != nil {
					t.Fatal(err)
				}
				actor := statusActor(ctx, "owner")
				uid := "member"
				if admission {
					uid = "new-member"
				}
				var command groupauthority.NoticeCommandInfo
				var commandCtx context.Context
				if admission {
					commandCtx, command, err = groupauthority.NewMemberAdmissionCommand(actor, s.notices.epoch, "g", []string{uid}, groupauthority.MemberAdmissionJoin)
				} else {
					commandCtx, command, err = groupauthority.NewNoticeCommand(actor, s.notices.epoch, groupauthority.NoticeKick, "g")
				}
				if err != nil {
					t.Fatal(err)
				}
				var removed *groupauthority.NoticeSnapshot
				var added *groupauthority.MemberAdmissionScope
				var notification groupeffects.Frozen
				stop := errors.New("synthetic abort after both durable obligations")
				_, err = authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: "g", Members: []string{uid}}}, func(tx context.Context) error {
					var err error
					if admission {
						added, err = groupauthority.BeginMemberAdmission(tx, db, "g")
					} else {
						removed, err = groupauthority.BeginNotice(tx, db, "g")
					}
					return err
				}, func(tx context.Context) error {
					var err error
					var message *sdkws.MsgData
					profile := &sdkws.PublicUserInfo{UserID: uid, Nickname: uid}
					actorProfile := &sdkws.PublicUserInfo{UserID: "owner", Nickname: "owner"}
					var callbackName groupeffects.CallbackName
					var callback any
					if admission {
						if err = members.Create(tx, []*relation.GroupMemberModel{{GroupID: "g", UserID: uid, RoleLevel: constant.GroupOrdinaryUsers, JoinTime: time.Unix(2, 0).UTC()}}); err != nil {
							return err
						}
						if err = added.Complete(tx, db); err != nil {
							return err
						}
						policy := s.notices.policies[groupauthority.NoticeAction(groupauthority.MemberAdmissionJoin)]
						message, err = s.admissionMessage(added, []string{uid}, groupauthority.MemberAdmissionJoin, actorProfile, map[string]*sdkws.PublicUserInfo{uid: profile}, policy)
						if err != nil {
							return err
						}
						notification, err = q.QueueMemberAdmissionNotification(tx, added, message, policy.delivery)
						callbackName = groupeffects.AfterJoin
						callback = callbackstruct.CallbackAfterJoinGroupReq{CallbackCommand: callbackstruct.CallbackAfterJoinGroupCommand, GroupID: "g", InviterUserID: uid}
					} else {
						if err = members.Delete(tx, "g", []string{uid}); err != nil {
							return err
						}
						if err = removed.Finish(tx, db); err != nil {
							return err
						}
						policy := s.notices.policies[groupauthority.NoticeKick]
						message, err = s.removalMessage(removed, []string{uid}, actorProfile, map[string]*sdkws.PublicUserInfo{uid: profile}, policy)
						if err != nil {
							return err
						}
						notification, err = q.QueueOrderedActionNotification(tx, removed, message, policy.delivery)
						callbackName = groupeffects.AfterKick
						callback = callbackstruct.CallbackKillGroupMemberReq{CallbackCommand: callbackstruct.CallbackKillGroupCommand, GroupID: "g", KickedUserIDs: []string{uid}}
					}
					if err != nil {
						return err
					}
					if err = s.queueMembershipAfter(tx, q, command, notification, callbackName, callback); err != nil {
						return err
					}
					if rollback {
						return stop
					}
					return nil
				})
				if rollback {
					if !errors.Is(err, stop) {
						t.Fatal("original abort lost", err)
					}
				} else if err != nil {
					t.Fatal("real notification/callback ordering rejected", err)
				}
				count, err := db.Collection(groupeffects.Collection).CountDocuments(ctx, bson.M{"kind": bson.M{"$in": []groupeffects.Kind{groupeffects.Notification, groupeffects.AfterCallback}}})
				if err != nil {
					t.Fatal(err)
				}
				wantCount := int64(2)
				if rollback {
					wantCount = 0
				}
				if count != wantCount {
					t.Fatal("transaction split business/effect obligations", count)
				}
				count, err = db.Collection("group_member").CountDocuments(ctx, bson.M{"group_id": "g", "user_id": uid})
				if err != nil {
					t.Fatal(err)
				}
				wantMember := int64(0)
				if admission != rollback {
					wantMember = 1
				}
				if count != wantMember {
					t.Fatal("actual member transaction outcome differs", count)
				}
				if rollback {
					return
				}
				var row struct {
					groupeffects.Receipt `bson:",inline"`
					Order                groupeffects.RoleEffectOrder `bson:"role_order"`
				}
				if err = db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"kind": groupeffects.AfterCallback}).Decode(&row); err != nil {
					t.Fatal(err)
				}
				if row.Order.PreviousID != notification.ID || row.Order.PreviousPayloadSHA256 != notification.PayloadSHA256 || row.Order.CommandID != command.ID || row.Order.Epoch != command.Epoch {
					t.Fatal("after callback detached from its original notification", row.Order)
				}
				if err = store.RoleOrderReady(ctx, row.ID); err == nil {
					t.Fatal("pending predecessor incorrectly became an acknowledged delivery")
				}
			})
		}
	}
}
