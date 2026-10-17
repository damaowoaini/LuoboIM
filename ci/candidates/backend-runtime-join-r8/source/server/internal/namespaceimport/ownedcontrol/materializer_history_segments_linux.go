//go:build linux

package ownedcontrol

import (
	"context"
	"errors"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// This context contains only the immutable target capture inputs. Target
// counts/page root are the OUTPUT of derivation and are intentionally absent;
// producer must never use them as authority or create a circular binding. The
// final verifier receives the full signed inventory before a private page.
func (m *HistoricalMaterializer) originalHistoryCaptureContext(ctx context.Context, h namespaceimport.Header) (namespaceimport.Inventory, error) {
	identity, e := m.captureIdentity(ctx)
	if e != nil || h.Validate() != nil || h.SHA256() != identity.HeaderSHA256 {
		return namespaceimport.Inventory{}, ErrOwnedEvidence
	}
	return namespaceimport.Inventory{HeaderSHA256: h.SHA256(), LeaseID: m.hold.LeaseID(), CaptureID: identity.CaptureID, CapturedAt: identity.CapturedAt, OriginalTrust: "UNTRUSTED", MongoRootSHA256: identity.MongoRootSHA256, RedisRootSHA256: identity.RedisRootSHA256, RawRootSHA256: identity.RawRootSHA256, HeldAdmissionGuardSHA256: m.hold.RecordSHA256()}, nil
}

// StepOriginalHistorySegments consumes ONE original row locator per call. The
// six concrete source schemas contain at most two intervals (base+future for
// prospective) or one other interval. This is a per-row schema budget, never a
// total owner/generation/history cap. Each interval is persisted independently;
// replay dedup is exact segment SHA, never a min/max union over a leave gap.
func (m *HistoricalMaterializer) StepOriginalHistorySegments(ctx context.Context, h namespaceimport.Header, producer namespaceimport.OriginalHistorySegmentProducer, verifier namespaceimport.OriginalHistorySegmentVerifier) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil || m.hold == nil || m.hold.ValidateExclusion() != nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, _, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	if st.Phase != "roots" {
		return st, ErrCaptureProgressPending
	}
	if st.Subcursor == "history-sources:begin" {
		return m.advanceRootCursor(ctx, "", "history-sources:rows", nil)
	}
	if st.Subcursor == "history-sources:rows" {
		rows, next, done, total, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("original-history-sources", m.snapshot.Fingerprint()), st.Cursor, 1)
		if e != nil {
			return st, e
		}
		if len(rows) == 0 {
			if !done {
				return st, ErrOwnedEvidence
			}
			return m.advanceRootCursor(ctx, "", "history-sources:tree", nil)
		}
		if historyPortNil(producer) || historyPortNil(verifier) {
			return st, namespaceimport.ErrUntrusted
		}
		fact, e := m.originalFact(ctx, rows[0])
		if e != nil || fact.Kind != "original-history-segment-source" || fact.SegmentSource == nil {
			return st, ErrOwnedEvidence
		}
		source := fact.SegmentSource
		inventory, e := m.originalHistoryCaptureContext(ctx, h)
		if e != nil {
			return st, e
		}
		if pager, ok := producer.(namespaceimport.OriginalHistorySegmentPager); ok {
			return m.stepOriginalHistoryScopePage(ctx, h, inventory, fact, rows[0], st.Cursor, next, total, pager, verifier)
		}
		intervals, e := producer.DeriveOriginalHistorySegments(ctx, h, inventory, source.Epoch, source.Kind, source.ID, m)
		var pending *namespaceimport.OriginalHistoryProofPending
		if e != nil && !errors.As(e, &pending) {
			return st, e
		}
		changes := []namespaceinventory.DerivedMutation{}
		if pending != nil {
			if !originalScopePendingReason(pending.Reason) || pending.Sequence < 0 || pending.Reason == "original-slot-scope-unavailable" && pending.Sequence == 0 {
				return st, ErrOwnedEvidence
			}
			refs := appendHistoricalRefs(nil, fact.Source)
			for _, ref := range pending.Evidence {
				if _, e = m.snapshot.ReadReference(ctx, m.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position); e != nil {
					return st, e
				}
				refs = appendHistoricalRefs(refs, ref)
			}
			gap := namespaceimport.HistoricalAuthorizationGap{CID: fact.CID, OwnerID: fact.OwnerID, Sequence: pending.Sequence, Reason: pending.Reason, Evidence: refs}
			sum, e := namespaceimport.HistoricalGapSHA256(gap)
			if e != nil {
				return st, e
			}
			change, e := derivedUpdate("history-gaps", fact.CID, sum, namespaceinventory.DerivedEntry{}, gap)
			if e != nil {
				return st, e
			}
			if _, found, e := m.workspace.ReadEntry(ctx, change.SetID, change.Key); e != nil {
				return st, e
			} else if !found {
				changes = append(changes, change)
			}
			// Exact locator plus raw refs are retained for a later separately reviewed
			// supplemental proof import. Current cut/source is never recaptured and
			// no global-CID quarantine or invented empty interval hides the old base.
			disposition := HistoricalProofDisposition{SourceEpoch: source.Epoch, SourceKind: source.Kind, SourceID: source.ID, CID: fact.CID, OwnerID: fact.OwnerID, Sequence: pending.Sequence, Reason: pending.Reason, GapSHA256: sum, Evidence: refs}
			change, e = derivedUpdate("history-proof-dispositions", m.snapshot.Fingerprint(), rows[0].Key, namespaceinventory.DerivedEntry{}, disposition)
			if e != nil {
				return st, e
			}
			changes = append(changes, change)
			intervals = nil
		}
		limit := 1
		if source.Kind == "namespace-owner-prospective-v1" {
			limit = 2
		}
		if len(intervals) > limit {
			return st, ErrOwnedEvidence
		}
		seen := map[string]bool{}
		for _, segment := range intervals {
			sameSource := segment.SourceKind == source.Kind && segment.SourceID == source.ID
			// A prospective row's base uses the SAME canonical actual boundary source
			// as its independent enumeration. The concrete verifier rederives that
			// exact boundary/forbid chain; naked equal CID/owner/range is never dedup.
			canonicalBase := source.Kind == "namespace-owner-prospective-v1" && segment.SourceKind == "namespace-import-boundary-v1"
			if segment.CID != fact.CID || segment.OwnerID != fact.OwnerID || (source.Epoch != "" && segment.SourceEpoch != source.Epoch) || !sameSource && !canonicalBase || segment.SourceEpoch == h.Epoch || segment.SourceKind == "member-notice-entitlement-v1" && segment.Lower != segment.Upper {
				return st, ErrOwnedEvidence
			}
			sum, e := namespaceimport.OwnerHistorySegmentSHA256(segment)
			if e != nil || seen[sum] {
				return st, ErrOwnedEvidence
			}
			seen[sum] = true
			originalIncluded := false
			for _, ref := range segment.Evidence {
				if ref == fact.Source {
					originalIncluded = true
				}
				if _, e = m.snapshot.ReadReference(ctx, m.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position); e != nil {
					return st, e
				}
			}
			if !originalIncluded {
				return st, ErrOwnedEvidence
			}
			if e = verifier.VerifyOriginalHistorySegment(ctx, h, inventory, segment, m); e != nil {
				return st, e
			}
			var old namespaceimport.OwnerHistorySegment
			_, found, e := m.readDerived(ctx, "preserved-history-segments", m.snapshot.Fingerprint(), sum, &old)
			if e != nil {
				return st, e
			}
			if found {
				if historicalJSONSHA(old) != historicalJSONSHA(segment) {
					return st, ErrOwnedEvidence
				}
				continue
			}
			change, e := derivedUpdate("preserved-history-segments", m.snapshot.Fingerprint(), sum, namespaceinventory.DerivedEntry{}, segment)
			if e != nil {
				return st, e
			}
			changes = append(changes, change)
		}
		// Even truly closed-empty source rows have a durable processed identity.
		// Missing chain errors above do NOT become an empty success/checkpoint.
		processed := struct {
			SourceSHA256 string                    `json:"source_sha256"`
			Source       namespaceimport.SourceRef `json:"source"`
			Segments     []string                  `json:"segments"`
			Disposition  string                    `json:"disposition,omitempty"`
		}{rows[0].Key, fact.Source, nil, ""}
		if pending != nil {
			processed.Disposition = pending.Reason
		}
		for _, segment := range intervals {
			sum, _ := namespaceimport.OwnerHistorySegmentSHA256(segment)
			processed.Segments = append(processed.Segments, sum)
		}
		change, e := derivedUpdate("processed-history-sources", m.snapshot.Fingerprint(), rows[0].Key, namespaceinventory.DerivedEntry{}, processed)
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
	if st.Subcursor == "history-sources:tree" {
		_, sources, e := m.workspace.DerivedSetIdentity(ctx, namespaceinventory.DerivedSetID("original-history-sources", m.snapshot.Fingerprint()))
		if e != nil {
			return st, e
		}
		_, processed, e := m.workspace.DerivedSetIdentity(ctx, namespaceinventory.DerivedSetID("processed-history-sources", m.snapshot.Fingerprint()))
		if e != nil || processed != sources {
			return st, ErrOwnedEvidence
		}
		_, complete, e := m.advanceTree(ctx, "preserved-history-segments", m.snapshot.Fingerprint())
		if e != nil {
			return st, e
		}
		if !complete {
			current, _, e := m.workspace.Checkpoint(ctx)
			return current, e
		}
		return m.advanceRootCursor(ctx, "", "global:0", nil)
	}
	return st, ErrCaptureProgressPending
}
