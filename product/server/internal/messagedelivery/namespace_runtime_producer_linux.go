//go:build linux

package messagedelivery

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type namespaceRuntimePreparationRecord struct {
	ID                       string `bson:"_id"`
	Kind                     string `bson:"kind"`
	HeaderSHA256             string `bson:"header_sha256"`
	HistoryPreparationID     string `bson:"history_preparation_id"`
	HistoryPreparationSHA256 string `bson:"history_preparation_sha256"`
	InventorySHA256          string `bson:"inventory_sha256"`
	ManifestSHA256           string `bson:"manifest_sha256"`
	MongoCatalogJSON         string `bson:"mongo_catalog_json"`
	MongoCatalogSHA256       string `bson:"mongo_catalog_sha256"`
	RequirementsJSON         string `bson:"requirements_json"`
	RequirementsSHA256       string `bson:"requirements_sha256"`
	ReleaseKeyID             string `bson:"release_key_id"`
	ReleasePublicKey         []byte `bson:"release_public_key"`
	SessionSHA256            string `bson:"session_sha256"`
	TokenSHA256              string `bson:"token_sha256"`
	LeaseID                  string `bson:"lease_id"`
	AdmissionGuardSHA256     string `bson:"admission_guard_sha256"`
}
type NamespaceRuntimePreparation struct {
	history      *HistoryReadinessPreparation
	record       namespaceRuntimePreparationRecord
	sha256       string
	sessionPath  string
	session      ownedcontrol.HistoryReadinessSession
	requirements []ownedcontrol.NamespaceRuntimeRequirement
	releaseKey   ed25519.PrivateKey
}

func (w *NamespaceImportWriter) readNamespaceRuntimePreparation(ctx context.Context) (namespaceRuntimePreparationRecord, bool, error) {
	var row namespaceRuntimePreparationRecord
	var raw bson.Raw
	e := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": ownedcontrol.NamespaceRuntimePreparationID(w.header)}).Decode(&raw)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return row, false, nil
	}
	if e != nil {
		return row, false, e
	}
	fields := []string{"_id", "kind", "header_sha256", "history_preparation_id", "history_preparation_sha256", "inventory_sha256", "manifest_sha256", "mongo_catalog_json", "mongo_catalog_sha256", "requirements_json", "requirements_sha256", "release_key_id", "release_public_key", "session_sha256", "token_sha256", "lease_id", "admission_guard_sha256"}
	if !importUniqueFields(raw) {
		return row, false, ErrConflict
	}
	elements, e := raw.Elements()
	if e != nil || len(elements) != len(fields) {
		return row, false, ErrConflict
	}
	known := map[string]bool{}
	for _, k := range fields {
		known[k] = true
	}
	for _, element := range elements {
		if !known[element.Key()] {
			return row, false, ErrConflict
		}
	}
	var requirements []ownedcontrol.NamespaceRuntimeRequirement
	if bson.Unmarshal(raw, &row) != nil || row.ID != ownedcontrol.NamespaceRuntimePreparationID(w.header) || row.Kind != "namespace-runtime-preparation-v1" || row.HeaderSHA256 != w.header.SHA256() || row.HistoryPreparationID != historyPreparationID(w.header) || decodeImportJSON(row.RequirementsJSON, &requirements) != nil || len(row.ReleasePublicKey) != ed25519.PublicKeySize || !identity(row.ReleaseKeyID) {
		return row, false, ErrConflict
	}
	var catalog []ownedcontrol.NamespaceRuntimeMongoCollection
	if decodeImportJSON(row.MongoCatalogJSON, &catalog) != nil {
		return row, false, ErrConflict
	}
	catalogSHA, e := ownedcontrol.NamespaceRuntimeMongoCatalogSHA256(catalog)
	if e != nil || catalogSHA != row.MongoCatalogSHA256 {
		return row, false, ErrConflict
	}
	sum, e := ownedcontrol.NamespaceRuntimeRequirementsSHA256(requirements)
	if e != nil || sum != row.RequirementsSHA256 {
		return row, false, ErrConflict
	}
	for _, s := range []string{row.HistoryPreparationSHA256, row.InventorySHA256, row.ManifestSHA256, row.SessionSHA256, row.TokenSHA256, row.LeaseID, row.AdmissionGuardSHA256} {
		if !validSHA(s) {
			return row, false, ErrConflict
		}
	}
	// Actual original signature/config/inventory validation, not a self-signed
	// runtime registration or a new set of marker booleans.
	history, present, e := w.readHistoryPreparation(ctx)
	if e != nil {
		return row, false, e
	}
	if !present || historyReadinessDigest(history) != row.HistoryPreparationSHA256 || history.InventorySHA256 != row.InventorySHA256 || history.ManifestSHA256 != row.ManifestSHA256 || history.LeaseID != row.LeaseID || history.AdmissionGuardSHA256 != row.AdmissionGuardSHA256 {
		return row, false, ErrConflict
	}
	return row, true, nil
}

// Prepare BEFORE starting the two original preparing readers. Runtime tickets
// name their original immutable history preparation, not a guessed future
// ReadReady SHA. Final publication independently reads the actual ReadReady.
func (p *HistoryReadinessPreparation) PrepareNamespaceRuntime(ctx context.Context, requirements []ownedcontrol.NamespaceRuntimeRequirement, sessionPath, keyID string, privateKey ed25519.PrivateKey) (*NamespaceRuntimePreparation, error) {
	if p == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || p.validate(ctx) != nil || !filepath.IsAbs(sessionPath) || filepath.Clean(sessionPath) != sessionPath || !identity(keyID) || len(privateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalid
	}
	directories, e := p.source.ReadinessSourceCustody(ctx, p.hold, p.writer.header, p.selection.Inventory, p.selection.ImportConfig)
	if e != nil {
		return nil, e
	}
	if forbiddenHistoryCustodyPath(sessionPath, directories) || sessionPath == p.selection.PrivateSessionPath {
		return nil, ErrInvalid
	}
	catalog, e := p.source.ReadinessMongoCatalog(ctx, p.hold, p.writer.header, p.selection.Inventory)
	if e != nil {
		return nil, e
	}
	catalogSHA, e := ownedcontrol.NamespaceRuntimeMongoCatalogSHA256(catalog)
	if e != nil {
		return nil, e
	}
	catalogJSON, e := json.Marshal(catalog)
	if e != nil {
		return nil, e
	}
	requirementsSHA, e := ownedcontrol.NamespaceRuntimeRequirementsSHA256(requirements)
	if e != nil {
		return nil, e
	}
	raw, e := json.Marshal(requirements)
	if e != nil {
		return nil, e
	}
	var frozen []ownedcontrol.NamespaceRuntimeRequirement
	if decodeImportJSON(string(raw), &frozen) != nil {
		return nil, ErrInvalid
	}
	// The two actual source-pinned reader processes remain the very same ones;
	// a writer graph may not substitute another bootstrap/source after DataReady.
	for _, reader := range p.selection.Readers {
		found := false
		for _, r := range frozen {
			if r.Role == reader.Role {
				left, _ := json.Marshal(r.Services)
				right, _ := json.Marshal(reader.Services)
				found = r.BinarySHA256 == reader.BinarySHA256 && r.SourceSHA256 == reader.SourceSHA256 && r.BuildReceiptSHA256 == reader.BuildReceiptSHA256 && r.BootstrapSHA256 == reader.BootstrapSHA256 && r.ConfigurationSHA256 == reader.ConfigurationSHA256 && r.EnvironmentSHA256 == reader.EnvironmentSHA256 && string(left) == string(right)
			}
		}
		if !found {
			return nil, ErrConflict
		}
	}
	session, sessionSHA, e := ownedcontrol.OpenHistoryReadinessSession(sessionPath, p.hold, p.writer.header, p.record.InventorySHA256, p.record.ManifestSHA256, requirementsSHA)
	if e != nil {
		return nil, e
	}
	public := privateKey.Public().(ed25519.PublicKey)
	row := namespaceRuntimePreparationRecord{ID: ownedcontrol.NamespaceRuntimePreparationID(p.writer.header), Kind: "namespace-runtime-preparation-v1", HeaderSHA256: p.writer.header.SHA256(), HistoryPreparationID: p.record.ID, HistoryPreparationSHA256: p.sha256, InventorySHA256: p.record.InventorySHA256, ManifestSHA256: p.record.ManifestSHA256, MongoCatalogJSON: string(catalogJSON), MongoCatalogSHA256: catalogSHA, RequirementsJSON: string(raw), RequirementsSHA256: requirementsSHA, ReleaseKeyID: keyID, ReleasePublicKey: append([]byte(nil), public...), SessionSHA256: sessionSHA, TokenSHA256: hash([]byte(session.Token)), LeaseID: p.hold.LeaseID(), AdmissionGuardSHA256: p.hold.RecordSHA256()}
	expected := historyReadinessDigest(row)
	cap := &NamespaceRuntimePreparation{history: p, record: row, sha256: expected, sessionPath: sessionPath, session: session, requirements: frozen, releaseKey: append(ed25519.PrivateKey(nil), privateKey...)}
	known, found, e := p.writer.readNamespaceRuntimePreparation(ctx)
	if e != nil {
		return nil, e
	}
	if found {
		if historyReadinessDigest(known) != expected {
			return nil, ErrConflict
		}
		return cap, nil
	}
	dispatch, e := ownedcontrol.BeginHistoryReadinessIntent(sessionPath+".prepare-intent", sessionSHA, "prepare", expected)
	if e != nil {
		return nil, e
	}
	if !dispatch {
		return nil, ErrCommitUnconfirmed
	}
	e = p.writer.store.transaction(ctx, func(tx context.Context) error {
		if e := p.validate(tx); e != nil {
			return e
		}
		if e := p.writer.store.requireTX(tx); e != nil {
			return e
		}
		if e := p.writer.checkStorageIdentity(tx, true); e != nil {
			return e
		}
		if _, e := p.writer.checkPublishedReadinessData(tx, p.selection.Inventory, p.record.ManifestSHA256); e != nil {
			return e
		}
		return immutableImportDocument(tx, p.writer.store.db.Collection(Receipts), row.ID, row)
	})
	if e != nil {
		return nil, errors.Join(ErrCommitUnconfirmed, e)
	}
	return cap, nil
}
func (p *NamespaceRuntimePreparation) validate(ctx context.Context) error {
	if p == nil || p.history == nil || p.history.validate(ctx) != nil || len(p.releaseKey) != ed25519.PrivateKeySize {
		return ErrUnprepared
	}
	row, found, e := p.history.writer.readNamespaceRuntimePreparation(ctx)
	if e != nil {
		return e
	}
	if !found || historyReadinessDigest(row) != p.sha256 {
		return ErrConflict
	}
	return nil
}
func (p *NamespaceRuntimePreparation) WriteRuntimeBootstrap(ctx context.Context, role, instance, path, registrationPath, releasePath string) (ownedcontrol.NamespaceRuntimeBootstrap, string, error) {
	var b ownedcontrol.NamespaceRuntimeBootstrap
	if p == nil || p.validate(ctx) != nil || forbiddenHistoryCustodyPath(path, p.history.custodyDirectories) || forbiddenHistoryCustodyPath(registrationPath, p.history.custodyDirectories) || forbiddenHistoryCustodyPath(releasePath, p.history.custodyDirectories) || path == p.sessionPath || path == registrationPath || path == releasePath {
		return b, "", ErrUnprepared
	}
	var selected *ownedcontrol.NamespaceRuntimeRequirement
	for _, r := range p.requirements {
		if r.Role == role {
			copy := r
			selected = &copy
		}
	}
	if selected == nil {
		return b, "", ErrInvalid
	}
	var catalog []ownedcontrol.NamespaceRuntimeMongoCollection
	if decodeImportJSON(p.record.MongoCatalogJSON, &catalog) != nil {
		return b, "", ErrConflict
	}
	b = ownedcontrol.NamespaceRuntimeBootstrap{Version: "namespace-runtime-private-bootstrap-v1", Header: p.history.writer.header, HistoryPreparationID: p.history.record.ID, HistoryPreparationSHA256: p.history.sha256, InventorySHA256: p.record.InventorySHA256, ManifestSHA256: p.record.ManifestSHA256, MongoCatalog: catalog, MongoCatalogSHA256: p.record.MongoCatalogSHA256, RuntimePreparationID: p.record.ID, RuntimePreparationSHA256: p.sha256, RuntimeToken: p.session.Token, RequirementsSHA256: p.record.RequirementsSHA256, Requirement: *selected, ExpectedInstanceID: instance, RegistrationPath: registrationPath, ReleasePath: releasePath, ReleaseKeyID: p.record.ReleaseKeyID, ReleasePublicKey: append(ed25519.PublicKey(nil), p.record.ReleasePublicKey...)}
	sum, e := ownedcontrol.WriteNamespaceRuntimeBootstrap(path, b)
	return b, sum, e
}
