//go:build linux

package messagedelivery

import (
	"context"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func (c *HistoricalEpochMetadataBootstrap) prepareEpochPublish(ctx context.Context, permit *namespaceimport.Permit, manifest namespaceimport.VerifiedManifest) error {
	if c == nil || ctx == nil || permit == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	c.mu.Lock()
	source, hold, config, original, expected := c.source, c.hold, c.targetConfig, c.original, c.originalSHA256
	c.mu.Unlock()
	if source == nil || hold == nil || expected == "" || len(original.OriginalDocuments) != 6 || permit.ValidateManifest(c.target.header, manifest) != nil || permit.LeaseID() != hold.LeaseID() {
		return ErrUnprepared
	}
	// Genuine source/Agent refresh outside the Mongo retry. This returns only the
	// original captured metadata reference, not fresh BSON or a mutable counter.
	ref, e := source.ValidateCapturedEpochCutover(ctx, hold, c.target.header, manifest.Inventory(), config, original.OriginalDocuments[5].SHA256)
	if e != nil {
		return e
	}
	var previousReady historyReadinessPublication
	if decodeImportJSON(original.PreviousReadinessJSON, &previousReady) != nil {
		return ErrConflict
	}
	if e = c.retirement.ValidateLocal(hold, c.previous.writer.header, c.target.header, previousReady.GraphSHA256); e != nil {
		return e
	}
	known, e := c.target.readEpochTransitionRecord(ctx, original.PreviousHeaderSHA256)
	if e != nil {
		return e
	}
	if known == nil || historyReadinessDigest(*known) != expected {
		return ErrConflict
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.source != source || c.hold != hold || c.originalSHA256 != expected {
		return ErrConflict
	}
	if c.sourceInventorySHA256 != "" && (c.sourceInventorySHA256 != manifest.Inventory().SHA256() || c.sourceRef != ref) {
		return ErrConflict
	}
	c.sourceInventorySHA256 = manifest.Inventory().SHA256()
	c.sourceRef = ref
	return nil
}

func (c *HistoricalEpochMetadataBootstrap) publishEpochCutover(ctx context.Context, permit *namespaceimport.Permit, manifest namespaceimport.VerifiedManifest, cut NamespaceCutover) error {
	if c == nil || ctx == nil || permit == nil || mongo.SessionFromContext(ctx) == nil {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hold == nil || c.originalSHA256 == "" || c.source == nil || c.sourceInventorySHA256 != manifest.Inventory().SHA256() || permit.ValidateManifest(c.target.header, manifest) != nil || permit.LeaseID() != c.hold.LeaseID() || permit.FenceObservation().AdmissionGuardSHA256 != c.hold.RecordSHA256() {
		return ErrUnprepared
	}
	var ready historyReadinessPublication
	if decodeImportJSON(c.original.PreviousReadinessJSON, &ready) != nil {
		return ErrConflict
	}
	if e := c.retirement.ValidateLocal(c.hold, c.previous.writer.header, c.target.header, ready.GraphSHA256); e != nil {
		return e
	}
	known, e := c.target.readEpochTransitionRecord(ctx, c.original.PreviousHeaderSHA256)
	if e != nil {
		return e
	}
	if known == nil || historyReadinessDigest(*known) != c.originalSHA256 {
		return ErrConflict
	}
	if e = c.target.store.requireTX(ctx); e != nil {
		return e
	}
	if e = c.target.checkStorageIdentity(ctx, true); e != nil {
		return e
	}
	previous := c.original.OriginalDocuments[5]
	var current bson.Raw
	if e = c.target.store.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": NamespaceCutoverID}).Decode(&current); e != nil {
		return e
	}
	if hash(current) != previous.SHA256 {
		return ErrConflict
	}
	if cut.ID != NamespaceCutoverID || cut.Epoch != c.target.header.Epoch || cut.ActivationSHA256 != c.target.header.ActivationSHA256 || cut.MigrationID != c.target.header.MigrationID || cut.ManifestSHA256 != manifest.SHA256() {
		return ErrConflict
	}
	result, e := c.target.store.db.Collection(Sequences).ReplaceOne(ctx, bson.M{"_id": NamespaceCutoverID, "epoch": c.previous.writer.header.Epoch, "activation_sha256": c.previous.writer.header.ActivationSHA256, "manifest_sha256": c.previous.record.ManifestSHA256}, cut)
	if e != nil {
		return e
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	refJSON, e := json.Marshal(c.sourceRef)
	if e != nil {
		return e
	}
	id := key(c.target.header.Epoch, "namespace-epoch-publication", c.target.header.MigrationID)
	receipt := bson.D{{Key: "_id", Value: id}, {Key: "kind", Value: "namespace-epoch-publication-v1"}, {Key: "header_sha256", Value: c.target.header.SHA256()}, {Key: "previous_header_sha256", Value: c.original.PreviousHeaderSHA256}, {Key: "transition_sha256", Value: c.originalSHA256}, {Key: "inventory_sha256", Value: c.sourceInventorySHA256}, {Key: "raw_source_root_sha256", Value: manifest.Inventory().RawRootSHA256}, {Key: "original_cutover_ref_json", Value: string(refJSON)}, {Key: "manifest_sha256", Value: manifest.SHA256()}, {Key: "cutover_sha256", Value: historyReadinessDigest(cut)}, {Key: "attempt_id", Value: permit.AttemptID()}}
	return immutableImportDocument(ctx, c.target.store.db.Collection(Receipts), id, receipt)
}

// A historical inspector can read its genuine old immutable receipt after the
// active singleton moved forward. The original fixed ID is NOT repurposed as
// an old Ready grant, and old runtime CheckReady still rejects the new epoch.
func (w *NamespaceImportWriter) readPublishedNamespaceCutover(ctx context.Context, expectedSHA string) (NamespaceCutover, string, error) {
	current, sum, currentErr := w.store.namespaceCutover(ctx)
	if currentErr == nil && sum == expectedSHA {
		return current, sum, nil
	}
	archive, e := w.readEpochTransitionRecord(ctx, w.header.SHA256())
	if e != nil {
		return NamespaceCutover{}, "", e
	}
	if archive == nil {
		if currentErr == nil {
			currentErr = ErrConflict
		}
		return NamespaceCutover{}, "", currentErr
	}
	previous, found, e := w.readHistoryReadiness(ctx)
	if e != nil {
		return NamespaceCutover{}, "", e
	}
	if !found || historyReadinessDigest(previous) != archive.PreviousReadinessSHA256 {
		return NamespaceCutover{}, "", ErrConflict
	}
	original := archive.OriginalDocuments[5]
	cut, e := decodeHistoryEpochCutover(w.header, bson.Raw(original.Raw))
	if e != nil || cut.ManifestSHA256 != previous.ManifestSHA256 {
		return NamespaceCutover{}, "", ErrConflict
	}
	if historyReadinessDigest(cut) != expectedSHA {
		return NamespaceCutover{}, "", ErrConflict
	}
	return cut, expectedSHA, nil
}

// Inspect is read-only, including after Mongo process restart under the same
// tenant/storage UUID/replica set. A missing record does NOT permit redispatch.
type HistoricalEpochTransitionInspection struct {
	Found                  bool
	TransitionSHA256       string
	PreviousHeaderSHA256   string
	NextHeaderSHA256       string
	TargetBaseMarkersMatch bool
	KnownTargetPublication bool
}

func (w *NamespaceImportWriter) InspectHistoricalEpochTransition(ctx context.Context, previous namespaceimport.Header) (HistoricalEpochTransitionInspection, error) {
	var out HistoricalEpochTransitionInspection
	if w == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || previous.Validate() != nil {
		return out, ErrInvalid
	}
	if e := w.checkImportInspectionReady(ctx); e != nil {
		return out, e
	}
	record, e := w.readEpochTransitionRecord(ctx, previous.SHA256())
	if e != nil {
		return out, e
	}
	if record == nil {
		return out, nil
	}
	if record.NextHeaderSHA256 != w.header.SHA256() || record.PreviousHeaderSHA256 != previous.SHA256() {
		return out, ErrConflict
	}
	out.Found = true
	out.TransitionSHA256 = historyReadinessDigest(*record)
	out.PreviousHeaderSHA256 = previous.SHA256()
	out.NextHeaderSHA256 = w.header.SHA256()
	out.TargetBaseMarkersMatch = w.store.checkEpoch(ctx) == nil
	known, e := w.readImportCutover(ctx)
	if e != nil {
		return out, e
	}
	out.KnownTargetPublication = known.Found
	return out, nil
}
