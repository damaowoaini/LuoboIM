//go:build linux

package ownedcontrol

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

var _ namespaceimport.OriginalHistoryPreparedReader = (*HistoricalMaterializer)(nil)

// This is the same source/materializer/snapshot as the original pager and
// final Controller. It neither recaptures nor renews the historical cut.
func (m *HistoricalMaterializer) OriginalHistoryPreparationBinding(ctx context.Context) (namespaceimport.OriginalHistoryPreparationBinding, error) {
	var out namespaceimport.OriginalHistoryPreparationBinding
	if m == nil || ctx == nil || m.hold == nil || m.snapshot == nil || m.workspace == nil || m.hold.ValidateExclusion() != nil {
		return out, ErrOwnedEvidence
	}
	seal, e := m.workspace.OriginalHistoryIndexBinding(ctx)
	if e != nil {
		return out, e
	}
	stamp, e := m.snapshot.CaptureStamp(ctx, m.live)
	if e != nil {
		return out, e
	}
	info := m.snapshot.Identity()
	out = namespaceimport.OriginalHistoryPreparationBinding{Capture: m, Snapshot: m.snapshot, SnapshotSHA256: m.snapshot.Fingerprint(), RawRootSHA256: info.RawRootSHA256, HeaderSHA256: m.hold.HeaderSHA256(), LeaseID: m.hold.LeaseID(), HeldGuardSHA256: m.hold.RecordSHA256(), MetadataSHA256: historicalJSONSHA(struct {
		Snapshot                          any
		Stamp                             any
		Seal, Header, Policy, Lease, Hold string
	}{info, stamp, seal, m.hold.HeaderSHA256(), m.policySHA, m.hold.LeaseID(), m.hold.RecordSHA256()})}
	if m.hold.ValidateExclusion() != nil {
		return namespaceimport.OriginalHistoryPreparationBinding{}, ErrOwnedEvidence
	}
	return out, nil
}
func (m *HistoricalMaterializer) RecheckOriginalHistoryPreparedRefs(ctx context.Context, want namespaceimport.OriginalHistoryPreparationBinding, refs []namespaceimport.SourceRef) error {
	if m == nil || want.Capture != m || want.Snapshot != m.snapshot {
		return ErrOwnedEvidence
	}
	current, e := m.OriginalHistoryPreparationBinding(ctx)
	if e != nil {
		return e
	}
	if current != want {
		return ErrOwnedEvidence
	}
	for _, ref := range refs {
		row, e := m.snapshot.ReadReference(ctx, m.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position)
		if e != nil {
			return e
		}
		if rawFactRef(row) != ref {
			return ErrOwnedEvidence
		}
	}
	current, e = m.OriginalHistoryPreparationBinding(ctx)
	if e != nil {
		return e
	}
	if current != want {
		return ErrOwnedEvidence
	}
	return nil
}
