package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Test-only capture from the REAL nonce-owned Mongo fixture after actual
// reservation/intake/clear. It supplies no decoded grants or fresh cuts. The
// production port is the separately sealed complete-index materializer reader.
type originalOwnedSnapshot map[string]map[string]bson.Raw

func (r originalOwnedSnapshot) ReadOriginalHistoryRecord(ctx context.Context, collection, id string) ([]byte, namespaceimport.SourceRef, error) {
	if e := ctx.Err(); e != nil {
		return nil, namespaceimport.SourceRef{}, e
	}
	rows, ok := r[collection]
	if !ok {
		return nil, namespaceimport.SourceRef{}, ErrInvalid
	}
	raw, ok := rows[id]
	if !ok {
		return nil, namespaceimport.SourceRef{}, namespaceimport.ErrOriginalHistoryRecordAbsent
	}
	ref := namespaceimport.SourceRef{Kind: "mongo", ObjectSHA256: hash([]byte(collection)), RecordSHA256: hash(raw), Position: 1}
	return append([]byte(nil), raw...), ref, nil
}
func originalOwnedCapture(t *testing.T, ctx context.Context, db *mongo.Database) originalOwnedSnapshot {
	t.Helper()
	out := originalOwnedSnapshot{}
	for _, collection := range []string{Sequences, Receipts, Effects, unrelation.MessageVisibilityCollection, table.Msg} {
		out[collection] = map[string]bson.Raw{}
		cursor, e := db.Collection(collection).Find(ctx, bson.M{}, options.Find().SetBatchSize(64))
		if e != nil {
			t.Fatal(e)
		}
		for cursor.Next(ctx) {
			raw := append(bson.Raw(nil), cursor.Current...)
			id, ok := raw.Lookup("_id").StringValueOK()
			if collection == table.Msg {
				id, ok = raw.Lookup("doc_id").StringValueOK()
			}
			if !ok {
				t.Fatal("fixture exact original object/document identity")
			}
			out[collection][id] = raw
		}
		if e = errors.Join(cursor.Err(), cursor.Close(ctx)); e != nil {
			t.Fatal(e)
		}
	}
	return out
}

// Canonical private parent is a fixture prerequisite, not a product relaxation.
func originalHistoryPrivateFixtureDir(t *testing.T) string {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(dir, 0700) != nil {
		t.Fatal("private original-history fixture directory unavailable")
	}
	return dir
}

func originalTestVerifier(t *testing.T, ctx context.Context, s *Store) (*OriginalHistoryVerifier, namespaceimport.Header, namespaceimport.Inventory) {
	t.Helper()
	_, cutSHA, e := s.namespaceCutover(ctx)
	if e != nil {
		t.Fatal(e)
	}
	h := namespaceimport.Header{Tenant: "owned-history-test", Database: s.db.Name(), StorageInstance: "owned-storage", ReplicaSet: "owned-rs", MongoProcess: "owned-mongo", MongoTopology: 1, RedisRunID: "owned-redis", RedisDB: 1, KafkaClusterID: "owned-kafka", Epoch: "b1111111-1111-4111-8111-111111111111", ActivationSHA256: hash([]byte("new activation")), MigrationID: "c1111111-1111-4111-8111-111111111111", DeploymentPolicySHA256: hash([]byte("reviewed test policy"))}
	if h.Epoch == s.epoch {
		t.Fatal("fixture epoch must change")
	}
	config := originalHistoryTrustConfig{Kind: "reviewed-original-history-source-trust-v1", TargetHeaderSHA256: h.SHA256(), Epochs: []OriginalHistoryEpochTrust{{Epoch: s.epoch, ActivationSHA256: s.activationSHA, Database: s.db.Name(), ProducerSourceSHA256: hash([]byte("actual reviewed managed source fixture")), CutoverSHA256: cutSHA}}}
	raw, e := json.Marshal(config)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(originalHistoryPrivateFixtureDir(t), "original-source-trust.json")
	if e = os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	v, e := LoadOriginalHistoryVerifier(path, hash(raw), h)
	if e != nil {
		t.Fatal(e)
	}
	i := namespaceimport.Inventory{HeaderSHA256: h.SHA256(), OriginalTrust: "UNTRUSTED", LeaseID: "owned-history-lease", RawRootSHA256: hash([]byte("actual test snapshot")), MongoRootSHA256: hash([]byte("actual test mongo")), RedisRootSHA256: hash([]byte("actual test redis"))}
	return v, h, i
}
func TestOriginalOwnerHistoryMongoRechecksActualSourceAndClear(t *testing.T) {
	for _, name := range []string{"actual-positive", "actual-clear", "changed-positive-duty", "changed-original-principal", "changed-receipt-seq", "trust-file-drift"} {
		t.Run(name, func(t *testing.T) {
			ctx, s, db := directFutureFixture(t)
			policy, policySHA := kafkaPolicyFixture(t, s)
			receipt, e := s.AcceptKafkaRecord(ctx, directFutureRecord(t, ctx, s, 3700, true), policySHA, policy)
			if e != nil {
				t.Fatal(e)
			}
			branch := receipt.Branches[0]
			if name == "actual-clear" {
				below := branch.Seq + 1
				e = s.MutateMessageVisibility(ctx, db, []table.PendingVisibilityChange{{ConversationID: branch.ConversationID, Owners: []string{"owner"}, Below: below}}, func(tx context.Context) error {
					return unrelation.NewMsgMongoDriver(db).AdvanceMessageVisibility(tx, []table.MessageVisibilityFloor{{ConversationID: branch.ConversationID, UserID: "owner", MinSeq: below}})
				})
				if e != nil {
					t.Fatal(e)
				}
			}
			v, h, i := originalTestVerifier(t, ctx, s)
			raw := originalOwnedCapture(t, ctx, db)
			mutate := func(collection, id, field string, value any) {
				var doc bson.D
				if bson.Unmarshal(raw[collection][id], &doc) != nil {
					t.Fatal("fixture BSON")
				}
				found := false
				for j := range doc {
					if doc[j].Key == field {
						doc[j].Value = value
						found = true
					}
				}
				if !found {
					t.Fatal("fixture field missing", field)
				}
				body, e := bson.Marshal(doc)
				if e != nil {
					t.Fatal(e)
				}
				raw[collection][id] = body
			}
			switch name {
			case "changed-positive-duty":
				mutate(Effects, key(s.epoch, "branch", branch.ID), "seq", branch.Seq+1)
			case "changed-original-principal":
				mutate(Sequences, namespacePrincipalsID(branch.ConversationID), "origin_sha256", hash([]byte("foreign tuple")))
			case "changed-receipt-seq":
				var changed Receipt
				if bson.Unmarshal(raw[Receipts][receipt.ID], &changed) != nil {
					t.Fatal("fixture receipt")
				}
				changed.Branches[0].Seq++
				body, e := bson.Marshal(changed)
				if e != nil {
					t.Fatal(e)
				}
				raw[Receipts][receipt.ID] = body
			case "trust-file-drift":
				if e = os.WriteFile(v.path, []byte("{}"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			rows, e := v.DeriveOriginalHistorySegments(ctx, h, i, s.epoch, "namespace-owner-prospective-v1", namespaceOwnerProspectiveID(s.epoch, branch.ConversationID, "owner"), raw)
			if name == "actual-clear" {
				if e != nil || len(rows) != 0 {
					t.Fatal("original clear failed to clip actual source", rows, e)
				}
				return
			}
			if name != "actual-positive" {
				if e == nil {
					t.Fatal("changed original chain accepted", name)
				}
				return
			}
			if e != nil || len(rows) != 1 || rows[0].Lower != 1 || rows[0].Upper != branch.Seq || rows[0].SourceEpoch != s.epoch {
				t.Fatal("actual source derivation", rows, e)
			}
			if e = v.VerifyOriginalHistorySegment(ctx, h, i, rows[0], raw); e != nil {
				t.Fatal("producer/verifier exact same original chain", e)
			}
			forged := rows[0]
			forged.Lower++
			if e = v.VerifyOriginalHistorySegment(ctx, h, i, forged, raw); e == nil {
				t.Fatal("bare altered range self-authorized")
			}
		})
	}
}

// Test-only completed snapshot lookup, not a grant. The production counterpart
// is the separately sealed exact CID/seq and doc_id index. All source rows are
// still re-read by their original typed _id by the concrete verifier.
func (r originalOwnedSnapshot) ReadOriginalHistorySourceAtSequence(ctx context.Context, cid string, seq int64) ([]byte, namespaceimport.SourceRef, error) {
	if e := ctx.Err(); e != nil {
		return nil, namespaceimport.SourceRef{}, e
	}
	found := ""
	for id, raw := range r[Effects] {
		actual, cok := raw.Lookup("conversation_id").StringValueOK()
		number, sok := raw.Lookup("seq").Int64OK()
		epoch, eok := raw.Lookup("epoch").StringValueOK()
		branch, bok := raw.Lookup("branch_id").StringValueOK()
		persist, pok := raw.Lookup("persist").BooleanOK()
		if cok && actual == cid && sok && number == seq && eok && bok && pok && persist && id == key(epoch, "branch", branch) {
			if found != "" {
				return nil, namespaceimport.SourceRef{}, ErrConflict
			}
			found = id
		}
	}
	if found == "" {
		return nil, namespaceimport.SourceRef{}, namespaceimport.ErrOriginalHistoryRecordAbsent
	}
	return r.ReadOriginalHistoryRecord(ctx, Effects, found)
}
func (r originalOwnedSnapshot) ReadOriginalHistoryMessageDocument(ctx context.Context, id string) ([]byte, namespaceimport.SourceRef, error) {
	if e := ctx.Err(); e != nil {
		return nil, namespaceimport.SourceRef{}, e
	}
	raw, found := r[table.Msg][id]
	if !found {
		return nil, namespaceimport.SourceRef{}, namespaceimport.ErrOriginalHistoryRecordAbsent
	}
	return append([]byte(nil), raw...), namespaceimport.SourceRef{Kind: "mongo", ObjectSHA256: hash([]byte(table.Msg)), RecordSHA256: hash(raw), Position: 1}, nil
}
