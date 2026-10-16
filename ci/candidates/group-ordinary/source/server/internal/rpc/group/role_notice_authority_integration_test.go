package group

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"go.mongodb.org/mongo-driver/bson"
)

// This directly exercises the NEW ordinary-owner RoleNotice authorization in
// the real strong Mongo transaction. The outer RPC's existing SQL risk-policy
// chain is deliberately outside this Mongo/effect fixture, not faked as tested.
func TestRoleNoticeActualMongoOrdinaryOwnerAndLatestRoleBoundary(t *testing.T) {
	for _, kind := range []string{"ordinary-owner", "non-owner", "expired-owner"} {
		t.Run(kind, func(t *testing.T) {
			ctx, s, db, _ := roleEffectsFixture(t, nil, nil)
			actor := "owner"
			if kind == "non-owner" {
				actor = "admin"
			}
			if authverify.IsAppManagerUid(statusActor(ctx, actor)) {
				t.Fatal("ordinary-owner fixture accidentally gained manager bypass")
			}
			if kind == "expired-owner" {
				if _, err := db.Collection("group_member").UpdateOne(ctx, bson.M{"group_id": "g", "user_id": "owner"}, bson.M{"$set": bson.M{"role_level": int32(20)}}); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Collection("group_member").UpdateOne(ctx, bson.M{"group_id": "g", "user_id": "admin"}, bson.M{"$set": bson.M{"role_level": int32(100)}}); err != nil {
					t.Fatal(err)
				}
			}
			current, _, err := groupauthority.NewOwnerTransferCommand(statusActor(ctx, actor), uuid.NewString(), "g", "owner", "member")
			if err != nil {
				t.Fatal(err)
			}
			var snapshot *groupauthority.RoleNoticeSnapshot
			authorizations, writes := 0, 0
			commit, err := s.authority.Mutation(current, []groupauthority.Target{{GroupID: "g", Members: []string{"owner", "member"}}}, func(tx context.Context) error {
				authorizations++
				var err error
				snapshot, err = groupauthority.BeginRoleNotice(tx, db, "g")
				return err
			}, func(tx context.Context) error {
				writes++
				if err := s.db.TransferGroupOwner(tx, "g", "owner", "member", constant.GroupOrdinaryUsers); err != nil {
					return err
				}
				return snapshot.Finish(tx, db)
			})
			if kind == "ordinary-owner" {
				if err != nil || commit == nil || authorizations != 1 || writes != 1 || roleRow(t, ctx, db, "g", "owner").RoleLevel != 20 || roleRow(t, ctx, db, "g", "member").RoleLevel != 100 {
					t.Fatal("ordinary owner did not reach the real guarded writer/Finish", err)
				}
			} else {
				if !errors.Is(err, groupauthority.ErrInvalidScope) || commit != nil || authorizations != 1 || writes != 0 {
					t.Fatal("new RoleNotice boundary did not reject before writer", err, authorizations, writes)
				}
				wantOwner := int32(100)
				if kind == "expired-owner" {
					wantOwner = 20
				}
				if roleRow(t, ctx, db, "g", "owner").RoleLevel != wantOwner || roleRow(t, ctx, db, "g", "member").RoleLevel != 20 {
					t.Fatal("denied transfer changed stored roles")
				}
			}
		})
	}
}
