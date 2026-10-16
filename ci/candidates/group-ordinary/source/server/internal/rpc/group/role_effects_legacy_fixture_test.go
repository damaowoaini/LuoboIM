package group

import (
	"context"
	"encoding/json"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Only already-exclusive nonce databases from groupRPCMongo reach this helper.
// It prepares the new required notice catalog without weakening production
// readiness or changing any original authority/rollback/permission assertion.
func attachRoleNoticeFixture(t *testing.T, ctx context.Context, s *groupServer, db *mongo.Database) {
	t.Helper()
	names, err := db.ListCollectionNames(ctx, bson.M{"name": groupeffects.Collection})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		if err = db.CreateCollection(ctx, groupeffects.Collection); err != nil {
			t.Fatal(err)
		}
	} else if len(names) != 1 {
		t.Fatal("unexpected effect catalog shape")
	}
	err = db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"_id": groupeffects.SchemaID}).Err()
	if err == mongo.ErrNoDocuments {
		_, err = db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)})
	}
	if err != nil {
		t.Fatal(err)
	}
	s.notices, err = newGroupNoticeRuntime(db, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.notices.effects.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
}
func assertExactRoleRevocationDuty(t *testing.T, ctx context.Context, db *mongo.Database) {
	t.Helper()
	// Read ALL non-schema effects, including AfterCallback/foreign producers.
	cursor, err := db.Collection(groupeffects.Collection).Find(ctx, bson.M{"_id": bson.M{"$ne": groupeffects.SchemaID}})
	if err != nil {
		t.Fatal(err)
	}
	var rows []storedRoleEffect
	if err = cursor.All(ctx, &rows); err != nil {
		t.Fatal(err)
	}
	if err = cursor.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatal("expected exactly one durable legitimate revocation duty", len(rows))
	}
	row := rows[0]
	var msg sdkws.MsgData
	if row.Kind != groupeffects.Notification || row.GroupID != "g" || row.Actor != "owner" || row.Producer != "group-role-v1" || row.State != groupeffects.Pending || row.Attempts != 0 || proto.Unmarshal(row.Payload, &msg) != nil || msg.ContentType != constant.GroupMemberSetToOrdinaryUserNotification {
		t.Fatal("revocation duty provenance changed")
	}
	if row.OperationID != roleEffectOperation(groupauthority.RoleCommandInfo{ID: row.Order.CommandID}, "g", "notice", 0) || row.Order.PreviousID != "" || row.Order.After != nil || msg.SendID != "owner" || msg.GroupID != "g" || msg.RecvID != "g" || msg.ClientMsgID != "ga3:"+row.ID {
		t.Fatal("command/operation/wire provenance changed")
	}
	if u, e := uuid.Parse(row.Order.CommandID); e != nil || u == uuid.Nil || u.String() != row.Order.CommandID {
		t.Fatal("invalid server command")
	}
	if u, e := uuid.Parse(row.Order.Epoch); e != nil || u == uuid.Nil || u.String() != row.Order.Epoch {
		t.Fatal("invalid frozen epoch")
	}
	canonical, e := groupeffects.NewNotification(row.OperationID, row.Ordinal, row.GroupID, row.Actor, &msg, row.CreatedAt)
	if e != nil || canonical.ID != row.ID || canonical.PayloadSHA256 != row.PayloadSHA256 {
		t.Fatal("frozen effect identity/digest changed", e)
	}
	plans, e := groupeffects.PlanNotificationBranches(row.Frozen)
	if e != nil || len(plans) != len(row.Targets) {
		t.Fatal("frozen branches missing", e)
	}
	expected := map[string]bool{}
	for _, branch := range plans {
		expected[branch.ID] = true
	}
	for _, target := range row.Targets {
		if !expected[target.BranchID] || !reflect.DeepEqual(target.UserIDs, []string{"admin", "member", "owner"}) {
			t.Fatal("frozen recipient provenance changed")
		}
		delete(expected, target.BranchID)
	}
	if len(expected) != 0 {
		t.Fatal("frozen branch repeated or missing")
	}
	var elem sdkws.NotificationElem
	if err := json.Unmarshal(msg.Content, &elem); err != nil {
		t.Fatal(err)
	}
	var tips sdkws.GroupMemberInfoSetTips
	if err := json.Unmarshal([]byte(elem.Detail), &tips); err != nil {
		t.Fatal(err)
	}
	if tips.OpUser == nil || tips.OpUser.UserID != "owner" || tips.ChangedUser == nil || tips.ChangedUser.UserID != "admin" || tips.ChangedUser.RoleLevel != 20 || tips.Group == nil || tips.Group.GroupID != "g" {
		t.Fatal("revocation duty targeted another member/group")
	}
}

// Only these three revocation scenarios use an action-specific zero assertion;
// every other original effectCount call remains the full collection count.
func roleProtectedActionCount(t *testing.T, ctx context.Context, db *mongo.Database, actions ...string) int64 {
	t.Helper()
	count, err := db.Collection(groupeffects.Collection).CountDocuments(ctx, bson.M{"kind": groupeffects.Notification, "action": bson.M{"$in": actions}})
	if err != nil {
		t.Fatal(err)
	}
	return count
}
