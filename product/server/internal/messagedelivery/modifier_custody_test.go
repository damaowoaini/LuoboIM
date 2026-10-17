package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

func writeCustodyPolicyFixture(t *testing.T, p UnsupportedCustodyPolicy) (string, string) {
	t.Helper()
	root := t.TempDir()
	if e := os.Chmod(root, 0700); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(root, "approved-custody.json")
	body, e := json.Marshal(p)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, body, 0600); e != nil {
		t.Fatal(e)
	}
	return path, hash(body)
}
func custodyPolicyFixture(database, epoch, activation, cluster, group string) UnsupportedCustodyPolicy {
	h := namespaceimport.Header{Tenant: "synthetic-tenant", Database: database, StorageInstance: uuid.NewString(), ReplicaSet: "synthetic-rs", MongoProcess: "synthetic-process", MongoTopology: 1, RedisRunID: "synthetic-redis", RedisDB: 0, KafkaClusterID: "synthetic-real-shape-cluster", Epoch: epoch, ActivationSHA256: activation, MigrationID: uuid.NewString(), DeploymentPolicySHA256: hash([]byte("synthetic deployment scope"))}
	return UnsupportedCustodyPolicy{Version: "owned-unsupported-modifier-custody-v1", Database: database, Epoch: epoch, ActivationSHA256: activation, Header: h, HeaderSHA256: h.SHA256(), AdmissionGeneration: 1, ClusterConfigurationSHA256: cluster, ConsumerGroup: group, Ranges: []UnsupportedCustodyRange{{Encoding: OrdinaryModifierEncoding, Topic: "owned-source", Partition: 2, Start: 40, End: 44, SourceProofSHA256: hash([]byte("synthetic finite original range/exclusion proof")), DeadlineUTC: time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond).Format(time.RFC3339Nano)}}}
}
func TestUnsupportedCustodyOwnedPolicyRejectsMissingExpiredScopeAndFilesystemDrift(t *testing.T) {
	client, e := mongo.NewClient()
	if e != nil {
		t.Fatal(e)
	} // Constructor only: no connect/network.
	db := client.Database("synthetic_owned_fixture")
	epoch, activation, cluster := uuid.NewString(), hash([]byte("synthetic activation")), hash([]byte("synthetic cluster configuration"))
	for _, name := range []string{"exact-and-deadline", "wrong-source-range", "symlink", "changed-reviewed-bytes"} {
		t.Run(name, func(t *testing.T) {
			p := custodyPolicyFixture(db.Name(), epoch, activation, cluster, "owned-group")
			path, pin := writeCustodyPolicyFixture(t, p)
			if name == "symlink" {
				alias := filepath.Join(filepath.Dir(path), "alias.json")
				if e := os.Symlink(path, alias); e != nil {
					t.Fatal(e)
				}
				path = alias
			}
			c, e := LoadOwnedUnsupportedKafkaCustody(context.Background(), db, epoch, activation, cluster, "owned-group", path, pin)
			if name == "symlink" {
				if e == nil {
					t.Fatal("private policy symlink accepted")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			record := KafkaRecord{Topic: "owned-source", Partition: 2, Offset: 40}
			if name == "changed-reviewed-bytes" {
				if e := os.WriteFile(path, []byte("{}"), 0600); e != nil {
					t.Fatal(e)
				}
				if _, e := c.sourceRange(record, time.Now().UTC()); e == nil {
					t.Fatal("reviewed source policy replacement accepted")
				}
				return
			}
			if name == "wrong-source-range" {
				record.Offset = 44
				if _, e := c.sourceRange(record, time.Now().UTC()); !errors.Is(e, ErrUnsupportedCustodyScope) {
					t.Fatal("exclusive end/unknown source admitted", e)
				}
				return
			}
			scope, e := c.sourceRange(record, time.Now().UTC())
			if e != nil {
				t.Fatal(e)
			}
			deadline, _ := custodyDeadline(scope.DeadlineUTC)
			if _, e := c.sourceRange(record, deadline); !errors.Is(e, ErrUnsupportedCustodyExpired) {
				t.Fatal("expired policy still grants new custody ACK", e)
			}
			p.AdmissionGeneration = 0
			if p.valid(db.Name(), epoch, activation, cluster, "owned-group") {
				t.Fatal("implicit generation zero accepted")
			}
			p.AdmissionGeneration = 1
			p.Ranges[0].DeadlineUTC = ""
			if p.valid(db.Name(), epoch, activation, cluster, "owned-group") {
				t.Fatal("missing deadline became permanent custody")
			}
		})
	}
}

// A real claimed test database is bootstrapped explicitly by its private
// fixture. This does not fake an AdmissionHold or certify the production Agent
// exclusion flow; the separate admission test rejects bare/nil capabilities.
func fixtureModifierCustodyCatalog(t *testing.T, ctx context.Context, c *OwnedUnsupportedKafkaCustody) {
	t.Helper()
	s := c.store
	if e := s.db.CreateCollection(ctx, UnsupportedCustodyCollection); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Collection(UnsupportedCustodyCollection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "deadline", Value: 1}}, Options: options.Index().SetName(unsupportedCustodyTTL).SetExpireAfterSeconds(0)}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Collection(UnsupportedCustodyCollection).InsertOne(ctx, bson.M{"_id": unsupportedCustodySchema, "kind": unsupportedCustodyKind, "epoch": s.epoch, "activation_sha256": s.activationSHA, "version": int32(1)}); e != nil {
		t.Fatal(e)
	}
	if _, e := s.db.Collection(Receipts).InsertOne(ctx, unsupportedCustodyGeneration{ID: s.unsupportedCustodyGenerationID(), Kind: unsupportedCustodyGenerationKind, Epoch: s.epoch, Activation: s.activationSHA, HeaderSHA: c.policy.HeaderSHA256, InitialPolicySHA: c.inputSHA, HoldSHA: hash([]byte("synthetic fixture explicit preparation; not an Agent capability")), Generation: c.policy.AdmissionGeneration, Serial: 1}); e != nil {
		t.Fatal(e)
	}
	if e := c.CheckReady(ctx); e != nil {
		t.Fatal(e)
	}
}
func modifierCustodyMongoFixture(t *testing.T, encoding string) (context.Context, *Store, *mongo.Database, *OwnedUnsupportedKafkaCustody, KafkaOriginalRecord) {
	t.Helper()
	ctx, s, db := ordinaryEphemeralMongoFixture(t)
	r := kafkaFixtureRecord(t, 40)
	r.Topic = "owned-source"
	r.Partition = 2
	r.Offset = 40
	var message sdkws.MsgData
	if proto.Unmarshal(r.Body, &message) != nil {
		t.Fatal("synthetic legacy body")
	}
	message.ContentType = constant.ReactionMessageModifier
	message.Seq = 0
	var e error
	r.Body, e = proto.MarshalOptions{Deterministic: true}.Marshal(&message)
	if e != nil {
		t.Fatal(e)
	}
	headers := []KafkaOriginalHeader{{Key: []byte(constant.OperationID), Value: []byte(r.OperationID)}, {Key: []byte(constant.OpUserID), Value: []byte(r.Actor)}, {Key: []byte(constant.OpUserPlatform), Value: []byte("5")}, {Key: []byte(constant.ConnID), Value: []byte("synthetic-connection")}}
	r.ContextSHA256 = custodyDigest([]string{r.OperationID, r.Actor, "5", "synthetic-connection"})
	raw := KafkaOriginalRecord{Encoding: encoding, Record: r, Headers: headers, TimestampUTC: time.UnixMilli(1000).UTC().Format(time.RFC3339Nano), BlockTimestampUTC: time.UnixMilli(1000).UTC().Format(time.RFC3339Nano)}
	cluster := hash([]byte("synthetic cluster configuration"))
	p := custodyPolicyFixture(db.Name(), s.epoch, s.activationSHA, cluster, "owned-group")
	p.Ranges[0].Encoding = encoding
	path, pin := writeCustodyPolicyFixture(t, p)
	c, e := LoadOwnedUnsupportedKafkaCustody(ctx, db, s.epoch, s.activationSHA, cluster, "owned-group", path, pin)
	if e != nil {
		t.Fatal(e)
	}
	fixtureModifierCustodyCatalog(t, ctx, c)
	return context.WithValue(ctx, constant.OpUserID, r.Actor), s, db, c, raw
}
func TestUnsupportedCustodyMongoKeepsRawAndKnownReceiptWithoutApplyingModifier(t *testing.T) {
	for _, name := range []string{"known-replay-and-conflict", "clear-atomic-redaction", "deadline-unresolved", "legacy-full-envelope", "clear-before-first-custody"} {
		t.Run(name, func(t *testing.T) {
			encoding := OrdinaryModifierEncoding
			if name == "legacy-full-envelope" {
				encoding = LegacyModifierBatchEncoding
			}
			ctx, s, db, c, raw := modifierCustodyMongoFixture(t, encoding)
			heads := ordinaryEphemeralHeads(t, ctx, s)
			if encoding == LegacyModifierBatchEncoding {
				var first sdkws.MsgData
				if proto.Unmarshal(raw.Record.Body, &first) != nil {
					t.Fatal("fixture")
				}
				second := proto.Clone(&first).(*sdkws.MsgData)
				second.ContentType = constant.ReactionMessageDeleter
				second.ClientMsgID = "synthetic-second-original-intent"
				var e error
				raw.Record.Body, e = proto.Marshal(&pbmsg.MsgDataToModifyByMQ{ConversationID: msgprocessor.GetNotificationConversationIDByMsg(&first), Messages: []*sdkws.MsgData{&first, second}})
				if e != nil {
					t.Fatal(e)
				}
			}
			if name == "clear-before-first-custody" {
				// Use the exact decoded source CID, never derive it from a guessed prefix.
				meta, e := modifierCustodyMessages(raw)
				if e != nil {
					t.Fatal(e)
				}
				change := []table.PendingVisibilityChange{{ConversationID: meta.Normal, Owners: []string{raw.Record.Actor}, Below: 1}}
				if e = s.MutateMessageVisibility(ctx, db, change, func(context.Context) error { return nil }); e != nil {
					t.Fatal(e)
				}
				if _, e = c.Hold(ctx, raw); !errors.Is(e, ErrUnsupportedCustodyScope) {
					t.Fatal("late original claim renewed its generation after Clear", e)
				}
				if n, e := db.Collection(UnsupportedCustodyCollection).CountDocuments(ctx, bson.M{"kind": unsupportedCustodyKind, "original": bson.M{"$exists": true}}); e != nil || n != 0 {
					t.Fatal("late Clear race persisted raw", n, e)
				}
				return
			}
			if encoding == LegacyModifierBatchEncoding {
				raw.Record.Actor = frozenModifierCustodyActor
				raw.Headers = []KafkaOriginalHeader{{Key: []byte("synthetic original legacy header"), Value: []byte("untrusted source bytes, not user authority")}}
				raw.Frozen = &KafkaFrozenCustodyWitness{ClusterID: "synthetic-real-shape-cluster", SourceID: hash([]byte("synthetic exact external source coordinate")), RecordSHA256: hash([]byte("synthetic whole broker frame")), RawSHA256: hash(raw.Record.Body), RangeProofSHA256: c.policy.Ranges[0].SourceProofSHA256}
				raw.Record.OperationID = raw.Frozen.SourceID
				raw.Record.ContextSHA256 = custodyDigest(raw.Headers)
				ctx = context.WithValue(ctx, constant.OpUserID, frozenModifierCustodyActor)
			}
			var receipt UnsupportedCustodyReceipt
			var e error
			if encoding == LegacyModifierBatchEncoding {
				receipt, e = c.holdOriginal(ctx, raw)
			} else {
				receipt, e = c.Hold(ctx, raw)
			}
			if e != nil || !receipt.KnownFor(raw) {
				t.Fatal("actual raw custody receipt", e)
			}
			var observed KafkaOriginalRecord
			if e = c.InspectOriginal(ctx, receipt, func(r KafkaOriginalRecord) error { observed = r; return nil }); e != nil || !reflect.DeepEqual(observed, raw) {
				t.Fatal("original body/key/headers/coordinate custody differs", e)
			}
			if !reflect.DeepEqual(heads, ordinaryEphemeralHeads(t, ctx, s)) {
				t.Fatal("unsupported raw custody allocated sequence")
			}
			if n, e := db.Collection(Effects).CountDocuments(ctx, bson.M{"source_id": receipt.SourceID}); e != nil || n != 0 {
				t.Fatal("held modifier created delivery/projection duty", n, e)
			}
			if n, e := db.Collection("msg").CountDocuments(ctx, bson.M{}); e != nil || n != 0 {
				t.Fatal("held modifier became positive history", n, e)
			}
			if name == "legacy-full-envelope" {
				if receipt.MessageCount != 2 || len(receipt.ContentTypes) != 2 || receipt.Encoding != LegacyModifierBatchEncoding {
					t.Fatal("batch was split into guessed coordinates or hidden modifier kinds")
				}
				invalid := raw
				invalid.Record = raw.Record
				invalid.Record.Offset++
				invalid.Record.Body, e = proto.Marshal(&pbmsg.MsgDataToModifyByMQ{ConversationID: receipt.NotificationCID, Messages: []*sdkws.MsgData{{SendID: "owner", RecvID: "g", SessionType: constant.SuperGroupChatType, ContentType: constant.ReactionMessageModifier}}})
				if e != nil {
					t.Fatal(e)
				}
				witness := *raw.Frozen
				invalid.Frozen = &witness
				invalid.Frozen.RawSHA256 = hash(invalid.Record.Body)
				if _, e = c.holdOriginal(ctx, invalid); e == nil {
					t.Fatal("batch lacking actual typed GroupID entered impossible-to-Clear raw custody")
				}
				if n, e := db.Collection(UnsupportedCustodyCollection).CountDocuments(ctx, bson.M{"original": bson.M{"$exists": true}}); e != nil || n != 1 {
					t.Fatal("unattributable batch left raw", n, e)
				}
				return
			}
			if name == "known-replay-and-conflict" {
				again, e := c.Hold(ctx, raw)
				if e != nil || !reflect.DeepEqual(again, receipt) {
					t.Fatal("same original source lost custody idempotency", e)
				}
				changed := raw
				changed.Record = raw.Record
				var m sdkws.MsgData
				if proto.Unmarshal(changed.Record.Body, &m) != nil {
					t.Fatal("fixture")
				}
				m.Content = []byte("changed synthetic legacy body")
				changed.Record.Body, e = proto.Marshal(&m)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = c.Hold(ctx, changed); !errors.Is(e, ErrConflict) {
					t.Fatal("one coordinate accepted different original bytes", e)
				}
				return
			}
			if name == "clear-atomic-redaction" {
				sentinel := errors.New("synthetic after-clear abort")
				change := []table.PendingVisibilityChange{{ConversationID: receipt.NormalCID, Owners: []string{raw.Record.Actor}, Below: 1}}
				e = s.transaction(ctx, func(tx context.Context) error {
					if e := s.MutateMessageVisibility(tx, db, change, func(context.Context) error { return nil }); e != nil {
						return e
					}
					return sentinel
				})
				if !errors.Is(e, sentinel) {
					t.Fatal(e)
				}
				if e = c.InspectOriginal(ctx, receipt, func(KafkaOriginalRecord) error { return nil }); e != nil {
					t.Fatal("aborted Clear erased raw", e)
				}
				if e = s.MutateMessageVisibility(ctx, db, change, func(context.Context) error { return nil }); e != nil {
					t.Fatal(e)
				}
				status, e := c.Inspect(ctx, receipt.SourceID)
				if e != nil || status.State != "redacted-unapplied" || status.RawSHA != receipt.RawSHA {
					t.Fatal("privacy clear lost unresolved receipt", e)
				}
				known, e := c.Hold(ctx, raw)
				if e != nil || known.State != "redacted-unapplied" {
					t.Fatal("known custody was reconstructed/rejected instead of inspected", e)
				}
				if e = c.InspectOriginal(ctx, status, func(KafkaOriginalRecord) error { t.Fatal("Clear disclosed raw"); return nil }); e == nil {
					t.Fatal("redacted source became readable")
				}
			} else {
				// Controlled UTC cutoff calls the SAME cleanup implementation. This proves
				// state/body transition, not real elapsed wall time or Mongo TTL latency.
				if e = c.expireAt(ctx, 32, receipt.Deadline); e != nil {
					t.Fatal(e)
				}
				status, e := c.Inspect(ctx, receipt.SourceID)
				if e != nil || status.State != "expired-unresolved" || status.RawSHA != receipt.RawSHA {
					t.Fatal("expiry was silently applied/deleted", e)
				}
				if e = c.InspectOriginal(ctx, status, func(KafkaOriginalRecord) error { t.Fatal("expired raw disclosed"); return nil }); e == nil {
					t.Fatal("expired source became readable")
				}
			}
			if n, e := db.Collection(UnsupportedCustodyCollection).CountDocuments(ctx, bson.M{"_id": receipt.ID}); e != nil || n != 0 {
				t.Fatal("raw body survived explicit redaction/expiry", n, e)
			}
		})
	}
}
