//go:build linux

package ownedcontrol_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func mongoCompositionSHA(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Only the already-owned ephemeral CI replica set is eligible. Missing fixture
// is a prerequisite FAILURE, never a skipped/passing integration obligation.
// No existing database is adopted; no SQL/Redis/Kafka/browser service is used.
func mongoCompositionDB(t *testing.T) (context.Context, *mongo.Database, namespaceimport.Header) {
	t.Helper()
	uri := os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")
	if uri != "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration" {
		t.Fatal("mandatory exact owned Mongo replica-set fixture not selected")
	}
	deadline := time.Now().Add(4 * time.Minute)
	if packageDeadline, ok := t.Deadline(); ok && packageDeadline.Add(-20*time.Second).Before(deadline) {
		// Leave the original exact nonce claim/drop/disconnect cleanups time to
		// finish before the unchanged 180s package alarm. Early cancellation is
		// still a test failure unless the original controller assertions finish.
		deadline = packageDeadline.Add(-20 * time.Second)
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	t.Cleanup(cancel)
	client, e := mongo.Connect(ctx, options.Client().ApplyURI(uri).SetDirect(true).SetRetryWrites(false).SetServerSelectionTimeout(2*time.Second))
	if e != nil {
		t.Fatal("owned Mongo connect failed")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if e := client.Disconnect(end); e != nil {
			t.Error("owned Mongo disconnect unconfirmed")
		}
	})
	var hello bson.Raw
	if e = client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); e != nil {
		t.Fatal("owned Mongo hello failed")
	}
	primary, pok := hello.Lookup("isWritablePrimary").BooleanOK()
	rs, rok := hello.Lookup("setName").StringValueOK()
	top, tok := hello.Lookup("topologyVersion").DocumentOK()
	if !pok || !primary || !rok || rs != "luoboIntegration" || !tok {
		t.Fatal("exact writable replica-set identity absent")
	}
	pid, iok := top.Lookup("processId").ObjectIDOK()
	revision, cok := top.Lookup("counter").Int64OK()
	if !iok || pid.IsZero() || !cok || revision < 0 {
		t.Fatal("real Mongo process identity absent")
	}
	var nonce [24]byte
	if _, e = rand.Read(nonce[:]); e != nil {
		t.Fatal("owned fixture nonce unavailable")
	}
	name, claim := "luobo_historical_join_test_"+hex.EncodeToString(nonce[:12]), hex.EncodeToString(nonce[12:])
	names, e := client.ListDatabaseNames(ctx, bson.M{"name": name})
	if e != nil || len(names) != 0 {
		t.Fatal("fresh nonce DB absence unconfirmed")
	}
	db := client.Database(name)
	if e = db.CreateCollection(ctx, "historical_composition_claim"); e != nil {
		t.Fatal("exclusive fixture claim unavailable; no drop authorized")
	}
	if _, e = db.Collection("historical_composition_claim").InsertOne(ctx, bson.M{"_id": "task-owner", "nonce": claim}); e != nil {
		t.Fatal("claim commit unknown; retain DB")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if db.Name() != name {
			t.Error("owned DB identity changed; retaining")
			return
		}
		var raw bson.Raw
		if e := db.Collection("historical_composition_claim").FindOne(end, bson.M{"_id": "task-owner", "nonce": claim}).Decode(&raw); e != nil {
			t.Error("owned DB claim absent; retaining")
			return
		}
		elements, e := raw.Elements()
		if e != nil || len(elements) != 2 {
			t.Error("owned DB claim shape changed; retaining")
			return
		}
		if e = db.Drop(end); e != nil {
			t.Error("exact nonce DB drop unconfirmed")
			return
		}
		left, e := client.ListDatabaseNames(end, bson.M{"name": name})
		if e != nil || len(left) != 0 {
			t.Error("nonce DB residual absence unconfirmed")
		}
	})
	h := namespaceimport.Header{Tenant: "synthetic-historical-composition", Database: name, StorageInstance: uuid.NewString(), ReplicaSet: rs, MongoProcess: pid.Hex(), MongoTopology: revision, RedisRunID: strings.Repeat("a", 40), RedisDB: 15, KafkaClusterID: "synthetic-no-broker", Epoch: uuid.NewString(), ActivationSHA256: mongoCompositionSHA("synthetic-target-activation"), MigrationID: uuid.NewString(), DeploymentPolicySHA256: mongoCompositionSHA("unselected-policy")}
	return ctx, db, h
}

// Deliberately lose a reply only AFTER the real Mongo transaction committed.
// All actual Writer ports/Permits remain intact. Recovery must use its exact
// immutable receipt, not retry the closure or publish business operation.
type mongoCompositionACKLoss struct {
	real                                 *messagedelivery.NamespaceImportWriter
	normalCID                            string
	applies                              map[uint64]int
	lostPage                             namespaceimport.PageIdentity
	lostPageACK, lostPublishACK          bool
	pageInspects, publishes, cutInspects int
}

func (w *mongoCompositionACKLoss) CheckTarget(ctx context.Context, h namespaceimport.Header) error {
	return w.real.CheckTarget(ctx, h)
}
func (w *mongoCompositionACKLoss) ApplyPage(ctx context.Context, p *namespaceimport.Permit, v namespaceimport.VerifiedPage) (namespaceimport.PageReceipt, error) {
	w.applies[v.Data().Index]++
	receipt, e := w.real.ApplyPage(ctx, p, v)
	if e != nil {
		return receipt, e
	}
	data := v.Data()
	if !w.lostPageACK && len(data.CoverageClosures) == 1 && data.CoverageClosures[0].Kind == "owners" && data.CoverageClosures[0].CID == w.normalCID && data.CoverageClosures[0].PreviousCursor == "" {
		w.lostPageACK = true
		w.lostPage = data.Identity()
		return namespaceimport.PageReceipt{}, namespaceimport.ErrCommitUnconfirmed
	}
	return receipt, nil
}
func (w *mongoCompositionACKLoss) InspectPage(ctx context.Context, h namespaceimport.Header, id namespaceimport.PageIdentity) (namespaceimport.PageInspection, error) {
	w.pageInspects++
	return w.real.InspectPage(ctx, h, id)
}
func (w *mongoCompositionACKLoss) PublishCutover(ctx context.Context, p *namespaceimport.Permit, v namespaceimport.VerifiedManifest) (namespaceimport.CutoverReceipt, error) {
	w.publishes++
	receipt, e := w.real.PublishCutover(ctx, p, v)
	if e != nil {
		return receipt, e
	}
	if !w.lostPublishACK {
		w.lostPublishACK = true
		return namespaceimport.CutoverReceipt{}, namespaceimport.ErrCommitUnconfirmed
	}
	return receipt, nil
}
func (w *mongoCompositionACKLoss) InspectCutover(ctx context.Context, h namespaceimport.Header) (namespaceimport.CutoverInspection, error) {
	w.cutInspects++
	return w.real.InspectCutover(ctx, h)
}

func TestHistoricalSourceMongoControllerPublishesExactPagesAndKeepsReadinessSeparate(t *testing.T) {
	ctx, db, h := mongoCompositionDB(t)
	var writer *messagedelivery.NamespaceImportWriter
	originals := map[string]map[string]bson.Raw{}
	f := ownedcontrol.NewHistoricalMongoCompositionForTest(ctx, t, h, func(prep context.Context, header namespaceimport.Header, hold *ownedcontrol.AdmissionHold, rawRows map[string][]bson.Raw) (map[string][]bson.Raw, error) {
		// Both raw source data and the target DB are synthetic and task-owned. The
		// same actual rows are captured AFTER the real held metadata bootstrap.
		for name, rows := range rawRows {
			if e := db.CreateCollection(prep, name); e != nil {
				return nil, e
			}
			for _, row := range rows {
				if _, e := db.Collection(name).InsertOne(prep, row); e != nil {
					return nil, e
				}
			}
		}
		base, e := messagedelivery.NewStore(db, header.Epoch, header.ActivationSHA256)
		if e != nil {
			return nil, e
		}
		writer, e = messagedelivery.NewNamespaceImportWriter(base, header)
		if e != nil {
			return nil, e
		}
		if e = writer.PrepareHeldMetadata(prep, hold, header); e != nil {
			return nil, e
		}
		// Verify the REAL held metadata path (not a test-inserted Ready row).
		var effectsSchema bson.Raw
		if e = db.Collection(groupeffects.Collection).FindOne(prep, bson.M{"_id": groupeffects.SchemaID}).Decode(&effectsSchema); e != nil {
			return nil, e
		}
		fields, e := effectsSchema.Elements()
		version, versionOK := effectsSchema.Lookup("version").Int32OK()
		if e != nil || len(fields) != 3 || effectsSchema.Lookup("_id").StringValue() != groupeffects.SchemaID || effectsSchema.Lookup("kind").StringValue() != "group-effects-schema-v1" || !versionOK || version != 1 {
			return nil, namespaceimport.ErrConflict
		}
		// Reentry must retain the original sentinel bytes and never reset it.
		if e = writer.PrepareHeldMetadata(prep, hold, header); e != nil {
			return nil, e
		}
		var replayedSchema bson.Raw
		if e = db.Collection(groupeffects.Collection).FindOne(prep, bson.M{"_id": groupeffects.SchemaID}).Decode(&replayedSchema); e != nil || !bytes.Equal(effectsSchema, replayedSchema) {
			return nil, errors.Join(e, namespaceimport.ErrConflict)
		}
		if e = writer.CheckImportCatalogReady(prep); e != nil {
			return nil, e
		}
		actual := map[string][]bson.Raw{}
		for name := range rawRows {
			cursor, e := db.Collection(name).Find(prep, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(512))
			if e != nil {
				return nil, e
			}
			var rows []bson.Raw
			e = cursor.All(prep, &rows)
			closeErr := cursor.Close(prep)
			if e != nil || closeErr != nil {
				return nil, errors.Join(e, closeErr)
			}
			if len(rows) >= 512 {
				return nil, namespaceimport.ErrInvalid
			} // fixture sanity, not a product roster cap
			actual[name] = rows
			originals[name] = map[string]bson.Raw{}
			for _, row := range rows {
				id, ok := row.Lookup("_id").StringValueOK()
				if !ok {
					return nil, namespaceimport.ErrInvalid
				}
				originals[name][id] = append(bson.Raw(nil), row...)
			}
		}
		return actual, nil
	})
	h = f.Config.Header
	trustPath, trustSHA := f.OriginalTrustForTest()
	originalVerifier, e := messagedelivery.LoadOriginalHistoryVerifier(trustPath, trustSHA, h)
	if e != nil {
		t.Fatal("actual exact empty-trust loader failed", e)
	}
	f.UseOriginalHistoryVerifierForTest(originalVerifier, originalVerifier)
	normal := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, "sg_group_with_under")
	notice := msgprocessor.GetNotificationConversationID(constant.SuperGroupChatType, "sg_group_with_under")
	if normal == notice {
		t.Fatal("real protocol namespace pair collapsed")
	}
	wrapped := &mongoCompositionACKLoss{real: writer, normalCID: normal, applies: map[uint64]int{}}
	top := t.TempDir()
	if e := os.Chmod(top, 0700); e != nil {
		t.Fatal(e)
	}
	journalPath := filepath.Join(top, "controller")
	if e := os.Mkdir(journalPath, 0700); e != nil {
		t.Fatal(e)
	}
	journal, e := namespaceimport.OpenPrivateJournal(journalPath)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if journal != nil {
			if e := journal.Close(); e != nil {
				t.Error("controller journal close failed", e)
			}
		}
	})
	controller, e := namespaceimport.NewController(f.Config, wrapped, f.Source, f.Source, f.ControlPlaneForTest(), journal)
	if e != nil {
		t.Fatal("real controller constructor rejected composition", e)
	}
	state, e := controller.Begin(ctx)
	if e != nil || state.Phase != "fenced" {
		t.Fatal("real Controller.Begin failed", e)
	}
	rawFingerprint := f.SnapshotFingerprintForTest()
	restarts := 0
	for step := 0; state.Phase != "published"; step++ {
		if step >= 30000 {
			t.Fatal("bounded source/controller fixture did not complete")
		}
		state, e = controller.Step(ctx)
		if errors.Is(e, namespaceimport.ErrCommitUnconfirmed) {
			if state.Phase != "pending-page" && state.Phase != "pending-cutover" {
				t.Fatal("unknown commit lost original pending identity", e)
			}
			if state.Phase == "pending-page" && (!wrapped.lostPageACK || state.PendingPage != wrapped.lostPage) || state.Phase == "pending-cutover" && !wrapped.lostPublishACK {
				t.Fatal("real Store failure occurred before deliberate ACK loss", e)
			}
			if ce := journal.Close(); ce != nil {
				t.Fatal(ce)
			}
			journal = nil
			journal, e = namespaceimport.OpenPrivateJournal(journalPath)
			if e != nil {
				t.Fatal(e)
			}
			controller, e = namespaceimport.NewController(f.Config, wrapped, f.Source, f.Source, f.ControlPlaneForTest(), journal)
			if e != nil {
				t.Fatal(e)
			}
			restored, e := controller.Begin(ctx)
			if e != nil || restored.PendingAttemptID != state.PendingAttemptID || restored.PendingPage != state.PendingPage || restored.PendingManifestSHA256 != state.PendingManifestSHA256 {
				t.Fatal("restart replaced original pending command", e)
			}
			state = restored
			restarts++
			if restarts > 2 {
				t.Fatal("unexpected third unknown ACK")
			}
			continue
		}
		if e != nil && !errors.Is(e, namespaceimport.ErrPending) {
			t.Fatal("actual source/controller/Store composition failed", e)
		}
	}

	if !t.Run("actual pages receipts64leaf closures and lost ACKs remain exact", func(t *testing.T) {
		i := state.Inventory
		if i.OriginalTrust != "UNTRUSTED" || i.Counts.Origins != 2 || i.Counts.Windows != 66 || i.ProspectiveCount != 4 || i.Counts.Quarantines != 0 {
			t.Fatal("actual signed inventory count/authority changed")
		}
		if restarts != 2 || !wrapped.lostPageACK || !wrapped.lostPublishACK || wrapped.publishes != 1 || wrapped.cutInspects != 1 || wrapped.pageInspects == 0 || wrapped.applies[wrapped.lostPage.Index] != 1 {
			t.Fatal("known committed operation replayed after lost ACK")
		}
		if state.NextPage != i.PageCount || len(wrapped.applies) != int(i.PageCount) || state.Cumulative != i.Counts || state.AppliedProspectives != i.ProspectiveCount || f.SnapshotFingerprintForTest() != rawFingerprint {
			t.Fatal("publication lost page chain or recaptured source")
		}
		for _, count := range wrapped.applies {
			if count != 1 {
				t.Fatal("page was physically dispatched more than once")
			}
		}
		for index := uint64(0); index < i.PageCount; index++ {
			f.RefreshSyntheticHoldForTest()
			page, e := f.Source.ReadPage(ctx, i, index)
			if e != nil {
				t.Fatal(e)
			}
			got, e := writer.InspectPage(ctx, h, page.Data.Identity())
			if e != nil || !got.Found || got.Receipt.Page != page.Data.Identity() || got.Receipt.InventorySHA256 != i.SHA256() {
				t.Fatal("actual immutable page receipt differs", e)
			}
		}
		var closure bson.Raw
		if e := db.Collection(messagedelivery.Receipts).FindOne(ctx, bson.M{"kind": "namespace-import-closure-v1", "header_sha256": h.SHA256(), "set_kind": "owners", "conversation_id": normal}).Decode(&closure); e != nil {
			t.Fatal(e)
		}
		count, cok := closure.Lookup("count").Int64OK()
		revision, rok := closure.Lookup("revision").Int64OK()
		complete, bok := closure.Lookup("complete").BooleanOK()
		cursor, sok := closure.Lookup("cursor").StringValueOK()
		prefix := "namespace-import-set:" + h.SHA256() + ":owners:"
		if !cok || count != 65 || !rok || revision != 2 || !bok || !complete || !sok || !strings.HasPrefix(cursor, prefix) {
			t.Fatal("64-leaf actual cursor/frontier did not close all65 owners")
		}
		published, e := writer.InspectCutover(ctx, h)
		if e != nil || !published.Found || published.Receipt != state.Cutover {
			t.Fatal("real publication inspection differs", e)
		}
		// Page bodies/proofs are always read from their original private FS source.
		changed := i
		changed.RawRootSHA256 = mongoCompositionSHA("foreign-raw-root")
		if _, e = f.Source.ReadPage(ctx, changed, 0); e == nil {
			t.Fatal("foreign source binding received an original page")
		}
	}) {
		return
	}

	t.Setenv(messagedelivery.HistoryWindowModeEnv, "required")
	t.Setenv(messagedelivery.HistoryWindowEpochEnv, h.Epoch)
	t.Setenv(messagedelivery.HistoryWindowActivationEnv, h.ActivationSHA256)
	reader, e := messagedelivery.NewHistoryWindowReaderFromEnvironment(db)
	if e != nil || reader == nil {
		t.Fatal("actual required reader factory failed", e)
	}
	if !t.Run("real publication does not mint membership readiness", func(t *testing.T) {
		for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts} {
			n, e := db.Collection(name).CountDocuments(ctx, bson.M{"_id": messagedelivery.MembershipSchemaID})
			if e != nil || n != 0 {
				t.Fatal("import silently minted reader-ready metadata", e)
			}
		}
		if e = reader.CheckReady(ctx); !errors.Is(e, messagedelivery.ErrUnprepared) {
			t.Fatal("missing actual readiness producer was bypassed", e)
		}
		if _, _, e = reader.ReadNamespaceHistoryUpper(ctx, normal, "owner"); e == nil {
			t.Fatal("unready real reader exposed imported history")
		}
	}) {
		return
	}

	// EXPLICIT TEST-ONLY PHASE. The real publication above remains insufficient
	// for runtime. This isolated schema fixture tests the reader algorithm only;
	// it is NOT a production producer, real reader deployment binding or Ready.
	if !t.Run("isolated read schema fixture preserves base gap and notification domain", func(t *testing.T) {
		for _, name := range []string{messagedelivery.Sequences, messagedelivery.Receipts} {
			_, e := db.Collection(name).InsertOne(ctx, bson.M{"_id": messagedelivery.MembershipSchemaID, "version": int32(1), "epoch": h.Epoch, "activation_sha256": h.ActivationSHA256, "legacy_message_writers_retired": true, "legacy_group_writers_retired": true, "owner_bounds_seeded": true, "readers_bound": true})
			if e != nil {
				t.Fatal("explicit synthetic read-schema fixture failed", e)
			}
		}
		if e = reader.CheckReady(ctx); e != nil {
			t.Fatal("explicit read-layer fixture is incompatible", e)
		}
		for _, check := range []struct {
			cid   string
			upper int64
			allow map[int64]bool
		}{{normal, 5, map[int64]bool{2: true, 3: true, 5: true}}, {notice, 2, map[int64]bool{2: true}}} {
			bounded, upper, e := reader.ReadNamespaceHistoryUpper(ctx, check.cid, "owner")
			if e != nil || !bounded || upper != check.upper {
				t.Fatal("reader mixed normal and notification sequence domains", e)
			}
			allowed, e := reader.FilterNamespaceHistorySequences(ctx, check.cid, "owner", []int64{1, 2, 3, 5, 6, 10, 11})
			if e != nil || len(allowed) != len(check.allow) {
				t.Fatal("base/future gap filter changed", e)
			}
			for seq := range check.allow {
				if !allowed[seq] {
					t.Fatal("proved historical sequence was lost")
				}
			}
		}
		allowed, e := reader.FilterNamespaceHistorySequences(ctx, normal, "closed-owner-63", []int64{1, 2, 3, 4, 10, 11})
		if e != nil || len(allowed) != 2 || !allowed[2] || !allowed[3] {
			t.Fatal("65th historical owner was omitted by closure or current roster", e)
		}
		allowed, e = reader.FilterNamespaceHistorySequences(ctx, normal, "new-member", []int64{2, 5, 10, 11})
		if e != nil || len(allowed) != 0 {
			t.Fatal("prospective roster granted an unproved base or unaccepted future", e)
		}
		if _, e = reader.FilterNamespaceHistorySequences(ctx, normal, "foreign-owner", []int64{2}); !errors.Is(e, messagedelivery.ErrNamespaceHistoryNotOwned) {
			t.Fatal("foreign owner gained authority", e)
		}
	}) {
		return
	}

	t.Run("reopened actual reader rechecks immutable evidence and missing marker", func(t *testing.T) {
		reopened, e := messagedelivery.NewHistoryWindowReaderFromEnvironment(db)
		if e != nil {
			t.Fatal(e)
		}
		allowed, e := reopened.FilterNamespaceHistorySequences(ctx, normal, "owner", []int64{2, 5, 6, 10, 11})
		if e != nil || len(allowed) != 2 || !allowed[2] || !allowed[5] {
			t.Fatal("reader reopen filled old gap or lost proven base", e)
		}
		if _, e = db.Collection(messagedelivery.Receipts).DeleteOne(ctx, bson.M{"_id": messagedelivery.MembershipSchemaID}); e != nil {
			t.Fatal(e)
		}
		if _, e = reopened.FilterNamespaceHistorySequences(ctx, normal, "owner", []int64{2}); e == nil {
			t.Fatal("reader cached a retired schema grant")
		}
	})

	t.Run("original captured rows remain byte exact and missing proof stays visible", func(t *testing.T) {
		for name, rows := range originals {
			for id, expected := range rows {
				var current bson.Raw
				if e := db.Collection(name).FindOne(ctx, bson.M{"_id": id}).Decode(&current); e != nil || !bytes.Equal(current, expected) {
					t.Fatal("import rewrote an original source row", e)
				}
			}
		}
		f.RefreshSyntheticHoldForTest()
		pending, e := f.Source.ReadHistoryProofDispositions(ctx, state.Inventory, "", 1)
		if e != nil || pending.Total != 1 || len(pending.Rows) != 1 || pending.Rows[0].Reason != "source-trust-unavailable" || pending.Rows[0].OwnerID != "former-owner" {
			t.Fatal("missing original trust was silently discarded", e)
		}
	})
}
