package messagedelivery

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Compatibility preserves the original separation between a proven namespace
// permission and physical body availability. ONLY the original signed imported
// boundary or independent segment, its private VerifiedPage/known receipt and
// published cut can provide
// this permission. Current counter seed/roster/Lower are never a source of it.
// The original Controller verified Config/CapturePublicKey before minting the
// private page. This reader rechecks that immutable same-inventory chain; it
// does not select a key from KeyID or mint a new production Ready capability.
func (s *Store) legacyGroupScopePermissions(ctx context.Context, d NamespaceOriginDescriptor, owner string, seqs []int64) (map[int64]bool, error) {
	allowed, e := s.legacyGroupBoundaryPermissions(ctx, d, owner, seqs)
	if e != nil {
		return nil, e
	}
	// A main owner window is not the complete read set. Previously proved
	// closed generations remain separate signed segments; do not intersect
	// them with the main window or replace their holes by a min/max union.
	if e = s.addLegacyGroupSegmentPermissions(ctx, d, owner, seqs, allowed); e != nil {
		return nil, e
	}
	return allowed, nil
}
func (s *Store) legacyGroupBoundaryPermissions(ctx context.Context, d NamespaceOriginDescriptor, owner string, seqs []int64) (map[int64]bool, error) {
	allowed := map[int64]bool{}
	if e := s.requireTX(ctx); e != nil {
		return nil, e
	}
	cut, _, e := s.namespaceCutover(ctx)
	if e != nil {
		return nil, e
	}
	var raw bson.Raw
	var proof namespaceBoundaryPageProof
	e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": boundaryPageProofID(s.epoch, d.CID, owner)}).Decode(&raw)
	if e == mongo.ErrNoDocuments {
		return allowed, nil
	}
	if e != nil {
		return nil, e
	}
	if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "header_sha256", "inventory_sha256", "boundary_sha256", "page_id", "page_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &proof) != nil || proof.ID != boundaryPageProofID(s.epoch, d.CID, owner) || proof.Kind != "namespace-import-boundary-source-v1" || proof.Epoch != s.epoch || proof.CID != d.CID || proof.Owner != owner {
		return nil, ErrConflict
	}
	h, inventory, page, e := s.readBoundImportPage(ctx, namespaceOwnerAdmissionProof{PageID: proof.PageID, PageSHA: proof.PageSHA, HeaderSHA: proof.HeaderSHA, InventorySHA: proof.InventorySHA})
	if e != nil {
		return nil, e
	}
	if h.MigrationID != cut.MigrationID || inventory.HeaderSHA256 != h.SHA256() || len(inventory.Signature) == 0 {
		return nil, ErrConflict
	}
	var boundary namespaceImportBoundary
	if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importBoundaryID(h, d.CID, owner)}).Decode(&raw); e != nil {
		return nil, e
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "conversation_id", "user_id", "owner_key_sha256", "boundary_sha256", "coverage_sha256", "boundary_json", "window_id", "window_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &boundary) != nil || boundary.ID != importBoundaryID(h, d.CID, owner) || boundary.Kind != "namespace-import-boundary-v1" || boundary.HeaderSHA256 != h.SHA256() || boundary.InventorySHA256 != inventory.SHA256() || boundary.ConversationID != d.CID || boundary.UserID != owner || boundary.OwnerKeySHA256 != namespaceimport.EffectiveOwnerKey(d.CID, owner) || boundary.BoundarySHA256 != proof.BoundarySHA {
		return nil, ErrConflict
	}
	var original namespaceimport.EffectiveOwnerBoundary
	if decodeImportJSON(boundary.BoundaryJSON, &original) != nil {
		return nil, ErrConflict
	}
	sum, e := namespaceimport.EffectiveBoundarySHA256(original)
	if e != nil || sum != boundary.BoundarySHA256 {
		return nil, ErrConflict
	}
	matches := 0
	for _, w := range page.EffectiveBoundaries {
		if directHistoryDigest(w.Boundary) != directHistoryDigest(original) {
			continue
		}
		if namespaceimport.CoverageSHA256(w.Coverage.Coverage) != boundary.CoverageSHA256 || verifyOriginalBoundaryWitness(h, inventory, w, d, owner) != nil {
			return nil, ErrConflict
		}
		matches++
	}
	if matches != 1 {
		return nil, ErrConflict
	}
	writer := &NamespaceImportWriter{store: s, header: h}
	published, e := writer.readImportCutover(ctx)
	if e != nil {
		return nil, e
	}
	if !published.Found || published.Receipt.ManifestSHA256 != cut.ManifestSHA256 {
		return nil, ErrUnprepared
	}
	var publication namespaceImportCutover
	if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importCutoverReceiptID(h)}).Decode(&publication); e != nil {
		return nil, e
	}
	var manifest namespaceimport.ManifestData
	if decodeImportJSON(publication.ManifestJSON, &manifest) != nil || publication.InventorySHA256 != inventory.SHA256() || manifest.InventorySHA256 != inventory.SHA256() || manifest.CoverageSetRootSHA256 != inventory.CoverageSetRootSHA256 || manifest.CoverageCount != inventory.CoverageCount || manifest.ProspectiveSetRootSHA256 != inventory.ProspectiveSetRootSHA256 || manifest.ProspectiveCount != inventory.ProspectiveCount || manifest.OwnerHistorySegmentSetRootSHA256 != inventory.OwnerHistorySegmentSetRootSHA256 || manifest.OwnerHistorySegmentCount != inventory.OwnerHistorySegmentCount || manifest.PageCount != inventory.PageCount || manifest.Counts != inventory.Counts {
		return nil, ErrConflict
	}
	upper := original.Window.Upper
	if !original.Window.Closed {
		upper, e = s.originalImportedScopeHead(ctx, h, inventory, d)
		if e != nil {
			return nil, e
		}
	}
	for _, seq := range seqs {
		if seq > 0 && seq >= original.Window.Lower && seq <= upper {
			allowed[seq] = true
		}
	}
	return allowed, nil
}

// A historical open boundary is capped by its ORIGINAL known import source
// record. A later seed rewrite or normal counter increment cannot extend it.
func (s *Store) originalImportedScopeHead(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, d NamespaceOriginDescriptor) (int64, error) {
	var raw bson.Raw
	var origin historicalOriginReceipt
	if e := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importOriginReceiptID(h, d.CID)}).Decode(&raw); e != nil {
		return 0, e
	}
	numbers := []string{"redis_maximum", "maximum"}
	if raw.Lookup("mongo_maximum").Type != 0 {
		numbers = append(numbers, "mongo_maximum")
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "page_sha256", "conversation_id", "origin_sha256", "seed_evidence_sha256", "source_record_sha256", "mongo_head_json", "redis_head_json", "source_record_json"}, numbers, nil, "") || bson.Unmarshal(raw, &origin) != nil || origin.ID != importOriginReceiptID(h, d.CID) || origin.Kind != "namespace-import-origin-v2" || origin.HeaderSHA256 != h.SHA256() || origin.InventorySHA256 != i.SHA256() || origin.ConversationID != d.CID || origin.OriginSHA256 != d.SHA256 || hash([]byte(origin.SourceRecordJSON)) != origin.SourceRecordSHA256 {
		return 0, ErrConflict
	}
	var source namespaceimport.OriginRecord
	if decodeImportJSON(origin.SourceRecordJSON, &source) != nil || source.Origin.CID != d.CID || source.Origin.Kind != d.Kind || source.Origin.GroupID != d.GroupID || source.Origin.SHA256 != d.SHA256 || source.SeedEvidenceSHA256 != origin.SeedEvidenceSHA256 || directHistoryDigest(source.MongoHead) != hash([]byte(origin.MongoHeadJSON)) || directHistoryDigest(source.RedisHead) != hash([]byte(origin.RedisHeadJSON)) {
		return 0, ErrConflict
	}
	if len(source.Origin.OriginIDs) != len(d.OriginIDs) {
		return 0, ErrConflict
	}
	for j, uid := range d.OriginIDs {
		if source.Origin.OriginIDs[j] != uid {
			return 0, ErrConflict
		}
	}
	upper, e := source.EffectiveSeedMaximum()
	if e != nil || upper != origin.Maximum || source.RedisHead.Value != origin.RedisMaximum {
		return 0, ErrConflict
	}
	if source.MongoHead.State == namespaceimport.LegacyCounterNotAllocatedHere {
		sum, e := namespaceimport.SourceSeedEvidenceSHA256(h, i, source)
		if e != nil || sum != source.SeedEvidenceSHA256 || origin.MongoMaximum != nil {
			return 0, ErrConflict
		}
	} else if origin.MongoMaximum == nil || *origin.MongoMaximum != source.MongoHead.Value {
		return 0, ErrConflict
	}
	// This known page receipt was created ONLY by ApplyPage's original Permit
	// and VerifiedPage (including actual-record membership in that page).
	var page namespaceImportPage
	if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"kind": "namespace-import-page-v1", "header_sha256": h.SHA256(), "inventory_sha256": i.SHA256(), "page_sha256": origin.PageSHA256}).Decode(&raw); e != nil {
		return 0, e
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "page_sha256", "previous_sha256", "receipt_json", "receipt_sha256", "attempt_id", "lease_id", "fence_sha256"}, []string{"page_index"}, nil, "") || bson.Unmarshal(raw, &page) != nil || page.PageIndex < 0 || page.ID != importPageID(h, uint64(page.PageIndex)) || page.HeaderSHA256 != h.SHA256() || page.InventorySHA256 != i.SHA256() || page.PageSHA256 != origin.PageSHA256 {
		return 0, ErrConflict
	}
	var receipt namespaceimport.PageReceipt
	if decodeImportJSON(page.ReceiptJSON, &receipt) != nil || receipt.HeaderSHA256 != h.SHA256() || receipt.InventorySHA256 != i.SHA256() || receipt.Page.Index != uint64(page.PageIndex) || receipt.Page.SHA256 != origin.PageSHA256 || receipt.Page.PreviousSHA256 != page.PreviousSHA256 || receipt.ReceiptSHA256 != page.ReceiptSHA256 || namespaceimport.ReceiptDigest(receipt) != page.ReceiptSHA256 {
		return 0, ErrConflict
	}
	return upper, nil
}

// Iterate only original imported segments intersecting this requested page.
// readHistoryInterval rechecks the exact immutable row, original private page,
// signed Inventory root membership and actual PublishedCutover receipt. The
// publication is then explicitly matched to THAT segment's Header/Inventory.
// Neither a bare live generation nor an increased current seed grants legacy
// scope exemption. The caller still ANDs these exact ranges with already
// filtered owner/global/HistoricalFloor permissions and permanent required_from.
// Body grants are distinct from legacy namespace/query metadata. Only an
// ACTUAL scoped source segment, original private page/known receipt and SAME
// signed Inventory/publication can authorize an existing scopeless old body.
func (s *Store) legacyGroupBodyScopePermissions(ctx context.Context, d NamespaceOriginDescriptor, owner string, seqs []int64) (map[int64]bool, error) {
	allowed := map[int64]bool{}
	e := s.addLegacyGroupSegmentPermissionsMode(ctx, d, owner, seqs, allowed, true)
	return allowed, e
}
func (s *Store) addLegacyGroupSegmentPermissions(ctx context.Context, d NamespaceOriginDescriptor, owner string, seqs []int64, allowed map[int64]bool) error {
	return s.addLegacyGroupSegmentPermissionsMode(ctx, d, owner, seqs, allowed, false)
}
func (s *Store) addLegacyGroupSegmentPermissionsMode(ctx context.Context, d NamespaceOriginDescriptor, owner string, seqs []int64, allowed map[int64]bool, body bool) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	ranges := make(bson.A, 0, len(seqs))
	for _, seq := range seqs {
		if seq > 0 {
			ranges = append(ranges, bson.M{"lower": bson.M{"$lte": seq}, "upper": bson.M{"$gte": seq}})
		}
	}
	if len(ranges) == 0 {
		return nil
	}
	cut, _, e := s.namespaceCutover(ctx)
	if e != nil {
		return e
	}
	filter := bson.M{"epoch": s.epoch, "conversation_id": d.CID, "user_id": owner, "kind": "namespace-owner-imported-history-v1", "$or": ranges}
	cursor, e := s.db.Collection(Sequences).Find(ctx, filter, options.Find().SetBatchSize(64))
	if e != nil {
		return e
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		interval, e := s.readHistoryInterval(ctx, d, owner, cursor.Current)
		if e != nil {
			return e
		}
		var proof ownerHistoryImportProof
		if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": interval.ProofID}).Decode(&proof); e != nil {
			return e
		}
		if body {
			var original namespaceimport.OwnerHistorySegment
			if decodeImportJSON(proof.SegmentJSON, &original) != nil || original.ValidateShape() != nil {
				return ErrConflict
			}
			if original.ScopeContract != namespaceimport.OriginalGroupSequenceScopeV1 {
				continue
			}
		}
		// The original proof was fully checked by readHistoryInterval in this same
		// snapshot; this is its exact publication binding, not a second authority.
		var publication namespaceImportCutover
		if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "namespace-import-cutover", cut.MigrationID)}).Decode(&publication); e != nil {
			return e
		}
		if publication.Kind != "namespace-import-cutover-v1" || publication.HeaderSHA256 != proof.HeaderSHA || publication.InventorySHA256 != proof.InventorySHA || publication.ManifestSHA256 != cut.ManifestSHA256 {
			return ErrConflict
		}
		for _, seq := range seqs {
			if seq >= interval.Lower && seq <= interval.Upper {
				allowed[seq] = true
			}
		}
	}
	return cursor.Err()
}
