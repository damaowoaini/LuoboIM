//go:build linux

package ownedcontrol

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// Private operator metadata only: no message content, read permission, Ready,
// tombstone or permanent abandonment. A later reviewed witness requires a new
// sibling derived workspace with the SAME original sealed raw/cut; the old
// selected trust/document/page history is never rewritten in place.
type HistoricalProofDisposition struct {
	Sequence    int64                       `json:"sequence,omitempty"`
	SourceEpoch string                      `json:"source_epoch,omitempty"`
	SourceKind  string                      `json:"source_kind"`
	SourceID    string                      `json:"source_id"`
	CID         string                      `json:"cid"`
	OwnerID     string                      `json:"owner_id"`
	Reason      string                      `json:"reason"`
	GapSHA256   string                      `json:"gap_sha256"`
	Evidence    []namespaceimport.SourceRef `json:"evidence"`
}
type HistoricalProofDispositionPage struct {
	Rows       []HistoricalProofDisposition `json:"rows"`
	NextCursor string                       `json:"next_cursor"`
	Complete   bool                         `json:"complete"`
	Total      uint64                       `json:"total"`
}

func (s *HistoricalCaptureSource) ReadHistoryProofDispositions(ctx context.Context, i namespaceimport.Inventory, after string, limit int) (HistoricalProofDispositionPage, error) {
	out := HistoricalProofDispositionPage{}
	if s == nil || limit < 1 || limit > 16 {
		return out, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, s.header, i); e != nil {
		return out, e
	}
	m := s.materializer
	rows, next, done, total, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("history-proof-dispositions", m.snapshot.Fingerprint()), after, limit)
	if e != nil {
		return out, e
	}
	for _, row := range rows {
		var d HistoricalProofDisposition
		if namespaceinventory.DecodeStrict(row.Value, &d) != nil || d.Reason != "source-trust-unavailable" && d.Reason != "original-chain-unavailable" && d.Reason != "boundary-witness-unavailable" && d.Reason != "original-slot-scope-unavailable" {
			return HistoricalProofDispositionPage{}, ErrOwnedEvidence
		}
		var gap namespaceimport.HistoricalAuthorizationGap
		_, found, e := m.readDerived(ctx, "history-gaps", d.CID, d.GapSHA256, &gap)
		if e != nil || !found || gap.Sequence != d.Sequence || d.Sequence < 0 || gap.CID != d.CID || gap.OwnerID != d.OwnerID || gap.Reason != d.Reason || historicalJSONSHA(gap.Evidence) != historicalJSONSHA(d.Evidence) {
			return HistoricalProofDispositionPage{}, ErrOwnedEvidence
		}
		sum, e := namespaceimport.HistoricalGapSHA256(gap)
		if e != nil || sum != d.GapSHA256 {
			return HistoricalProofDispositionPage{}, ErrOwnedEvidence
		}
		if e = s.sourceReferences(ctx, d.Evidence); e != nil {
			return HistoricalProofDispositionPage{}, e
		}
		out.Rows = append(out.Rows, d)
	}
	out.NextCursor = next
	out.Complete = done
	out.Total = total
	return out, nil
}

// This inspect path is available before signing as well. In particular an
// unattributable original row cannot disappear behind ErrUntrusted: its count
// and exact bounded raw-ref metadata stay available for explicit source review.
type HistoricalCaptureProgress struct {
	HeaderSHA256     string                               `json:"header_sha256"`
	SnapshotSHA256   string                               `json:"snapshot_sha256"`
	OriginalTrust    string                               `json:"original_trust"`
	Checkpoint       namespaceinventory.DerivedCheckpoint `json:"checkpoint"`
	MissingProofs    uint64                               `json:"missing_proofs"`
	UnattributedRows uint64                               `json:"unattributed_rows"`
}

func (s *HistoricalCaptureSource) InspectProgress(ctx context.Context) (HistoricalCaptureProgress, error) {
	out := HistoricalCaptureProgress{}
	if s == nil {
		return out, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkReviewedSource() != nil || s.held(ctx) != nil {
		return out, ErrOwnedEvidence
	}
	if e := s.openDerived(ctx, false); e != nil {
		return out, e
	}
	m := s.materializer
	checkpoint, _, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return out, e
	}
	_, missing, e := m.workspace.DerivedSetIdentity(ctx, namespaceinventory.DerivedSetID("history-proof-dispositions", m.snapshot.Fingerprint()))
	if e != nil {
		return out, e
	}
	_, unknown, e := m.workspace.DerivedSetIdentity(ctx, namespaceinventory.DerivedSetID("unattributed-gaps", m.snapshot.Fingerprint()))
	if e != nil {
		return out, e
	}
	return HistoricalCaptureProgress{HeaderSHA256: s.header.SHA256(), SnapshotSHA256: m.snapshot.Fingerprint(), OriginalTrust: "UNTRUSTED", Checkpoint: checkpoint, MissingProofs: missing, UnattributedRows: unknown}, nil
}
func (s *HistoricalCaptureSource) ReadUnattributedEvidence(ctx context.Context, after string, limit int) ([]HistoricalFact, string, bool, error) {
	if s == nil || limit < 1 || limit > 16 {
		return nil, "", false, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.checkReviewedSource() != nil || s.held(ctx) != nil {
		return nil, "", false, ErrOwnedEvidence
	}
	if e := s.openDerived(ctx, false); e != nil {
		return nil, "", false, e
	}
	m := s.materializer
	rows, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("unattributed-gaps", m.snapshot.Fingerprint()), after, limit)
	if e != nil {
		return nil, "", false, e
	}
	out := make([]HistoricalFact, 0, len(rows))
	for _, row := range rows {
		fact, e := m.originalFact(ctx, row)
		if e != nil || fact.CID != "" || fact.Kind != "unresolved-source-row" {
			return nil, "", false, ErrOwnedEvidence
		}
		out = append(out, fact)
	}
	return out, next, done, nil
}
