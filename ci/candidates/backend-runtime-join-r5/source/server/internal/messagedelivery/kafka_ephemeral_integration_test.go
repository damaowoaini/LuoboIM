package messagedelivery

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Reuse the claimed ephemeral replica-set fixture and ACTUAL authorized group
// creation. The new normal/n_ counters and member commands are produced in
// that real transaction, never fabricated by the Seq0 cut implementation.
func ordinaryEphemeralMongoFixture(t *testing.T) (context.Context, *Store, *mongo.Database) {
	t.Helper()
	ctx, authority, base, db, groups, members := originMongoFixture(t, nil, nil)
	info, receipt, err := applyNewGroupSeed(ctx, authority, base, groups, members, "g", nil)
	if err != nil {
		t.Fatal("actual pair/member seed", err)
	}
	prepareCreatedMembershipFixture(t, ctx, base, db, info, receipt)
	// Clear also redacts typed application sources in this same database. Its
	// production schema gate is required even when this fixture has no sources.
	if err = db.CreateCollection(ctx, groupeffects.Collection); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); err != nil {
		t.Fatal(err)
	}
	effects, err := groupeffects.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = effects.CheckReady(ctx); err != nil {
		t.Fatal("actual owned group-effects catalog", err)
	}
	s, err := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	return context.WithValue(ctx, constant.OpUserID, "owner"), s, db
}
func ordinaryEphemeralRecord(t *testing.T, ctx context.Context, s *Store, offset int64, offline bool) KafkaRecord {
	t.Helper()
	record := kafkaFixtureRecord(t, offset)
	var m sdkws.MsgData
	if proto.Unmarshal(record.Body, &m) != nil {
		t.Fatal("typed message fixture")
	}
	m.Options = msgprocessor.NewOptions(msgprocessor.WithNotNotification(true), msgprocessor.WithHistory(false), msgprocessor.WithOfflinePush(offline))
	permit, err := s.ReserveMessageNamespaces(ctx, &m)
	if err != nil || permit == nil || permit.Check(ctx, &m) != nil {
		t.Fatal("real pre-enqueue source/namespace gate", err)
	}
	record.Body, err = proto.MarshalOptions{Deterministic: true}.Marshal(&m)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
func ordinaryEphemeralHeads(t *testing.T, ctx context.Context, s *Store) map[string]int64 {
	t.Helper()
	heads := map[string]int64{}
	for _, cid := range []string{"sg_g", "n_g"} {
		var c counter
		if e := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", cid)}).Decode(&c); e != nil {
			t.Fatal("pair head", e)
		}
		heads[cid] = c.Maximum
	}
	return heads
}

type ordinaryEphemeralGatewayFixture struct {
	store    *Store
	policy   string
	unknown  bool
	before   func() error
	physical int
	last     OnlineCall
}

func (a *ordinaryEphemeralGatewayFixture) PolicyFingerprint() string { return a.policy }
func (a *ordinaryEphemeralGatewayFixture) Deliver(ctx context.Context, call OnlineCall) (OnlineReport, error) {
	a.last = call
	if a.before != nil {
		if e := a.before(); e != nil {
			return OnlineReport{}, e
		}
	}
	if e := a.store.AcquireKafkaEphemeralPhysical(ctx, call); e != nil {
		return OnlineReport{}, e
	}
	a.physical++
	if a.unknown {
		return OnlineReport{DispatchStarted: true}, ErrOnlineUnconfirmed
	}
	return ordinaryEphemeralReport(call), nil
}
func ordinaryEphemeralReport(call OnlineCall) OnlineReport {
	report := OnlineReport{DispatchStarted: true}
	for _, u := range call.Recipients {
		report.ACKs = append(report.ACKs, OnlineACK{UserID: u, ResponseSHA256: hash([]byte("synthetic definitive ACK " + u))})
		report.Routes = append(report.Routes, OnlineRouteResult{UserID: u, ResponseSHA256: hash([]byte("synthetic definitive route " + u)), OnlineAccepted: u == "owner"})
	}
	return report
}

type ordinaryEphemeralNoVendorFixture struct {
	store  *Store
	policy string
	calls  int
}

func (a *ordinaryEphemeralNoVendorFixture) PolicyFingerprint() string { return a.policy }
func (a *ordinaryEphemeralNoVendorFixture) CheckReady(ctx context.Context) error {
	return a.store.CheckKafkaReady(ctx)
}
func (a *ordinaryEphemeralNoVendorFixture) DeliverOffline(ctx context.Context, call KafkaOfflineCall) error {
	for _, u := range call.DispatchRecipients {
		a.calls++
		if e := a.store.RecordKafkaNoVendor(ctx, call, u); e != nil {
			return e
		}
	}
	return nil
}
func TestOrdinaryEphemeralMongoSeparatesHistoryAndSettlesOriginalUnknown(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(map[bool]string{false: "known-online-offline", true: "unknown-no-replay"}[unknown], func(t *testing.T) {
			ctx, s, db := ordinaryEphemeralMongoFixture(t)
			record := ordinaryEphemeralRecord(t, ctx, s, 710, !unknown)
			before := ordinaryEphemeralHeads(t, ctx, s)
			p, sha := kafkaPolicyFixture(t, s)
			receipt, e := s.AcceptKafkaRecord(ctx, record, sha, p)
			if e != nil || len(receipt.Branches) != 1 {
				t.Fatal("same-TX source", e)
			}
			b := receipt.Branches[0]
			if b.Persist || b.Seq != 0 || b.ConversationID != "sg_g" {
				t.Fatal("Seq0 became history")
			}
			online, e := s.ListKafkaEphemeralOnlinePending(ctx, "", 32)
			if e != nil || len(online) != 1 {
				t.Fatal("distinct Seq0 query", e)
			}
			if positive, e := s.ListKafkaOnlinePending(ctx, "", 32); e != nil || len(positive) != 0 {
				t.Fatal("positive query admitted Seq0", e)
			}
			gateway := &ordinaryEphemeralGatewayFixture{store: s, policy: sha, unknown: unknown}
			if e = s.RecoverKafkaOnline(ctx, b.ID, &staleBodyReader{}, discardProjection{}, gateway); !errors.Is(e, ErrUnprepared) {
				t.Fatal("positive recover accepted Seq0", e)
			}
			e = s.RecoverKafkaEphemeralOnline(ctx, b.ID, gateway)
			if unknown {
				if !errors.Is(e, ErrOnlineUnconfirmed) {
					t.Fatal("unknown physical ACK declared applied", e)
				}
				if e = s.RecoverKafkaEphemeralOnline(ctx, b.ID, gateway); !errors.Is(e, ErrOnlineUnconfirmed) || gateway.physical != 1 {
					t.Fatal("old unknown page replayed", e)
				}
				expected, e := s.InspectKafkaEphemeral(ctx, b.ID)
				if e != nil || expected.State != "unconfirmed" || expected.InstanceID != s.instanceID {
					t.Fatal("original claim not inspectable", e)
				}
				// Synthetic operator evidence settles only this exact original claim; it
				// is not a claim that a successful local Stop proves remote application.
				drain := KafkaAttemptDrainProof{InstanceID: expected.InstanceID, EvidenceSHA256: hash([]byte("synthetic exact-instance drain proof"))}
				if e = s.ConfirmKafkaEphemeralApplied(ctx, expected, ordinaryEphemeralReport(gateway.last), hash([]byte("synthetic definitive original outcomes")), drain); e != nil {
					t.Fatal("exact operator settlement", e)
				}
				if e = s.RecoverKafkaEphemeralOnline(ctx, b.ID, gateway); e != nil || gateway.physical != 1 {
					t.Fatal("settled original claim dispatched again", e)
				}
			} else {
				if e != nil || gateway.physical != 1 {
					t.Fatal("known page not settled", e)
				}
				adapter := &ordinaryEphemeralNoVendorFixture{store: s, policy: sha}
				if e = s.RecoverKafkaEphemeralOffline(ctx, b.ID, adapter); e != nil || adapter.calls != 1 {
					t.Fatal("Seq0 original offline routing not honored", e)
				}
			}
			if !reflect.DeepEqual(before, ordinaryEphemeralHeads(t, ctx, s)) {
				t.Fatal("Seq0 allocated/advanced history")
			}
			if n, e := db.Collection("msg").CountDocuments(ctx, bson.M{}); e != nil || n != 0 {
				t.Fatal("Seq0 wrote history body", n, e)
			}
			var duty deliveryDuty
			if e = db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": b.ID}).Decode(&duty); e != nil || duty.State != "done" || duty.OnlinePending || duty.OfflinePending || len(duty.Payload) != 0 {
				t.Fatal("original duty not durably cleaned", e)
			}
			if replay, e := s.ResolveKafkaRecord(ctx, record); e != nil || !reflect.DeepEqual(replay, receipt) {
				t.Fatal("completion lost source idempotency", e)
			}
		})
	}
}
func TestOrdinaryEphemeralMongoOwnerClearBeforePhysicalDispatchErasesDuty(t *testing.T) {
	ctx, s, db := ordinaryEphemeralMongoFixture(t)
	record := ordinaryEphemeralRecord(t, ctx, s, 711, false)
	p, sha := kafkaPolicyFixture(t, s)
	receipt, e := s.AcceptKafkaRecord(ctx, record, sha, p)
	if e != nil {
		t.Fatal(e)
	}
	branch := receipt.Branches[0].ID
	cleared := false
	gateway := &ordinaryEphemeralGatewayFixture{store: s, policy: sha, before: func() error {
		err := s.MutateMessageVisibility(ctx, db, []table.PendingVisibilityChange{{ConversationID: "sg_g", Below: 1}}, func(context.Context) error { return nil })
		cleared = err == nil
		return err
	}}
	if e = s.RecoverKafkaEphemeralOnline(ctx, branch, gateway); e == nil || gateway.physical != 0 {
		t.Fatal("clear lost final source cut", e)
	}
	if !cleared {
		t.Fatal("authenticated Clear did not commit before physical admission")
	}
	var duty deliveryDuty
	if e = db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": branch}).Decode(&duty); e != nil || !duty.Hidden || duty.PushPending || len(duty.Payload) != 0 || len(duty.Recipients) != 0 {
		t.Fatal("clear retained or revived pending plaintext", e)
	}
}

type ordinaryEphemeralPartialGatewayFixture struct {
	ordinaryEphemeralGatewayFixture
}

func (a *ordinaryEphemeralPartialGatewayFixture) Deliver(ctx context.Context, call OnlineCall) (OnlineReport, error) {
	a.last = call
	if err := a.store.AcquireKafkaEphemeralPhysical(ctx, call); err != nil {
		return OnlineReport{}, err
	}
	a.physical++
	report := ordinaryEphemeralReport(call)
	if len(report.ACKs) < 2 || len(report.Routes) != len(report.ACKs) {
		return OnlineReport{}, ErrInvalid
	}
	report.ACKs = report.ACKs[:1]
	report.Routes = report.Routes[:1]
	return report, ErrOnlineUnconfirmed
}

func TestOrdinaryEphemeralMongoPartialUnknownSettlementRetainsOriginalRoutes(t *testing.T) {
	ctx, s, db := ordinaryEphemeralMongoFixture(t)
	record := ordinaryEphemeralRecord(t, ctx, s, 712, true)
	p, sha := kafkaPolicyFixture(t, s)
	receipt, err := s.AcceptKafkaRecord(ctx, record, sha, p)
	if err != nil || len(receipt.Branches) != 1 {
		t.Fatal("same-TX original source", err)
	}
	branch := receipt.Branches[0].ID
	gateway := &ordinaryEphemeralPartialGatewayFixture{ordinaryEphemeralGatewayFixture{store: s, policy: sha}}
	if err = s.RecoverKafkaEphemeralOnline(ctx, branch, gateway); !errors.Is(err, ErrOnlineUnconfirmed) || gateway.physical != 1 {
		t.Fatal("partial original attempt lost sticky unknown", err)
	}
	var first deliveryDuty
	var raw bson.Raw
	if err = db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": branch}).Decode(&raw); err != nil || bson.Unmarshal(raw, &first) != nil || len(first.OfflineRouting) != 1 {
		t.Fatal("first confirmed route was not retained", err)
	}
	if _, ok := raw.Lookup("offline_routing").DocumentOK(); !ok {
		t.Fatal("route map remained BSON null")
	}
	if err = s.RecoverKafkaEphemeralOnline(ctx, branch, gateway); !errors.Is(err, ErrOnlineUnconfirmed) || gateway.physical != 1 {
		t.Fatal("partial unknown was automatically replayed", err)
	}
	expected, err := s.InspectKafkaEphemeral(ctx, branch)
	if err != nil || expected.State != "unconfirmed" {
		t.Fatal("original partial claim unavailable", err)
	}
	report := OnlineReport{DispatchStarted: true}
	for _, uid := range gateway.last.Recipients {
		if validSHA(first.OnlineACKs[hash([]byte(uid))]) {
			continue
		}
		report.ACKs = append(report.ACKs, OnlineACK{UserID: uid, ResponseSHA256: hash([]byte("definitive remaining original ACK " + uid))})
		report.Routes = append(report.Routes, OnlineRouteResult{UserID: uid, ResponseSHA256: hash([]byte("definitive remaining original route " + uid)), OnlineAccepted: uid == "owner"})
	}
	drain := KafkaAttemptDrainProof{InstanceID: expected.InstanceID, EvidenceSHA256: hash([]byte("synthetic exact original partial worker joined"))}
	if err = s.ConfirmKafkaEphemeralApplied(ctx, expected, report, hash([]byte("synthetic definitive remaining original outcomes")), drain); err != nil {
		t.Fatal("remaining original claim failed to settle", err)
	}
	var settled deliveryDuty
	if err = db.Collection(Effects).FindOne(ctx, bson.M{"branch_id": branch}).Decode(&settled); err != nil || settled.OnlinePending || !settled.OfflinePending || len(settled.OfflineRouting) != len(gateway.last.Recipients) {
		t.Fatal("settlement lost original offline obligations", err)
	}
	for owner, route := range first.OfflineRouting {
		if settled.OfflineRouting[owner] != route {
			t.Fatal("later original confirmation erased an already confirmed route")
		}
	}
	if err = s.ConfirmKafkaEphemeralApplied(ctx, expected, report, hash([]byte("reused stale proof")), drain); !errors.Is(err, ErrConflict) || gateway.physical != 1 {
		t.Fatal("old claim could be settled again or physically replayed", err)
	}
	adapter := &ordinaryEphemeralNoVendorFixture{store: s, policy: sha}
	if err = s.RecoverKafkaEphemeralOffline(ctx, branch, adapter); err != nil || adapter.calls != 1 {
		t.Fatal("partial-confirmed routes did not retain exact offline work", err)
	}
}
