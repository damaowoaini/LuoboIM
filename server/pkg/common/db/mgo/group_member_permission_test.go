package mgo

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
)

func TestGroupMemberPermissionReadRejectsUnboundedInputs(t *testing.T) {
	store := &GroupMemberMgo{}
	for _, input := range []struct {
		name, group string
		ids         []string
	}{
		{"empty-group", "", []string{"synthetic-u"}},
		{"nil-ids", "synthetic-g", nil},
		{"empty-ids", "synthetic-g", []string{}},
		{"blank-member", "synthetic-g", []string{"synthetic-u", ""}},
	} {
		t.Run(input.name, func(t *testing.T) {
			// The nil collection proves rejection happens before any query.
			if _, err := store.FindForPermission(context.Background(), input.group, input.ids); !errs.ErrArgs.Is(err) {
				t.Fatal("unbounded permission query reached storage", err)
			}
		})
	}
}

func TestMongoGroupMemberPermissionReadIsExactAndFresh(t *testing.T) {
	// Reuse the existing exact-loopback URI guard and random temporary DB with
	// error-reporting Drop/Disconnect. No MinIO/object provider is accessed.
	ctx, db, _ := objectAtomicDB(t)
	store, err := NewGroupMember(db)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Collection("group_member").InsertMany(ctx, []any{
		bson.M{"group_id": "synthetic-g", "user_id": "synthetic-u", "role_level": 20},
		bson.M{"group_id": "synthetic-h", "user_id": "synthetic-u", "role_level": 100},
		bson.M{"group_id": "synthetic-g", "user_id": "synthetic-peer", "role_level": 60},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.FindForPermission(ctx, "synthetic-g", []string{"synthetic-u"})
	if err != nil || len(first) != 1 || first[0].GroupID != "synthetic-g" || first[0].UserID != "synthetic-u" || first[0].RoleLevel != 20 {
		t.Fatal("permission read crossed group/member identity", err)
	}
	if _, err := db.Collection("group_member").UpdateOne(ctx, bson.M{"group_id": "synthetic-g", "user_id": "synthetic-u"}, bson.M{"$set": bson.M{"role_level": 60}}); err != nil {
		t.Fatal(err)
	}
	second, err := store.FindForPermission(ctx, "synthetic-g", []string{"synthetic-u"})
	if err != nil || len(second) != 1 || second[0].RoleLevel != 60 || first[0].RoleLevel != 20 {
		t.Fatal("permission read reused the earlier role snapshot", err)
	}
	missing, err := store.FindForPermission(ctx, "synthetic-g", []string{"synthetic-missing"})
	if err != nil || len(missing) != 0 {
		t.Fatal("missing exact member became an unbounded group read", err)
	}
}
