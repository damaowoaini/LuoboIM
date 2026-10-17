//go:build linux

package messagedelivery

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func (w *NamespaceImportWriter) readNamespaceRuntimeAdmission(ctx context.Context) (ownedcontrol.NamespaceRuntimeAdmissionProof, bool, error) {
	var row ownedcontrol.NamespaceRuntimeAdmissionProof
	var raw bson.Raw
	e := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": ownedcontrol.NamespaceRuntimeAdmissionID(w.header)}).Decode(&raw)
	if errors.Is(e, mongo.ErrNoDocuments) {
		return row, false, nil
	}
	if e != nil {
		return row, false, e
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "runtime_preparation_sha256", "history_preparation_sha256", "history_readiness_sha256", "inventory_sha256", "manifest_sha256", "requirements_sha256", "graph_json", "graph_sha256", "lease_id", "admission_guard_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &row) != nil || row.ID != ownedcontrol.NamespaceRuntimeAdmissionID(w.header) || row.Kind != "namespace-runtime-admission-v1" || row.HeaderSHA256 != w.header.SHA256() {
		return row, false, ErrConflict
	}
	for _, sum := range []string{row.RuntimePreparationSHA256, row.HistoryPreparationSHA256, row.HistoryReadinessSHA256, row.InventorySHA256, row.ManifestSHA256, row.RequirementsSHA256, row.GraphSHA256, row.LeaseID, row.AdmissionGuardSHA256} {
		if !validSHA(sum) {
			return row, false, ErrConflict
		}
	}
	if hash([]byte(row.GraphJSON)) != row.GraphSHA256 {
		return row, false, ErrConflict
	}
	preparation, found, e := w.readNamespaceRuntimePreparation(ctx)
	if e != nil {
		return row, false, e
	}
	if !found || historyReadinessDigest(preparation) != row.RuntimePreparationSHA256 || preparation.HistoryPreparationSHA256 != row.HistoryPreparationSHA256 || preparation.InventorySHA256 != row.InventorySHA256 || preparation.ManifestSHA256 != row.ManifestSHA256 || preparation.RequirementsSHA256 != row.RequirementsSHA256 || preparation.LeaseID != row.LeaseID || preparation.AdmissionGuardSHA256 != row.AdmissionGuardSHA256 {
		return row, false, ErrConflict
	}
	readiness, found, e := w.readHistoryReadiness(ctx)
	if e != nil {
		return row, false, e
	}
	if !found || historyReadinessDigest(readiness) != row.HistoryReadinessSHA256 || readiness.PreparationSHA256 != row.HistoryPreparationSHA256 {
		return row, false, ErrConflict
	}
	if e = validateNamespaceRuntimeObservedRows(preparation, readiness, row.GraphJSON); e != nil {
		return row, false, e
	}
	return row, true, nil
}

// Complete fixed role set plus unchanged original preparing-reader identities.
// The caller's private VerifiedGraph additionally supplies real process liveness;
// this decoder is not an alternative constructor for such a capability.
func validateNamespaceRuntimeObservedRows(preparation namespaceRuntimePreparationRecord, history historyReadinessPublication, body string) error {
	var rows []ownedcontrol.ObservedNamespaceRuntime
	var requirements []ownedcontrol.NamespaceRuntimeRequirement
	var original []ownedcontrol.ObservedHistoryReader
	if decodeImportJSON(preparation.RequirementsJSON, &requirements) != nil || decodeImportJSON(history.GraphJSON, &original) != nil || decodeImportJSON(body, &rows) != nil || len(rows) != len(ownedcontrol.NamespaceRuntimeRoles()) || len(requirements) != len(rows) || len(original) != 2 {
		return ErrConflict
	}
	for at, row := range rows {
		r, expected := row.Registration, requirements[at]
		if row.Role != ownedcontrol.NamespaceRuntimeRoles()[at] || expected.Role != row.Role || row.PID <= 1 || row.StartToken == "" || row.BootID == "" || !validSHA(row.OwnerNonceSHA256) || !validSHA(row.RegistrationSHA256) || row.BinarySHA256 != expected.BinarySHA256 || row.SourceSHA256 != expected.SourceSHA256 || row.BuildReceiptSHA256 != expected.BuildReceiptSHA256 || r.ValidateShape() != nil || r.Role != expected.Role || r.HeaderSHA256 != preparation.HeaderSHA256 || r.RuntimePreparationSHA256 != historyReadinessDigest(preparation) || r.RuntimeTokenSHA256 != preparation.TokenSHA256 || r.InventorySHA256 != preparation.InventorySHA256 || r.ManifestSHA256 != preparation.ManifestSHA256 || r.RequirementsSHA256 != preparation.RequirementsSHA256 || r.BootstrapSHA256 != expected.BootstrapSHA256 || r.ConfigurationSHA256 != expected.ConfigurationSHA256 || r.EnvironmentSHA256 != expected.EnvironmentSHA256 || r.ContractSHA256 != expected.ContractSHA256 {
			return ErrConflict
		}
		a, _ := json.Marshal(r.Services)
		b, _ := json.Marshal(expected.Services)
		if string(a) != string(b) {
			return ErrConflict
		}
		a, _ = json.Marshal(r.Components)
		b, _ = json.Marshal(expected.Components)
		if string(a) != string(b) {
			return ErrConflict
		}
		if row.Role == "rpc-msg" || row.Role == "rpc-conversation" {
			matched := false
			for _, old := range original {
				if old.Role == row.Role {
					matched = row.PID == old.PID && row.UID == old.UID && row.StartToken == old.StartToken && row.BootID == old.BootID && row.BinarySHA256 == old.BinarySHA256 && row.SourceSHA256 == old.SourceSHA256 && row.BuildReceiptSHA256 == old.BuildReceiptSHA256 && row.OwnerNonceSHA256 == old.OwnerNonceSHA256 && r.InstanceID == old.Registration.InstanceID
				}
			}
			if !matched {
				return ErrConflict
			}
		}
	}
	return nil
}

// Admission is published only after ALL actual replacement resources have
// been constructed/sealed, while every Serve/intake/deferred source loop stays
// closed. This does not remove legacy exclusion, invent provider ACKs or reset
// history; the actual original start and canonical ready/drain phases follow.
func (p *NamespaceRuntimePreparation) PublishRuntimeAdmission(ctx context.Context, graph *ownedcontrol.VerifiedNamespaceRuntimeGraph) (string, error) {
	if p == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || p.validate(ctx) != nil || graph == nil {
		return "", ErrUnprepared
	}
	h := p.history
	w := h.writer
	if _, e := h.source.ReadinessSourceCustody(ctx, h.hold, w.header, h.selection.Inventory, h.selection.ImportConfig); e != nil {
		return "", e
	}
	if e := graph.Refresh(ctx); e != nil {
		return "", e
	}
	check := func() error {
		return graph.ValidateLocal(h.hold, w.header.SHA256(), p.sha256, p.record.InventorySHA256, p.record.ManifestSHA256, p.record.RequirementsSHA256)
	}
	if e := check(); e != nil {
		return "", e
	}
	readiness, found, e := w.readHistoryReadiness(ctx)
	if e != nil {
		return "", e
	}
	if !found || readiness.PreparationSHA256 != h.sha256 {
		return "", ErrUnprepared
	}
	rows, e := json.Marshal(graph.AuditRows())
	if e != nil {
		return "", e
	}
	if e = validateNamespaceRuntimeObservedRows(p.record, readiness, string(rows)); e != nil {
		return "", e
	}
	row := ownedcontrol.NamespaceRuntimeAdmissionProof{ID: ownedcontrol.NamespaceRuntimeAdmissionID(w.header), Kind: "namespace-runtime-admission-v1", HeaderSHA256: w.header.SHA256(), RuntimePreparationSHA256: p.sha256, HistoryPreparationSHA256: h.sha256, HistoryReadinessSHA256: historyReadinessDigest(readiness), InventorySHA256: p.record.InventorySHA256, ManifestSHA256: p.record.ManifestSHA256, RequirementsSHA256: p.record.RequirementsSHA256, GraphJSON: string(rows), GraphSHA256: hash(rows), LeaseID: h.hold.LeaseID(), AdmissionGuardSHA256: h.hold.RecordSHA256()}
	expected := historyReadinessDigest(row)
	known, found, e := w.readNamespaceRuntimeAdmission(ctx)
	if e != nil {
		return "", e
	}
	if found {
		if historyReadinessDigest(known) != expected {
			return "", ErrConflict
		}
		return expected, nil
	}
	dispatch, e := ownedcontrol.BeginHistoryReadinessIntent(p.sessionPath+".publish-intent", p.record.SessionSHA256, "publish", expected)
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
		if e := w.store.checkMembershipSchema(tx); e != nil {
			return e
		}
		if e := w.checkStorageIdentity(tx, true); e != nil {
			return e
		}
		if _, e := w.checkPublishedReadinessData(tx, h.selection.Inventory, h.record.ManifestSHA256); e != nil {
			return e
		}
		current, ready, e := w.readHistoryReadiness(tx)
		if e != nil {
			return e
		}
		if !ready || historyReadinessDigest(current) != row.HistoryReadinessSHA256 {
			return ErrConflict
		}
		return immutableImportDocument(tx, w.store.db.Collection(Receipts), row.ID, row)
	})
	if e != nil {
		return "", errors.Join(ErrCommitUnconfirmed, e)
	}
	return expected, nil
}

// Releases are transport for the exact KNOWN actual Mongo publication. The
// signing key alone has no method accepting an arbitrary publication/body.
// An absent/unknown DB result or changed actual graph returns no release.
func (p *NamespaceRuntimePreparation) WriteRuntimeReleases(ctx context.Context, graph *ownedcontrol.VerifiedNamespaceRuntimeGraph, processes []ownedcontrol.NamespaceRuntimeProcess) ([]string, error) {
	if p == nil || ctx == nil || graph == nil || p.validate(ctx) != nil {
		return nil, ErrUnprepared
	}
	if e := graph.Refresh(ctx); e != nil {
		return nil, e
	}
	h := p.history
	if e := graph.ValidateLocal(h.hold, h.writer.header.SHA256(), p.sha256, p.record.InventorySHA256, p.record.ManifestSHA256, p.record.RequirementsSHA256); e != nil {
		return nil, e
	}
	publication, found, e := h.writer.readNamespaceRuntimeAdmission(ctx)
	if e != nil {
		return nil, e
	}
	if !found {
		return nil, ErrCommitUnconfirmed
	}
	rows := graph.AuditRows()
	body, e := json.Marshal(rows)
	if e != nil || string(body) != publication.GraphJSON || len(processes) != len(rows) {
		return nil, ErrConflict
	}
	raw, e := json.Marshal(publication)
	if e != nil {
		return nil, e
	}
	var written []string
	for at, process := range processes {
		b, row := process.Bootstrap, rows[at]
		if b.ValidateShape() != nil || b.Header != h.writer.header || b.RuntimePreparationSHA256 != p.sha256 || b.Requirement.Role != row.Role || process.Service == nil || forbiddenHistoryCustodyPath(b.ReleasePath, h.custodyDirectories) {
			return written, ErrConflict
		}
		release := ownedcontrol.NamespaceRuntimeRelease{Version: "namespace-runtime-release-v1", KeyID: p.record.ReleaseKeyID, Role: row.Role, InstanceID: row.Registration.InstanceID, OwnerNonceSHA256: row.OwnerNonceSHA256, RegistrationSHA256: row.RegistrationSHA256, PublicationJSON: string(raw), PublicationSHA256: hash(raw)}
		signing, e := release.SigningBytes()
		if e != nil {
			return written, e
		}
		release.Signature = ed25519.Sign(p.releaseKey, signing)
		if _, e = ownedcontrol.WriteOrInspectNamespaceRuntimeRelease(b.ReleasePath, release); e != nil {
			return written, e
		}
		written = append(written, row.Role)
	}
	return written, nil
}

func (p *NamespaceRuntimePreparation) ValidateNamespaceRuntimeStarted(ctx context.Context, expected string, rows []ownedcontrol.ObservedNamespaceRuntime) error {
	if p == nil || ctx == nil || p.validate(ctx) != nil || !validSHA(expected) {
		return ErrUnprepared
	}
	row, found, e := p.history.writer.readNamespaceRuntimeAdmission(ctx)
	if e != nil {
		return e
	}
	if !found || historyReadinessDigest(row) != expected {
		return ErrCommitUnconfirmed
	}
	raw, e := json.Marshal(rows)
	if e != nil || string(raw) != row.GraphJSON {
		return ErrConflict
	}
	return nil
}
