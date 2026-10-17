package messagedelivery

import (
	"context"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Explicit operator preparation ONLY in the original exclusively claimed fresh
// test DB, AFTER the real private Create command and committed pair receipt.
// No production constructor, Ready check, namespace seed or cut is weakened.
func prepareCreatedMembershipFixture(t *testing.T, ctx context.Context, base *Store, db *mongo.Database, info groupauthority.NewGroupCreationInfo, receipt NewGroupSeedReceipt) {
	t.Helper()
	ownership, ok := ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership)
	if !ok || ownership == nil || !ownership.dropAllowed.Load() || base == nil || db == nil || base.db == nil || base.db.Client() != db.Client() || base.db.Name() != db.Name() || !strings.HasPrefix(db.Name(), "luobo_owned_receiver_test_") || receipt.CommandID != info.ID {
		t.Fatal("exclusive real-created membership fixture required")
	}
	var claim struct {
		ID    string `bson:"_id"`
		Nonce string `bson:"nonce"`
	}
	if err := db.Collection("authority_fixture_claim").FindOne(ctx, bson.M{"_id": "task-claim"}).Decode(&claim); err != nil {
		t.Fatal("exact fixture claim", err)
	}
	if decoded, err := hex.DecodeString(claim.Nonce); err != nil || len(decoded) != 12 || claim.ID != "task-claim" {
		t.Fatal("exact fixture nonce")
	}
	resolved, err := base.InspectNewGroupSeed(ctx, info.ID)
	if err != nil || !reflect.DeepEqual(resolved, receipt) {
		t.Fatal("actual committed group creation receipt", err)
	}
	if err := base.transaction(ctx, func(tx context.Context) error {
		if err := base.verifySeedState(tx, receipt, info.Members); err != nil {
			return err
		}
		for _, name := range []string{Sequences, Receipts} {
			if _, err := db.Collection(name).InsertOne(tx, bson.M{"_id": MembershipSchemaID, "version": int32(1), "epoch": base.epoch, "activation_sha256": base.activationSHA, "legacy_message_writers_retired": true, "legacy_group_writers_retired": true, "owner_bounds_seeded": true, "readers_bound": true}); err != nil {
				return err
			}
		}
		return base.checkMembershipSchema(tx)
	}); err != nil {
		t.Fatal("owned fixture membership preparation after real seed proof", err)
	}
}
