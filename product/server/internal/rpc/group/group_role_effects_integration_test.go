package group

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
)

func roleEffectsFixture(t *testing.T, monitor *event.CommandMonitor, configure func()) (context.Context, *groupServer, *mongo.Database, *groupeffects.Store) {
	t.Helper()
	memberEditSetup(t)
	reflect.ValueOf(&config.Config.Callback).Elem().SetZero()
	if configure != nil {
		configure()
	}
	ctx, authority, db := roleRPCMongo(t, monitor)
	if err := db.CreateCollection(ctx, groupeffects.Collection); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); err != nil {
		t.Fatal(err)
	}
	for _, gid := range []string{"g", "h"} {
		if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": gid, "status": int32(constant.GroupOk), "group_type": int32(constant.WorkingGroup), groupauthority.RevisionField: int64(1)}); err != nil {
			t.Fatal(err)
		}
		for uid, role := range map[string]int32{"owner": constant.GroupOwner, "admin": constant.GroupAdmin, "member": constant.GroupOrdinaryUsers} {
			if _, err := db.Collection("group_member").InsertOne(ctx, &relation.GroupMemberModel{GroupID: gid, UserID: uid, RoleLevel: role, Nickname: uid, FaceURL: "synthetic-avatar", JoinTime: time.Unix(1, 0)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	service, _ := realAuthorityRPC(t, ctx, authority, db)
	runtime, err := newGroupNoticeRuntime(db, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	service.notices = runtime
	store := runtime.effects.(*groupeffects.Store)
	if err = store.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
	return ctx, service, db, store
}
func roleRow(t *testing.T, ctx context.Context, db *mongo.Database, gid, uid string) relation.GroupMemberModel {
	t.Helper()
	var row relation.GroupMemberModel
	if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": gid, "user_id": uid}).Decode(&row); err != nil {
		t.Fatal(err)
	}
	return row
}

type storedRoleEffect struct {
	groupeffects.Receipt `bson:",inline"`
	Order                groupeffects.RoleEffectOrder       `bson:"role_order"`
	Targets              []groupeffects.NotificationTargets `bson:"delivery_targets"`
	Producer             string                             `bson:"producer_version"`
}

func roleEffects(t *testing.T, ctx context.Context, db *mongo.Database) []storedRoleEffect {
	t.Helper()
	cur, err := db.Collection(groupeffects.Collection).Find(ctx, bson.M{"role_order": bson.M{"$exists": true}})
	if err != nil {
		t.Fatal(err)
	}
	var rows []storedRoleEffect
	if err = cur.All(ctx, &rows); err != nil {
		t.Fatal(err)
	}
	if err = cur.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return rows
}
func TestRoleEffectsSelfProfileAndRoleAssignmentContract(t *testing.T) {
	ctx, s, db, _ := roleEffectsFixture(t, nil, nil)
	for _, role := range []*int32{nil, func() *int32 { v := int32(20); return &v }()} {
		name := "kept-profile"
		if _, err := s.SetGroupMemberInfo(statusActor(ctx, "member"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", role, &name, nil)}}); err != nil {
			t.Fatal("ordinary self-profile rejected", err)
		}
	}
	before := len(roleEffects(t, ctx, db))
	role := int32(60)
	if _, err := s.SetGroupMemberInfo(statusActor(ctx, "member"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", &role, nil, nil)}}); !errs.ErrNoPermission.Is(err) {
		t.Fatal("ordinary role escalation admitted", err)
	}
	if roleRow(t, ctx, db, "g", "member").RoleLevel != 20 || len(roleEffects(t, ctx, db)) != before {
		t.Fatal("denied role changed business/effects")
	}
}
func TestRoleEffectsCrossGroupBatchIsAtomicAndReadScopeDoesNotWidenWrites(t *testing.T) {
	ctx, s, db, _ := roleEffectsFixture(t, nil, nil)
	base := s.notices.effects
	s.notices.effects = &roleEffectsProbe{groupNoticeQueue: base, roleEffectQueue: base.(roleEffectQueue), after: func(tx context.Context, snapshot *groupauthority.RoleNoticeSnapshot) error {
		if err := groupauthority.RequireMembers(tx, db, snapshot.Group().GroupID, []string{"owner"}); !errors.Is(err, groupauthority.ErrInvalidScope) {
			t.Fatal("full roster widened write scope", err)
		}
		return nil
	}}
	name := "batch-name"
	req := &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", nil, &name, nil), memberEdit("h", "member", nil, &name, nil)}}
	if _, err := s.SetGroupMemberInfo(statusActor(ctx, "member"), req); err != nil {
		t.Fatal(err)
	}
	rows := roleEffects(t, ctx, db)
	if len(rows) != 2 || rows[0].Order.CommandID != rows[1].Order.CommandID {
		t.Fatal("batch split its command")
	}
	for _, gid := range []string{"g", "h"} {
		if roleRow(t, ctx, db, gid, "member").Nickname != name {
			t.Fatal("cross-group batch incomplete")
		}
	}
	// A legitimate first edit must roll back when a later group's permission fails.
	bad := int32(60)
	other := "must-not-apply"
	if _, err := s.SetGroupMemberInfo(statusActor(ctx, "member"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", nil, &other, nil), memberEdit("h", "member", &bad, nil, nil)}}); !errs.ErrNoPermission.Is(err) {
		t.Fatal(err)
	}
	if roleRow(t, ctx, db, "g", "member").Nickname != name || len(roleEffects(t, ctx, db)) != 2 {
		t.Fatal("denied mixed batch partially applied")
	}
}

type roleEffectsProbe struct {
	groupNoticeQueue
	roleEffectQueue
	after func(context.Context, *groupauthority.RoleNoticeSnapshot) error
}

func (p *roleEffectsProbe) QueueRoleNotification(ctx context.Context, snapshot *groupauthority.RoleNoticeSnapshot, target string, f groupeffects.Frozen, policy groupeffects.ActionDeliveryPolicy, order groupeffects.RoleEffectOrder) error {
	if err := p.roleEffectQueue.QueueRoleNotification(ctx, snapshot, target, f, policy, order); err != nil {
		return err
	}
	if p.after != nil {
		return p.after(ctx, snapshot)
	}
	return nil
}
func TestRoleEffectsQueueFailureRollsBackBothGroupsAndCacheJobs(t *testing.T) {
	ctx, s, db, _ := roleEffectsFixture(t, nil, nil)
	base := s.notices.effects
	sentinel := errors.New("synthetic effect write failure")
	s.notices.effects = &roleEffectsProbe{groupNoticeQueue: base, roleEffectQueue: base.(roleEffectQueue), after: func(context.Context, *groupauthority.RoleNoticeSnapshot) error { return sentinel }}
	name := "not-committed"
	_, err := s.SetGroupMemberInfo(statusActor(ctx, "member"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", nil, &name, nil), memberEdit("h", "member", nil, &name, nil)}})
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	for _, gid := range []string{"g", "h"} {
		if roleRow(t, ctx, db, gid, "member").Nickname != "member" {
			t.Fatal("aborted batch leaked")
		}
	}
	n, err := db.Collection(groupauthority.InvalidationsCollection).CountDocuments(ctx, bson.M{})
	if err != nil || n != 0 || len(roleEffects(t, ctx, db)) != 0 {
		t.Fatal("aborted obligations leaked", err)
	}
}
func TestRoleEffectsKnownCommitCacheFailureReturnsACKAndRetainsAfter(t *testing.T) {
	ctx, s, db, _ := roleEffectsFixture(t, nil, func() {
		config.Config.Callback.CallbackUrl = "https://synthetic.invalid/callback"
		config.Config.Callback.CallbackAfterSetGroupMemberInfo = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
	})
	s.invalidate = func(context.Context, string) error { return errors.New("synthetic cache offline") }
	role := int32(60)
	if _, err := s.SetGroupMemberInfo(statusActor(ctx, "owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", &role, nil, nil)}}); err != nil {
		t.Fatal("known committed write lost ordinary ACK", err)
	}
	if roleRow(t, ctx, db, "g", "member").RoleLevel != 60 || len(roleEffects(t, ctx, db)) != 2 {
		t.Fatal("known commit dropped typed obligations")
	}
	n, err := db.Collection(groupauthority.InvalidationsCollection).CountDocuments(ctx, bson.M{})
	if err != nil || n != 1 {
		t.Fatal("cache recovery duty missing", err)
	}
}
func TestRoleEffectsTransferPreservesOldOwnerSuccessorRoleAndOneOwner(t *testing.T) {
	ctx, s, db, _ := roleEffectsFixture(t, nil, func() { config.Config.Manager.UserID = append(config.Config.Manager.UserID, "owner") })
	req := &pbgroup.TransferGroupOwnerReq{GroupID: "g", OldOwnerUserID: "owner", NewOwnerUserID: "admin"}
	if _, err := s.TransferGroupOwner(statusActor(ctx, "owner"), req); err != nil {
		t.Fatal(err)
	}
	if roleRow(t, ctx, db, "g", "owner").RoleLevel != 60 || roleRow(t, ctx, db, "g", "admin").RoleLevel != 100 || len(roleEffects(t, ctx, db)) != 1 {
		t.Fatal("transfer role/wire contract changed")
	}
	if _, err := s.TransferGroupOwner(statusActor(ctx, "owner"), req); !errs.ErrNoPermission.Is(err) {
		t.Fatal("stale owner replay admitted", err)
	}
	if len(roleEffects(t, ctx, db)) != 1 {
		t.Fatal("stale transfer created duplicate notification")
	}
}

type roleBarrierKey struct{}

func TestRoleEffectsCommittedRevocationFencesDelayedMemberBatch(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	var once, resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(release) }) }
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(roleBarrierKey{}) != true || e.CommandName != "update" {
			return
		}
		name, _ := e.Command.Lookup("update").StringValueOK()
		if name != "group" {
			return
		}
		once.Do(func() {
			close(reached)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}}
	ctx, s, db, _ := roleEffectsFixture(t, monitor, nil)
	result := make(chan error, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		resume()
		select {
		case <-done:
		case <-ctx.Done():
			t.Error("RPC drain unconfirmed")
		}
	})
	go func() {
		defer close(done)
		_, err := s.SetGroupMemberInfo(context.WithValue(statusActor(ctx, "admin"), roleBarrierKey{}, true), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{{GroupID: "g", UserID: "member", Nickname: wrapperspb.String("revoked-write")}}})
		result <- err
	}()
	select {
	case <-reached:
	case <-ctx.Done():
		t.Fatal("RPC did not reach revision CAS")
	}
	role := int32(20)
	if _, err := s.SetGroupMemberInfo(statusActor(ctx, "owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "admin", &role, nil, nil)}}); err != nil {
		t.Fatal(err)
	}
	resume()
	select {
	case err := <-result:
		if !errs.ErrNoPermission.Is(err) {
			t.Fatal("revocation lost", err)
		}
	case <-ctx.Done():
		t.Fatal("RPC did not finish")
	}
	if roleRow(t, ctx, db, "g", "member").Nickname != "member" || len(roleEffects(t, ctx, db)) != 1 {
		t.Fatal("revoked edit leaked; only revocation effect is legitimate")
	}
}
func TestRoleEffectsTransferCallbackOrderingAndUnknownNoReplay(t *testing.T) {
	for _, fixture := range []struct {
		name         string
		status       int
		allow, ready bool
	}{{"ack", 200, false, true}, {"unknown-blocks", 500, false, false}, {"legacy-failed-continue", 500, true, true}, {"permission-still-blocks", 403, true, false}} {
		t.Run(fixture.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				w.WriteHeader(fixture.status)
				_, _ = w.Write([]byte(`{"actionCode":0,"errCode":0}`))
			}))
			t.Cleanup(server.Close)
			ctx, s, db, store := roleEffectsFixture(t, nil, func() {
				config.Config.Manager.UserID = append(config.Config.Manager.UserID, "owner")
				config.Config.Callback.CallbackUrl = server.URL
				config.Config.Callback.CallbackTransferGroupOwnerAfter = config.CallBackConfig{Enable: true, CallbackTimeOut: 2, CallbackFailedContinue: &fixture.allow}
				opposite := !fixture.allow
				config.Config.Callback.CallbackBeforeJoinGroup = config.CallBackConfig{CallbackTimeOut: 9, CallbackFailedContinue: &opposite}
			})
			if _, err := s.TransferGroupOwner(statusActor(ctx, "owner"), &pbgroup.TransferGroupOwnerReq{GroupID: "g", OldOwnerUserID: "owner", NewOwnerUserID: "member"}); err != nil {
				t.Fatal(err)
			}
			rows := roleEffects(t, ctx, db)
			if len(rows) != 2 {
				t.Fatal("after/notice duty missing")
			}
			var after, notice storedRoleEffect
			for _, row := range rows {
				if row.Kind == groupeffects.AfterCallback {
					after = row
				} else {
					notice = row
				}
			}
			if after.Order.After == nil || after.Order.After.TimeoutSeconds != 2 || after.Order.After.FailedContinue != fixture.allow {
				t.Fatal("transfer callback borrowed BeforeJoin transport configuration")
			}
			if notice.Order.PreviousID != after.ID || after.Order.PreviousID != "" {
				t.Fatal("transfer order changed")
			}
			if err := store.RoleOrderReady(ctx, notice.ID); !errors.Is(err, groupeffects.ErrConflict) {
				t.Fatal("notice passed pending callback", err)
			}
			wrong, err := groupeffects.NewEndpoint(server.URL, db.Name(), groupeffects.AfterTransfer, []byte(config.Config.Secret), 9*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.DeliverCallback(ctx, after.ID, after.Revision, after.PayloadSHA256, wrong); !errors.Is(err, groupeffects.ErrInvalid) || posts.Load() != 0 {
				t.Fatal("BeforeJoin timeout retargeted callback or consumed an attempt", err)
			}
			endpoint, err := groupeffects.NewEndpoint(server.URL, db.Name(), groupeffects.AfterTransfer, []byte(config.Config.Secret), 2*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			err = store.DeliverCallback(ctx, after.ID, after.Revision, after.PayloadSHA256, endpoint)
			if fixture.status == 200 && err != nil || fixture.status != 200 && !errors.Is(err, groupeffects.ErrUnconfirmed) {
				t.Fatal(err)
			}
			var terminal struct {
				groupeffects.Receipt `bson:",inline"`
				ContinueAllowed      bool `bson:"role_continue_allowed"`
			}
			if err := db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"_id": after.ID}).Decode(&terminal); err != nil {
				t.Fatal(err)
			}
			wantState := groupeffects.Unconfirmed
			if fixture.status == 200 {
				wantState = groupeffects.Done
			}
			wantContinue := fixture.allow && fixture.status == 500
			if terminal.State != wantState || terminal.Revision != after.Revision+2 || terminal.Attempts != 1 || terminal.ContinueAllowed != wantContinue || (terminal.State == groupeffects.Done) != (len(terminal.Payload) == 0) {
				t.Fatal("callback did not durably finalize the exact unknown/ACK/continue branch")
			}
			ready := store.RoleOrderReady(ctx, notice.ID) == nil
			if ready != fixture.ready {
				t.Fatal("frozen continuation contract changed", ready)
			}
			if _, err = store.BeginCallback(ctx, after.ID, after.Revision, after.PayloadSHA256); err == nil || posts.Load() != 1 {
				t.Fatal("unknown/ACK callback replayed", err)
			}
		})
	}
}

// The actual controller type stays in the production path. This assertion guards
// accidental replacement by an in-memory role writer in future fixture edits.
var _ controller.GroupDatabase = (*rpcNotificationReadDB)(nil)

func TestRoleEffectsRetryKeepsCommandAndBeforeCallbackRunsOnce(t *testing.T) {
	ctx, s, db, _ := roleEffectsFixture(t, nil, nil)
	callbacks := memberEditCallback(t, func() {}, map[string]any{})
	base := s.notices.effects
	calls := 0
	var old *groupauthority.RoleNoticeSnapshot
	var first groupeffects.RoleEffectOrder
	s.notices.effects = &roleEffectsProbe{groupNoticeQueue: base, roleEffectQueue: base.(roleEffectQueue), after: func(tx context.Context, n *groupauthority.RoleNoticeSnapshot) error {
		calls++
		if calls == 1 {
			old = n
			first = groupeffects.RoleEffectOrder{CommandID: n.Command().ID, Epoch: n.Command().Epoch}
			return mongo.CommandError{Code: 112, Message: "synthetic transaction retry", Labels: []string{"TransientTransactionError"}}
		}
		if old == n || first.CommandID != n.Command().ID || first.Epoch != n.Command().Epoch {
			t.Fatal("retry renewed command or borrowed stale capability")
		}
		if _, _, err := old.NotificationRecipients(tx, db, "member", uuid.NewString(), strings.Repeat("0", 64), constant.GroupMemberInfoSetNotification); err == nil {
			t.Fatal("abandoned snapshot survived retry")
		}
		return nil
	}}
	name := "retry-profile"
	if _, err := s.SetGroupMemberInfo(statusActor(ctx, "owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", nil, &name, nil)}}); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || callbacks.Load() != 1 || len(roleEffects(t, ctx, db)) != 1 || roleRow(t, ctx, db, "g", "member").Nickname != name {
		t.Fatal("retry repeated callback or duplicated effect", calls, callbacks.Load())
	}
}
