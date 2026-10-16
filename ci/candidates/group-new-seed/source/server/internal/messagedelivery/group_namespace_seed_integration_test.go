package messagedelivery

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestNewGroupSeedProtocolPairAndClaimsUseExactIndependentNamespaces(t *testing.T) {
	store := &Store{epoch: uuid.NewString(), activationSHA: hash([]byte("synthetic pure activation"))}
	for _, item := range []struct{ gid, normal, notice string }{{"g", "sg_g", "n_g"}, {"a_b", "sg_a_b", "n_a_b"}, {"sg_a_b", "sg_sg_a_b", "n_sg_a_b"}} {
		normal, notice, err := groupNamespacePair(item.gid)
		if err != nil || normal != item.normal || notice != item.notice {
			t.Fatal("new group uses a non-OpenIM CID mapping", err)
		}
		claims := claimRows(groupauthority.NewGroupCreationInfo{ID: uuid.NewString(), Epoch: store.epoch, ActivationSHA256: store.activationSHA, GroupID: item.gid}, normal, notice, hash([]byte("synthetic cutover")))
		if len(claims) != 3 || claims[0].ID != groupOriginClaimID(item.gid) || claims[1].ID != namespaceClaimID(normal) || claims[2].ID != namespaceClaimID(notice) || claims[1].ID == key(store.epoch, "sequence", normal) {
			t.Fatal("permanent claim shares a counter/epoch key")
		}
		info := groupauthority.NewGroupCreationInfo{ID: uuid.NewString(), Epoch: store.epoch, ActivationSHA256: store.activationSHA, GroupID: item.gid, Owner: "owner", Actor: "owner", InputSHA256: hash([]byte("input")), GroupSHA256: hash([]byte("group")), MembersSHA256: hash([]byte("members")), Members: []groupauthority.InitialGroupMember{{UserID: "owner", Role: 100}}, CreatedAt: time.UnixMilli(1000).UTC()}
		valid := seedReceipt(info, normal, notice, hash([]byte("cutover")), hash([]byte("evidence")))
		body, err := bson.Marshal(valid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.decodeNewGroupSeed(body); err != nil {
			t.Fatal("valid exact pair rejected", err)
		}
		valid.NotificationCID = "n_" + normal
		body, err = bson.Marshal(valid)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.decodeNewGroupSeed(body); !errors.Is(err, ErrConflict) {
			t.Fatal("wrong n_+normal mapping accepted", err)
		}
	}
}

// The inherited helper owns an exact ephemeral RS/nonce DB and verifies drop
// and disconnect. This fixture's nine claims account for its existing three
// synthetic group rows. It is NOT the missing production inventory importer.
func newGroupSeedFixture(t *testing.T) (context.Context, *groupauthority.Store, *Store, *mongo.Database, relation.GroupModelInterface, relation.GroupMemberModelInterface) {
	t.Helper()
	ctx, authority, store, db := ownedReceiverMongo(t, nil, nil)
	groups, err := mgo.NewGroupMongoContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	members, err := mgo.NewGroupMemberContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	cut := NamespaceCutover{ID: NamespaceCutoverID, Kind: "immutable-namespace-cutover-v1", Database: db.Name(), Epoch: store.epoch, ActivationSHA256: store.activationSHA, MigrationID: uuid.NewString(), State: "all-inventoried-namespaces-permanently-claimed", ManifestSHA256: hash([]byte("synthetic manifest init,left,right")), MongoInventorySHA256: hash([]byte("synthetic Mongo inventory")), RedisInventorySHA256: hash([]byte("synthetic absent Redis fixture")), DrainSHA256: hash([]byte("synthetic no legacy worker")), OldBinaryFenceSHA256: hash([]byte("synthetic isolated fixture only")), NamespaceCount: 9, ClaimedCount: 9, RegisteredAt: time.Now().UTC().Truncate(time.Millisecond)}
	for _, gid := range []string{"init", "left", "right"} {
		normal, notice, err := groupNamespacePair(gid)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range claimRows(groupauthority.NewGroupCreationInfo{ID: uuid.NewString(), Epoch: store.epoch, ActivationSHA256: store.activationSHA, GroupID: gid, InputSHA256: hash([]byte("synthetic old " + gid)), CreatedAt: cut.RegisteredAt}, normal, notice, hash([]byte("synthetic completed cutover"))) {
			if _, err := db.Collection(Sequences).InsertOne(ctx, row); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := db.Collection(Sequences).InsertOne(ctx, cut); err != nil {
		t.Fatal(err)
	}
	return ctx, authority, store, db, groups, members
}

func initialSeedRows(gid string) (*relation.GroupModel, []*relation.GroupMemberModel) {
	at := time.Unix(10, 123456).UTC()
	group := &relation.GroupModel{GroupID: gid, GroupType: constant.WorkingGroup, CreatorUserID: "owner", CreateTime: at}
	return group, []*relation.GroupMemberModel{{GroupID: gid, UserID: "owner", RoleLevel: constant.GroupOwner, JoinTime: at}, {GroupID: gid, UserID: "member", RoleLevel: constant.GroupOrdinaryUsers, JoinTime: at}}
}

func applyNewGroupSeed(ctx context.Context, authority *groupauthority.Store, store *Store, groups relation.GroupModelInterface, members relation.GroupMemberModelInterface, gid string, after func(context.Context, *NewGroupSeedReservation, NewGroupSeedReceipt) error) (groupauthority.NewGroupCreationInfo, NewGroupSeedReceipt, error) {
	group, rows := initialSeedRows(gid)
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	ctx, info, err := store.NewGroupCreationCommand(ctx, "owner", group, rows)
	if err != nil {
		return info, NewGroupSeedReceipt{}, err
	}
	var receipt NewGroupSeedReceipt
	_, err = authority.Mutation(ctx, []groupauthority.Target{{GroupID: gid, Create: true, Members: []string{"owner", "member"}}}, func(context.Context) error { return nil }, func(tx context.Context) error {
		reservation, err := store.BeginNewGroupSeed(tx, gid)
		if err != nil {
			return err
		}
		if err := groups.Create(tx, []*relation.GroupModel{group}); err != nil {
			return err
		}
		if err := members.Create(tx, rows); err != nil {
			return err
		}
		receipt, err = store.FinishNewGroupSeed(tx, reservation)
		if err != nil {
			return err
		}
		if after != nil {
			return after(tx, reservation, receipt)
		}
		return nil
	})
	return info, receipt, err
}

func TestNewGroupSeedCommitsProtocolPairAndNeverResetsAdvancedHead(t *testing.T) {
	ctx, authority, store, db, groups, members := newGroupSeedFixture(t)
	var expired *groupauthority.NewGroupSeedScope
	info, receipt, err := applyNewGroupSeed(ctx, authority, store, groups, members, "sg_new_under", func(tx context.Context, reservation *NewGroupSeedReservation, first NewGroupSeedReceipt) error {
		var err error
		expired, err = groupauthority.CaptureNewGroupSeed(tx, db, first.GroupID)
		if err != nil {
			return err
		}
		if _, err := expired.Inspect(tx, db.Client().Database(db.Name()+"_not_owned")); !errors.Is(err, groupauthority.ErrInvalidScope) {
			return errors.New("seed authority crossed database boundary")
		}
		result, err := db.Collection(Sequences).UpdateOne(tx, bson.M{"_id": key(store.epoch, "sequence", first.NormalCID), "maximum": int64(0)}, bson.M{"$set": bson.M{"maximum": int64(7), "revision": int64(2)}})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 {
			return errors.New("synthetic shared head advance missed")
		}
		again, err := store.FinishNewGroupSeed(tx, reservation)
		if err != nil || !reflect.DeepEqual(first, again) {
			return errors.Join(errors.New("same attempt did not inspect stable receipt"), err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := expired.Inspect(ctx, db); !errors.Is(err, groupauthority.ErrInvalidScope) {
		t.Fatal("committed transaction leaked a usable Create capability", err)
	}
	if _, err := store.BeginNewGroupSeed(ctx, info.GroupID); !errors.Is(err, ErrUnprepared) {
		t.Fatal("bare context minted seed reservation", err)
	}
	if receipt.NormalCID != "sg_sg_new_under" || receipt.NotificationCID != "n_sg_new_under" || receipt.MemberCount != 2 || receipt.CommandID != info.ID {
		t.Fatal("protocol CID pair or server command differs")
	}
	for _, item := range []struct {
		cid  string
		want int64
	}{{receipt.NormalCID, 7}, {receipt.NotificationCID, 0}} {
		var row counter
		if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(store.epoch, "sequence", item.cid)}).Decode(&row); err != nil || row.Maximum != item.want || row.SeedMaximum != 0 {
			t.Fatal("seed reset advanced counter or confused mixed sequence row kinds", err)
		}
	}
	for _, uid := range []string{"owner", "member"} {
		var row MembershipWindow
		if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(store.epoch, "membership-window", receipt.NormalCID+"\x00"+uid)}).Decode(&row); err != nil || row.UserID != uid || row.GroupID != receipt.GroupID || row.Closed || row.LastCommand != info.ID || row.SeedEvidenceSHA256 != receipt.EvidenceSHA256 {
			t.Fatal("actual member lacks matching initial owner window", err)
		}
	}
	if n, err := db.Collection("group_membership_history").CountDocuments(ctx, bson.M{"group_id": receipt.GroupID}); err != nil || n != 2 {
		t.Fatal("actual Joined history did not share create commit", err)
	}
	resolved, err := store.InspectNewGroupSeed(ctx, info.ID)
	if err != nil || !reflect.DeepEqual(resolved, receipt) {
		t.Fatal("restart inspection lost exact seed receipt", err)
	}
	if _, err := store.InspectNewGroupSeed(ctx, uuid.NewString()); !errors.Is(err, ErrCommitUnconfirmed) {
		t.Fatal("missing receipt falsely proved rollback", err)
	}
	if err := store.CheckNewGroupSeedReady(ctx); err != nil {
		t.Fatal("component readiness unavailable", err)
	}
	if err := store.CheckNewGroupSeedRuntimeReady(ctx); !errors.Is(err, ErrNamespaceWritersUnbound) || !errors.Is(err, ErrUnprepared) {
		t.Fatal("migration record falsely certified future/direct allocator binding", err)
	}
	if _, err := store.ReadMembershipWindow(ctx, receipt.NormalCID, "owner"); !errors.Is(err, ErrUnprepared) {
		t.Fatal("zero seed manufactured global reader Ready")
	}
}

func TestNewGroupSeedRejectsPostWriteIdentityOrRoleDrift(t *testing.T) {
	for _, drift := range []string{"group-field", "owner-role", "extra-member"} {
		t.Run(drift, func(t *testing.T) {
			ctx, authority, store, db, groups, members := newGroupSeedFixture(t)
			group, rows := initialSeedRows("drift")
			ctx = context.WithValue(ctx, constant.OpUserID, "owner")
			ctx, _, err := store.NewGroupCreationCommand(ctx, "owner", group, rows)
			if err != nil {
				t.Fatal(err)
			}
			_, err = authority.Mutation(ctx, []groupauthority.Target{{GroupID: "drift", Create: true, Members: []string{"owner", "member"}}}, func(context.Context) error { return nil }, func(tx context.Context) error {
				reservation, err := store.BeginNewGroupSeed(tx, "drift")
				if err != nil {
					return err
				}
				if err := groups.Create(tx, []*relation.GroupModel{group}); err != nil {
					return err
				}
				if err := members.Create(tx, rows); err != nil {
					return err
				}
				// Deliberately corrupt the transaction's actual rows; the
				// seed proof must read them, not trust the caller's pointers.
				switch drift {
				case "group-field":
					_, err = db.Collection("group").UpdateOne(tx, bson.M{"group_id": "drift"}, bson.M{"$set": bson.M{"group_name": "changed after capture"}})
				case "owner-role":
					_, err = db.Collection("group_member").UpdateOne(tx, bson.M{"group_id": "drift", "user_id": "owner"}, bson.M{"$set": bson.M{"role_level": int32(20)}})
				case "extra-member":
					_, err = db.Collection("group_member").InsertOne(tx, &relation.GroupMemberModel{GroupID: "drift", UserID: "unexpected", RoleLevel: constant.GroupOrdinaryUsers})
				}
				if err != nil {
					return err
				}
				_, err = store.FinishNewGroupSeed(tx, reservation)
				return err
			})
			if !errors.Is(err, groupauthority.ErrConflict) && !errors.Is(err, groupauthority.ErrInvalidScope) {
				t.Fatal("seed accepted drifted actual rows", err)
			}
			if n, err := db.Collection("group").CountDocuments(ctx, bson.M{"group_id": "drift"}); err != nil || n != 0 {
				t.Fatal("invalid seed committed group row", err)
			}
		})
	}
}

func TestNewGroupSeedAbortRollsBackRealModelsHistoryClaimsAndPair(t *testing.T) {
	ctx, authority, store, db, groups, members := newGroupSeedFixture(t)
	stop := errors.New("synthetic abort after actual dual-counter seed")
	_, _, err := applyNewGroupSeed(ctx, authority, store, groups, members, "abort", func(context.Context, *NewGroupSeedReservation, NewGroupSeedReceipt) error { return stop })
	if !errors.Is(err, stop) {
		t.Fatal("did not reach seed then abort", err)
	}
	for _, name := range []string{"group", "group_member", "group_membership_history", Sequences, Receipts} {
		if n, err := db.Collection(name).CountDocuments(ctx, bson.M{"group_id": "abort"}); err != nil || n != 0 {
			t.Fatal("seed write escaped aborted group transaction", name, err)
		}
	}
	for _, cid := range []string{"sg_abort", "n_abort"} {
		if n, err := db.Collection(Sequences).CountDocuments(ctx, bson.M{"_id": key(store.epoch, "sequence", cid)}); err != nil || n != 0 {
			t.Fatal("aborted zero counter survived", err)
		}
	}
}

func TestNewGroupSeedRejectsOccupiedNamespacesBeforeJoinedUpsert(t *testing.T) {
	for _, collision := range []string{"direct-notification", "legacy-normal", "old-history", "permanent-other-epoch", "missing-cutover"} {
		t.Run(collision, func(t *testing.T) {
			ctx, authority, store, db, groups, members := newGroupSeedFixture(t)
			gid := "collision_peer"
			switch collision {
			case "direct-notification":
				direct := msgprocessor.GetNotificationConversationID(constant.SingleChatType, "collision", "peer")
				if direct != "n_"+gid {
					t.Fatal("synthetic direct/group collision is not a real protocol collision")
				}
				if _, err := db.Collection("msg").InsertOne(ctx, bson.M{"doc_id": direct + ":0", "sentinel": true}); err != nil {
					t.Fatal(err)
				}
			case "legacy-normal":
				if _, err := db.Collection("msg").InsertOne(ctx, bson.M{"doc_id": "g_" + gid + ":0", "sentinel": true}); err != nil {
					t.Fatal(err)
				}
			case "old-history":
				if _, err := db.Collection("group_membership_history").InsertOne(ctx, bson.M{"group_id": gid, "user_id": "owner", "last_left_at": time.Unix(5, 0).UTC()}); err != nil {
					t.Fatal(err)
				}
			case "permanent-other-epoch":
				if _, err := db.Collection(Sequences).InsertOne(ctx, bson.M{"_id": namespaceClaimID("n_" + gid), "kind": "historical-direct-namespace", "epoch": uuid.NewString()}); err != nil {
					t.Fatal(err)
				}
			case "missing-cutover":
				if _, err := db.Collection(Sequences).DeleteOne(ctx, bson.M{"_id": NamespaceCutoverID}); err != nil {
					t.Fatal(err)
				}
			}
			_, _, err := applyNewGroupSeed(ctx, authority, store, groups, members, gid, nil)
			if collision == "missing-cutover" && !errors.Is(err, ErrUnprepared) || collision != "missing-cutover" && !errors.Is(err, ErrConflict) {
				t.Fatal("old/orphan/direct namespace was reused", err)
			}
			if n, err := db.Collection("group_member").CountDocuments(ctx, bson.M{"group_id": gid}); err != nil || n != 0 {
				t.Fatal("collision reached real member writer", err)
			}
			if collision == "old-history" {
				if n, err := db.Collection("group_membership_history").CountDocuments(ctx, bson.M{"group_id": gid, "last_left_at": time.Unix(5, 0).UTC()}); err != nil || n != 1 {
					t.Fatal("Joined upsert erased old membership evidence before admission", err)
				}
			}
		})
	}
}

func TestNewGroupSeedPermanentOriginSurvivesGroupDeletionAndRejectsOverflow(t *testing.T) {
	ctx, authority, store, db, groups, members := newGroupSeedFixture(t)
	_, receipt, err := applyNewGroupSeed(ctx, authority, store, groups, members, "never-reuse", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit test corruption/removal only, never a production cleanup API.
	for _, name := range []string{"group", "group_member", "group_membership_history", Receipts} {
		if _, err := db.Collection(name).DeleteMany(ctx, bson.M{"group_id": receipt.GroupID}); err != nil {
			t.Fatal(err)
		}
	}
	for _, cid := range []string{receipt.NormalCID, receipt.NotificationCID} {
		if _, err := db.Collection(Sequences).DeleteOne(ctx, bson.M{"_id": key(store.epoch, "sequence", cid)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Collection(Sequences).DeleteMany(ctx, bson.M{"kind": "membership-window-v1", "group_id": receipt.GroupID}); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Collection(Sequences).CountDocuments(ctx, bson.M{"_id": bson.M{"$in": []string{groupOriginClaimID(receipt.GroupID), namespaceClaimID(receipt.NormalCID), namespaceClaimID(receipt.NotificationCID)}}}); err != nil || n != 3 {
		t.Fatal("permanent claims were removed with operational rows", err)
	}
	if _, err := store.PlanNewGroupNamespaces(ctx, receipt.GroupID); !errors.Is(err, ErrConflict) {
		t.Fatal("deleted group made a permanent namespace reusable", err)
	}
	for _, invalid := range []struct {
		name string
		head int64
	}{{"negative", -1}, {"max-minus-one", math.MaxInt64 - 1}, {"max", math.MaxInt64}} {
		t.Run(invalid.name, func(t *testing.T) {
			gid := uuid.NewString()
			_, _, err := applyNewGroupSeed(ctx, authority, store, groups, members, gid, func(tx context.Context, reservation *NewGroupSeedReservation, first NewGroupSeedReceipt) error {
				if _, err := db.Collection(Sequences).UpdateOne(tx, bson.M{"_id": key(store.epoch, "sequence", first.NormalCID)}, bson.M{"$set": bson.M{"maximum": invalid.head}}); err != nil {
					return err
				}
				_, err := store.FinishNewGroupSeed(tx, reservation)
				return err
			})
			if !errors.Is(err, ErrConflict) {
				t.Fatal("negative/overflow head was adopted", err)
			}
		})
	}
}
