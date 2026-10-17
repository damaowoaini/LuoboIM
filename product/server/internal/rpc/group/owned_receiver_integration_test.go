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
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

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
	session, err := db.Client().StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.EndSession(ctx)
	opts := options.Transaction().SetReadConcern(readconcern.Snapshot()).SetReadPreference(readpref.Primary()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true)))
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (any, error) {
		for _, cid := range []string{"sg_g", "n_g"} {
			if err := store.InitializeSequence(tx, cid, messagedelivery.Seed{MongoKnown: true, RedisKnown: true, EvidenceSHA256: deliveryprofile.SHA([]byte("explicit test migration"))}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}, opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"owner", "admin", "member"} {
		window := messagedelivery.MembershipWindow{ID: deliveryprofile.Digest([]string{epoch, "membership-window", "sg_g\x00" + uid}), Kind: "membership-window-v1", Epoch: epoch, ConversationID: "sg_g", GroupID: "g", UserID: uid, Revision: 1, GroupRevision: 1, SeedEvidenceSHA256: deliveryprofile.SHA([]byte("original member seed"))}
		if _, err := db.Collection(messagedelivery.Sequences).InsertOne(ctx, window); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.CheckKafkaReady(ctx); err != nil {
		t.Fatal(err)
	}
	return store
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
			ctx, s, db, _ := roleEffectsFixture(t, nil, func() {
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
			count, err = db.Collection(messagedelivery.Receipts).CountDocuments(ctx, bson.M{"source_id": id})
			if err != nil || count != 1 {
				t.Fatal("recovery created duplicate source", err)
			}
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
