package messagedelivery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Actual claimed Mongo fixture, new-group pair seed, scoped member deletion,
// same-TX closure and the PRODUCTION ordered notification Queue. No raw cut
// constructor supplies a removed member or substitutes for NoticeSnapshot.
func removalVisibilityFixture(t *testing.T, action groupauthority.NoticeAction, persist bool, visible ...bool) (context.Context, *Store, *mongo.Database, *groupeffects.Store, groupeffects.Frozen, *deliveryprofile.MemberRemovalVisibilityCut, []groupeffects.NotificationBranch) {
	t.Helper()
	return removalVisibilityFixtureMonitored(t, action, persist, nil, visible...)
}
func removalVisibilityFixtureMonitored(t *testing.T, action groupauthority.NoticeAction, persist bool, monitor *event.CommandMonitor, visible ...bool) (context.Context, *Store, *mongo.Database, *groupeffects.Store, groupeffects.Frozen, *deliveryprofile.MemberRemovalVisibilityCut, []groupeffects.NotificationBranch) {
	t.Helper()
	ctx, authority, base, db, groups, members := originMongoFixture(t, monitor, nil)
	if _, _, e := applyNewGroupSeed(ctx, authority, base, groups, members, "g", nil); e != nil {
		t.Fatal(e)
	}
	s, e := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.CreateCollection(ctx, groupeffects.Collection); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); e != nil {
		t.Fatal(e)
	}
	effects, e := groupeffects.NewStore(db)
	if e != nil {
		t.Fatal(e)
	}
	actor := "owner"
	kind := int32(constant.MemberKickedNotification)
	if action == groupauthority.NoticeQuit {
		actor = "member"
		kind = constant.MemberQuitNotification
	}
	ctx = context.WithValue(ctx, constant.OpUserID, actor)
	commandCtx, command, e := groupauthority.NewNoticeCommand(ctx, s.epoch, action, "g")
	if e != nil {
		t.Fatal(e)
	}
	profile := deliveryprofile.Group{Version: "owned-group-effect-v1", RiskAction: "group-message", Transport: deliveryprofile.Transport{Version: "group-effect-v1", Epoch: s.epoch, ActivationSHA256: s.activationSHA, ConfigurationSHA256: hash([]byte("synthetic exact removal configuration")), Discovery: "zookeeper", Vendor: "none"}}
	policy := groupeffects.ActionDeliveryPolicy{Owned: &profile, Epoch: s.epoch, ConfigurationSHA256: profile.Transport.ConfigurationSHA256}
	var snapshot *groupauthority.NoticeSnapshot
	var expired *groupauthority.MemberRemovalScope
	var frozen groupeffects.Frozen
	_, e = authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: "g", Members: []string{"member"}}}, func(tx context.Context) error {
		var e error
		snapshot, e = groupauthority.BeginNotice(tx, db, "g")
		return e
	}, func(tx context.Context) error {
		if e := members.Delete(tx, "g", []string{"member"}); e != nil {
			return e
		}
		if e := snapshot.Finish(tx, db); e != nil {
			return e
		}
		var e error
		expired, e = groupauthority.CompletedMemberRemoval(tx, db, "g")
		if e != nil {
			return e
		}
		if _, e = s.RecordMemberRemoval(tx, expired); e != nil {
			return e
		}
		message := &sdkws.MsgData{SendID: actor, RecvID: "g", GroupID: "g", MsgFrom: constant.SysMsgType, SessionType: constant.SuperGroupChatType, ContentType: kind, Content: []byte(`{"detail":"synthetic removal fixture"}`), CreateTime: command.CreatedAt.UnixMilli(), Options: msgprocessor.NewOptions(msgprocessor.WithHistory(persist), msgprocessor.WithSendMsg(len(visible) == 1 && visible[0]))}
		frozen, e = effects.QueueOrderedActionNotification(tx, snapshot, message, policy)
		if e != nil {
			return e
		}
		return s.RecordMemberNotificationPending(tx, expired, frozen.ID)
	})
	if e != nil {
		t.Fatal("actual removal/ordered producer transaction", e)
	}
	if _, e = expired.Inspect(commandCtx, db); !errors.Is(e, groupauthority.ErrInvalidScope) {
		t.Fatal("private removal authority escaped committed transaction", e)
	}
	var cut *deliveryprofile.MemberRemovalVisibilityCut
	if e = s.transaction(ctx, func(tx context.Context) error {
		cap, e := effects.ReadOwnedNotificationDelivery(tx, frozen.ID)
		if e != nil {
			return e
		}
		cut = cap.MemberRemovalVisibilitySnapshot()
		return nil
	}); e != nil || cut == nil || !cut.Valid() {
		t.Fatal("durable typed removal cut", e)
	}
	plans, e := groupeffects.PlanNotificationBranches(frozen)
	if e != nil {
		t.Fatal(e)
	}
	return ctx, s, db, effects, frozen, cut, plans
}
func removalNoticePlan(t *testing.T, plans []groupeffects.NotificationBranch) groupeffects.NotificationBranch {
	t.Helper()
	for _, p := range plans {
		if p.ConversationID == "n_g" {
			return p
		}
	}
	t.Fatal("real notification branch missing")
	return groupeffects.NotificationBranch{}
}
func removalDenied(t *testing.T, ctx context.Context, s *Store, cut deliveryprofile.MemberRemovalVisibilityCut, call deliveryprofile.RemovalVisibilityIdentity) (bool, error) {
	t.Helper()
	var denied map[string]bool
	e := s.transaction(ctx, func(tx context.Context) error {
		var e error
		denied, e = deliveryprofile.CompareMemberRemovalVisibilityCut(tx, s.db, s.epoch, s.activationSHA, cut, call, []string{"member"}, 0, 0, true)
		return e
	})
	return denied["member"], e
}
func removalCall(cut *deliveryprofile.MemberRemovalVisibilityCut, p groupeffects.NotificationBranch) deliveryprofile.RemovalVisibilityIdentity {
	return deliveryprofile.RemovalVisibilityIdentity{EffectID: cut.EffectID, BranchID: p.ID, CID: p.ConversationID, ProfileSHA256: cut.ProfileSHA256, Payload: append([]byte(nil), p.Payload...)}
}
func TestMemberRemovalVisibilityMongoProductionQueueKeepsClosedWindowExact(t *testing.T) {
	for _, item := range []struct {
		name    string
		action  groupauthority.NoticeAction
		persist bool
	}{{"kick-seq0", groupauthority.NoticeKick, false}, {"quit-persisted", groupauthority.NoticeQuit, true}} {
		t.Run(item.name, func(t *testing.T) {
			ctx, s, db, effects, frozen, cut, plans := removalVisibilityFixture(t, item.action, item.persist)
			plan := removalNoticePlan(t, plans)
			before := map[string]MembershipWindow{}
			for _, cid := range []string{"sg_g", "n_g"} {
				w, e := s.ReadMembershipWindow(ctx, cid, "member")
				if e != nil || !w.Closed || w.Upper != 0 || w.AllowsSequence(1, 0) {
					t.Fatal("original empty closure widened", e)
				}
				before[cid] = w
			}
			if denied, e := removalDenied(t, ctx, s, *cut, removalCall(cut, plan)); e != nil || denied {
				t.Fatal("exact original self notice rejected", e)
			}
			e := s.transaction(ctx, func(tx context.Context) error {
				cap, e := effects.ReadOwnedNotificationDelivery(tx, frozen.ID)
				if e != nil {
					return e
				}
				denied, e := effects.ValidateOwnedVisibility(tx, cap, plan.ID, []string{"member"}, 0, 0, true)
				if e != nil || denied["member"] {
					return errors.Join(errors.New("production capability did not consume exact removal cut"), e)
				}
				generic, e := deliveryprofile.CompareBranchVisibilityCut(tx, db, s.epoch, s.activationSHA, cut.Base, "n_g", []string{"member"}, 0, 0, true)
				if e != nil || !generic["member"] {
					return errors.Join(errors.New("generic v2 granted a closed owner"), e)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			// A comparison may CAS its read fence; it cannot change any authority bound.
			for cid, old := range before {
				now, e := s.ReadMembershipWindow(ctx, cid, "member")
				if e != nil {
					t.Fatal(e)
				}
				now.Revision = old.Revision
				if !reflect.DeepEqual(now, old) {
					t.Fatal("notice widened or renewed membership", cid)
				}
			}
			if n, e := db.Collection("msg").CountDocuments(ctx, bson.M{}); e != nil || n != 0 {
				t.Fatal("capture/Seq0 generated history body", n, e)
			}
			// The actual stored BSON must round trip with exact nested shape; dropping
			// a branch identity field or changing a BSON-long is not defaulted authority.
			body, e := bson.Marshal(cut)
			if e != nil {
				t.Fatal(e)
			}
			var round deliveryprofile.MemberRemovalVisibilityCut
			if e = bson.Unmarshal(body, &round); e != nil || deliveryprofile.Digest(round) != deliveryprofile.Digest(*cut) {
				t.Fatal("strict persisted cut round trip", e)
			}
			var raw bson.M
			if e = bson.Unmarshal(body, &raw); e != nil {
				t.Fatal(e)
			}
			raw["group_revision"] = int32(1)
			body, e = bson.Marshal(raw)
			if e != nil {
				t.Fatal(e)
			}
			if e = bson.Unmarshal(body, &round); e == nil {
				t.Fatal("narrow BSON number silently admitted")
			}
		})
	}
}
func TestMemberRemovalVisibilityMongoIdentityPrivacyAndIncarnationStayBound(t *testing.T) {
	for _, name := range []string{"identity-and-payload", "clear", "rejoin-window"} {
		t.Run(name, func(t *testing.T) {
			ctx, s, db, _, _, cut, plans := removalVisibilityFixture(t, groupauthority.NoticeKick, false)
			plan := removalNoticePlan(t, plans)
			call := removalCall(cut, plan)
			if name == "identity-and-payload" {
				changes := []func(*deliveryprofile.RemovalVisibilityIdentity){func(c *deliveryprofile.RemovalVisibilityIdentity) { c.EffectID = uuid.NewString() }, func(c *deliveryprofile.RemovalVisibilityIdentity) { c.BranchID = uuid.NewString() }, func(c *deliveryprofile.RemovalVisibilityIdentity) {
					c.ProfileSHA256 = hash([]byte("different profile"))
				}, func(c *deliveryprofile.RemovalVisibilityIdentity) {
					var m sdkws.MsgData
					if proto.Unmarshal(c.Payload, &m) != nil {
						t.Fatal("fixture")
					}
					m.Content = []byte("changed body")
					var e error
					c.Payload, e = proto.MarshalOptions{Deterministic: true}.Marshal(&m)
					if e != nil {
						t.Fatal(e)
					}
				}}
				for _, change := range changes {
					next := call
					next.Payload = append([]byte(nil), call.Payload...)
					change(&next)
					if _, e := removalDenied(t, ctx, s, *cut, next); e == nil {
						t.Fatal("foreign effect/branch/profile/body borrowed grant")
					}
				}
				if denied, e := removalDenied(t, ctx, s, *cut, call); e != nil || denied {
					t.Fatal("negative calls altered exact original grant", e)
				}
				return
			}
			if name == "clear" {
				if e := s.transaction(ctx, func(tx context.Context) error {
					return deliveryprofile.BumpVisibilityCut(tx, db, s.epoch, s.activationSHA, "n_g", []string{"member"}, 1, false)
				}); e != nil {
					t.Fatal(e)
				}
			} else {
				// Direct, owned fixture state change models a later distinct membership
				// command; full admission RPC/callback semantics belong to its own tests.
				nextCommand := uuid.NewString()
				for _, cid := range []string{"sg_g", "n_g"} {
					r, e := db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": key(s.epoch, "membership-window", cid+"\x00member")}, bson.M{"$set": bson.M{"closed": false, "upper": int64(0), "last_command": nextCommand}, "$inc": bson.M{"revision": int64(1), "group_revision": int64(1)}})
					if e != nil || r.ModifiedCount != 1 {
						t.Fatal("owned ABA fixture transition", e)
					}
				}
			}
			if denied, e := removalDenied(t, ctx, s, *cut, call); e != nil || !denied {
				t.Fatal("clear/new membership revived original removed-owner notice", e)
			}
		})
	}
}
func TestMemberWindowLowerMongoUsesExactActualBranchAndRejectsMissingBounds(t *testing.T) {
	for _, name := range []string{"branch-lower", "missing-lower", "int32-lower", "negative-lower"} {
		t.Run(name, func(t *testing.T) {
			ctx, s, db := ordinaryEphemeralMongoFixture(t)
			var cut deliveryprofile.VisibilityCut
			if e := s.transaction(ctx, func(tx context.Context) error {
				var e error
				cut, e = deliveryprofile.CaptureVisibilityCut(tx, db, s.epoch, s.activationSHA, "sg_g", []string{"member"})
				return e
			}); e != nil {
				t.Fatal(e)
			}
			update := bson.M{"$set": bson.M{"lower": int64(2)}}
			switch name {
			case "missing-lower":
				update = bson.M{"$unset": bson.M{"lower": ""}}
			case "int32-lower":
				update = bson.M{"$set": bson.M{"lower": int32(0)}}
			case "negative-lower":
				update = bson.M{"$set": bson.M{"lower": int64(-1)}}
			}
			if r, e := db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": key(s.epoch, "membership-window", "n_g\x00member")}, update); e != nil || r.ModifiedCount != 1 {
				t.Fatal("owned lower fixture", e)
			}
			var denied map[string]bool
			e := s.transaction(ctx, func(tx context.Context) error {
				var e error
				denied, e = deliveryprofile.CompareBranchVisibilityCut(tx, db, s.epoch, s.activationSHA, cut, "n_g", []string{"member"}, 1, 0, true)
				return e
			})
			if name != "branch-lower" {
				if e == nil {
					t.Fatal("missing/malformed lower became zero")
				}
				return
			}
			if e != nil || !denied["member"] {
				t.Fatal("actual n_ lower ignored", e)
			}
			if e = s.transaction(ctx, func(tx context.Context) error {
				var e error
				denied, e = deliveryprofile.CompareBranchVisibilityCut(tx, db, s.epoch, s.activationSHA, cut, "sg_g", []string{"member"}, 1, 1, true)
				return e
			}); e != nil || denied["member"] {
				t.Fatal("n_ lower incorrectly widened or removed independent sg_ authority", e)
			}
		})
	}
}
