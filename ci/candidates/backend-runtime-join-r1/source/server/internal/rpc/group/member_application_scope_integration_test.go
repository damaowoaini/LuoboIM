package group

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

// Actual authority TX + exact request/member DAO. This does not fabricate a
// namespace cutover receipt or count as RPC/Kafka/provider integration proof.
func TestMemberApplicationScopeUsesActualRequestWriterAndCanonicalDirectMessage(t *testing.T) {
	for _, mode := range []string{"create", "accept-new", "accept-existing", "reject", "rollback", "wrong-scope", "wrong-actor", "tampered-message"} {
		t.Run(mode, func(t *testing.T) {
			ctx, authority, db := groupRPCMongo(t, nil)
			if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", "group_type": int32(constant.WorkingGroup), "status": int32(constant.GroupOk), groupauthority.RevisionField: int64(1)}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: "g", UserID: "owner", RoleLevel: constant.GroupOwner, Nickname: "owner"}); err != nil {
				t.Fatal(err)
			}
			requests, err := mgo.NewGroupRequestMgoContext(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			members, err := mgo.NewGroupMemberContext(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "accept-existing" {
				if _, err = db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: "g", UserID: "applicant", RoleLevel: constant.GroupOrdinaryUsers}); err != nil {
					t.Fatal(err)
				}
			}
			kind := groupauthority.MemberApplicationCreated
			if mode == "accept-new" || mode == "accept-existing" {
				kind = groupauthority.MemberApplicationAccepted
			}
			if mode == "reject" {
				kind = groupauthority.MemberApplicationRejected
			}
			if kind != groupauthority.MemberApplicationCreated {
				if _, err = db.Collection("group_request").InsertOne(ctx, &relation.GroupRequestModel{GroupID: "g", UserID: "applicant", ReqMsg: "original request", ReqTime: time.Unix(1, 0).UTC()}); err != nil {
					t.Fatal(err)
				}
			}
			actor := context.WithValue(ctx, constant.OpUserID, "owner")
			commandCtx, command, err := groupauthority.NewMemberApplicationCommand(actor, uuid.NewString(), "g", []string{"applicant"}, kind)
			if err != nil {
				t.Fatal(err)
			}
			targets := []groupauthority.Target{{GroupID: "g", Members: []string{"applicant"}}}
			if mode == "wrong-scope" {
				targets[0].Members = append(targets[0].Members, "foreign")
			}
			var cap *groupauthority.MemberApplicationScope
			var message *sdkws.MsgData
			abort := errors.New("abort after actual request and proof")
			_, err = authority.Mutation(commandCtx, targets, func(tx context.Context) error {
				var err error
				cap, err = groupauthority.BeginMemberApplication(tx, db, "g", map[string]*sdkws.PublicUserInfo{"owner": {UserID: "owner", Nickname: "public owner"}, "applicant": {UserID: "applicant", Nickname: "public applicant"}})
				return err
			}, func(tx context.Context) error {
				if kind == groupauthority.MemberApplicationCreated {
					if err := requests.Create(tx, []*relation.GroupRequestModel{{GroupID: "g", UserID: "applicant", ReqMsg: "new request", ReqTime: time.Unix(2, 0).UTC()}}); err != nil {
						return err
					}
				} else {
					result := int32(constant.GroupResponseRefuse)
					if kind == groupauthority.MemberApplicationAccepted {
						result = constant.GroupResponseAgree
					}
					if err := requests.UpdateHandler(tx, "g", "applicant", "actual handled text", result); err != nil {
						return err
					}
					if mode == "accept-new" {
						if err := members.Create(tx, []*relation.GroupMemberModel{{GroupID: "g", UserID: "applicant", RoleLevel: constant.GroupOrdinaryUsers}}); err != nil {
							return err
						}
					}
				}
				if err := cap.Complete(tx, db); err != nil {
					return err
				}
				recipients, err := cap.RecipientsFor(tx, db, "applicant")
				if err != nil {
					return err
				}
				want := 1
				if kind == groupauthority.MemberApplicationCreated {
					want = 2
				}
				if len(recipients) != want {
					return errors.New("standard direct recipient policy changed")
				}
				message, err = cap.Message(tx, db, "applicant", "owner", map[string]bool{constant.IsNotNotification: false, constant.IsSenderSync: true})
				if err != nil {
					return err
				}
				proof, err := cap.InspectMessage(tx, db, message)
				if err != nil {
					return err
				}
				if proof.Command != command || proof.Applicant != "applicant" || proof.Recipient != "owner" || proof.MessageSHA256 == "" {
					return errors.New("actual typed source proof differs")
				}
				if mode == "wrong-actor" {
					if _, err := cap.InspectMessage(context.WithValue(tx, constant.OpUserID, "foreign"), db, message); err == nil {
						return errors.New("foreign actor borrowed original source")
					}
				}
				if mode == "tampered-message" {
					bad := proto.Clone(message).(*sdkws.MsgData)
					bad.RecvID = "foreign"
					if _, err := cap.InspectMessage(tx, db, bad); err == nil {
						return errors.New("modified recipient borrowed original body proof")
					}
					bad = proto.Clone(message).(*sdkws.MsgData)
					bad.Content = []byte("forged")
					if _, err := cap.InspectMessage(tx, db, bad); err == nil {
						return errors.New("modified body borrowed original proof")
					}
				}
				if mode == "rollback" {
					return abort
				}
				return nil
			})
			if mode == "wrong-scope" {
				if err == nil {
					t.Fatal("widened member write accepted")
				}
				return
			}
			if mode == "rollback" {
				if !errors.Is(err, abort) {
					t.Fatal("original abort lost", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if _, err := cap.InspectMessage(commandCtx, db, message); err == nil {
				t.Fatal("ended transaction renewed direct source")
			}
			var row relation.GroupRequestModel
			err = db.Collection("group_request").FindOne(ctx, bson.M{"group_id": "g", "user_id": "applicant"}).Decode(&row)
			if mode == "rollback" {
				if err == nil {
					t.Fatal("aborted request committed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if kind != groupauthority.MemberApplicationCreated && (row.HandledMsg != "actual handled text" || row.HandleUserID != "owner" || row.HandledTime.IsZero()) {
				t.Fatal("real handler metadata did not survive actual DAO decode", row)
			}
		})
	}
}
