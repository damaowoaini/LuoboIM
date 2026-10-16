package groupcleanup

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
)

func (f *cleanupFixture) accept(t *testing.T) {
	t.Helper()
	if _, err := f.delivery.AcceptGroupNotificationCommitted(f.ctx, f.frozen.ID); err != nil {
		t.Fatal("actual typed receiver acceptance failed", err)
	}
}
func (f *cleanupFixture) count(t *testing.T, collection string, filter bson.M) int64 {
	t.Helper()
	n, err := f.db.Collection(collection).CountDocuments(f.ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
func (f *cleanupFixture) history(t *testing.T, uid string) []byte {
	t.Helper()
	var row bson.Raw
	if err := f.db.Collection("group_membership_history").FindOne(f.ctx, bson.M{"group_id": f.gid, "user_id": uid}).Decode(&row); err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), row...)
}

func TestDismissCleanupMongoRequiresAcceptedNotificationAndSurvivesCacheAdapterFailure(t *testing.T) {
	f := cleanupMongo(t)
	if _, err := f.cleanup.RecoverDismiss(f.ctx, f.intent.ID, f.members); !errors.Is(err, ErrDependency) {
		t.Fatal("cleanup ran before typed notification receiver acceptance", err)
	}
	if f.count(t, "group_member", bson.M{"group_id": f.gid}) != 3 {
		t.Fatal("preacceptance cleanup removed members")
	}
	f.accept(t)
	commit, err := f.cleanup.RecoverDismiss(f.ctx, f.intent.ID, f.members)
	if err != nil || commit == nil {
		t.Fatal("accepted dismissal cleanup did not commit", err)
	}
	if f.count(t, "group_member", bson.M{"group_id": f.gid}) != 0 || f.count(t, "group_membership_history", bson.M{"group_id": f.gid, "last_left_at": bson.M{"$ne": nil}}) != 3 {
		t.Fatal("actual Mgo deletion/history not atomic")
	}
	// The failure here is an injected cache adapter, not a real Redis outage.
	// Foundation's independent Redis TagAsDeleted2 tests cover its transport.
	if err := commit.Flush(f.ctx, func(context.Context, string) error { return errors.New("synthetic cache boundary unavailable") }); !errors.Is(err, groupauthority.ErrCacheSyncPending) {
		t.Fatal("cache failure was swallowed", err)
	}
	if f.count(t, groupauthority.InvalidationsCollection, bson.M{}) == 0 {
		t.Fatal("known committed cleanup lost cache recovery obligation")
	}
	row, err := f.cleanup.read(f.ctx, f.intent.ID)
	if err != nil || row.State != "done" || len(row.Members) != 0 {
		t.Fatal("cleanup did not leave minimal terminal receipt", err)
	}
	before := f.history(t, "a")
	restarted, err := NewStore(f.db, f.cleanup.epoch, f.cleanup.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	if cap, err := restarted.RecoverDismiss(f.ctx, f.intent.ID, f.members); err != nil || cap != nil {
		t.Fatal("restart replayed a completed cleanup", err)
	}
	if !bytes.Equal(before, f.history(t, "a")) {
		t.Fatal("restart rewrote authoritative membership history")
	}
	if err := f.authority.FlushPending(f.ctx, 64, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal("durable cache obligation did not recover through adapter", err)
	}
	if f.count(t, groupauthority.InvalidationsCollection, bson.M{}) != 0 {
		t.Fatal("recovered cache obligations remained")
	}
}

func TestDismissCleanupMongoAllowsAlreadyQuitSubsetWithoutRewritingItsHistory(t *testing.T) {
	f := cleanupMongo(t)
	f.accept(t)
	_, err := f.authority.Mutation(f.ctx, []groupauthority.Target{{GroupID: f.gid, Members: []string{"z"}}}, func(context.Context) error { return nil }, func(tx context.Context) error { return f.members.Delete(tx, f.gid, []string{"z"}) })
	if err != nil {
		t.Fatal("synthetic legitimate quit transaction failed", err)
	}
	before := f.history(t, "z")
	if _, err := f.cleanup.RecoverDismiss(f.ctx, f.intent.ID, f.members); err != nil {
		t.Fatal("legitimate subset stranded cleanup", err)
	}
	if !bytes.Equal(before, f.history(t, "z")) || f.count(t, "group_member", bson.M{"group_id": f.gid}) != 0 {
		t.Fatal("cleanup changed departed member's history or left current subset")
	}
}

func TestDismissCleanupMongoRefusesRevivedGroupOrNewMember(t *testing.T) {
	for _, name := range []string{"revived-status", "unexpected-member"} {
		t.Run(name, func(t *testing.T) {
			f := cleanupMongo(t)
			f.accept(t)
			// Raw fixture writes intentionally emulate incompatible/out-of-cohort old
			// binaries. No real group/user data or public caller is exercised.
			if name == "revived-status" {
				if _, err := f.db.Collection("group").UpdateOne(f.ctx, bson.M{"group_id": f.gid}, bson.M{"$set": bson.M{"status": int32(constant.GroupOk)}}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := f.db.Collection("group_member").InsertOne(f.ctx, bson.M{"group_id": f.gid, "user_id": "foreign-new-member", "role_level": int32(20)}); err != nil {
					t.Fatal(err)
				}
			}
			before := f.count(t, "group_member", bson.M{"group_id": f.gid})
			if _, err := f.cleanup.RecoverDismiss(f.ctx, f.intent.ID, f.members); !errors.Is(err, ErrConflict) {
				t.Fatal("cleanup accepted changed terminal group/roster", err)
			}
			if f.count(t, "group_member", bson.M{"group_id": f.gid}) != before {
				t.Fatal("rejected cleanup deleted a member")
			}
			row, err := f.cleanup.read(f.ctx, f.intent.ID)
			if err != nil || row.State != "pending" {
				t.Fatal("rejected cleanup became completed", err)
			}
		})
	}
}

type failAfterMembershipDelete struct {
	relation.GroupMemberModelInterface
}

func (f failAfterMembershipDelete) Delete(ctx context.Context, gid string, uids []string) error {
	if err := f.GroupMemberModelInterface.Delete(ctx, gid, uids); err != nil {
		return err
	}
	return errors.New("synthetic error after actual membership/history writes")
}
func TestDismissCleanupMongoWriterFailureRollsBackMembersHistoryAndReceipt(t *testing.T) {
	f := cleanupMongo(t)
	f.accept(t)
	if _, err := f.cleanup.RecoverDismiss(f.ctx, f.intent.ID, failAfterMembershipDelete{f.members}); err == nil {
		t.Fatal("actual model write failure was swallowed")
	}
	if f.count(t, "group_member", bson.M{"group_id": f.gid}) != 3 || f.count(t, "group_membership_history", bson.M{"group_id": f.gid}) != 0 {
		t.Fatal("aborted cleanup leaked partial members/history")
	}
	row, err := f.cleanup.read(f.ctx, f.intent.ID)
	if err != nil || row.State != "pending" || len(row.Members) != 3 {
		t.Fatal("aborted cleanup retired its retry payload", err)
	}
	if _, err := f.cleanup.RecoverDismiss(f.ctx, f.intent.ID, f.members); err != nil {
		t.Fatal("same exact cleanup did not recover", err)
	}
}
