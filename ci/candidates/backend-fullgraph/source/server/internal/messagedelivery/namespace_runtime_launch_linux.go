//go:build linux

package messagedelivery

import (
	"context"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

// The old reader group still owns the exact two original exec handles used by
// ReadReady and epoch retirement. An extra runtime ticket cannot substitute a
// reader source/bootstrap after the original preparation was committed.
func (p *HistoryReadinessPreparation) ValidateNamespaceRuntimeLaunch(ctx context.Context, b ownedcontrol.NamespaceRuntimeBootstrap) error {
	if p == nil || ctx == nil || p.validate(ctx) != nil || b.ValidateShape() != nil || b.Header != p.writer.header || b.HistoryPreparationID != p.record.ID || b.HistoryPreparationSHA256 != p.sha256 || b.InventorySHA256 != p.record.InventorySHA256 || b.ManifestSHA256 != p.record.ManifestSHA256 {
		return ErrUnprepared
	}
	row, found, e := p.writer.readNamespaceRuntimePreparation(ctx)
	if e != nil {
		return e
	}
	if !found || historyReadinessDigest(row) != b.RuntimePreparationSHA256 || row.ID != b.RuntimePreparationID || row.TokenSHA256 != hash([]byte(b.RuntimeToken)) || row.RequirementsSHA256 != b.RequirementsSHA256 || row.MongoCatalogSHA256 != b.MongoCatalogSHA256 || row.ReleaseKeyID != b.ReleaseKeyID || string(row.ReleasePublicKey) != string(b.ReleasePublicKey) {
		return ErrConflict
	}
	var requirements []ownedcontrol.NamespaceRuntimeRequirement
	if decodeImportJSON(row.RequirementsJSON, &requirements) != nil {
		return ErrConflict
	}
	matches := 0
	for _, required := range requirements {
		a, _ := json.Marshal(required)
		z, _ := json.Marshal(b.Requirement)
		if string(a) == string(z) {
			matches++
		}
	}
	if matches != 1 {
		return ErrConflict
	}
	return nil
}
func (p *NamespaceRuntimePreparation) ValidateNamespaceRuntimeLaunch(ctx context.Context, b ownedcontrol.NamespaceRuntimeBootstrap) error {
	if p == nil || ctx == nil || p.validate(ctx) != nil || b.RuntimePreparationSHA256 != p.sha256 || b.RuntimeToken != p.session.Token {
		return ErrUnprepared
	}
	return p.history.ValidateNamespaceRuntimeLaunch(ctx, b)
}
