package messagedelivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Explicit synthetic IMPORT OUTPUT confined to the original exclusively
// claimed nonce DB. These rows exercise strict runtime consumers; they are not
// an importer/old-process-retirement/StartTransfer activation proof.
func prepareMembershipNamespaceFixture(t *testing.T, ctx context.Context, authority *groupauthority.Store, s *Store, db *mongo.Database, gid string, owners []string, normalHead, noticeHead int64) {
	t.Helper()
	if ctx.Value(sequenceFixtureOwnershipKey{}) == nil || !strings.HasPrefix(db.Name(), "luobo_message_delivery_test_") {
		t.Fatal("exclusive original nonce fixture required")
	}
	if err := db.Collection("authority_fixture_claim").FindOne(ctx, bson.M{"_id": "task-claim"}).Err(); err != nil {
		t.Fatal(err)
	}
	cut := NamespaceCutover{ID: NamespaceCutoverID, Kind: "immutable-namespace-cutover-v1", Database: db.Name(), Epoch: s.epoch, ActivationSHA256: s.activationSHA, MigrationID: uuid.NewString(), State: "all-inventoried-namespaces-permanently-claimed", ManifestSHA256: hash([]byte("fixture manifest")), MongoInventorySHA256: hash([]byte("fixture mongo")), RedisInventorySHA256: hash([]byte("fixture redis")), DrainSHA256: hash([]byte("fixture drain")), OldBinaryFenceSHA256: hash([]byte("fixture old binary")), NamespaceCount: 2, ClaimedCount: 2, RegisteredAt: time.Unix(1, 0).UTC()}
	if _, err := db.Collection(Sequences).InsertOne(ctx, cut); err != nil {
		t.Fatal(err)
	}
	normal, notice, err := groupNamespacePair(gid)
	if err != nil {
		t.Fatal(err)
	}
	command := uuid.NewString()
	stamp := time.Unix(2, 0).UTC()
	for _, entry := range []struct{ id, cid, branch string }{{groupOriginClaimID(gid), "", "group-origin"}, {namespaceClaimID(normal), normal, "normal"}, {namespaceClaimID(notice), notice, "notification"}} {
		claim := groupNamespaceClaim{ID: entry.id, Kind: "permanent-new-group-namespace-v1", ConversationID: entry.cid, GroupID: gid, Branch: entry.branch, Epoch: s.epoch, ActivationSHA256: s.activationSHA, CommandID: command, InputSHA256: hash([]byte("fixture original creation")), CutoverSHA256: hash([]byte("fixture cutover body")), CreatedAt: stamp}
		if _, err := db.Collection(Sequences).InsertOne(ctx, claim); err != nil {
			t.Fatal(err)
		}
	}
	initializeSequence(t, ctx, authority, s, normal, normalHead)
	initializeSequence(t, ctx, authority, s, notice, noticeHead)
	message := &sdkws.MsgData{SendID: "owner", RecvID: gid, GroupID: gid, SessionType: constant.SuperGroupChatType}
	d, err := DescribeNamespace(message)
	if err != nil {
		t.Fatal(err)
	}
	if err := sequenceTxn(ctx, authority, "init", func(tx context.Context) error {
		for _, cid := range []string{normal, notice} {
			copy := d
			copy.CID = cid
			if err := s.recordNamespacePrincipals(tx, copy); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, uid := range owners {
		for _, cid := range []string{normal, notice} {
			w := MembershipWindow{ID: key(s.epoch, "membership-window", cid+"\x00"+uid), Kind: "membership-window-v1", Epoch: s.epoch, ConversationID: cid, GroupID: gid, UserID: uid, Revision: 1, GroupRevision: 1, SeedEvidenceSHA256: hash([]byte("fixture historical owner:" + uid))}
			if _, err := db.Collection(Sequences).InsertOne(ctx, w); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func jointEffects(t *testing.T, ctx context.Context, db *mongo.Database) *groupeffects.Store {
	t.Helper()
	for _, name := range []string{groupeffects.Collection, "conversation"} {
		names, err := db.ListCollectionNames(ctx, bson.M{"name": name})
		if err != nil || len(names) > 1 {
			t.Fatal("owned collection inspection", err)
		}
		if len(names) == 0 {
			if err := db.CreateCollection(ctx, name); err != nil {
				t.Fatal(err)
			}
		}
	}
	var raw bson.Raw
	err := db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"_id": groupeffects.SchemaID}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if _, err := db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); err != nil {
			t.Fatal(err)
		}
	} else {
		fields, e := raw.Elements()
		kind, ok := raw.Lookup("kind").StringValueOK()
		version, valid := raw.Lookup("version").Int32OK()
		if err != nil || e != nil || len(fields) != 3 || !ok || kind != "group-effects-schema-v1" || !valid || version != 1 {
			t.Fatal("owned effects schema differs", err)
		}
	}
	s, err := groupeffects.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func jointRemoveMember(ctx context.Context, authority *groupauthority.Store, s *Store, db *mongo.Database, uid string) error {
	members, err := mgo.NewGroupMemberContext(ctx, db)
	if err != nil {
		return err
	}
	commandCtx, _, err := groupauthority.NewNoticeCommand(context.WithValue(ctx, constant.OpUserID, "owner"), s.epoch, groupauthority.NoticeKick, "g")
	if err != nil {
		return err
	}
	var snapshot *groupauthority.NoticeSnapshot
	_, err = authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: "g", Members: []string{uid}}}, func(tx context.Context) error {
		var err error
		snapshot, err = groupauthority.BeginNotice(tx, db, "g")
		return err
	}, func(tx context.Context) error {
		if err := members.Delete(tx, "g", []string{uid}); err != nil {
			return err
		}
		if err := snapshot.Finish(tx, db); err != nil {
			return err
		}
		cap, err := groupauthority.CompletedMemberRemoval(tx, db, "g")
		if err != nil {
			return err
		}
		_, err = s.RecordMemberRemoval(tx, cap)
		return err
	})
	return err
}

func jointAddMember(t *testing.T, ctx context.Context, authority *groupauthority.Store, s *Store, db *mongo.Database, uid string, forbidHistory bool, queue func(context.Context, *groupauthority.MemberAdmissionScope) error) groupauthority.NoticeCommandInfo {
	t.Helper()
	commandCtx, command, err := groupauthority.NewMemberAdmissionCommand(context.WithValue(ctx, constant.OpUserID, "owner"), s.epoch, "g", []string{uid}, groupauthority.MemberAdmissionInvite)
	if err != nil {
		t.Fatal(err)
	}
	members, err := mgo.NewGroupMemberContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	var cap *groupauthority.MemberAdmissionScope
	_, err = authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: "g", Members: []string{uid}}}, func(tx context.Context) error {
		var err error
		cap, err = groupauthority.BeginMemberAdmission(tx, db, "g")
		return err
	}, func(tx context.Context) error {
		if err := groupauthority.RequireMembers(tx, db, "g", []string{uid}); err != nil {
			return err
		}
		if err := members.Create(tx, []*relation.GroupMemberModel{{GroupID: "g", UserID: uid, RoleLevel: constant.GroupOrdinaryUsers, JoinTime: time.Now().UTC().Truncate(time.Millisecond)}}); err != nil {
			return err
		}
		if err := cap.Complete(tx, db); err != nil {
			return err
		}
		if err := s.RecordMemberAdmission(tx, cap, forbidHistory); err != nil {
			return err
		}
		if queue != nil {
			return queue(tx, cap)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return command
}
