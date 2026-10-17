package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type namespaceImportCoverage struct {
	ID              string `bson:"_id"`
	Kind            string `bson:"kind"`
	HeaderSHA256    string `bson:"header_sha256"`
	InventorySHA256 string `bson:"inventory_sha256"`
	ConversationID  string `bson:"conversation_id"`
	OriginSHA256    string `bson:"origin_sha256"`
	CoverageSHA256  string `bson:"coverage_sha256"`
	CoverageJSON    string `bson:"coverage_json"`
}

// The native _id index is also the sorted-set index. Owner sets may span any
// number of pages; neither the group roster nor a whole set is embedded in one
// document, loaded into memory, or constrained by a page's read budget.
type namespaceImportSetLeaf struct {
	ID              string `bson:"_id"`
	Kind            string `bson:"kind"`
	HeaderSHA256    string `bson:"header_sha256"`
	InventorySHA256 string `bson:"inventory_sha256"`
	ConversationID  string `bson:"conversation_id"`
	ValueSHA256     string `bson:"value_sha256"`
	ReferenceID     string `bson:"reference_id"`
}

type namespaceImportBoundary struct {
	ID              string `bson:"_id"`
	Kind            string `bson:"kind"`
	HeaderSHA256    string `bson:"header_sha256"`
	InventorySHA256 string `bson:"inventory_sha256"`
	ConversationID  string `bson:"conversation_id"`
	UserID          string `bson:"user_id"`
	OwnerKeySHA256  string `bson:"owner_key_sha256"`
	BoundarySHA256  string `bson:"boundary_sha256"`
	CoverageSHA256  string `bson:"coverage_sha256"`
	BoundaryJSON    string `bson:"boundary_json"`
	WindowID        string `bson:"window_id"`
	WindowSHA256    string `bson:"window_sha256"`
}

type directNamespaceWindow struct {
	ID             string `bson:"_id"`
	Kind           string `bson:"kind"`
	Epoch          string `bson:"epoch"`
	ConversationID string `bson:"conversation_id"`
	UserID         string `bson:"user_id"`
	OriginSHA256   string `bson:"origin_sha256"`
	Lower          int64  `bson:"lower"`
	Upper          int64  `bson:"upper"`
	Closed         bool   `bson:"closed"`
	HeaderSHA256   string `bson:"header_sha256"`
	BoundarySHA256 string `bson:"boundary_sha256"`
}

func importSetPrefix(h namespaceimport.Header, kind, cid string) string {
	return "namespace-import-set:" + h.SHA256() + ":" + kind + ":" + hash([]byte(cid)) + ":"
}
func importCoverageID(h namespaceimport.Header, cid string) string {
	return key(h.Epoch, "namespace-import-coverage", h.MigrationID+"\x00"+cid)
}
func importBoundaryID(h namespaceimport.Header, cid, owner string) string {
	return key(h.Epoch, "namespace-import-boundary", h.MigrationID+"\x00"+namespaceimport.EffectiveOwnerKey(cid, owner))
}
func directNamespaceWindowID(epoch, cid, owner string) string {
	return key(epoch, "direct-namespace-window", cid+"\x00"+owner)
}

func (w *NamespaceImportWriter) stageOriginCoverage(ctx context.Context, page namespaceimport.VerifiedPage, coverage namespaceimport.OriginCoverage, origin namespaceimport.OriginRecord) error {
	if err := w.store.requireTX(ctx); err != nil {
		return err
	}
	if err := w.requireImportSetOpen(ctx, "coverage", ""); err != nil {
		return err
	}
	if err := coverage.ValidateBinding(w.header, page.Inventory(), origin.Origin); err != nil {
		return err
	}
	value := namespaceimport.CoverageSHA256(coverage)
	body, err := json.Marshal(coverage)
	if err != nil {
		return err
	}
	row := namespaceImportCoverage{ID: importCoverageID(w.header, coverage.CID), Kind: "namespace-import-coverage-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), ConversationID: coverage.CID, OriginSHA256: coverage.OriginSHA256, CoverageSHA256: value, CoverageJSON: string(body)}
	if _, err = w.store.db.Collection(Receipts).InsertOne(ctx, row); err != nil {
		return err
	}
	leaf := namespaceImportSetLeaf{ID: importSetPrefix(w.header, "coverage", "") + value, Kind: "namespace-import-set-leaf-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), ConversationID: coverage.CID, ValueSHA256: value, ReferenceID: row.ID}
	_, err = w.store.db.Collection(Receipts).InsertOne(ctx, leaf)
	return err
}

func (w *NamespaceImportWriter) readImportCoverage(ctx context.Context, cid, inventory string) (namespaceImportCoverage, namespaceimport.OriginCoverage, error) {
	var raw bson.Raw
	if err := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importCoverageID(w.header, cid)}).Decode(&raw); err != nil {
		return namespaceImportCoverage{}, namespaceimport.OriginCoverage{}, err
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "conversation_id", "origin_sha256", "coverage_sha256", "coverage_json"}, nil, nil, "") {
		return namespaceImportCoverage{}, namespaceimport.OriginCoverage{}, ErrConflict
	}
	var row namespaceImportCoverage
	var coverage namespaceimport.OriginCoverage
	if bson.Unmarshal(raw, &row) != nil || row.ID != importCoverageID(w.header, cid) || row.Kind != "namespace-import-coverage-v1" || row.HeaderSHA256 != w.header.SHA256() || row.InventorySHA256 != inventory || row.ConversationID != cid || !validSHA(row.OriginSHA256) || !validSHA(row.CoverageSHA256) || decodeImportJSON(row.CoverageJSON, &coverage) != nil || namespaceimport.CoverageSHA256(coverage) != row.CoverageSHA256 || coverage.CID != cid || coverage.HeaderSHA256 != row.HeaderSHA256 || coverage.OriginSHA256 != row.OriginSHA256 {
		return row, coverage, ErrConflict
	}
	return row, coverage, nil
}

func (w *NamespaceImportWriter) stageHistoricalBoundary(ctx context.Context, page namespaceimport.VerifiedPage, witness namespaceimport.EffectiveBoundaryWitness) error {
	if err := w.store.requireTX(ctx); err != nil {
		return err
	}
	boundary := witness.Boundary
	source := boundary.Window
	for _, kind := range []string{"owners", "boundaries"} {
		if err := w.requireImportSetOpen(ctx, kind, source.CID); err != nil {
			return err
		}
	}
	value, err := namespaceimport.EffectiveBoundarySHA256(boundary)
	if err != nil {
		return err
	}
	coverageRow, coverage, err := w.readImportCoverage(ctx, source.CID, page.InventorySHA256())
	if err != nil {
		return err
	}
	if coverageRow.CoverageSHA256 != namespaceimport.CoverageSHA256(witness.Coverage.Coverage) || coverage.OriginSHA256 != source.OriginSHA256 || coverage.HistoryPolicySHA256 != boundary.HistoryPolicySHA256 || coverage.EffectiveOwners == 0 {
		return ErrConflict
	}
	// A real original namespace claim/counter must precede every owner, in this
	// page or an earlier committed page. The CID cannot infer origin or owner.
	origin, err := w.store.readNamespacePrincipals(ctx, source.CID)
	if err != nil {
		return err
	}
	if origin.SHA256 != source.OriginSHA256 || origin.Kind != coverage.Domain || origin.GroupID != source.GroupID {
		return ErrConflict
	}
	if err := w.store.requireNamespaceCounter(ctx, source.CID); err != nil {
		return err
	}
	var head counter
	if err := w.store.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(w.header.Epoch, "sequence", source.CID)}).Decode(&head); err != nil {
		return err
	}
	if source.Closed && source.Upper > head.Maximum {
		return ErrConflict
	}
	ownerKey := namespaceimport.EffectiveOwnerKey(source.CID, source.OwnerID)
	row := namespaceImportBoundary{ID: importBoundaryID(w.header, source.CID, source.OwnerID), Kind: "namespace-import-boundary-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), ConversationID: source.CID, UserID: source.OwnerID, OwnerKeySHA256: ownerKey, BoundarySHA256: value, CoverageSHA256: coverageRow.CoverageSHA256}
	if err := absent(ctx, w.store.db.Collection(Receipts), bson.M{"_id": row.ID}); err != nil {
		return err
	}
	if origin.Kind == "group" {
		if !identity(source.GroupID) {
			return ErrConflict
		}
		revision := int64(1)
		var raw bson.Raw
		err := w.store.db.Collection("group").FindOne(ctx, bson.M{"group_id": source.GroupID}).Decode(&raw)
		if err == nil {
			if !importUniqueFields(raw) {
				return ErrConflict
			}
			var ok bool
			revision, ok = raw.Lookup(groupauthority.RevisionField).Int64OK()
			if !ok || revision < 1 || revision >= math.MaxInt64 {
				return ErrConflict
			}
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		window := MembershipWindow{ID: key(w.header.Epoch, "membership-window", source.CID+"\x00"+source.OwnerID), Kind: "membership-window-v1", Epoch: w.header.Epoch, ConversationID: source.CID, GroupID: source.GroupID, UserID: source.OwnerID, Lower: source.Lower, Upper: source.Upper, Closed: source.Closed, Revision: 1, GroupRevision: revision, LastCommand: w.header.MigrationID, SeedEvidenceSHA256: value}
		if !membershipWindowShape(window) {
			return ErrConflict
		}
		var prior bson.Raw
		err = w.store.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": window.ID}).Decode(&prior)
		if errors.Is(err, mongo.ErrNoDocuments) {
			if _, err = w.store.db.Collection(Sequences).InsertOne(ctx, window); err != nil {
				return err
			}
			prior, err = bson.Marshal(window)
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			if !exactFields(prior, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"lower", "upper", "revision", "group_revision"}, []string{"closed"}, "") {
				return ErrConflict
			}
			var existing MembershipWindow
			if bson.Unmarshal(prior, &existing) != nil || !w.store.validMembershipWindow(existing, source.CID, source.OwnerID) || existing.GroupID != source.GroupID || existing.Lower != source.Lower || existing.Upper != source.Upper || existing.Closed != source.Closed || existing.GroupRevision > revision {
				return ErrConflict
			}
		}
		row.WindowID, row.WindowSHA256 = window.ID, hash(prior)
	} else {
		participant := false
		for _, uid := range origin.OriginIDs {
			participant = participant || uid == source.OwnerID
		}
		if !participant || source.GroupID != "" {
			return ErrConflict
		}
		window := directNamespaceWindow{ID: directNamespaceWindowID(w.header.Epoch, source.CID, source.OwnerID), Kind: "direct-namespace-window-v1", Epoch: w.header.Epoch, ConversationID: source.CID, UserID: source.OwnerID, OriginSHA256: source.OriginSHA256, Lower: source.Lower, Upper: source.Upper, Closed: source.Closed, HeaderSHA256: w.header.SHA256(), BoundarySHA256: value}
		if err := immutableImportDocument(ctx, w.store.db.Collection(Sequences), window.ID, window); err != nil {
			return err
		}
		var actual bson.Raw
		if err := w.store.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": window.ID}).Decode(&actual); err != nil {
			return err
		}
		row.WindowID, row.WindowSHA256 = window.ID, hash(actual)
	}
	body, err := json.Marshal(boundary)
	if err != nil {
		return err
	}
	row.BoundaryJSON = string(body)
	if _, err = w.store.db.Collection(Receipts).InsertOne(ctx, row); err != nil {
		return err
	}
	for _, set := range []struct{ kind, value string }{{"owners", ownerKey}, {"boundaries", value}} {
		leaf := namespaceImportSetLeaf{ID: importSetPrefix(w.header, set.kind, source.CID) + set.value, Kind: "namespace-import-set-leaf-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), ConversationID: source.CID, ValueSHA256: set.value, ReferenceID: row.ID}
		if _, err = w.store.db.Collection(Receipts).InsertOne(ctx, leaf); err != nil {
			return err
		}
	}
	return nil
}
