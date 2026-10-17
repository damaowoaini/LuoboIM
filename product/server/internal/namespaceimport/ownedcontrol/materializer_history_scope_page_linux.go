//go:build linux

package ownedcontrol

import (
	"context"
	"errors"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

type historicalScopeProgress struct {
	SourceSHA256    string `json:"source_sha256"`
	Next            string `json:"next"`
	PageChainSHA256 string `json:"page_chain_sha256"`
	Pages           uint64 `json:"pages"`
	Segments        uint64 `json:"segments"`
	Dispositions    uint64 `json:"dispositions"`
	Complete        bool   `json:"complete"`
}

func originalScopePendingReason(s string) bool {
	return s == "source-trust-unavailable" || s == "original-chain-unavailable" || s == "boundary-witness-unavailable" || s == "original-slot-scope-unavailable"
}

// ONE source sequence/legacy witness per journal commit. The source locator
// remains current until all its pages finish. Segment leaves and unresolved
// raw references are committed with the SAME page cursor, so unknown commit
// cannot skip an unresolved notice, replay a permission or restart at seq1.
func (m *HistoricalMaterializer) stepOriginalHistoryScopePage(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, fact HistoricalFact, entry namespaceinventory.DerivedEntry, previous, next string, total uint64, producer namespaceimport.OriginalHistorySegmentPager, verifier namespaceimport.OriginalHistorySegmentVerifier) (namespaceinventory.DerivedCheckpoint, error) {
	st, _, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	source := fact.SegmentSource
	if source == nil {
		return st, ErrOwnedEvidence
	}
	progress := historicalScopeProgress{SourceSHA256: entry.Key, PageChainSHA256: historicalJSONSHA([]string{h.SHA256(), m.snapshot.Fingerprint(), entry.Key})}
	prior, found, e := m.readDerived(ctx, "history-source-scope-progress", m.snapshot.Fingerprint(), entry.Key, &progress)
	if e != nil {
		return st, e
	}
	if found && (progress.SourceSHA256 != entry.Key || progress.Complete || progress.Next == "" || progress.Pages == 0) {
		return st, ErrOwnedEvidence
	}
	after := progress.Next
	page, e := producer.DeriveOriginalHistorySegmentPage(ctx, h, i, source.Epoch, source.Kind, source.ID, m, after)
	var pending *namespaceimport.OriginalHistoryProofPending
	if e != nil {
		if !errors.As(e, &pending) || !originalScopePendingReason(pending.Reason) {
			return st, e
		}
		page = namespaceimport.OriginalHistorySegmentPage{Complete: true, Pending: []namespaceimport.OriginalHistorySequencePending{{Sequence: pending.Sequence, Reason: pending.Reason, Evidence: pending.Evidence}}}
	}
	if len(page.Segments) > 1 || len(page.Pending) > 1 || len(page.Segments)+len(page.Pending) > 1 || !page.Complete && (page.Next == "" || page.Next == after) || page.Complete && page.Next != "" || progress.Pages == ^uint64(0) {
		return st, ErrOwnedEvidence
	}
	changes := []namespaceinventory.DerivedMutation{}
	for _, segment := range page.Segments {
		same := segment.SourceKind == source.Kind && segment.SourceID == source.ID
		canonical := source.Kind == "namespace-owner-prospective-v1" && segment.SourceKind == "namespace-import-boundary-v1"
		if segment.CID != fact.CID || segment.OwnerID != fact.OwnerID || (source.Epoch != "" && segment.SourceEpoch != source.Epoch) || !same && !canonical || segment.SourceEpoch == h.Epoch {
			return st, ErrOwnedEvidence
		}
		included := false
		for _, ref := range segment.Evidence {
			included = included || ref == fact.Source
			if _, e = m.snapshot.ReadReference(ctx, m.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position); e != nil {
				return st, e
			}
		}
		if !included {
			return st, ErrOwnedEvidence
		}
		if e = verifier.VerifyOriginalHistorySegment(ctx, h, i, segment, m); e != nil {
			return st, e
		}
		sum, e := namespaceimport.OwnerHistorySegmentSHA256(segment)
		if e != nil {
			return st, e
		}
		var old namespaceimport.OwnerHistorySegment
		_, exists, e := m.readDerived(ctx, "preserved-history-segments", m.snapshot.Fingerprint(), sum, &old)
		if e != nil {
			return st, e
		}
		if exists {
			if historicalJSONSHA(old) != historicalJSONSHA(segment) {
				return st, ErrOwnedEvidence
			}
		} else {
			change, e := derivedUpdate("preserved-history-segments", m.snapshot.Fingerprint(), sum, namespaceinventory.DerivedEntry{}, segment)
			if e != nil {
				return st, e
			}
			changes = append(changes, change)
		}
		if progress.Segments == ^uint64(0) {
			return st, ErrOwnedEvidence
		}
		progress.Segments++
	}
	for _, unresolved := range page.Pending {
		if !originalScopePendingReason(unresolved.Reason) || unresolved.Sequence < 0 || unresolved.Reason == "original-slot-scope-unavailable" && unresolved.Sequence == 0 {
			return st, ErrOwnedEvidence
		}
		refs := appendHistoricalRefs(nil, fact.Source)
		for _, ref := range unresolved.Evidence {
			if _, e = m.snapshot.ReadReference(ctx, m.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position); e != nil {
				return st, e
			}
			refs = appendHistoricalRefs(refs, ref)
		}
		gap := namespaceimport.HistoricalAuthorizationGap{CID: fact.CID, OwnerID: fact.OwnerID, Sequence: unresolved.Sequence, Reason: unresolved.Reason, Evidence: refs}
		sum, e := namespaceimport.HistoricalGapSHA256(gap)
		if e != nil {
			return st, e
		}
		change, e := derivedUpdate("history-gaps", fact.CID, sum, namespaceinventory.DerivedEntry{}, gap)
		if e != nil {
			return st, e
		}
		if _, exists, e := m.workspace.ReadEntry(ctx, change.SetID, change.Key); e != nil {
			return st, e
		} else if !exists {
			changes = append(changes, change)
		}
		disposition := HistoricalProofDisposition{SourceEpoch: source.Epoch, SourceKind: source.Kind, SourceID: source.ID, CID: fact.CID, OwnerID: fact.OwnerID, Sequence: unresolved.Sequence, Reason: unresolved.Reason, GapSHA256: sum, Evidence: refs}
		key := historicalJSONSHA([]any{entry.Key, unresolved.Sequence})
		change, e = derivedUpdate("history-proof-dispositions", m.snapshot.Fingerprint(), key, namespaceinventory.DerivedEntry{}, disposition)
		if e != nil {
			return st, e
		}
		changes = append(changes, change)
		if progress.Dispositions == ^uint64(0) {
			return st, ErrOwnedEvidence
		}
		progress.Dispositions++
	}
	// The exact page metadata is bounded by the existing64KiB private-document
	// guard. This is not a new signed grant: private VerifiedPage still invokes
	// the concrete verifier and the final Inventory closes all actual leaf roots.
	pageKey := historicalJSONSHA([]string{entry.Key, after})
	change, e := derivedUpdate("history-source-scope-pages", m.snapshot.Fingerprint(), pageKey, namespaceinventory.DerivedEntry{}, page)
	if e != nil {
		return st, e
	}
	changes = append(changes, change)
	progress.PageChainSHA256 = historicalJSONSHA([]any{progress.PageChainSHA256, pageKey, page})
	progress.Pages++
	progress.Next = page.Next
	progress.Complete = page.Complete
	change, e = derivedUpdate("history-source-scope-progress", m.snapshot.Fingerprint(), entry.Key, prior, progress)
	if e != nil {
		return st, e
	}
	changes = append(changes, change)
	if !page.Complete {
		return m.advanceRootCursor(ctx, previous, "history-sources:rows", changes)
	}
	processed := struct {
		SourceSHA256 string                    `json:"source_sha256"`
		Source       namespaceimport.SourceRef `json:"source"`
		Progress     historicalScopeProgress   `json:"scope_progress"`
	}{entry.Key, fact.Source, progress}
	change, e = derivedUpdate("processed-history-sources", m.snapshot.Fingerprint(), entry.Key, namespaceinventory.DerivedEntry{}, processed)
	if e != nil {
		return st, e
	}
	changes = append(changes, change)
	_, count, e := m.workspace.DerivedSetIdentity(ctx, namespaceinventory.DerivedSetID("processed-history-sources", m.snapshot.Fingerprint()))
	if e != nil || count >= total {
		return st, ErrOwnedEvidence
	}
	return m.advanceRootCursor(ctx, next, "history-sources:rows", changes)
}
