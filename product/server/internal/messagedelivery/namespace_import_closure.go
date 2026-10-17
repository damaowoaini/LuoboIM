package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type namespaceImportClosure struct {
	ID              string `bson:"_id"`
	Kind            string `bson:"kind"`
	HeaderSHA256    string `bson:"header_sha256"`
	InventorySHA256 string `bson:"inventory_sha256"`
	SetKind         string `bson:"set_kind"`
	ConversationID  string `bson:"conversation_id"`
	CoverageSHA256  string `bson:"coverage_sha256"`
	Cursor          string `bson:"cursor"`
	FrontierJSON    string `bson:"frontier_json"`
	RootSHA256      string `bson:"root_sha256"`
	Count           int64  `bson:"count"`
	Revision        int64  `bson:"revision"`
	Complete        bool   `bson:"complete"`
}

func importClosureID(h namespaceimport.Header, kind, cid string) string {
	return key(h.Epoch, "namespace-import-closure", h.MigrationID+"\x00"+kind+"\x00"+cid)
}

func (w *NamespaceImportWriter) readImportClosure(ctx context.Context, kind, cid, inventory string) (namespaceImportClosure, importSetFrontier, bool, error) {
	var raw bson.Raw
	err := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importClosureID(w.header, kind, cid)}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return namespaceImportClosure{}, importSetFrontier{}, false, nil
	}
	if err != nil {
		return namespaceImportClosure{}, importSetFrontier{}, false, err
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "set_kind", "conversation_id", "coverage_sha256", "cursor", "frontier_json", "root_sha256"}, []string{"count", "revision"}, []string{"complete"}, "") {
		return namespaceImportClosure{}, importSetFrontier{}, false, ErrConflict
	}
	var row namespaceImportClosure
	var frontier importSetFrontier
	if bson.Unmarshal(raw, &row) != nil || row.ID != importClosureID(w.header, kind, cid) || row.Kind != "namespace-import-closure-v1" || row.HeaderSHA256 != w.header.SHA256() || row.InventorySHA256 != inventory || row.SetKind != kind || row.ConversationID != cid || !validSHA(row.CoverageSHA256) || row.Count < 0 || row.Revision < 1 || row.Revision >= math.MaxInt64 || decodeImportJSON(row.FrontierJSON, &frontier) != nil || !frontier.valid() || frontier.Count != row.Count {
		return row, frontier, false, ErrConflict
	}
	prefix := importSetPrefix(w.header, kind, cid)
	if row.Count == 0 && row.Cursor != "" || row.Count > 0 && (!strings.HasPrefix(row.Cursor, prefix) || !validSHA(strings.TrimPrefix(row.Cursor, prefix))) {
		return row, frontier, false, ErrConflict
	}
	if row.Complete {
		root, err := frontier.root()
		if err != nil || row.RootSHA256 != root {
			return row, frontier, false, ErrConflict
		}
	} else if row.RootSHA256 != "" {
		return row, frontier, false, ErrConflict
	}
	return row, frontier, true, nil
}

func (w *NamespaceImportWriter) requireImportSetOpen(ctx context.Context, kind, cid string) error {
	err := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importClosureID(w.header, kind, cid)}).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

func (w *NamespaceImportWriter) checkCompletedImportSet(ctx context.Context, kind, cid, inventory, expectedRoot string, expectedCount uint64) error {
	row, _, found, err := w.readImportClosure(ctx, kind, cid, inventory)
	if err != nil {
		return err
	}
	if !found || !row.Complete || row.RootSHA256 != expectedRoot || expectedCount > math.MaxInt64 || row.Count != int64(expectedCount) {
		return namespaceimport.ErrCoverageIncomplete
	}
	return nil
}

func (w *NamespaceImportWriter) validateImportSetLeaf(ctx context.Context, leaf namespaceImportSetLeaf, kind, cid, inventory string) error {
	prefix := importSetPrefix(w.header, kind, cid)
	if leaf.ID != prefix+leaf.ValueSHA256 || leaf.Kind != "namespace-import-set-leaf-v1" || leaf.HeaderSHA256 != w.header.SHA256() || leaf.InventorySHA256 != inventory || !validSHA(leaf.ValueSHA256) || !identity(leaf.ReferenceID) || kind != "coverage" && kind != "prospective" && kind != "history-segments" && leaf.ConversationID != cid {
		return ErrConflict
	}
	if kind == "prospective" {
		return w.validateProspectiveLeaf(ctx, leaf, inventory)
	}
	if kind == "history-segments" {
		return w.validateOwnerHistoryLeaf(ctx, leaf, inventory)
	}
	if kind == "coverage" {
		row, coverage, err := w.readImportCoverage(ctx, leaf.ConversationID, inventory)
		if err != nil {
			return err
		}
		if row.ID != leaf.ReferenceID || row.CoverageSHA256 != leaf.ValueSHA256 {
			return ErrConflict
		}
		if err := w.checkCompletedImportSet(ctx, "owners", coverage.CID, inventory, coverage.OwnerSetRootSHA256, coverage.EffectiveOwners); err != nil {
			return err
		}
		return w.checkCompletedImportSet(ctx, "boundaries", coverage.CID, inventory, coverage.EffectiveBoundarySetRootSHA256, coverage.EffectiveOwners)
	}
	var raw bson.Raw
	if err := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": leaf.ReferenceID}).Decode(&raw); err != nil {
		return err
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "conversation_id", "user_id", "owner_key_sha256", "boundary_sha256", "coverage_sha256", "boundary_json", "window_id", "window_sha256"}, nil, nil, "") {
		return ErrConflict
	}
	var row namespaceImportBoundary
	var boundary namespaceimport.EffectiveOwnerBoundary
	if bson.Unmarshal(raw, &row) != nil || row.ID != importBoundaryID(w.header, cid, row.UserID) || row.Kind != "namespace-import-boundary-v1" || row.HeaderSHA256 != w.header.SHA256() || row.InventorySHA256 != inventory || row.ConversationID != cid || row.OwnerKeySHA256 != namespaceimport.EffectiveOwnerKey(cid, row.UserID) || decodeImportJSON(row.BoundaryJSON, &boundary) != nil {
		return ErrConflict
	}
	value, err := namespaceimport.EffectiveBoundarySHA256(boundary)
	if err != nil {
		return err
	}
	if row.BoundarySHA256 != value || boundary.Window.CID != cid || boundary.Window.OwnerID != row.UserID || kind == "owners" && leaf.ValueSHA256 != row.OwnerKeySHA256 || kind == "boundaries" && leaf.ValueSHA256 != value {
		return ErrConflict
	}
	coverageRow, coverage, err := w.readImportCoverage(ctx, cid, inventory)
	if err != nil {
		return err
	}
	if coverageRow.CoverageSHA256 != row.CoverageSHA256 || boundary.HistoryPolicySHA256 != coverage.HistoryPolicySHA256 || boundary.Window.OriginSHA256 != coverage.OriginSHA256 {
		return ErrConflict
	}
	var window bson.Raw
	if err := w.store.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": row.WindowID}).Decode(&window); err != nil {
		return err
	}
	if !validSHA(row.WindowSHA256) || hash(window) != row.WindowSHA256 {
		return ErrConflict
	}
	return nil
}

// Each signed closure page advances at most 64 actual DB leaves. Its cursor is
// compared with our durable cursor, never accepted as an instruction to skip.
// A complete set cannot subsequently accept a new origin/boundary. Publication
// consumes completed actual roots; it does not run another whole-history scan.
func (w *NamespaceImportWriter) stageImportClosure(ctx context.Context, page namespaceimport.VerifiedPage, step namespaceimport.CoverageClosure) error {
	if err := w.store.requireTX(ctx); err != nil {
		return err
	}
	if step.Kind != "owners" && step.Kind != "boundaries" && step.Kind != "coverage" && step.Kind != "prospective" && step.Kind != "history-segments" || (step.Kind == "coverage" || step.Kind == "prospective" || step.Kind == "history-segments") && step.CID != "" || step.Kind != "coverage" && step.Kind != "prospective" && step.Kind != "history-segments" && !identity(step.CID) {
		return ErrInvalid
	}
	inventory := page.Inventory()
	expectedRoot := ""
	expectedCount := uint64(0)
	coverageSHA := ""
	if step.Kind == "history-segments" {
		expectedRoot, expectedCount = inventory.OwnerHistorySegmentSetRootSHA256, inventory.OwnerHistorySegmentCount
		coverageSHA = expectedRoot
	} else if step.Kind == "prospective" {
		expectedRoot, expectedCount = inventory.ProspectiveSetRootSHA256, inventory.ProspectiveCount
		coverageSHA = expectedRoot
	} else if step.Kind == "coverage" {
		expectedRoot, expectedCount = inventory.CoverageSetRootSHA256, inventory.CoverageCount
		coverageSHA = expectedRoot
	} else {
		row, coverage, err := w.readImportCoverage(ctx, step.CID, page.InventorySHA256())
		if err != nil {
			return err
		}
		coverageSHA = row.CoverageSHA256
		expectedCount = coverage.EffectiveOwners
		expectedRoot = coverage.OwnerSetRootSHA256
		if step.Kind == "boundaries" {
			expectedRoot = coverage.EffectiveBoundarySetRootSHA256
		}
	}
	if !validSHA(expectedRoot) || expectedCount >= math.MaxInt64 {
		return ErrInvalid
	}
	row, frontier, found, err := w.readImportClosure(ctx, step.Kind, step.CID, page.InventorySHA256())
	if err != nil {
		return err
	}
	if !found {
		if step.PreviousCursor != "" {
			return ErrConflict
		}
		row = namespaceImportClosure{ID: importClosureID(w.header, step.Kind, step.CID), Kind: "namespace-import-closure-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), SetKind: step.Kind, ConversationID: step.CID, CoverageSHA256: coverageSHA, Revision: 1}
	} else if row.Complete || row.Cursor != step.PreviousCursor || row.CoverageSHA256 != coverageSHA || row.Revision >= math.MaxInt64-1 {
		return ErrConflict
	}
	prefix := importSetPrefix(w.header, step.Kind, step.CID)
	rangeFilter := bson.M{"$gte": prefix, "$lt": prefix + "g"}
	if row.Cursor != "" {
		rangeFilter = bson.M{"$gt": row.Cursor, "$lt": prefix + "g"}
	}
	cursor, err := w.store.db.Collection(Receipts).Find(ctx, bson.M{"_id": rangeFilter}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(65).SetBatchSize(65))
	if err != nil {
		return err
	}
	next := row
	processed := 0
	more := false
	for cursor.Next(ctx) {
		if processed == 64 {
			more = true
			break
		}
		raw := cursor.Current
		if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "conversation_id", "value_sha256", "reference_id"}, nil, nil, "") {
			_ = cursor.Close(ctx)
			return ErrConflict
		}
		var leaf namespaceImportSetLeaf
		if cursor.Decode(&leaf) != nil || leaf.ID <= next.Cursor {
			_ = cursor.Close(ctx)
			return ErrConflict
		}
		if err := w.validateImportSetLeaf(ctx, leaf, step.Kind, step.CID, page.InventorySHA256()); err != nil {
			_ = cursor.Close(ctx)
			return err
		}
		if err := frontier.append(leaf.ValueSHA256); err != nil {
			_ = cursor.Close(ctx)
			return err
		}
		next.Cursor = leaf.ID
		processed++
	}
	queryErr, closeErr := cursor.Err(), cursor.Close(ctx)
	if queryErr != nil || closeErr != nil {
		return errors.Join(queryErr, closeErr)
	}
	if uint64(frontier.Count) > expectedCount || more && uint64(frontier.Count) >= expectedCount {
		return ErrConflict
	}
	next.Count = frontier.Count
	if !more {
		root, err := frontier.root()
		if err != nil {
			return err
		}
		if uint64(frontier.Count) != expectedCount || root != expectedRoot {
			return namespaceimport.ErrCoverageIncomplete
		}
		next.Complete = true
		next.RootSHA256 = root
	}
	body, err := json.Marshal(frontier)
	if err != nil {
		return err
	}
	next.FrontierJSON = string(body)
	if !found {
		_, err = w.store.db.Collection(Receipts).InsertOne(ctx, next)
		return err
	}
	next.Revision++
	updated, err := w.store.db.Collection(Receipts).ReplaceOne(ctx, bson.M{"_id": row.ID, "header_sha256": row.HeaderSHA256, "inventory_sha256": row.InventorySHA256, "revision": row.Revision, "cursor": row.Cursor, "complete": false}, next)
	if err != nil {
		return err
	}
	if updated.MatchedCount != 1 || updated.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
