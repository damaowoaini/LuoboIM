package msgtransfer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

func offlineWorkerKey(epoch, kind, id string) string {
	b, _ := json.Marshal([]string{epoch, kind, id})
	h, _ := kafkaOwnedDigest(json.RawMessage(b))
	return h
}

// Reuse the same fixture authority contract: exact loopback RS, nonce DB must
// not exist, exclusive claim ACK, no failpoint, exact claimed drop+disconnect.
func offlineWorkerMongo(t *testing.T) (context.Context, *mongo.Database, string, string, *atomic.Bool) {
	t.Helper()
	raw := os.Getenv("LUOBO_OFFLINE_CALLBACK_R1_MONGO_URI")
	if raw == "" {
		raw = os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")
	}
	if raw == "" {
		t.Skip("explicit owned Mongo fixture required")
	}
	if raw != "mongodb://127.0.0.1:38417/?replicaSet=luoboIntegration" && raw != "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration" {
		t.Fatal("exact owned replica URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(raw).SetDirect(true).SetRetryWrites(false).SetServerSelectionTimeout(2*time.Second))
	if err != nil {
		t.Fatal("owned fixture connection failed")
	}
	t.Cleanup(func() {
		end, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := client.Disconnect(end); err != nil {
			t.Error("owned fixture disconnect unconfirmed", err)
		}
	})
	var hello bson.M
	if err := client.Database("admin").RunCommand(ctx, bson.M{"hello": 1}).Decode(&hello); err != nil || hello["setName"] != "luoboIntegration" || hello["isWritablePrimary"] != true {
		t.Fatal("owned writable RS required")
	}
	var nonce [24]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	name, claim := "luobo_offline_callback_test_"+hex.EncodeToString(nonce[:12]), hex.EncodeToString(nonce[12:])
	names, err := client.ListDatabaseNames(ctx, bson.M{"name": name})
	if err != nil || len(names) != 0 {
		t.Fatal("fresh DB absence unconfirmed")
	}
	db := client.Database(name)
	if err := db.CreateCollection(ctx, "callback_fixture_claim"); err != nil {
		t.Fatal("exclusive claim failed; no drop authorized")
	}
	if _, err := db.Collection("callback_fixture_claim").InsertOne(ctx, bson.M{"_id": "task-claim", "nonce": claim}); err != nil {
		t.Fatal("claim ACK unconfirmed; retain DB")
	}
	canDrop := &atomic.Bool{}
	canDrop.Store(true)
	t.Cleanup(func() {
		end, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if !canDrop.Load() {
			t.Error("worker drain unconfirmed; retain fixture")
			return
		}
		if db.Name() != name {
			t.Error("DB binding changed")
			return
		}
		if err := db.Collection("callback_fixture_claim").FindOne(end, bson.M{"_id": "task-claim", "nonce": claim}).Err(); err != nil {
			t.Error("claim unconfirmed; retain DB", err)
			return
		}
		if err := db.Drop(end); err != nil {
			t.Error("owned drop unconfirmed", err)
			return
		}
		left, err := client.ListDatabaseNames(end, bson.M{"name": name})
		if err != nil || len(left) != 0 {
			t.Error("owned DB removal unconfirmed", err)
		}
	})
	epoch, activation := uuid.NewString(), kafkaHashForFixture("synthetic worker activation")
	for _, collection := range []string{"group", "group_member", groupauthority.InvalidationsCollection, messagedelivery.Sequences, messagedelivery.Receipts, messagedelivery.Effects, "msg", "message_visibility_floors"} {
		if err := db.CreateCollection(ctx, collection); err != nil {
			t.Fatal(err)
		}
	}
	for _, index := range []struct {
		collection string
		keys       bson.D
	}{{"group", bson.D{{Key: "group_id", Value: 1}}}, {"group_member", bson.D{{Key: "group_id", Value: 1}, {Key: "user_id", Value: 1}}}, {"msg", bson.D{{Key: "doc_id", Value: 1}}}} {
		if _, err := db.Collection(index.collection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: index.keys, Options: options.Index().SetUnique(true)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, collection := range []string{messagedelivery.Sequences, messagedelivery.Receipts, messagedelivery.Effects} {
		if _, err := db.Collection(collection).InsertOne(ctx, bson.M{"_id": messagedelivery.SchemaID, "version": int32(1), "epoch": epoch, "activation_sha256": activation, "legacy_writers_retired": true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, collection := range []string{messagedelivery.Sequences, messagedelivery.Receipts} {
		if _, err := db.Collection(collection).InsertOne(ctx, bson.M{"_id": messagedelivery.MembershipSchemaID, "version": int32(1), "epoch": epoch, "activation_sha256": activation, "legacy_message_writers_retired": true, "legacy_group_writers_retired": true, "owner_bounds_seeded": true, "readers_bound": true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", "status": int32(constant.GroupOk), "group_type": int32(constant.WorkingGroup), groupauthority.RevisionField: int64(1)}); err != nil {
		t.Fatal(err)
	}
	for user, role := range map[string]int32{"owner": constant.GroupOwner, "member": constant.GroupOrdinaryUsers} {
		if _, err := db.Collection("group_member").InsertOne(ctx, bson.M{"group_id": "g", "user_id": user, "role_level": role, "join_time": time.Unix(1, 0).UTC()}); err != nil {
			t.Fatal(err)
		}
		w := messagedelivery.MembershipWindow{ID: offlineWorkerKey(epoch, "membership-window", "sg_g\x00"+user), Kind: "membership-window-v1", Epoch: epoch, ConversationID: "sg_g", GroupID: "g", UserID: user, Revision: 1, GroupRevision: 1, SeedEvidenceSHA256: kafkaHashForFixture("synthetic seed")}
		if _, err := db.Collection(messagedelivery.Sequences).InsertOne(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, db, epoch, activation, canDrop
}
func kafkaHashForFixture(value string) string { // Actual source SHA helper, no secret material.
	body := []byte(value)
	sum := sha256SumForOfflineFixture(body)
	return hex.EncodeToString(sum[:])
}
func sha256SumForOfflineFixture(body []byte) [32]byte { return sha256.Sum256(body) }

type offlineWorkerReader struct{ message *sdkws.MsgData }

func (r offlineWorkerReader) GetMsgBySeqs(context.Context, string, string, []int64) (int64, int64, []*sdkws.MsgData, error) {
	return 1, 0, []*sdkws.MsgData{proto.Clone(r.message).(*sdkws.MsgData)}, nil
}

type offlineWorkerProjection struct{}

func (offlineWorkerProjection) RaiseMaximum(context.Context, string, int64) error { return nil }
func (offlineWorkerProjection) RaiseSenderRead(context.Context, string, string, int64) error {
	return nil
}

type offlineWorkerGateway struct{ fingerprint string }

func (g offlineWorkerGateway) PolicyFingerprint() string { return g.fingerprint }
func (g offlineWorkerGateway) Deliver(_ context.Context, call messagedelivery.OnlineCall) (messagedelivery.OnlineReport, error) {
	out := messagedelivery.OnlineReport{DispatchStarted: true}
	for _, user := range call.Recipients {
		proof := kafkaHashForFixture("synthetic exact gateway " + user)
		out.ACKs = append(out.ACKs, messagedelivery.OnlineACK{UserID: user, ResponseSHA256: proof})
		out.Routes = append(out.Routes, messagedelivery.OnlineRouteResult{UserID: user, ResponseSHA256: proof, OnlineAccepted: user == "owner"})
	}
	return out, nil
}

type tracedOwnedNoVendor struct {
	actual   *ownedNoVendorOffline
	observed int
}

func (t *tracedOwnedNoVendor) PolicyFingerprint() string            { return t.actual.PolicyFingerprint() }
func (t *tracedOwnedNoVendor) CheckReady(ctx context.Context) error { return t.actual.CheckReady(ctx) }
func (t *tracedOwnedNoVendor) PrepareOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	return t.actual.PrepareOffline(ctx, call)
}
func (t *tracedOwnedNoVendor) Close() error { return t.actual.Close() }
func (t *tracedOwnedNoVendor) DeliverOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	m, err := t.actual.store.KafkaOfflineProviderMessage(ctx, call)
	if err != nil {
		return err
	}
	if m.OfflinePushInfo == nil || m.OfflinePushInfo.Title != "callback title" {
		return errors.New("exact callback output did not reach real adapter")
	}
	t.observed += len(call.DispatchRecipients)
	return t.actual.DeliverOffline(ctx, call)
}
func offlineWorkerPrivateDoc(t *testing.T, root, name string, value any) (string, string) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, kafkaHashForFixture(string(body))
}
func TestManagedOfflineMongoPrivateFactoryWorkerAndRealCallbackHTTPStayInOneBoundLifecycle(t *testing.T) {
	ctx, db, epoch, activation, canDrop := offlineWorkerMongo(t)
	var callbacks atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbacks.Add(1)
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var request callbackstruct.CallbackBeforePushReq
		if json.Unmarshal(body, &request) != nil || request.CallbackCommand != callbackstruct.CallbackOfflinePushCommand || !reflect.DeepEqual(request.UserIDList, []string{"member"}) || r.Header.Get("X-OpenIM-Callback-Signature") == "" {
			t.Error("real callback DTO/signature/candidates not preserved")
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"userIDList":["member","owner"],"offlinePushInfo":{"title":"callback title"}}`))
	}))
	t.Cleanup(server.Close)
	oldCallback, oldSecret, oldDiscovery, oldPush, oldEncryption := config.Config.Callback, config.Config.Secret, config.Config.Envs.Discovery, config.Config.Push.Enable, config.Config.LongConnSvr.IsEncryption
	t.Cleanup(func() {
		config.Config.Callback = oldCallback
		config.Config.Secret = oldSecret
		config.Config.Envs.Discovery = oldDiscovery
		config.Config.Push.Enable = oldPush
		config.Config.LongConnSvr.IsEncryption = oldEncryption
	})
	config.Config.Callback.CallbackUrl = server.URL
	config.Config.Callback.CallbackBeforeSuperGroupOnlinePush = config.CallBackConfig{}
	config.Config.Callback.CallbackOnlinePush = config.CallBackConfig{}
	config.Config.Callback.CallbackOfflinePush = config.CallBackConfig{Enable: true, CallbackTimeOut: 3}
	config.Config.Secret = "synthetic callback signing"
	config.Config.Envs.Discovery = "zookeeper"
	config.Config.Push.Enable = "none"
	config.Config.LongConnSvr.IsEncryption = false
	policy, err := FreezeOwnedKafkaPolicy(db.Name(), epoch, activation)
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	store, err := messagedelivery.NewMembershipBoundStore(db, epoch, activation)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.PrepareSequence(ctx, "sg_g", messagedelivery.Seed{MongoKnown: true, RedisKnown: true, EvidenceSHA256: kafkaHashForFixture("synthetic explicit zero seed")}); err != nil {
		t.Fatal(err)
	}
	message := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", ServerMsgID: uuid.NewString(), ClientMsgID: uuid.NewString(), ContentType: constant.Text, MsgFrom: constant.UserMsgType, SessionType: constant.SuperGroupChatType, CreateTime: 2000, SendTime: 2000, Content: []byte("synthetic original body"), Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(true), msgprocessor.WithHistory(true), msgprocessor.WithOfflinePush(true))}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	ownerContext := context.WithValue(ctx, constant.OpUserID, "owner")
	accepted, err := store.AcceptKafkaRecord(ownerContext, messagedelivery.KafkaRecord{Topic: "synthetic-owned-source", Partition: 0, Offset: 501, Key: []byte("sg_g"), Body: body, Actor: "owner", OperationID: "synthetic-operation", ContextSHA256: kafkaHashForFixture("synthetic headers")}, fingerprint, policy)
	if err != nil || len(accepted.Branches) != 1 {
		t.Fatal("source acceptance", err)
	}
	branch := accepted.Branches[0].ID
	message.Seq = accepted.Branches[0].Seq
	reader := offlineWorkerReader{message: message}
	if err := store.RecoverKafkaOnline(ownerContext, branch, reader, offlineWorkerProjection{}, offlineWorkerGateway{fingerprint: fingerprint}); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	registry := aliasvendor.OwnedOfflinePolicyRegistry{Version: "ordinary-owned-offline-policies-v1", Database: db.Name(), Epoch: epoch, ActivationSHA256: activation, CurrentPolicySHA256: fingerprint, Entries: []aliasvendor.OwnedOfflinePolicySpec{{Policy: policy, Fingerprint: fingerprint, BindingSeedSHA256: policy.ConfigurationSHA256}}}
	registryPath, registrySHA := offlineWorkerPrivateDoc(t, root, "vendors.json", registry)
	callbackPath, callbackSHA := offlineWorkerPrivateDoc(t, root, "callbacks.json", map[string]any{"version": "ordinary-offline-callbacks-v1", "database": db.Name(), "epoch": epoch, "activationSHA256": activation, "entries": []any{map[string]any{"policySHA256": fingerprint, "base": server.URL, "secret": "synthetic callback signing", "enable": true, "timeout": 3, "failedContinue": nil}}})
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", DB: 15, MaxRetries: -1, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() {
		if err := rdb.Close(); err != nil {
			t.Error(err)
		}
	})
	runtime, err := NewOwnedOrdinaryOfflineRuntime(ownerContext, db, rdb, cache.NewMsgCacheModel(rdb), reader, epoch, activation, kafkaHashForFixture("synthetic badge activation"), aliasvendor.OwnedInputFile{Path: registryPath, ExpectedSHA256: registrySHA}, push.OwnedKafkaCallbackFile{Path: callbackPath, ExpectedSHA256: callbackSHA})
	if err != nil {
		t.Fatal("real owned startup rejected complete material", err)
	}
	t.Cleanup(func() {
		end, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		if err := runtime.Stop(end); err != nil {
			canDrop.Store(false)
			t.Error("real runtime stop unconfirmed; retaining DB", err)
		}
	})
	actual, ok := runtime.worker.transports[fingerprint].(*ownedNoVendorOffline)
	if !ok {
		t.Fatal("real no-vendor adapter not assembled")
	}
	trace := &tracedOwnedNoVendor{actual: actual}
	runtime.worker.transports[fingerprint] = trace
	if err := runtime.Start(); err != nil {
		t.Fatal("real managed worker not started", err)
	}
	done := false
	for ctx.Err() == nil {
		var row struct {
			Pending bool `bson:"offline_pending"`
		}
		if err := db.Collection(messagedelivery.Effects).FindOne(ctx, bson.M{"branch_id": branch, "producer_version": "ordinary-kafka-v1"}).Decode(&row); err != nil {
			t.Fatal(err)
		}
		if !row.Pending {
			done = true
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Millisecond):
		}
	}
	if !done {
		t.Fatal("real worker did not settle original duty")
	}
	stop, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	drainPath := filepath.Join(root, "drained.json")
	if _, err := runtime.StopAndWriteDrain(stop, drainPath); err != nil {
		canDrop.Store(false)
		t.Fatal("real managed join/close/drain failed", err)
	}
	if callbacks.Load() != 1 || trace.observed != 2 {
		t.Fatal("callback or original recipient attempt duplicated/lost")
	}
	var slot table.MsgDocModel
	if err := db.Collection("msg").FindOne(ctx, bson.M{"doc_id": "sg_g:0"}).Decode(&slot); err != nil || len(slot.Msg) == 0 || slot.Msg[0].Msg == nil {
		t.Fatal("original history missing", err)
	}
	if original := convert.MsgDB2Pb(slot.Msg[0].Msg); original.OfflinePushInfo != nil || original.ClientMsgID != message.ClientMsgID {
		t.Fatal("callback changed original history")
	}
	var receipt bson.M
	if err := db.Collection(messagedelivery.Effects).FindOne(ctx, bson.M{"kind": "kafka-offline-callback-v1", "branch_id": branch}).Decode(&receipt); err != nil || receipt["output_erased"] != true {
		t.Fatal("private callback output not retired", err)
	}
	if _, found := receipt["offline_info"]; found {
		t.Fatal("settled callback retained private output")
	}
}
