package group

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/testutil/riskmysql"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/riskcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type newGroupSeedRPCDB struct {
	controller.GroupDatabase
	db      *mongo.Database
	groups  relation.GroupModelInterface
	members relation.GroupMemberModelInterface
	writes  int
}

func (d *newGroupSeedRPCDB) TakeGroup(ctx context.Context, gid string) (*relation.GroupModel, error) {
	var row relation.GroupModel
	err := d.db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, errs.ErrRecordNotFound.Wrap("synthetic fresh group")
	}
	return &row, err
}
func (d *newGroupSeedRPCDB) CreateGroup(ctx context.Context, groups []*relation.GroupModel, members []*relation.GroupMemberModel) error {
	d.writes++
	if err := d.groups.Create(ctx, groups); err != nil {
		return err
	}
	return d.members.Create(ctx, members)
}

// Stop only AFTER real model writes and seed were observed in the same strong
// transaction. This tests RPC write admission+rollback, not concrete legacy
// notification success, known-commit reply or production migration readiness.
type newGroupSeedRPCAbort struct {
	authority *groupauthority.Store
	db        *mongo.Database
	stop      error
	receipt   messagedelivery.NewGroupSeedReceipt
	reached   bool
}

func (a *newGroupSeedRPCAbort) Mutation(ctx context.Context, targets []groupauthority.Target, authorize, write func(context.Context) error) (*groupauthority.Commit, error) {
	return a.authority.Mutation(ctx, targets, authorize, func(tx context.Context) error {
		if err := write(tx); err != nil {
			return err
		}
		if len(targets) != 1 || !targets[0].Create {
			return errors.New("RPC did not use the real Create scope")
		}
		if err := a.db.Collection(messagedelivery.Receipts).FindOne(tx, bson.M{"kind": "new-group-seed-pair-v1", "group_id": targets[0].GroupID}).Decode(&a.receipt); err != nil {
			return err
		}
		if a.receipt.MemberCount != 2 || a.receipt.Owner != "owner" || a.receipt.NormalCID != "sg_new_rpc" || a.receipt.NotificationCID != "n_new_rpc" {
			return errors.New("RPC seed receipt has unexpected identity/pair")
		}
		for _, pair := range []struct {
			name   string
			filter bson.M
			want   int64
		}{
			{"group", bson.M{"group_id": targets[0].GroupID, groupauthority.RevisionField: int64(1)}, 1},
			{"group_member", bson.M{"group_id": targets[0].GroupID}, 2},
			{"group_membership_history", bson.M{"group_id": targets[0].GroupID}, 2},
			{messagedelivery.Sequences, bson.M{"kind": "permanent-new-group-namespace-v1", "group_id": targets[0].GroupID}, 3},
			{messagedelivery.Sequences, bson.M{"kind": "membership-window-v1", "group_id": targets[0].GroupID}, 2},
			{messagedelivery.Sequences, bson.M{"conversation_id": bson.M{"$in": []string{"sg_new_rpc", "n_new_rpc"}}, "maximum": int64(0), "seed_evidence_sha256": a.receipt.EvidenceSHA256}, 2},
		} {
			if n, err := a.db.Collection(pair.name).CountDocuments(tx, pair.filter); err != nil || n != pair.want {
				return errors.Join(errors.New("RPC did not freeze complete initial namespace state"), err)
			}
		}
		a.reached = true
		return a.stop
	})
}

func TestCreateGroupActualRPCSeedsOnlyAuthorizedOwnerOrManagerAndAbortsAtomically(t *testing.T) {
	sql := riskmysql.Open(t)
	before := config.Config
	t.Cleanup(func() { config.Config = before })
	config.Config.Mysql = sql.Config()
	sql.ReplacePolicy(t, map[string]string{riskcontrol.RiskControlEnabledKey: "true", riskcontrol.RiskDisableGroupActionKey: "false"})
	config.Config.Manager.UserID = []string{"seed-manager"}
	config.Config.IMAdmin.UserID = nil
	config.Config.Callback.CallbackBeforeCreateGroup.Enable = false
	config.Config.Callback.CallbackBeforeMemberJoinGroup.Enable = false
	for _, actor := range []string{"owner", "seed-manager", "foreign"} {
		t.Run(actor, func(t *testing.T) {
			ctx, authority, db := groupRPCMongo(t, nil)
			for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts, messagedelivery.Effects, "msg", "message_visibility_floors"} {
				if err := db.CreateCollection(ctx, name); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Collection("msg").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "doc_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
				t.Fatal(err)
			}
			epoch, activation := uuid.NewString(), deliveryprofile.SHA([]byte("synthetic empty fixture inventory"))
			for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts, messagedelivery.Effects} {
				if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": messagedelivery.SchemaID, "version": int32(1), "epoch": epoch, "activation_sha256": activation, "legacy_writers_retired": true}); err != nil {
					t.Fatal(err)
				}
			}
			// Only this freshly claimed, still-group-empty synthetic DB may
			// use zero inventory. No global reader Ready rows are installed.
			proof := deliveryprofile.SHA([]byte("synthetic no preexisting CID or worker"))
			cut := messagedelivery.NamespaceCutover{ID: messagedelivery.NamespaceCutoverID, Kind: "immutable-namespace-cutover-v1", Database: db.Name(), Epoch: epoch, ActivationSHA256: activation, MigrationID: uuid.NewString(), State: "all-inventoried-namespaces-permanently-claimed", ManifestSHA256: proof, MongoInventorySHA256: proof, RedisInventorySHA256: proof, DrainSHA256: proof, OldBinaryFenceSHA256: proof, RegisteredAt: time.Now().UTC().Truncate(time.Millisecond)}
			if _, err := db.Collection(messagedelivery.Sequences).InsertOne(ctx, cut); err != nil {
				t.Fatal(err)
			}
			store, err := messagedelivery.NewStore(db, epoch, activation)
			if err != nil {
				t.Fatal(err)
			}
			groups, err := mgo.NewGroupMongoContext(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			members, err := mgo.NewGroupMemberContext(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			database := &newGroupSeedRPCDB{db: db, groups: groups, members: members}
			users := &joinApplicantUserClient{}
			stop := errors.New("synthetic RPC rollback after actual seed")
			gate := &newGroupSeedRPCAbort{authority: authority, db: db, stop: stop}
			server := &groupServer{db: database, User: rpcclient.UserRpcClient{Client: users}, authority: gate, newGroups: store}
			ctx = context.WithValue(context.WithValue(ctx, constant.OperationID, "synthetic-new-group-seed"), constant.OpUserID, actor)
			if actor == "owner" && authverify.IsAppManagerUid(ctx) {
				t.Fatal("ordinary owner acquired manager bypass")
			}
			resp, err := server.CreateGroup(ctx, &pbgroup.CreateGroupReq{OwnerUserID: "owner", MemberUserIDs: []string{"member"}, GroupInfo: &sdkws.GroupInfo{GroupID: "new_rpc", CreatorUserID: "owner", GroupType: constant.WorkingGroup}})
			if actor == "foreign" {
				if resp != nil || !errs.ErrNoPermission.Is(err) || users.calls != 0 || database.writes != 0 || gate.reached {
					t.Fatal("foreign creation crossed an owner/data dependency", err)
				}
			} else {
				if resp != nil || !errors.Is(err, stop) || database.writes != 1 || !gate.reached || gate.receipt.Actor != actor {
					t.Fatal("legal RPC did not reach actual seed and exact rollback", err)
				}
			}
			for _, name := range []string{"group", "group_member", "group_membership_history", messagedelivery.Sequences, messagedelivery.Receipts} {
				if n, err := db.Collection(name).CountDocuments(ctx, bson.M{"group_id": "new_rpc"}); err != nil || n != 0 {
					t.Fatal("aborted/denied RPC left durable group/claim state", name, err)
				}
			}
		})
	}
}
