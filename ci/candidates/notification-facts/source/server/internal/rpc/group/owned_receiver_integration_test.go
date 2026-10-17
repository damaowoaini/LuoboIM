package group

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push"
	"github.com/openimsdk/open-im-server/v3/internal/testutil/riskmysql"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/riskcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

import (
	"reflect"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/event"
)

// This one owned-receiver fixture starts without business groups. Reuse the
// existing nonce DB/primary/drop/disconnect and notice setup, then create real
// groups through the private same-TX NewGroupSeed path below. No old group is
// deleted/adopted and no permanent claim or seed capability is hand-built.
func ownedRPCFreshFixture(t *testing.T, monitor *event.CommandMonitor, configure func()) (context.Context, *groupServer, *mongo.Database, *groupeffects.Store) {
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

func ownedRPCSeed(t *testing.T, ctx context.Context, db *mongo.Database, epoch, activation string) *messagedelivery.Store {
	t.Helper()
	for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts, messagedelivery.Effects, "msg", "message_visibility_floors"} {
		if err := db.CreateCollection(ctx, name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Collection("msg").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "doc_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts, messagedelivery.Effects} {
		if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": messagedelivery.SchemaID, "version": int32(1), "epoch": epoch, "activation_sha256": activation, "legacy_writers_retired": true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts} {
		if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": messagedelivery.MembershipSchemaID, "version": int32(1), "epoch": epoch, "activation_sha256": activation, "legacy_message_writers_retired": true, "legacy_group_writers_retired": true, "owner_bounds_seeded": true, "readers_bound": true}); err != nil {
			t.Fatal(err)
		}
	}
	store, err := messagedelivery.NewMembershipBoundStore(db, epoch, activation)
	if err != nil {
		t.Fatal(err)
	}
	// Fixture-only historical cutover: this fresh exclusive nonce DB has no
	// groups, messages, old counters or legacy workers. This is not production
	// drain/lifecycle evidence and never activates CheckNewGroupSeedRuntimeReady.
	for _, name := range []string{"group", "group_member", "msg"} {
		if n, err := db.Collection(name).CountDocuments(ctx, bson.M{}); err != nil || n != 0 {
			t.Fatal("fresh fixture was not empty before creation", err)
		}
	}
	for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts} {
		if n, err := db.Collection(name).CountDocuments(ctx, bson.M{"_id": bson.M{"$nin": []string{messagedelivery.SchemaID, messagedelivery.MembershipSchemaID}}}); err != nil || n != 0 {
			t.Fatal("fixture contains unclaimed old namespace state", err)
		}
	}
	cutover := messagedelivery.NamespaceCutover{ID: messagedelivery.NamespaceCutoverID, Kind: "immutable-namespace-cutover-v1", Database: db.Name(), Epoch: epoch, ActivationSHA256: activation, MigrationID: uuid.NewString(), State: "all-inventoried-namespaces-permanently-claimed", ManifestSHA256: deliveryprofile.SHA([]byte("fixture empty historical manifest")), MongoInventorySHA256: deliveryprofile.SHA([]byte("actual empty owned nonce collections")), RedisInventorySHA256: deliveryprofile.SHA([]byte("synthetic no legacy Redis writer or namespace")), DrainSHA256: deliveryprofile.SHA([]byte("no worker exists in fresh fixture")), OldBinaryFenceSHA256: deliveryprofile.SHA([]byte("test-only isolated fixture, never runtime activation")), NamespaceCount: 0, ClaimedCount: 0, RegisteredAt: time.Now().UTC().Truncate(time.Millisecond)}
	if _, err := db.Collection(messagedelivery.Sequences).InsertOne(ctx, cutover); err != nil {
		t.Fatal(err)
	}
	authority, err := groupauthority.NewStore(db)
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
	for _, gid := range []string{"g", "h"} {
		at := time.Unix(1, 0).UTC()
		group := &relation.GroupModel{GroupID: gid, Status: constant.GroupOk, GroupType: constant.WorkingGroup, CreatorUserID: "owner", CreateTime: at}
		rows := []*relation.GroupMemberModel{{GroupID: gid, UserID: "owner", RoleLevel: constant.GroupOwner, Nickname: "owner", FaceURL: "synthetic-avatar", JoinTime: at}, {GroupID: gid, UserID: "admin", RoleLevel: constant.GroupAdmin, Nickname: "admin", FaceURL: "synthetic-avatar", JoinTime: at}, {GroupID: gid, UserID: "member", RoleLevel: constant.GroupOrdinaryUsers, Nickname: "member", FaceURL: "synthetic-avatar", JoinTime: at}}
		creation, command, err := store.NewGroupCreationCommand(statusActor(ctx, "owner"), "owner", group, rows)
		if err != nil {
			t.Fatal(err)
		}
		var receipt messagedelivery.NewGroupSeedReceipt
		_, err = authority.Mutation(creation, []groupauthority.Target{{GroupID: gid, Create: true, Members: []string{"owner", "admin", "member"}}}, func(context.Context) error { return nil }, func(tx context.Context) error {
			reservation, err := store.BeginNewGroupSeed(tx, gid)
			if err != nil {
				return err
			}
			if err = groups.Create(tx, []*relation.GroupModel{group}); err != nil {
				return err
			}
			if err = members.Create(tx, rows); err != nil {
				return err
			}
			receipt, err = store.FinishNewGroupSeed(tx, reservation)
			return err
		})
		if err != nil {
			t.Fatal("real fixture creation/seed failed", err)
		}
		resolved, err := store.InspectNewGroupSeed(ctx, command.ID)
		if err != nil || resolved.ID != receipt.ID || resolved.MemberCount != 3 || receipt.NormalCID != msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, gid) || receipt.NotificationCID != msgprocessor.GetNotificationConversationID(constant.SuperGroupChatType, gid) {
			t.Fatal("committed group/seed receipt mismatch", err)
		}
	}
	if err := store.CheckKafkaReady(ctx); err != nil {
		t.Fatal(err)
	}
	return store
}

// Facts add legitimate rows to the shared Receipts collection. Still demand
// exactly one actual source receipt, and inspect ALL same-source rows to reject
// unknown extras rather than silently filtering them out. The expected facts
// count comes from the real accepted branch plan, not an arbitrary one-row cap.
func assertOwnedRPCFactsRows(t *testing.T, ctx context.Context, db *mongo.Database, epoch, activation string, source storedRoleEffect, receipt messagedelivery.Receipt) {
	t.Helper()
	notice := msgprocessor.GetNotificationConversationID(constant.SuperGroupChatType, source.GroupID)
	wanted := map[string]messagedelivery.AcceptedBranch{}
	for _, branch := range receipt.Branches {
		if branch.ConversationID == notice && branch.Persist && branch.Seq > 0 {
			id := deliveryprofile.Digest([]string{epoch, "owned-notification-facts", branch.ID})
			if _, exists := wanted[id]; exists {
				t.Fatal("duplicate accepted facts branch")
			}
			wanted[id] = branch
		}
	}
	cur, err := db.Collection(messagedelivery.Receipts).Find(ctx, bson.M{"source_id": source.ID})
	if err != nil {
		t.Fatal(err)
	}
	var all []bson.Raw
	if err = cur.All(ctx, &all); err != nil {
		t.Fatal(err)
	}
	if err = cur.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if len(all) != 1+len(wanted) {
		t.Fatal("unexpected source/facts rows in unfiltered source query", len(all))
	}
	sourceSeen := false
	for _, raw := range all {
		id, ok := raw.Lookup("_id").StringValueOK()
		if !ok {
			t.Fatal("source/facts row ID malformed")
		}
		if id == receipt.ID {
			var actual messagedelivery.Receipt
			if sourceSeen || bson.Unmarshal(raw, &actual) != nil || actual.SourceID != source.ID || actual.Epoch != epoch || actual.InputSHA256 != receipt.InputSHA256 || actual.AcceptedAt.UnixMilli() != receipt.AcceptedAt.UnixMilli() || !reflect.DeepEqual(actual.Branches, receipt.Branches) {
				t.Fatal("original source receipt changed")
			}
			sourceSeen = true
			continue
		}
		branch, ok := wanted[id]
		if !ok {
			t.Fatal("unknown extra receipt/facts record")
		}
		var fact struct {
			Kind       string `bson:"kind"`
			FactsOnly  bool   `bson:"facts_only"`
			Database   string `bson:"database"`
			Epoch      string `bson:"epoch"`
			Activation string `bson:"activation_sha256"`
			Source     string `bson:"source_id"`
			Operation  string `bson:"source_operation_id"`
			Command    string `bson:"command_id"`
			Group      string `bson:"group_id"`
			Actor      string `bson:"actor"`
			Producer   string `bson:"producer_version"`
			Branch     string `bson:"branch_id"`
			CID        string `bson:"conversation_id"`
			Seq        int64  `bson:"seq"`
			Hidden     bool   `bson:"accepted_hidden"`
			InputSHA   string `bson:"source_input_sha256"`
			Recipients int64  `bson:"recipient_count"`
		}
		if bson.Unmarshal(raw, &fact) != nil || fact.Kind != "owned-notification-facts-v1" || !fact.FactsOnly || fact.Database != db.Name() || fact.Epoch != epoch || fact.Activation != activation || fact.Source != source.ID || fact.Operation != source.OperationID || fact.Command != source.Order.CommandID || fact.Group != source.GroupID || fact.Actor != source.Actor || fact.Producer != source.Producer || fact.Branch != branch.ID || fact.CID != branch.ConversationID || fact.Seq != branch.Seq || fact.Hidden != branch.Hidden || fact.InputSHA != receipt.InputSHA256 {
			t.Fatal("facts borrowed another source/command/branch")
		}
		foundTarget := false
		for _, target := range source.Targets {
			if target.BranchID == branch.ID {
				if foundTarget || fact.Recipients != int64(len(target.UserIDs)) {
					t.Fatal("facts recipients do not match frozen branch")
				}
				foundTarget = true
			}
		}
		if !foundTarget {
			t.Fatal("facts branch not in original frozen plan")
		}
		delete(wanted, id)
	}
	if !sourceSeen || len(wanted) != 0 {
		t.Fatal("source receipt or branch facts missing")
	}
}

// Actual member RPC -> same-TX profile/cut/source -> real signed loopback HTTP
// -> durable receiver. No real Kafka/vendor/device is impersonated here.
func TestOwnedRoleRPCReceiverRiskSQLSignedCallbackUnknownRestartAndReceiptFirst(t *testing.T) {
	sql := riskmysql.Open(t)
	for _, persist := range []bool{false, true} {
		name := "silent"
		if persist {
			name = "stored-n"
		}
		t.Run(name, func(t *testing.T) {
			var posts atomic.Int64
			var drop atomic.Bool
			drop.Store(true)
			const secret = "synthetic-owned-receiver-signing-key"
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				if err != nil {
					t.Error("synthetic callback body read failed")
					w.WriteHeader(500)
					return
				}
				stamp, nonce := r.Header.Get("X-OpenIM-Callback-Timestamp"), r.Header.Get("X-OpenIM-Callback-Nonce")
				mac := hmac.New(sha256.New, []byte(secret))
				mac.Write([]byte(stamp + "\n" + nonce + "\n" + strings.TrimPrefix(r.URL.Path, "/") + "\n"))
				mac.Write(body)
				if stamp == "" || nonce == "" || r.Header.Get("X-OpenIM-Callback-Signature") != hex.EncodeToString(mac.Sum(nil)) {
					t.Error("actual callback signature invalid")
					w.WriteHeader(403)
					return
				}
				posts.Add(1)
				if drop.Load() {
					h, ok := w.(http.Hijacker)
					if !ok {
						t.Error("synthetic HTTP fault unsupported")
						return
					}
					connection, _, err := h.Hijack()
					if err == nil {
						_ = connection.Close()
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"actionCode":0}`))
			}))
			defer server.Close()
			ctx, s, db, _ := ownedRPCFreshFixture(t, nil, func() {
				config.Config.Callback.CallbackUrl = server.URL
				config.Config.Secret = secret
				config.Config.Callback.CallbackBeforeSendGroupMsg = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
				config.Config.Envs.Discovery = "zookeeper"
				config.Config.Push.Enable = "none"
				config.Config.Notification.GroupMemberInfoSet = config.NotificationConf{IsSendMsg: false, ReliabilityLevel: constant.UnreliableNotification}
				config.Config.Notification.GroupMemberSetToAdmin = config.NotificationConf{IsSendMsg: false, ReliabilityLevel: constant.UnreliableNotification}
				if persist {
					config.Config.Notification.GroupMemberSetToAdmin.ReliabilityLevel = constant.ReliableNotificationNoMsg
				}
			})
			epoch, activation := uuid.NewString(), deliveryprofile.SHA([]byte("owned RPC explicit migrated epoch"))
			store := ownedRPCSeed(t, ctx, db, epoch, activation)
			notices, err := newOwnedGroupNoticeRuntime(db, epoch, activation, "")
			if err != nil {
				t.Fatal(err)
			}
			s.notices = notices
			profile, err := deliveryprofile.FreezeGroup(db.Name(), epoch, activation, "")
			if err != nil {
				t.Fatal(err)
			}
			profileSHA, _ := profile.Fingerprint()
			dir := t.TempDir()
			if os.Chmod(dir, 0700) != nil {
				t.Fatal("private callback fixture unavailable")
			}
			registry := map[string]any{"version": "owned-group-message-callbacks-v1", "database": db.Name(), "epoch": epoch, "activationSHA256": activation, "entries": []any{map[string]any{"profileSHA256": profileSHA, "base": server.URL, "secret": secret, "before": config.Config.Callback.CallbackBeforeSendGroupMsg, "modify": config.CallBackConfig{}, "after": config.CallBackConfig{}, "superGroup": config.CallBackConfig{}, "online": config.CallBackConfig{}}}}
			raw, _ := json.Marshal(registry)
			path := filepath.Join(dir, "callbacks.json")
			if os.WriteFile(path, raw, 0600) != nil {
				t.Fatal("private callback fixture unavailable")
			}
			engines, err := push.LoadOwnedGroupMessageCallbacks(ctx, store, push.OwnedKafkaCallbackFile{Path: path, ExpectedSHA256: deliveryprofile.SHA(raw)}, db.Name(), epoch, activation, []deliveryprofile.Group{profile})
			if err != nil {
				t.Fatal(err)
			}
			defer engines[profileSHA].Close()
			sql.ReplacePolicy(t, map[string]string{riskcontrol.RiskControlEnabledKey: "true", riskcontrol.RiskDisableGroupActionKey: "false", riskcontrol.RiskDisableGroupMsgKey: "true", riskcontrol.RiskControlStartKey: "00:00", riskcontrol.RiskControlEndKey: "00:00"})
			config.Config.Mysql = sql.Config()
			role := int32(60)
			if _, err = s.SetGroupMemberInfo(statusActor(ctx, "owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", &role, nil, nil)}}); err != nil {
				t.Fatal("actual ordinary owner role RPC failed", err)
			}
			rows := roleEffects(t, ctx, db)
			if len(rows) != 1 {
				t.Fatal("real RPC did not commit one typed source")
			}
			id := rows[0].ID
			if _, err = store.RecoverOwnedGroupNotification(ctx, id, engines[profileSHA]); !errors.Is(err, messagedelivery.ErrOwnedMessageRejected) || posts.Load() != 0 {
				t.Fatal("actual SQL message policy did not reject before callback/receipt", err)
			}
			count, err := db.Collection(messagedelivery.Receipts).CountDocuments(ctx, bson.M{"source_id": id})
			if err != nil || count != 0 {
				t.Fatal("blocked message acquired accepted source", err)
			}
			sql.ReplacePolicy(t, map[string]string{riskcontrol.RiskControlEnabledKey: "true", riskcontrol.RiskDisableGroupActionKey: "false", riskcontrol.RiskDisableGroupMsgKey: "false"})
			if _, err = store.RecoverOwnedGroupNotification(ctx, id, engines[profileSHA]); !errors.Is(err, messagedelivery.ErrOwnedMessageUnknown) || posts.Load() != 1 {
				t.Fatal("dropped HTTP ACK lost durable unknown", err)
			}
			restarted, err := messagedelivery.NewMembershipBoundStore(db, epoch, activation)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.RecoverOwnedGroupNotification(ctx, id, engines[profileSHA]); !errors.Is(err, messagedelivery.ErrOwnedMessageUnknown) || posts.Load() != 1 {
				t.Fatal("restart replayed unknown callback", err)
			}
			expected, err := store.InspectOwnedMessageCallback(ctx, id, "before-message")
			if err != nil {
				t.Fatal(err)
			}
			if err = restarted.ConfirmOwnedMessageCallback(ctx, expected, "not-applied", deliveryprofile.SHA([]byte("independently known not applied")), messagedelivery.KafkaAttemptDrainProof{InstanceID: expected.InstanceID, EvidenceSHA256: deliveryprofile.SHA([]byte("actual worker joined before operator confirmation"))}); err != nil {
				t.Fatal(err)
			}
			drop.Store(false)
			result, err := restarted.RecoverOwnedGroupNotification(ctx, id, engines[profileSHA])
			if err != nil || posts.Load() != 2 || len(result.Branches) != 1 {
				t.Fatal("explicit confirmed retry did not resolve original source", err)
			}
			branch := result.Branches[0]
			protocolCID := msgprocessor.GetNotificationConversationIDByMsg(&sdkws.MsgData{SessionType: constant.SuperGroupChatType, GroupID: "g"})
			if branch.ConversationID != protocolCID || branch.ConversationID != "n_g" || branch.Persist != persist || persist && branch.Seq != 1 || !persist && branch.Seq != 0 {
				t.Fatal("silent/stored n_ configuration changed")
			}
			again, err := restarted.RecoverOwnedGroupNotification(ctx, id, engines[profileSHA])
			if err != nil || again.InputSHA256 != result.InputSHA256 || posts.Load() != 2 {
				t.Fatal("accepted source did not settle receipt-first", err)
			}
			count, err = db.Collection(messagedelivery.Receipts).CountDocuments(ctx, bson.M{"_id": result.ID, "epoch": epoch, "source_id": id, "branches": bson.M{"$exists": true}, "accepted_at": bson.M{"$exists": true}})
			if err != nil || count != 1 {
				t.Fatal("recovery created duplicate source", err)
			}
			assertOwnedRPCFactsRows(t, ctx, db, epoch, activation, rows[0], result)
			if !persist {
				transportSHA, _ := messagedelivery.KafkaDeliveryPolicy(profile.Transport).Fingerprint()
				gateway := &ownedSilentGateway{store: restarted, policy: transportSHA}
				if err := restarted.RecoverOwnedActionOnline(ctx, branch.ID, ownedSilentReader{}, ownedSilentProjection{}, gateway); !errors.Is(err, messagedelivery.ErrOnlineUnconfirmed) || gateway.calls.Load() != 1 {
					t.Fatal("Seq0 did not persist partial unknown gateway outcome", err)
				}
				summary, err := restarted.InspectOwnedSilent(ctx, branch.ID)
				if err != nil || summary.State != "unconfirmed" {
					t.Fatal("Seq0 unknown receipt unavailable", err)
				}
				another, err := messagedelivery.NewMembershipBoundStore(db, epoch, activation)
				if err != nil {
					t.Fatal(err)
				}
				gateway.store = another
				if err := another.RecoverOwnedActionOnline(ctx, branch.ID, ownedSilentReader{}, ownedSilentProjection{}, gateway); !errors.Is(err, messagedelivery.ErrOnlineUnconfirmed) || gateway.calls.Load() != 1 {
					t.Fatal("restart replayed unknown Seq0", err)
				}
				var duty struct {
					Recipients []string          `bson:"recipients"`
					ACKs       map[string]string `bson:"online_acks"`
				}
				if err := db.Collection(messagedelivery.Effects).FindOne(ctx, bson.M{"branch_id": branch.ID}).Decode(&duty); err != nil {
					t.Fatal(err)
				}
				report := messagedelivery.OnlineReport{DispatchStarted: true}
				for _, uid := range duty.Recipients {
					if duty.ACKs[deliveryprofile.SHA([]byte(uid))] != "" {
						continue
					}
					sha := deliveryprofile.SHA([]byte("independently observed remaining route " + uid))
					report.ACKs = append(report.ACKs, messagedelivery.OnlineACK{UserID: uid, ResponseSHA256: sha})
					report.Routes = append(report.Routes, messagedelivery.OnlineRouteResult{UserID: uid, ResponseSHA256: sha, OnlineAccepted: true})
				}
				drain := messagedelivery.KafkaAttemptDrainProof{InstanceID: summary.InstanceID, EvidenceSHA256: deliveryprofile.SHA([]byte("old local worker joined; independent remote receipt checked"))}
				if err := another.ConfirmOwnedSilentApplied(ctx, summary, report, deliveryprofile.SHA([]byte("independent original Seq0 outcomes")), drain); err != nil {
					t.Fatal(err)
				}
				if err := another.ConfirmOwnedSilentApplied(ctx, summary, report, deliveryprofile.SHA([]byte("stale proof")), drain); !errors.Is(err, messagedelivery.ErrConflict) {
					t.Fatal("old Seq0 receipt borrowed new revision", err)
				}
				if gateway.calls.Load() != 1 {
					t.Fatal("operator confirmation performed a gateway send")
				}
			}

			// A second real RPC commits plaintext, then the real visibility DAO clears
			// before any callback/physical admission. Recovery cannot renew this source.
			nextName := "synthetic post-receipt profile"
			if _, err := s.SetGroupMemberInfo(statusActor(ctx, "owner"), &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{memberEdit("g", "member", nil, &nextName, nil)}}); err != nil {
				t.Fatal(err)
			}
			allRows := roleEffects(t, ctx, db)
			nextID := ""
			for _, row := range allRows {
				if row.ID != id {
					nextID = row.ID
				}
			}
			if nextID == "" {
				t.Fatal("second real RPC produced no source")
			}
			driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, restarted)
			if err != nil {
				t.Fatal(err)
			}
			if err := driver.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "sg_g", MinSeq: 1}}); err != nil {
				t.Fatal("actual source Clear transaction failed", err)
			}
			cleared, err := restarted.RecoverOwnedGroupNotification(ctx, nextID, engines[profileSHA])
			if err != nil || posts.Load() != 2 {
				t.Fatal("Clear-before-admission dispatched stale plaintext", err)
			}
			if len(cleared.Branches) != 1 || !cleared.Branches[0].Hidden {
				t.Fatal("cleared source was rebuilt as visible")
			}
			var final bson.M
			if err := db.Collection(groupeffects.Collection).FindOne(ctx, bson.M{"_id": nextID}).Decode(&final); err != nil {
				t.Fatal(err)
			}
			if _, exists := final["payload"]; exists {
				t.Fatal("cleared pending source retained plaintext")
			}

		})
	}
}

type ownedSilentReader struct{}

func (ownedSilentReader) GetMsgBySeqs(context.Context, string, string, []int64) (int64, int64, []*sdkws.MsgData, error) {
	return 0, 0, nil, errors.New("Seq0 must not pretend to read a persisted source slot")
}

type ownedSilentProjection struct{}

func (ownedSilentProjection) RaiseMaximum(context.Context, string, int64) error { return nil }
func (ownedSilentProjection) RaiseSenderRead(context.Context, string, string, int64) error {
	return nil
}

type ownedSilentGateway struct {
	store  *messagedelivery.Store
	policy string
	calls  atomic.Int64
}

func (g *ownedSilentGateway) PolicyFingerprint() string { return g.policy }
func (g *ownedSilentGateway) Deliver(ctx context.Context, call messagedelivery.OnlineCall) (messagedelivery.OnlineReport, error) {
	if err := g.store.AcquireOwnedSilentPhysical(ctx, call); err != nil {
		return messagedelivery.OnlineReport{}, err
	}
	g.calls.Add(1)
	report := messagedelivery.OnlineReport{DispatchStarted: true}
	if len(call.Recipients) > 0 {
		uid := call.Recipients[0]
		sha := deliveryprofile.SHA([]byte("exact first gateway outcome"))
		report.ACKs = []messagedelivery.OnlineACK{{UserID: uid, ResponseSHA256: sha}}
		report.Routes = []messagedelivery.OnlineRouteResult{{UserID: uid, ResponseSHA256: sha, OnlineAccepted: true}}
	}
	return report, errors.New("synthetic gateway ACK incomplete after physical permit")
}
