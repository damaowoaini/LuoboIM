package messagedelivery

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

type jointApplication struct {
	ctx       context.Context
	store     *Store
	db        *mongo.Database
	effects   *groupeffects.Store
	message   *sdkws.MsgData
	operation string
}
type jointApplicationGateway struct {
	store   *Store
	policy  string
	targets []string
}

func (a *jointApplicationGateway) PolicyFingerprint() string { return a.policy }
func (a *jointApplicationGateway) Deliver(ctx context.Context, call OnlineCall) (OnlineReport, error) {
	if err := a.store.ValidateKafkaOnlineCall(ctx, call); err != nil {
		return OnlineReport{}, err
	}
	if call.Message.Seq == 0 {
		if err := a.store.AcquireKafkaEphemeralPhysical(ctx, call); err != nil {
			return OnlineReport{}, err
		}
	}
	a.targets = append(a.targets, call.Recipients...)
	return ordinaryEphemeralReport(call), nil
}

func jointApplicationFixture(t *testing.T, persist bool) *jointApplication {
	t.Helper()
	ctx, authority, s, db, _ := kafkaFixture(t, nil)
	effects := jointEffects(t, ctx, db)
	oldManager, oldAdmin := config.Config.Manager.UserID, config.Config.IMAdmin.UserID
	config.Config.Manager.UserID, config.Config.IMAdmin.UserID = []string{"application-service"}, nil
	t.Cleanup(func() { config.Config.Manager.UserID, config.Config.IMAdmin.UserID = oldManager, oldAdmin })
	requests, err := mgo.NewGroupRequestMgoContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	commandCtx, command, err := groupauthority.NewMemberApplicationCommand(ctx, s.epoch, "g", []string{"applicant"}, groupauthority.MemberApplicationCreated)
	if err != nil {
		t.Fatal(err)
	}
	var cap *groupauthority.MemberApplicationScope
	var message *sdkws.MsgData
	policy := config.NotificationConf{IsSendMsg: true, ReliabilityLevel: constant.UnreliableNotification}
	if persist {
		policy.ReliabilityLevel = constant.ReliableNotificationNoMsg
	}
	_, err = authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: "g", Members: []string{"applicant"}}}, func(tx context.Context) error {
		var err error
		cap, err = groupauthority.BeginMemberApplication(tx, db, "g", map[string]*sdkws.PublicUserInfo{"owner": {UserID: "owner"}, "applicant": {UserID: "applicant"}})
		return err
	}, func(tx context.Context) error {
		if err := requests.Create(tx, []*relation.GroupRequestModel{{GroupID: "g", UserID: "applicant", ReqMsg: "owned synthetic request", ReqTime: time.Unix(3, 0).UTC()}}); err != nil {
			return err
		}
		if err := cap.Complete(tx, db); err != nil {
			return err
		}
		var err error
		message, err = cap.Message(tx, db, "applicant", "applicant", config.GetOptionsByNotification(policy))
		if err != nil {
			return err
		}
		if err := s.RecordMemberApplicationSource(tx, cap, message); err != nil {
			return err
		}
		return effects.QueueMemberApplicationDirect(tx, cap, message, "application-service", s.activationSHA, command.ID)
	})
	if err != nil {
		t.Fatal("actual typed request source transaction", err)
	}
	return &jointApplication{ctx: ctx, store: s, db: db, effects: effects, message: message, operation: command.ID}
}

func (f *jointApplication) send(t *testing.T, unknown bool) KafkaRecord {
	t.Helper()
	calls := 0
	var accepted *sdkws.MsgData
	err := f.effects.DeliverMemberApplicationDirect(f.ctx, f.message.ClientMsgID, func(ctx context.Context, request *pbmsg.SendMsgReq) (*pbmsg.SendMsgResp, error) {
		calls++
		accepted = proto.Clone(request.MsgData).(*sdkws.MsgData)
		if unknown {
			return nil, errors.New("physical ACK deliberately unavailable")
		}
		return &pbmsg.SendMsgResp{ClientMsgID: accepted.ClientMsgID, ServerMsgID: "server-original", SendTime: 4000}, nil
	})
	if calls != 1 || unknown && !errors.Is(err, groupeffects.ErrMemberApplicationDispatchUnconfirmed) || !unknown && err != nil {
		t.Fatal("single actual RPC attempt differs", calls, err)
	}
	accepted.ServerMsgID, accepted.SendTime = "server-original", 4000
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(accepted)
	if err != nil {
		t.Fatal(err)
	}
	return KafkaRecord{Topic: "synthetic-owned-source", Partition: 0, Offset: 100, Key: []byte(msgprocessor.GetChatConversationIDByMsg(accepted)), Body: body, Actor: "application-service", OperationID: f.operation, ContextSHA256: hash([]byte("actual internal service headers"))}
}

func (f *jointApplication) accept(t *testing.T, r KafkaRecord) (Receipt, error) {
	t.Helper()
	policy, sha := kafkaPolicyFixture(t, f.store)
	return f.store.AcceptKafkaRecord(context.WithValue(f.ctx, constant.OpUserID, r.Actor), r, sha, policy)
}

func (f *jointApplication) clearRecipient(t *testing.T) {
	t.Helper()
	driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(f.db, f.store)
	if err != nil {
		t.Fatal(err)
	}
	if err = driver.AdvanceMessageVisibility(f.ctx, []table.MessageVisibilityFloor{{ConversationID: msgprocessor.GetChatConversationIDByMsg(f.message), UserID: "applicant", MinSeq: 1}}); err != nil {
		t.Fatal(err)
	}
}

func TestMemberApplicationIntakeJointBindsInterleavedPeerToSameReceipt(t *testing.T) {
	for _, persist := range []bool{false, true} {
		name := "Seq0-tip"
		if persist {
			name = "positive-tip"
		}
		t.Run(name, func(t *testing.T) {
			f := jointApplicationFixture(t, persist)
			record := f.send(t, false)
			ordinary := proto.Clone(f.message).(*sdkws.MsgData)
			ordinary.ClientMsgID, ordinary.ServerMsgID = uuid.NewString(), uuid.NewString()
			ordinary.SendTime = 3000
			ordinary.ContentType = constant.Text
			ordinary.MsgFrom = constant.UserMsgType
			ordinary.Content = []byte("interleaved ordinary body")
			ordinary.Options = msgprocessor.NewOptions(msgprocessor.WithNotNotification(true), msgprocessor.WithHistory(true))
			body, err := proto.MarshalOptions{Deterministic: true}.Marshal(ordinary)
			if err != nil {
				t.Fatal(err)
			}
			interleaved := record
			interleaved.Offset = 99
			interleaved.Actor = "owner"
			interleaved.Body = body
			interleaved.OperationID = ""
			first, err := f.accept(t, interleaved)
			if err != nil || len(first.Branches) != 1 || first.Branches[0].Seq != 1 {
				t.Fatal("actual interleaved allocation failed", err)
			}
			accepted, err := f.accept(t, record)
			if err != nil || len(accepted.Branches) != 2 {
				t.Fatal("original application source was not accepted", err)
			}
			for _, branch := range accepted.Branches {
				d, err := f.store.readDutyRecord(f.ctx, branch.ID)
				if err != nil || d.ApplicationSource == nil || d.OrdinaryCut.Normal.SourceHead != 0 {
					t.Fatal("original source cut replaced by current capture", err)
				}
				if branch.ConversationID == msgprocessor.GetNotificationConversationIDByMsg(f.message) {
					if d.StoredPeerSeq != 2 || d.StoredPeerSeq == d.OrdinaryCut.Normal.SourceHead+1 {
						t.Fatal("peer was guessed instead of bound to actual receipt")
					}
					if !persist && !ordinaryEphemeralDuty(d) {
						t.Fatal("valid exact-receipt Seq0 peer was rejected")
					}
				}
				if err = f.store.requireApplicationVisibility(f.ctx, d, []string{"applicant"}); err != nil {
					t.Fatal("valid original recipient rejected", err)
				}
				bad := d
				bad.StoredPeerSeq++
				if _, err = f.store.acceptedApplicationBranch(f.ctx, bad); err == nil {
					t.Fatal("another positive peer borrowed same receipt")
				}
			}
			resolved, err := f.store.ResolveKafkaRecord(context.WithValue(f.ctx, constant.OpUserID, record.Actor), record)
			if err != nil || !reflect.DeepEqual(resolved, accepted) {
				t.Fatal("read-only source resolve changed result", err)
			}
			status, err := f.effects.InspectMemberApplicationDirect(f.ctx, f.message.ClientMsgID)
			if err != nil || status.KafkaSourceID == "" || !status.PayloadRedacted || status.ServerMsgID != "server-original" {
				t.Fatal("original source ACK/custody did not survive actual receipt", status, err)
			}
		})
	}
}

func TestMemberApplicationIntakeJointClearBeforeKafkaCannotRenewEitherBranch(t *testing.T) {
	for _, persist := range []bool{false, true} {
		name := "Seq0-tip"
		if persist {
			name = "positive-tip"
		}
		t.Run(name, func(t *testing.T) {
			f := jointApplicationFixture(t, persist)
			record := f.send(t, false)
			f.clearRecipient(t)
			accepted, err := f.accept(t, record)
			if err != nil {
				t.Fatal(err)
			}
			for _, branch := range accepted.Branches {
				d, err := f.store.readDutyRecord(f.ctx, branch.ID)
				if err != nil {
					t.Fatal(err)
				}
				if err = f.store.requireApplicationVisibility(f.ctx, d, []string{"applicant"}); !errors.Is(err, ErrConflict) {
					t.Fatal("Clear-before-Kafka source was granted a fresh cut", err)
				}
				gateway := &jointApplicationGateway{store: f.store, policy: d.PolicySHA}
				if branch.Persist {
					var message sdkws.MsgData
					if proto.Unmarshal(d.Payload, &message) != nil {
						t.Fatal("actual duty body")
					}
					err = f.store.RecoverKafkaOnline(f.ctx, branch.ID, &staleBodyReader{message: &message}, discardProjection{}, gateway)
				} else {
					err = f.store.RecoverKafkaEphemeralOnline(f.ctx, branch.ID, gateway)
				}
				if err != nil {
					t.Fatal("actual recovery failed to suppress only revoked target", err)
				}
				for _, uid := range gateway.targets {
					if uid == "applicant" {
						t.Fatal("revoked original recipient reached physical gateway")
					}
				}
			}
		})
	}
}

func TestMemberApplicationIntakeJointRejectsDetachedSourceAndSecondCoordinate(t *testing.T) {
	for _, mode := range []string{"missing-source", "changed-body", "changed-operation", "second-coordinate"} {
		t.Run(mode, func(t *testing.T) {
			f := jointApplicationFixture(t, false)
			record := f.send(t, false)
			if mode == "second-coordinate" {
				if _, err := f.accept(t, record); err != nil {
					t.Fatal(err)
				}
				record.Offset++
			} else if mode == "changed-operation" {
				record.OperationID = uuid.NewString()
			} else {
				var message sdkws.MsgData
				if proto.Unmarshal(record.Body, &message) != nil {
					t.Fatal("fixture body")
				}
				if mode == "missing-source" {
					message.ClientMsgID = uuid.NewString()
				} else {
					message.Content = []byte("changed")
				}
				var err error
				record.Body, err = proto.MarshalOptions{Deterministic: true}.Marshal(&message)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.accept(t, record); err == nil {
				t.Fatal("detached source/coordinate acquired application custody")
			}
		})
	}
}

func TestMemberApplicationJointUnknownAttemptIsInspectableAndNeverAutoResent(t *testing.T) {
	f := jointApplicationFixture(t, false)
	f.send(t, true)
	calls := 0
	err := f.effects.DeliverMemberApplicationDirect(f.ctx, f.message.ClientMsgID, func(context.Context, *pbmsg.SendMsgReq) (*pbmsg.SendMsgResp, error) { calls++; return nil, nil })
	if !errors.Is(err, groupeffects.ErrMemberApplicationDispatchUnconfirmed) || calls != 0 {
		t.Fatal("unknown original intent was resent", err)
	}
	f.clearRecipient(t)
	status, err := f.effects.InspectMemberApplicationDirect(f.ctx, f.message.ClientMsgID)
	if err != nil || status.State != "unknown" || status.CommandID != f.operation || status.ClientMsgID != f.message.ClientMsgID || status.ClaimID == "" || !status.PayloadRedacted {
		t.Fatal("privacy revoke lost minimal original unknown custody", status, err)
	}
}
