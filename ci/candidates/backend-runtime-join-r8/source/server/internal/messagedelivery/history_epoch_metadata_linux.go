//go:build linux

package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type historyEpochOriginalDocument struct {
	Collection string `bson:"collection" json:"collection"`
	ID         string `bson:"id" json:"id"`
	Raw        []byte `bson:"raw" json:"raw"`
	SHA256     string `bson:"sha256" json:"sha256"`
}
type historyEpochTransitionRecord struct {
	ID                        string                         `bson:"_id"`
	Kind                      string                         `bson:"kind"`
	PreviousHeaderJSON        string                         `bson:"previous_header_json"`
	PreviousHeaderSHA256      string                         `bson:"previous_header_sha256"`
	NextHeaderJSON            string                         `bson:"next_header_json"`
	NextHeaderSHA256          string                         `bson:"next_header_sha256"`
	PreviousPreparationJSON   string                         `bson:"previous_preparation_json"`
	PreviousPreparationSHA256 string                         `bson:"previous_preparation_sha256"`
	PreviousReadinessJSON     string                         `bson:"previous_readiness_json"`
	PreviousReadinessSHA256   string                         `bson:"previous_readiness_sha256"`
	OriginalDocuments         []historyEpochOriginalDocument `bson:"original_documents"`
	RetirementJSON            string                         `bson:"retirement_json"`
	RetirementSHA256          string                         `bson:"retirement_sha256"`
	SessionSHA256             string                         `bson:"session_sha256"`
	TargetLeaseID             string                         `bson:"target_lease_id"`
	TargetAdmissionSHA256     string                         `bson:"target_admission_sha256"`
}

func historyEpochTransitionID(previous namespaceimport.Header) string {
	return key("permanent", "namespace-epoch-transition", previous.SHA256())
}
func historyEpochOriginalKeys() [][2]string {
	return [][2]string{{Sequences, SchemaID}, {Receipts, SchemaID}, {Effects, SchemaID}, {Sequences, MembershipSchemaID}, {Receipts, MembershipSchemaID}, {Sequences, NamespaceCutoverID}}
}

// This adapter is composed into the real OwnedAgent.HeldMetadataBootstrap.
// It cannot be minted from a JSON Config/Ready bool. A completed genuine old
// reader retirement, the new real exclusion hold and original source custody
// are independent mandatory capabilities. Normal first-epoch bootstrap stays
// on NamespaceImportWriter.PrepareHeldMetadata with no transition adapter.
type HistoricalEpochMetadataBootstrap struct {
	mu                    sync.Mutex
	previous              *HistoryReadinessPreparation
	retirement            *ownedcontrol.VerifiedHistoryReaderRetirement
	target                *NamespaceImportWriter
	targetConfig          namespaceimport.Config
	sessionPath           string
	hold                  *ownedcontrol.AdmissionHold
	original              historyEpochTransitionRecord
	originalSHA256        string
	source                *ownedcontrol.HistoricalCaptureSource
	sourceInventorySHA256 string
	sourceRef             namespaceimport.SourceRef
}

// The original owning operator begins retirement only after its actual known
// ReadReady publication is checked. Advance then stops/joins the original
// actual children; a merely prepared-not-serving child has no runtime drain.
func (p *HistoryReadinessPreparation) BeginReaderRetirement(ctx context.Context, group *ownedcontrol.HistoryReaderLaunchGroup) (*ownedcontrol.VerifiedHistoryReaderRetirement, error) {
	if p == nil || ctx == nil || group == nil {
		return nil, ErrInvalid
	}
	if _, e := p.source.ReadinessSourceCustody(ctx, p.hold, p.writer.header, p.selection.Inventory, p.selection.ImportConfig); e != nil {
		return nil, e
	}
	if e := p.validate(ctx); e != nil {
		return nil, e
	}
	known, found, e := p.writer.readHistoryReadiness(ctx)
	if e != nil {
		return nil, e
	}
	if !found || known.PreparationSHA256 != p.sha256 {
		return nil, ErrUnprepared
	}
	return group.BeginEpochRetirement(ctx, p.writer.header, known.GraphSHA256)
}

func NewHistoricalEpochMetadataBootstrap(previous *HistoryReadinessPreparation, retirement *ownedcontrol.VerifiedHistoryReaderRetirement, target *NamespaceImportWriter, targetConfig namespaceimport.Config, sessionPath string) (*HistoricalEpochMetadataBootstrap, error) {
	if previous == nil || previous.writer == nil || previous.source == nil || retirement == nil || target == nil || !target.validHeader(target.header) || targetConfig.Header != target.header || namespaceimport.DeploymentPolicySHA256(targetConfig) != target.header.DeploymentPolicySHA256 || previous.writer.header.Epoch == target.header.Epoch || previous.writer.store.db.Client() != target.store.db.Client() || !filepath.IsAbs(sessionPath) || filepath.Clean(sessionPath) != sessionPath {
		return nil, ErrInvalid
	}
	raw, e := json.Marshal(targetConfig)
	if e != nil {
		return nil, e
	}
	var copied namespaceimport.Config
	if decodeImportJSON(string(raw), &copied) != nil {
		return nil, ErrInvalid
	}
	c := &HistoricalEpochMetadataBootstrap{previous: previous, retirement: retirement, target: target, targetConfig: copied, sessionPath: sessionPath}
	if e = target.installEpochPublisher(c); e != nil {
		return nil, e
	}
	return c, nil
}

// Bind only a concrete actual target source, never a PageSource interface or
// caller-provided root. The complete signed Inventory/source/raw cutover must
// be rechecked outside the eventual publication transaction before use.
func (c *HistoricalEpochMetadataBootstrap) BindCapturedSource(source *ownedcontrol.HistoricalCaptureSource) error {
	if c == nil || source == nil || source == c.previous.source {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.source != nil && c.source != source {
		return ErrConflict
	}
	c.source = source
	return nil
}

func (c *HistoricalEpochMetadataBootstrap) PrepareHeldMetadata(ctx context.Context, hold *ownedcontrol.AdmissionHold, h namespaceimport.Header) error {
	if c == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || h != c.target.header || hold == nil || hold.HeaderSHA256() != h.SHA256() || hold.ValidateExclusion() != nil {
		return ErrInvalid
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	previous := c.previous
	old := previous.writer
	next := c.target
	if c.hold != nil && c.hold != hold {
		return ErrConflict
	}
	c.hold = hold
	if e := next.checkLiveTarget(ctx, true); e != nil {
		return e
	}
	// Resuming the SAME live capability only inspects its exact immutable
	// commit. It cannot reconstruct a missing original preimage from fresh DB.
	if c.originalSHA256 != "" {
		found, e := next.readEpochTransitionRecord(ctx, c.original.PreviousHeaderSHA256)
		if e != nil {
			return e
		}
		if found == nil || historyReadinessDigest(*found) != c.originalSHA256 {
			return ErrCommitUnconfirmed
		}
		if e := next.store.CheckReady(ctx); e != nil {
			return e
		}
		return next.PrepareHeldMetadata(ctx, hold, h)
	}
	directories, e := previous.source.ReadinessSourceCustody(ctx, previous.hold, old.header, previous.selection.Inventory, previous.selection.ImportConfig)
	if e != nil {
		return e
	}
	if forbiddenHistoryCustodyPath(c.sessionPath, directories) {
		return ErrInvalid
	}
	if e = old.CheckImportCatalogReady(ctx); e != nil {
		return e
	}
	if _, e = old.checkPublishedReadinessData(ctx, previous.selection.Inventory, previous.record.ManifestSHA256); e != nil {
		return e
	}
	known, found, e := old.readHistoryReadiness(ctx)
	if e != nil {
		return e
	}
	if !found || known.PreparationSHA256 != previous.sha256 {
		return ErrUnprepared
	}
	if e = c.retirement.ValidateLocal(hold, old.header, h, known.GraphSHA256); e != nil {
		return e
	}
	_, sessionSHA, e := ownedcontrol.OpenHistoryReadinessSession(c.sessionPath, hold, h, previous.record.InventorySHA256, previous.record.ManifestSHA256, previous.record.RequirementsSHA256)
	if e != nil {
		return e
	}
	oldJSON, _ := json.Marshal(old.header)
	nextJSON, _ := json.Marshal(h)
	prepJSON, _ := json.Marshal(previous.record)
	readyJSON, _ := json.Marshal(known)
	retiredJSON, _ := json.Marshal(c.retirement.Evidence())
	record := historyEpochTransitionRecord{ID: historyEpochTransitionID(old.header), Kind: "namespace-epoch-transition-v1", PreviousHeaderJSON: string(oldJSON), PreviousHeaderSHA256: old.header.SHA256(), NextHeaderJSON: string(nextJSON), NextHeaderSHA256: h.SHA256(), PreviousPreparationJSON: string(prepJSON), PreviousPreparationSHA256: previous.sha256, PreviousReadinessJSON: string(readyJSON), PreviousReadinessSHA256: historyReadinessDigest(known), RetirementJSON: string(retiredJSON), RetirementSHA256: hash(retiredJSON), SessionSHA256: sessionSHA, TargetLeaseID: hold.LeaseID(), TargetAdmissionSHA256: hold.RecordSHA256()}
	for _, key := range historyEpochOriginalKeys() {
		var raw bson.Raw
		if e = next.store.db.Collection(key[0]).FindOne(ctx, bson.M{"_id": key[1]}).Decode(&raw); e != nil {
			return e
		}
		record.OriginalDocuments = append(record.OriginalDocuments, historyEpochOriginalDocument{key[0], key[1], append([]byte(nil), raw...), hash(raw)})
	}
	if e = validateHistoryEpochOriginals(old.header, record); e != nil {
		return e
	}
	// Persist this exact expected record in the live private capability BEFORE
	// one dispatch; neither a failed/unknown ACK nor DB absence renews identity.
	c.original = record
	c.originalSHA256 = historyReadinessDigest(record)
	dispatch, e := ownedcontrol.BeginHistoryReadinessIntent(c.sessionPath+".prepare-intent", sessionSHA, "prepare", c.originalSHA256)
	if e != nil {
		return e
	}
	if !dispatch {
		return ErrCommitUnconfirmed
	}
	e = next.store.transaction(ctx, func(tx context.Context) error {
		if e := hold.ValidateExclusion(); e != nil {
			return e
		}
		if e := c.retirement.ValidateLocal(hold, old.header, h, known.GraphSHA256); e != nil {
			return e
		}
		// Both writers are same actual client/database. The original old epoch's
		// strict strong-TX checks apply BEFORE the only controlled marker changes.
		if e := old.store.requireTX(tx); e != nil {
			return e
		}
		if e := old.store.checkMembershipSchema(tx); e != nil {
			return e
		}
		if e := next.checkStorageIdentity(tx, true); e != nil {
			return e
		}
		current, found, e := old.readHistoryReadiness(tx)
		if e != nil {
			return e
		}
		if !found || historyReadinessDigest(current) != record.PreviousReadinessSHA256 {
			return ErrConflict
		}
		if _, e = old.checkPublishedReadinessData(tx, previous.selection.Inventory, previous.record.ManifestSHA256); e != nil {
			return e
		}
		for _, doc := range record.OriginalDocuments {
			var actual bson.Raw
			if e = next.store.db.Collection(doc.Collection).FindOne(tx, bson.M{"_id": doc.ID}).Decode(&actual); e != nil || hash(actual) != doc.SHA256 {
				return ErrConflict
			}
		}
		if e = immutableImportDocument(tx, next.store.db.Collection(Receipts), record.ID, record); e != nil {
			return e
		}
		schema := bson.D{{Key: "_id", Value: SchemaID}, {Key: "version", Value: int32(1)}, {Key: "epoch", Value: h.Epoch}, {Key: "activation_sha256", Value: h.ActivationSHA256}, {Key: "legacy_writers_retired", Value: true}}
		for _, name := range []string{Sequences, Receipts, Effects} {
			result, e := next.store.db.Collection(name).ReplaceOne(tx, bson.M{"_id": SchemaID, "epoch": old.header.Epoch, "activation_sha256": old.header.ActivationSHA256}, schema)
			if e != nil {
				return e
			}
			if result.MatchedCount != 1 || result.ModifiedCount != 1 {
				return ErrConflict
			}
		}
		for _, name := range []string{Sequences, Receipts} {
			result, e := next.store.db.Collection(name).DeleteOne(tx, bson.M{"_id": MembershipSchemaID, "epoch": old.header.Epoch, "activation_sha256": old.header.ActivationSHA256})
			if e != nil {
				return e
			}
			if result.DeletedCount != 1 {
				return ErrConflict
			}
		}
		// NamespaceCutoverID is deliberately retained until the final new signed
		// publication. Old raw custody and source proof remain observable meanwhile.
		return next.store.requireTX(tx)
	})
	if e != nil {
		return errors.Join(ErrCommitUnconfirmed, e)
	}
	return next.PrepareHeldMetadata(ctx, hold, h)
}

func historyEpochVersionOne(raw bson.Raw) bool {
	n, ok := raw.Lookup("version").Int32OK()
	return ok && n == 1
}

// These sentinels are canonically written with BSON int32 version1. The
// general exactFields helper intentionally admits int64 sequence/revision
// fields; using it for version would contradict historyEpochVersionOne.
func historyEpochMarkerFields(raw bson.Raw, flags []string) bool {
	fields, e := raw.Elements()
	if e != nil || len(fields) != 4+len(flags) || !importUniqueFields(raw) || !historyEpochVersionOne(raw) {
		return false
	}
	for _, name := range []string{"_id", "epoch", "activation_sha256"} {
		if _, ok := raw.Lookup(name).StringValueOK(); !ok {
			return false
		}
	}
	for _, name := range flags {
		if _, ok := raw.Lookup(name).BooleanOK(); !ok {
			return false
		}
	}
	return true
}

func validateHistoryEpochOriginals(h namespaceimport.Header, r historyEpochTransitionRecord) error {
	keys := historyEpochOriginalKeys()
	if len(r.OriginalDocuments) != len(keys) || r.PreviousHeaderSHA256 != h.SHA256() {
		return ErrConflict
	}
	for at, d := range r.OriginalDocuments {
		raw := bson.Raw(d.Raw)
		if d.Collection != keys[at][0] || d.ID != keys[at][1] || hash(d.Raw) != d.SHA256 || !importUniqueFields(raw) {
			return ErrConflict
		}
		id, ok := raw.Lookup("_id").StringValueOK()
		epoch, eok := raw.Lookup("epoch").StringValueOK()
		activation, aok := raw.Lookup("activation_sha256").StringValueOK()
		if !ok || !eok || !aok || id != d.ID || epoch != h.Epoch || activation != h.ActivationSHA256 {
			return ErrConflict
		}
		if at < 3 {
			if !historyEpochMarkerFields(raw, []string{"legacy_writers_retired"}) || !raw.Lookup("legacy_writers_retired").Boolean() {
				return ErrConflict
			}
		}
		if at == 3 || at == 4 {
			if !historyEpochMarkerFields(raw, []string{"legacy_message_writers_retired", "legacy_group_writers_retired", "owner_bounds_seeded", "readers_bound"}) {
				return ErrConflict
			}
			for _, f := range []string{"legacy_message_writers_retired", "legacy_group_writers_retired", "owner_bounds_seeded", "readers_bound"} {
				if !raw.Lookup(f).Boolean() {
					return ErrConflict
				}
			}
		}
		if at == 5 {
			if _, e := decodeHistoryEpochCutover(h, raw); e != nil {
				return e
			}
		}
	}
	return nil
}

func decodeHistoryEpochRecord(raw bson.Raw, previousHeaderSHA string) (*historyEpochTransitionRecord, error) {
	if !validSHA(previousHeaderSHA) {
		return nil, ErrInvalid
	}
	expected := []string{"_id", "kind", "previous_header_json", "previous_header_sha256", "next_header_json", "next_header_sha256", "previous_preparation_json", "previous_preparation_sha256", "previous_readiness_json", "previous_readiness_sha256", "original_documents", "retirement_json", "retirement_sha256", "session_sha256", "target_lease_id", "target_admission_sha256"}
	if !importUniqueFields(raw) {
		return nil, ErrConflict
	}
	elements, e := raw.Elements()
	if e != nil || len(elements) != len(expected) {
		return nil, ErrConflict
	}
	seen := map[string]bool{}
	for _, f := range expected {
		seen[f] = true
	}
	for _, f := range elements {
		if !seen[f.Key()] {
			return nil, ErrConflict
		}
	}
	var r historyEpochTransitionRecord
	var old, next namespaceimport.Header
	var preparation historyReadinessPreparationRecord
	var ready historyReadinessPublication
	if bson.Unmarshal(raw, &r) != nil || r.Kind != "namespace-epoch-transition-v1" || r.PreviousHeaderSHA256 != previousHeaderSHA || decodeImportJSON(r.PreviousHeaderJSON, &old) != nil || old.SHA256() != previousHeaderSHA || old.Validate() != nil || r.ID != historyEpochTransitionID(old) || decodeImportJSON(r.NextHeaderJSON, &next) != nil || next.SHA256() != r.NextHeaderSHA256 || next.Validate() != nil || decodeImportJSON(r.PreviousPreparationJSON, &preparation) != nil || historyReadinessDigest(preparation) != r.PreviousPreparationSHA256 || decodeImportJSON(r.PreviousReadinessJSON, &ready) != nil || historyReadinessDigest(ready) != r.PreviousReadinessSHA256 || ready.PreparationSHA256 != r.PreviousPreparationSHA256 || ready.HeaderSHA256 != previousHeaderSHA || hash([]byte(r.RetirementJSON)) != r.RetirementSHA256 || validateHistoryEpochOriginals(old, r) != nil {
		return nil, ErrConflict
	}
	for _, s := range []string{r.SessionSHA256, r.TargetLeaseID, r.TargetAdmissionSHA256} {
		if !validSHA(s) {
			return nil, ErrConflict
		}
	}
	if old.Epoch == next.Epoch || old.ActivationSHA256 == next.ActivationSHA256 || old.Tenant != next.Tenant || old.Database != next.Database || old.StorageInstance != next.StorageInstance || old.ReplicaSet != next.ReplicaSet || old.RedisRunID != next.RedisRunID || old.RedisDB != next.RedisDB || old.KafkaClusterID != next.KafkaClusterID {
		return nil, ErrConflict
	}
	return &r, nil
}

func (w *NamespaceImportWriter) readEpochTransitionRecord(ctx context.Context, previousHeaderSHA string) (*historyEpochTransitionRecord, error) {
	if w == nil || !validSHA(previousHeaderSHA) {
		return nil, ErrInvalid
	}
	var raw bson.Raw
	e := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key("permanent", "namespace-epoch-transition", previousHeaderSHA)}).Decode(&raw)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if e != nil {
		return nil, e
	}
	decoded, e := decodeHistoryEpochRecord(raw, previousHeaderSHA)
	if e != nil {
		return nil, e
	}
	r := *decoded
	var old namespaceimport.Header
	if decodeImportJSON(r.PreviousHeaderJSON, &old) != nil {
		return nil, ErrConflict
	}
	// Preserve and independently re-read the original immutable records. This
	// historical evidence never uses today's marker as the old epoch's proof.
	oldStore, e := NewStore(w.store.db, old.Epoch, old.ActivationSHA256)
	if e != nil {
		return nil, e
	}
	oldWriter, e := NewNamespaceImportWriter(oldStore, old)
	if e != nil {
		return nil, e
	}
	actualPreparation, present, e := oldWriter.readHistoryPreparation(ctx)
	if e != nil {
		return nil, e
	}
	if !present || historyReadinessDigest(actualPreparation) != r.PreviousPreparationSHA256 {
		return nil, ErrConflict
	}
	actualReady, present, e := oldWriter.readHistoryReadiness(ctx)
	if e != nil {
		return nil, e
	}
	if !present || historyReadinessDigest(actualReady) != r.PreviousReadinessSHA256 {
		return nil, ErrConflict
	}
	return &r, nil
}
