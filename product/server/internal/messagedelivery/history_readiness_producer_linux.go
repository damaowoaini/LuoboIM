//go:build linux

package messagedelivery

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// This constructor is internal offline operator composition, never RPC input.
// A signed inventory alone is NOT an exclusion proof or a reader grant. The
// actual current AdmissionHold, exact real import publication and actual owned
// process registrations remain required at each phase below.
type HistoryReadinessSelection struct {
	ImportConfig       namespaceimport.Config
	Inventory          namespaceimport.Inventory
	ManifestSHA256     string
	Readers            []ownedcontrol.HistoryReaderRequirement
	PrivateSessionPath string
}
type historyReadinessPreparationRecord struct {
	ID                   string `bson:"_id"`
	Kind                 string `bson:"kind"`
	Epoch                string `bson:"epoch"`
	HeaderJSON           string `bson:"header_json"`
	HeaderSHA256         string `bson:"header_sha256"`
	InventoryJSON        string `bson:"inventory_json"`
	InventorySHA256      string `bson:"inventory_sha256"`
	ManifestJSON         string `bson:"manifest_json"`
	ManifestSHA256       string `bson:"manifest_sha256"`
	ConfigJSON           string `bson:"config_json"`
	RequirementsJSON     string `bson:"requirements_json"`
	RequirementsSHA256   string `bson:"requirements_sha256"`
	SessionSHA256        string `bson:"session_sha256"`
	TokenSHA256          string `bson:"token_sha256"`
	LeaseID              string `bson:"lease_id"`
	AdmissionGuardSHA256 string `bson:"admission_guard_sha256"`
}
type historyReadinessPublication struct {
	ID                   string `bson:"_id"`
	Kind                 string `bson:"kind"`
	Epoch                string `bson:"epoch"`
	HeaderSHA256         string `bson:"header_sha256"`
	PreparationID        string `bson:"preparation_id"`
	PreparationSHA256    string `bson:"preparation_sha256"`
	InventorySHA256      string `bson:"inventory_sha256"`
	ManifestSHA256       string `bson:"manifest_sha256"`
	RequirementsSHA256   string `bson:"requirements_sha256"`
	GraphJSON            string `bson:"graph_json"`
	GraphSHA256          string `bson:"graph_sha256"`
	LeaseID              string `bson:"lease_id"`
	AdmissionGuardSHA256 string `bson:"admission_guard_sha256"`
}

func historyPreparationID(h namespaceimport.Header) string {
	return key(h.Epoch, "history-readiness-preparation", h.MigrationID)
}
func historyReadinessID(h namespaceimport.Header) string {
	return key(h.Epoch, "history-readiness-publication", h.MigrationID)
}
func historyReadinessDigest(v any) string {
	body, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return hash(body)
}
func historyPreparationStrings() []string {
	return []string{"_id", "kind", "epoch", "header_json", "header_sha256", "inventory_json", "inventory_sha256", "manifest_json", "manifest_sha256", "config_json", "requirements_json", "requirements_sha256", "session_sha256", "token_sha256", "lease_id", "admission_guard_sha256"}
}
func historyPublicationStrings() []string {
	return []string{"_id", "kind", "epoch", "header_sha256", "preparation_id", "preparation_sha256", "inventory_sha256", "manifest_sha256", "requirements_sha256", "graph_json", "graph_sha256", "lease_id", "admission_guard_sha256"}
}

// Cannot be constructed from JSON, a SHA, Ready bool or an external event.
// Reopening it requires the same original private session and known receipt.
type HistoryReadinessPreparation struct {
	writer             *NamespaceImportWriter
	hold               *ownedcontrol.AdmissionHold
	source             *ownedcontrol.HistoricalCaptureSource
	custodyDirectories []string
	selection          HistoryReadinessSelection
	session            ownedcontrol.HistoryReadinessSession
	record             historyReadinessPreparationRecord
	sha256             string
}

func validateReadinessSelection(h namespaceimport.Header, s HistoryReadinessSelection) error {
	c, i := s.ImportConfig, s.Inventory
	if h.Validate() != nil || c.Header != h || namespaceimport.DeploymentPolicySHA256(c) != h.DeploymentPolicySHA256 || len(c.CapturePublicKey) != ed25519.PublicKeySize || c.CaptureKeyID != i.KeyID || c.CaptureAgent != i.CaptureAgent || i.HeaderSHA256 != h.SHA256() || i.OriginalTrust != "UNTRUSTED" || !validSHA(s.ManifestSHA256) {
		return ErrInvalid
	}
	unsigned := i
	unsigned.Signature = nil
	body, e := json.Marshal(unsigned)
	if e != nil || !ed25519.Verify(c.CapturePublicKey, body, i.Signature) {
		return ErrConflict
	}
	if _, e = ownedcontrol.HistoryReaderRequirementsSHA256(s.Readers); e != nil {
		return ErrInvalid
	}
	for _, r := range s.Readers {
		if r.ReadContractSHA256 != HistoryReaderContractSHA256() {
			return ErrConflict
		}
	}
	return nil
}

// All historical work has ALREADY been bounded by Controller's pages/closures.
// This is O(1) in owners/history: exact terminal receipt, actual state and actual
// completed set roots/counts, never another table sweep or caller-only counts.
func (w *NamespaceImportWriter) checkPublishedReadinessData(ctx context.Context, i namespaceimport.Inventory, expectedManifest string) (namespaceImportCutover, error) {
	var row namespaceImportCutover
	if w == nil || !w.validHeader(w.header) || ctx == nil {
		return row, ErrInvalid
	}
	known, e := w.readImportCutover(ctx)
	if e != nil || !known.Found || known.Receipt.ManifestSHA256 != expectedManifest {
		if e == nil {
			e = ErrUnprepared
		}
		return row, e
	}
	if e = w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importCutoverReceiptID(w.header)}).Decode(&row); e != nil {
		return row, e
	}
	var m namespaceimport.ManifestData
	if decodeImportJSON(row.ManifestJSON, &m) != nil || row.HeaderSHA256 != w.header.SHA256() || row.InventorySHA256 != i.SHA256() || row.ManifestSHA256 != expectedManifest || m.InventorySHA256 != i.SHA256() || m.PageCount != i.PageCount || m.Counts != i.Counts || m.CoverageCount != i.CoverageCount || m.CoverageSetRootSHA256 != i.CoverageSetRootSHA256 || m.ProspectiveCount != i.ProspectiveCount || m.ProspectiveSetRootSHA256 != i.ProspectiveSetRootSHA256 || m.OwnerHistorySegmentCount != i.OwnerHistorySegmentCount || m.OwnerHistorySegmentSetRootSHA256 != i.OwnerHistorySegmentSetRootSHA256 {
		return row, ErrConflict
	}
	state, found, e := w.readImportState(ctx)
	if e != nil || !found || state.NextPage != int64(i.PageCount) || state.LastPageSHA256 != m.FinalPageSHA256 || state.InventorySHA256 != i.SHA256() || state.InventoryBindingSHA256 != i.BindingSHA256() || state.RawSourceRootSHA256 != i.RawRootSHA256 || state.Origins != int64(i.Counts.Origins) || state.Windows != int64(i.Counts.Windows) || state.Quarantines != int64(i.Counts.Quarantines) || state.Prospective != int64(i.ProspectiveCount) || state.HistorySegments != int64(i.OwnerHistorySegmentCount) {
		if e == nil {
			e = ErrConflict
		}
		return row, e
	}
	if e = w.checkCompletedImportSet(ctx, "coverage", "", i.SHA256(), i.CoverageSetRootSHA256, i.CoverageCount); e != nil {
		return row, e
	}
	for _, set := range []struct {
		kind, root string
		count      uint64
	}{{"prospective", i.ProspectiveSetRootSHA256, i.ProspectiveCount}, {"history-segments", i.OwnerHistorySegmentSetRootSHA256, i.OwnerHistorySegmentCount}} {
		if set.root != "" || set.count != 0 {
			if e = w.checkCompletedImportSet(ctx, set.kind, "", i.SHA256(), set.root, set.count); e != nil {
				return row, e
			}
		}
	}
	return row, nil
}

// Canonical immutable identity only. Signature verification remains mandatory
// before the private startup binding is created; later reads recheck this whole
// exact row against that capability, not a cross-invocation decode cache.
func (w *NamespaceImportWriter) readHistoryPreparationDocument(ctx context.Context) (historyReadinessPreparationRecord, bool, error) {
	var row historyReadinessPreparationRecord
	var raw bson.Raw
	e := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": historyPreparationID(w.header)}).Decode(&raw)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return row, false, nil
	}
	if e != nil {
		return row, false, e
	}
	if !exactFields(raw, historyPreparationStrings(), nil, nil, "") || bson.Unmarshal(raw, &row) != nil || row.ID != historyPreparationID(w.header) || row.Kind != "history-readiness-preparation-v1" || row.Epoch != w.header.Epoch || row.HeaderSHA256 != w.header.SHA256() {
		return row, false, ErrConflict
	}
	return row, true, nil
}
func (w *NamespaceImportWriter) readBoundHistoryPreparation(ctx context.Context, expectedSHA string) (historyReadinessPreparationRecord, bool, error) {
	row, found, e := w.readHistoryPreparationDocument(ctx)
	if e != nil || !found {
		return row, found, e
	}
	if !validSHA(expectedSHA) || historyReadinessDigest(row) != expectedSHA {
		return row, false, ErrConflict
	}
	return row, true, nil
}
func (w *NamespaceImportWriter) readHistoryPreparation(ctx context.Context) (historyReadinessPreparationRecord, bool, error) {
	row, found, e := w.readHistoryPreparationDocument(ctx)
	if e != nil || !found {
		return row, found, e
	}
	var h namespaceimport.Header
	var i namespaceimport.Inventory
	var c namespaceimport.Config
	var m namespaceimport.ManifestData
	var requirements []ownedcontrol.HistoryReaderRequirement
	if decodeImportJSON(row.HeaderJSON, &h) != nil || h != w.header || decodeImportJSON(row.InventoryJSON, &i) != nil || i.SHA256() != row.InventorySHA256 || decodeImportJSON(row.ConfigJSON, &c) != nil || decodeImportJSON(row.RequirementsJSON, &requirements) != nil || decodeImportJSON(row.ManifestJSON, &m) != nil || hash([]byte(row.ManifestJSON)) != row.ManifestSHA256 || m.HeaderSHA256 != h.SHA256() || m.InventorySHA256 != i.SHA256() {
		return row, false, ErrConflict
	}
	scope := HistoryReadinessSelection{ImportConfig: c, Inventory: i, ManifestSHA256: row.ManifestSHA256, Readers: requirements}
	if validateReadinessSelection(h, scope) != nil {
		return row, false, ErrConflict
	}
	requirementsSHA, e := ownedcontrol.HistoryReaderRequirementsSHA256(requirements)
	if e != nil || requirementsSHA != row.RequirementsSHA256 {
		return row, false, ErrConflict
	}
	for _, v := range []string{row.SessionSHA256, row.TokenSHA256, row.LeaseID, row.AdmissionGuardSHA256} {
		if !validSHA(v) {
			return row, false, ErrConflict
		}
	}
	return row, true, nil
}

func (w *NamespaceImportWriter) PrepareHistoryReadiness(ctx context.Context, hold *ownedcontrol.AdmissionHold, source *ownedcontrol.HistoricalCaptureSource, selected HistoryReadinessSelection) (*HistoryReadinessPreparation, error) {
	if w == nil || !w.validHeader(w.header) || ctx == nil || mongo.SessionFromContext(ctx) != nil || validateReadinessSelection(w.header, selected) != nil || !filepath.IsAbs(selected.PrivateSessionPath) || filepath.Clean(selected.PrivateSessionPath) != selected.PrivateSessionPath || source == nil || hold == nil || hold.HeaderSHA256() != w.header.SHA256() || hold.ValidateExclusion() != nil {
		return nil, ErrInvalid
	}
	// Clone all caller-owned slices/keys before the first durable operation.
	body, e := json.Marshal(selected)
	if e != nil {
		return nil, e
	}
	var s HistoryReadinessSelection
	if json.Unmarshal(body, &s) != nil {
		return nil, ErrInvalid
	}
	directories, e := source.ReadinessSourceCustody(ctx, hold, w.header, s.Inventory, s.ImportConfig)
	if e != nil {
		return nil, e
	}
	if forbiddenHistoryCustodyPath(s.PrivateSessionPath, directories) {
		return nil, ErrInvalid
	}
	if e = w.CheckImportCatalogReady(ctx); e != nil {
		return nil, e
	}
	published, e := w.checkPublishedReadinessData(ctx, s.Inventory, s.ManifestSHA256)
	if e != nil {
		return nil, e
	}
	graphSHA, e := ownedcontrol.HistoryReaderRequirementsSHA256(s.Readers)
	if e != nil {
		return nil, e
	}
	session, sessionSHA, e := ownedcontrol.OpenHistoryReadinessSession(s.PrivateSessionPath, hold, w.header, s.Inventory.SHA256(), s.ManifestSHA256, graphSHA)
	if e != nil {
		return nil, e
	}
	hJSON, _ := json.Marshal(w.header)
	iJSON, _ := json.Marshal(s.Inventory)
	cJSON, _ := json.Marshal(s.ImportConfig)
	rJSON, _ := json.Marshal(s.Readers)
	row := historyReadinessPreparationRecord{ID: historyPreparationID(w.header), Kind: "history-readiness-preparation-v1", Epoch: w.header.Epoch, HeaderJSON: string(hJSON), HeaderSHA256: w.header.SHA256(), InventoryJSON: string(iJSON), InventorySHA256: s.Inventory.SHA256(), ManifestJSON: published.ManifestJSON, ManifestSHA256: s.ManifestSHA256, ConfigJSON: string(cJSON), RequirementsJSON: string(rJSON), RequirementsSHA256: graphSHA, SessionSHA256: sessionSHA, TokenSHA256: hash([]byte(session.Token)), LeaseID: hold.LeaseID(), AdmissionGuardSHA256: hold.RecordSHA256()}
	expected := historyReadinessDigest(row)
	existing, found, e := w.readHistoryPreparation(ctx)
	if e != nil {
		return nil, e
	}
	cap := &HistoryReadinessPreparation{writer: w, hold: hold, source: source, custodyDirectories: directories, selection: s, session: session, record: row, sha256: expected}
	if found {
		if historyReadinessDigest(existing) != expected {
			return nil, ErrConflict
		}
		return cap, nil
	}
	dispatch, e := ownedcontrol.BeginHistoryReadinessIntent(s.PrivateSessionPath+".prepare-intent", sessionSHA, "prepare", expected)
	if e != nil {
		return nil, e
	}
	if !dispatch {
		return nil, ErrCommitUnconfirmed
	}
	e = w.store.transaction(ctx, func(tx context.Context) error {
		if e := hold.ValidateExclusion(); e != nil {
			return e
		}
		if e := w.store.requireTX(tx); e != nil {
			return e
		}
		if e := w.checkStorageIdentity(tx, true); e != nil {
			return e
		}
		if _, e := w.checkPublishedReadinessData(tx, s.Inventory, s.ManifestSHA256); e != nil {
			return e
		}
		// Cannot adopt a previously hand-written compatibility marker as this
		// preparation or use it to open admission before any real registration.
		for _, name := range []string{Sequences, Receipts} {
			if e := absent(tx, w.store.db.Collection(name), bson.M{"_id": MembershipSchemaID}); e != nil {
				return e
			}
		}
		return immutableImportDocument(tx, w.store.db.Collection(Receipts), row.ID, row)
	})
	if e != nil {
		return nil, errors.Join(ErrCommitUnconfirmed, e)
	}
	return cap, nil
}
func (p *HistoryReadinessPreparation) validate(ctx context.Context) error {
	if p == nil || p.writer == nil || p.hold == nil || p.hold.ValidateExclusion() != nil || p.hold.HeaderSHA256() != p.writer.header.SHA256() || p.record.LeaseID != p.hold.LeaseID() || p.record.AdmissionGuardSHA256 != p.hold.RecordSHA256() {
		return ErrUnprepared
	}
	got, found, e := p.writer.readHistoryPreparation(ctx)
	if e != nil {
		return e
	}
	if !found || historyReadinessDigest(got) != p.sha256 {
		return ErrConflict
	}
	return nil
}
func (p *HistoryReadinessPreparation) WriteReaderBootstrap(ctx context.Context, role, path, bindingPath string) (ownedcontrol.HistoryReaderBootstrap, string, error) {
	var b ownedcontrol.HistoryReaderBootstrap
	if p == nil || p.validate(ctx) != nil || path == p.selection.PrivateSessionPath || path == bindingPath || forbiddenHistoryCustodyPath(path, p.custodyDirectories) || forbiddenHistoryCustodyPath(bindingPath, p.custodyDirectories) {
		return b, "", ErrUnprepared
	}
	var selected *ownedcontrol.HistoryReaderRequirement
	for _, r := range p.selection.Readers {
		if r.Role == role {
			copy := r
			selected = &copy
		}
	}
	if selected == nil {
		return b, "", ErrInvalid
	}
	b = ownedcontrol.HistoryReaderBootstrap{Version: "private-history-reader-prepare-v1", Header: p.writer.header, PreparationID: p.record.ID, PreparationSHA256: p.sha256, PreparationToken: p.session.Token, InventorySHA256: p.record.InventorySHA256, ManifestSHA256: p.record.ManifestSHA256, RequirementsSHA256: p.record.RequirementsSHA256, Requirement: *selected, BindingPath: bindingPath}
	sum, e := ownedcontrol.WriteHistoryReaderBootstrap(path, b)
	return b, sum, e
}

// The real operator assembly obtains its launch group from the private known
// preparation, not by passing free-form requirements into a generic launcher.
func (p *HistoryReadinessPreparation) NewReaderLaunchGroup(ctx context.Context, dir string) (*ownedcontrol.HistoryReaderLaunchGroup, error) {
	if p == nil || p.validate(ctx) != nil || forbiddenHistoryCustodyPath(filepath.Join(dir, "role-owner.json"), p.custodyDirectories) {
		return nil, ErrUnprepared
	}
	return ownedcontrol.NewHistoryReaderLaunchGroup(p.hold, dir, p.selection.Readers, p)
}
func (p *HistoryReadinessPreparation) ValidateHistoryReaderLaunch(ctx context.Context, b ownedcontrol.HistoryReaderBootstrap) error {
	if p == nil || p.validate(ctx) != nil || b.ValidateShape() != nil || b.Header != p.writer.header || b.PreparationID != p.record.ID || b.PreparationSHA256 != p.sha256 || b.InventorySHA256 != p.record.InventorySHA256 || b.ManifestSHA256 != p.record.ManifestSHA256 || b.RequirementsSHA256 != p.record.RequirementsSHA256 || forbiddenHistoryCustodyPath(b.BindingPath, p.custodyDirectories) || !historyReadinessTokenMatches(b.PreparationToken, p.record.TokenSHA256) {
		return ErrConflict
	}
	expected, _ := json.Marshal(b.Requirement)
	matches := 0
	for _, r := range p.selection.Readers {
		raw, _ := json.Marshal(r)
		if string(raw) == string(expected) {
			matches++
		}
	}
	if matches != 1 {
		return ErrConflict
	}
	_, e := p.writer.checkPublishedReadinessData(ctx, p.selection.Inventory, p.record.ManifestSHA256)
	return e
}

func (w *NamespaceImportWriter) readHistoryReadiness(ctx context.Context) (historyReadinessPublication, bool, error) {
	preparation, found, e := w.readHistoryPreparation(ctx)
	if e != nil {
		return historyReadinessPublication{}, false, e
	}
	return w.readHistoryReadinessWithPreparation(ctx, preparation, found)
}

// Callers here already possess either a freshly signature-verified record or
// the exact immutable preparation SHA held by the private reader instance.
func (w *NamespaceImportWriter) readHistoryReadinessWithPreparation(ctx context.Context, preparation historyReadinessPreparationRecord, preparationFound bool) (historyReadinessPublication, bool, error) {
	var r historyReadinessPublication
	var raw bson.Raw
	e := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": historyReadinessID(w.header)}).Decode(&raw)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return r, false, nil
	}
	if e != nil {
		return r, false, e
	}
	if !exactFields(raw, historyPublicationStrings(), nil, nil, "") || bson.Unmarshal(raw, &r) != nil || r.ID != historyReadinessID(w.header) || r.Kind != "history-readiness-publication-v1" || r.Epoch != w.header.Epoch || r.HeaderSHA256 != w.header.SHA256() {
		return r, false, ErrConflict
	}
	if !preparationFound || r.PreparationID != preparation.ID || r.PreparationSHA256 != historyReadinessDigest(preparation) || r.InventorySHA256 != preparation.InventorySHA256 || r.ManifestSHA256 != preparation.ManifestSHA256 || r.RequirementsSHA256 != preparation.RequirementsSHA256 || r.LeaseID != preparation.LeaseID || r.AdmissionGuardSHA256 != preparation.AdmissionGuardSHA256 {
		return r, false, ErrConflict
	}
	var requirements []ownedcontrol.HistoryReaderRequirement
	if decodeImportJSON(preparation.RequirementsJSON, &requirements) != nil || len(requirements) != 2 {
		return r, false, ErrConflict
	}
	var rows []ownedcontrol.ObservedHistoryReader
	if decodeImportJSON(r.GraphJSON, &rows) != nil || len(rows) != 2 || hash([]byte(r.GraphJSON)) != r.GraphSHA256 {
		return r, false, ErrConflict
	}
	for n, v := range rows {
		required := requirements[n]
		registration := v.Registration
		if registration.ValidateShape() != nil || v.Role != required.Role || registration.Role != required.Role || v.PID <= 1 || v.StartToken == "" || v.BootID == "" || !validSHA(v.OwnerNonceSHA256) || !validSHA(v.RegistrationSHA256) || v.BinarySHA256 != required.BinarySHA256 || v.SourceSHA256 != required.SourceSHA256 || v.BuildReceiptSHA256 != required.BuildReceiptSHA256 || registration.HeaderSHA256 != w.header.SHA256() || registration.DatabaseSHA256 != hash([]byte(w.header.Database)) || registration.Epoch != w.header.Epoch || registration.ActivationSHA256 != w.header.ActivationSHA256 || registration.PreparationID != preparation.ID || registration.PreparationSHA256 != r.PreparationSHA256 || registration.PreparationTokenSHA256 != preparation.TokenSHA256 || registration.InventorySHA256 != r.InventorySHA256 || registration.ManifestSHA256 != r.ManifestSHA256 || registration.RequirementsSHA256 != r.RequirementsSHA256 || registration.BootstrapSHA256 != required.BootstrapSHA256 || registration.ConfigurationSHA256 != required.ConfigurationSHA256 || registration.EnvironmentSHA256 != required.EnvironmentSHA256 || registration.ReadContractSHA256 != required.ReadContractSHA256 {
			return r, false, ErrConflict
		}
		expected, _ := json.Marshal(required.Services)
		actual, _ := json.Marshal(registration.Services)
		if string(actual) != string(expected) {
			return r, false, ErrConflict
		}
	}
	return r, true, nil
}

// Real old-writer exclusion remains held. No socket is opened, handler served,
// producer activated or StartTransfer gate removed by this publication.
// A caller cannot pass JSON as graph: its type has only a genuine live owned
// exec/nonce/image/registration constructor and a short-lived private seal.
func (p *HistoryReadinessPreparation) PublishReadReady(ctx context.Context, graph *ownedcontrol.VerifiedHistoryReaderGraph) (string, error) {
	if p == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || p.validate(ctx) != nil {
		return "", ErrUnprepared
	}
	if _, e := p.source.ReadinessSourceCustody(ctx, p.hold, p.writer.header, p.selection.Inventory, p.selection.ImportConfig); e != nil {
		return "", e
	}
	if e := graph.Refresh(ctx); e != nil {
		return "", e
	}
	check := func() error {
		return graph.ValidateLocal(p.hold, p.writer.header.SHA256(), p.sha256, p.record.InventorySHA256, p.record.ManifestSHA256, p.record.RequirementsSHA256)
	}
	if e := check(); e != nil {
		return "", e
	}
	body, e := json.Marshal(graph.AuditRows())
	if e != nil {
		return "", e
	}
	w := p.writer
	r := historyReadinessPublication{ID: historyReadinessID(w.header), Kind: "history-readiness-publication-v1", Epoch: w.header.Epoch, HeaderSHA256: w.header.SHA256(), PreparationID: p.record.ID, PreparationSHA256: p.sha256, InventorySHA256: p.record.InventorySHA256, ManifestSHA256: p.record.ManifestSHA256, RequirementsSHA256: p.record.RequirementsSHA256, GraphJSON: string(body), GraphSHA256: hash(body), LeaseID: p.hold.LeaseID(), AdmissionGuardSHA256: p.hold.RecordSHA256()}
	expected := historyReadinessDigest(r)
	current, found, e := w.readHistoryReadiness(ctx)
	if e != nil {
		return "", e
	}
	if found {
		if historyReadinessDigest(current) != expected {
			return "", ErrConflict
		}
		return expected, nil
	}
	dispatch, e := ownedcontrol.BeginHistoryReadinessIntent(p.selection.PrivateSessionPath+".publish-intent", p.record.SessionSHA256, "publish", expected)
	if e != nil {
		return "", e
	}
	if !dispatch {
		return "", ErrCommitUnconfirmed
	}
	e = w.store.transaction(ctx, func(tx context.Context) error {
		if e := p.validate(tx); e != nil {
			return e
		}
		if e := check(); e != nil {
			return e
		}
		if e := w.store.requireTX(tx); e != nil {
			return e
		}
		if e := w.checkStorageIdentity(tx, true); e != nil {
			return e
		}
		if _, e := w.checkPublishedReadinessData(tx, p.selection.Inventory, p.record.ManifestSHA256); e != nil {
			return e
		}
		if e := immutableImportDocument(tx, w.store.db.Collection(Receipts), r.ID, r); e != nil {
			return e
		}
		marker := bson.D{{Key: "_id", Value: MembershipSchemaID}, {Key: "version", Value: int32(1)}, {Key: "epoch", Value: w.header.Epoch}, {Key: "activation_sha256", Value: w.header.ActivationSHA256}, {Key: "legacy_message_writers_retired", Value: true}, {Key: "legacy_group_writers_retired", Value: true}, {Key: "owner_bounds_seeded", Value: true}, {Key: "readers_bound", Value: true}}
		for _, name := range []string{Sequences, Receipts} {
			if e := immutableImportDocument(tx, w.store.db.Collection(name), MembershipSchemaID, marker); e != nil {
				return e
			}
		}
		return nil
	})
	if e != nil {
		return "", errors.Join(ErrCommitUnconfirmed, e)
	}
	return expected, nil
}

// Read-only recovery is allowed under the same storage UUID/RS after process
// restart. It returns status/evidence only, never a new hold or write Permit.
type HistoryReadinessInspection struct {
	PreparationFound, Published          bool
	PreparationSHA256, PublicationSHA256 string
}

func (w *NamespaceImportWriter) InspectHistoryReadiness(ctx context.Context, h namespaceimport.Header) (HistoryReadinessInspection, error) {
	var out HistoryReadinessInspection
	if w == nil || !w.validHeader(h) || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return out, ErrInvalid
	}
	if e := w.checkImportInspectionReady(ctx); e != nil {
		return out, e
	}
	p, found, e := w.readHistoryPreparation(ctx)
	if e != nil {
		return out, e
	}
	out.PreparationFound = found
	if found {
		out.PreparationSHA256 = historyReadinessDigest(p)
	}
	r, ready, e := w.readHistoryReadiness(ctx)
	if e != nil {
		return out, e
	}
	if ready {
		if !found || r.PreparationSHA256 != out.PreparationSHA256 {
			return out, ErrConflict
		}
		if e = w.store.checkMembershipSchema(ctx); e != nil {
			return out, e
		}
		out.Published = true
		out.PublicationSHA256 = historyReadinessDigest(r)
	}
	return out, nil
}

// Exact compiled read surface consumed by BOTH role registrations. A source
// build identity is checked separately; this string is not a claim of testing.
func HistoryReaderContractSHA256() string {
	value := []string{"namespace-history-read-contract-v1", "ReadNamespaceHistoryUpper", "FilterNamespaceHistorySequences", "ReadDirectNamespaceHistoryBounds", "typed-original-principals", "independent-sg-and-n-domains", "earliest-owner-floor", "base-gap-future-and-notice-finalfilter", "positive-slot-source-ACL-after-all-interval-and-notice-unions", "permanent-required-from-across-epochs"}
	return historyReadinessDigest(value)
}

// A useful constant-time comparison for the startup path, without exposing the
// private preparation token in any public receipt/log/error.
func historyReadinessTokenMatches(token, sum string) bool {
	decoded, e := hex.DecodeString(token)
	return e == nil && len(decoded) == 32 && token == hex.EncodeToString(decoded) && hash([]byte(token)) == sum
}

// Canonical containment is checked BEFORE private file creation, including
// aliases to custody. Failure to resolve a parent is rejection, never fallback.
func forbiddenHistoryCustodyPath(path string, directories []string) bool {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(directories) != 2 {
		return true
	}
	parent, e := filepath.EvalSymlinks(filepath.Dir(path))
	if e != nil {
		return true
	}
	canonical := filepath.Join(parent, filepath.Base(path))
	for _, dir := range directories {
		at, e := filepath.EvalSymlinks(dir)
		if e != nil || at != dir {
			return true
		}
		rel, e := filepath.Rel(at, canonical)
		if e != nil || rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
