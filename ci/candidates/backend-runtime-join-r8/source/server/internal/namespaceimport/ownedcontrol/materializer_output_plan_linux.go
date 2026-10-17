//go:build linux

package ownedcontrol

import (
	"context"
	"fmt"
	"math"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

type materializedOutputSpec struct {
	Index uint64 `json:"index"`
	Kind  string `json:"kind"`
	CID   string `json:"cid,omitempty"`
	Key   string `json:"key,omitempty"`
}
type materializedNamespaceExport struct {
	CID                  string                        `json:"cid"`
	Origin               *namespaceimport.OriginRecord `json:"origin,omitempty"`
	Quarantine           *namespaceimport.Quarantine   `json:"quarantine,omitempty"`
	Prospective          bool                          `json:"prospective"`
	ExpectedWindows      uint64                        `json:"expected_windows"`
	ExpectedProspectives uint64                        `json:"expected_prospectives"`
}
type materializedOutputPlan struct {
	HeaderSHA256          string                 `json:"header_sha256"`
	SnapshotSHA256        string                 `json:"snapshot_sha256"`
	Stage                 string                 `json:"stage"`
	NamespaceCursor       string                 `json:"namespace_cursor"`
	CID                   string                 `json:"cid,omitempty"`
	Cursor                string                 `json:"cursor,omitempty"`
	DataPages             uint64                 `json:"data_pages"`
	ClosurePages          uint64                 `json:"closure_pages"`
	Counts                namespaceimport.Counts `json:"counts"`
	ProspectiveCount      uint64                 `json:"prospective_count"`
	SegmentCount          uint64                 `json:"segment_count"`
	NamespaceWindows      uint64                 `json:"namespace_windows"`
	NamespaceProspectives uint64                 `json:"namespace_prospectives"`
	PageCount             uint64                 `json:"page_count,omitempty"`
}

func outputPlanKey(n uint64) string { return fmt.Sprintf("%064x", n) }
func addOutputCount(n *uint64, delta uint64) error {
	if *n > ^uint64(0)-delta {
		return ErrOwnedEvidence
	}
	*n += delta
	return nil
}
func closurePageCount(n uint64) uint64 {
	if n == 0 {
		return 1
	}
	return n/64 + boolUint(n%64 != 0)
}
func boolUint(v bool) uint64 {
	if v {
		return 1
	}
	return 0
}
func (m *HistoricalMaterializer) outputPlan(ctx context.Context, h namespaceimport.Header) (materializedOutputPlan, namespaceinventory.DerivedEntry, bool, error) {
	var p materializedOutputPlan
	prior, found, e := m.readDerived(ctx, "output-plan", m.snapshot.Fingerprint(), m.snapshot.Fingerprint(), &p)
	if e != nil {
		return p, prior, found, e
	}
	if found && (p.HeaderSHA256 != h.SHA256() || p.SnapshotSHA256 != m.snapshot.Fingerprint()) {
		return p, prior, false, ErrOwnedEvidence
	}
	return p, prior, found, nil
}
func (m *HistoricalMaterializer) saveOutputPlan(ctx context.Context, h namespaceimport.Header, p materializedOutputPlan, prior namespaceinventory.DerivedEntry, spec *materializedOutputSpec, extra []namespaceinventory.DerivedMutation) (namespaceinventory.DerivedCheckpoint, error) {
	if p.HeaderSHA256 != h.SHA256() || p.SnapshotSHA256 != m.snapshot.Fingerprint() {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	if spec != nil {
		if p.DataPages == ^uint64(0) {
			return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
		}
		spec.Index = p.DataPages
		p.DataPages++
		change, e := derivedUpdate("output-specs", m.snapshot.Fingerprint(), outputPlanKey(spec.Index), namespaceinventory.DerivedEntry{}, *spec)
		if e != nil {
			return namespaceinventory.DerivedCheckpoint{}, e
		}
		extra = append(extra, change)
	}
	change, e := derivedUpdate("output-plan", m.snapshot.Fingerprint(), m.snapshot.Fingerprint(), prior, p)
	if e != nil {
		return namespaceinventory.DerivedCheckpoint{}, e
	}
	extra = append(extra, change)
	return m.advanceRootCursor(ctx, "", "output-plan:"+p.Stage, extra)
}
func (m *HistoricalMaterializer) quarantineOriginalNamespace(ctx context.Context, plan materializerNamespacePlan, reason string) (materializedNamespaceExport, error) {
	facts, _, _, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("namespace-facts", plan.CID), "", 1)
	if e != nil || len(facts) != 1 {
		return materializedNamespaceExport{}, ErrOwnedEvidence
	}
	fact, e := m.originalFact(ctx, facts[0])
	if e != nil || fact.CID != plan.CID {
		return materializedNamespaceExport{}, ErrOwnedEvidence
	}
	// The semantic source validator still rederives the precise failure from the
	// complete sealed namespace set; this one real row is not an absence grant.
	return materializedNamespaceExport{CID: plan.CID, Quarantine: &namespaceimport.Quarantine{CID: plan.CID, Reason: reason, Evidence: []namespaceimport.SourceRef{fact.Source}}}, nil
}
func (m *HistoricalMaterializer) exportNamespacePlan(ctx context.Context, h namespaceimport.Header, p materializerNamespacePlan) (materializedNamespaceExport, error) {
	if p.NamespaceQuarantineReason != "" {
		return m.quarantineOriginalNamespace(ctx, p, p.NamespaceQuarantineReason)
	}
	if p.Origin == nil {
		return m.quarantineOriginalNamespace(ctx, p, "original-origin-not-source-proven")
	}
	i, e := m.originalHistoryCaptureContext(ctx, h)
	if e != nil {
		return materializedNamespaceExport{}, e
	}
	r := namespaceimport.OriginRecord{Origin: *p.Origin, Evidence: append([]namespaceimport.SourceRef(nil), p.OwnershipEvidence...)}
	if p.MongoHead != nil {
		r.MongoHead = *p.MongoHead
	} else {
		r.MongoHead, e = m.LegacyMongoAbsence(ctx, h, i, p.CID)
		if e != nil {
			return m.quarantineOriginalNamespace(ctx, p, "original-mongo-plane-absence-not-proven")
		}
	}
	if p.RedisHead == nil {
		return m.quarantineOriginalNamespace(ctx, p, "original-redis-plane-head-not-proven")
	}
	r.RedisHead = *p.RedisHead
	maximum, e := r.EffectiveSeedMaximum()
	if e != nil || maximum < 0 || maximum >= math.MaxInt64 {
		return materializedNamespaceExport{}, ErrOwnedEvidence
	}
	windows, e := m.rootState(ctx, "effective-boundaries", p.CID)
	if e != nil {
		return materializedNamespaceExport{}, e
	}
	owners, e := m.rootState(ctx, "effective-owners", p.CID)
	if e != nil || windows.Count != owners.Count {
		return materializedNamespaceExport{}, ErrOwnedEvidence
	}
	out := materializedNamespaceExport{CID: p.CID, Origin: &r, ExpectedWindows: windows.Count}
	if r.Origin.Kind == "group" {
		var roster historicalRosterProgress
		_, found, e := m.readDerived(ctx, "roster-progress", m.snapshot.Fingerprint(), sha([]byte(p.CID)), &roster)
		if e != nil {
			return out, e
		}
		if found && roster.Group != nil {
			if e = m.recheckFact(ctx, roster.Group); e != nil {
				return out, e
			}
		}
		if found && roster.Complete && !roster.Conflicting && roster.CID == p.CID && roster.GroupID == r.Origin.GroupID && roster.Group != nil && roster.Group.GroupStatus != nil && *roster.Group.GroupStatus != constant.GroupStatusDismissed && roster.Owners == 1 && roster.Members > 0 {
			if maximum >= math.MaxInt64-1 {
				return out, ErrOwnedEvidence
			}
			tree, e := m.rootState(ctx, "roster-members", p.CID)
			if e != nil || tree.Count != roster.Members {
				return out, ErrOwnedEvidence
			}
			out.Prospective = true
			out.ExpectedProspectives = tree.Count
		}
	}
	// Direct future ownership is created only by the separate actual positive
	// source capability; tuple principals are not turned into a current roster.
	return out, nil
}

// StepOutputPlan creates real per-row output specs and exact count/page totals,
// in bounded persisted calls. Counts are computed from actual immutable sets,
// not operator JSON. They fix the later inventory Binding before coverage and
// prospective bodies/root hashes are generated; no circular root is guessed.
// This stage signs nothing and is NOT the InventorySource Capture result.
func (m *HistoricalMaterializer) StepOutputPlan(ctx context.Context, h namespaceimport.Header) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil || m.hold == nil || m.hold.ValidateExclusion() != nil || h.Validate() != nil || h.SHA256() != m.hold.HeaderSHA256() {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, _, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	if st.Phase != "roots" {
		return st, ErrCaptureProgressPending
	}
	p, prior, found, e := m.outputPlan(ctx, h)
	if e != nil {
		return st, e
	}
	if !found {
		if st.Subcursor != "prepared" {
			return st, ErrCaptureProgressPending
		}
		if _, e = m.rootState(ctx, "preserved-history-segments", m.snapshot.Fingerprint()); e != nil {
			return st, e
		}
		p = materializedOutputPlan{HeaderSHA256: h.SHA256(), SnapshotSHA256: m.snapshot.Fingerprint(), Stage: "namespaces"}
		return m.saveOutputPlan(ctx, h, p, prior, nil, nil)
	}
	if st.Subcursor != "output-plan:"+p.Stage {
		return st, ErrOwnedEvidence
	}
	switch p.Stage {
	case "namespaces":
		rows, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("namespace-plans", m.snapshot.Fingerprint()), p.NamespaceCursor, 1)
		if e != nil {
			return st, e
		}
		if len(rows) == 0 {
			if !done {
				return st, ErrOwnedEvidence
			}
			p.Stage = "segments"
			p.Cursor = ""
			return m.saveOutputPlan(ctx, h, p, prior, nil, nil)
		}
		var raw materializerNamespacePlan
		if namespaceinventory.DecodeStrict(rows[0].Value, &raw) != nil || sha([]byte(raw.CID)) != next {
			return st, ErrOwnedEvidence
		}
		export, e := m.exportNamespacePlan(ctx, h, raw)
		if e != nil {
			return st, e
		}
		change, e := derivedUpdate("namespace-export-plans", m.snapshot.Fingerprint(), next, namespaceinventory.DerivedEntry{}, export)
		if e != nil {
			return st, e
		}
		spec := &materializedOutputSpec{CID: raw.CID, Key: next}
		p.NamespaceCursor = next
		p.CID = raw.CID
		p.Cursor = ""
		p.NamespaceWindows = 0
		p.NamespaceProspectives = 0
		if export.Origin == nil {
			if export.Quarantine == nil {
				return st, ErrOwnedEvidence
			}
			spec.Kind = "quarantine"
			if e = addOutputCount(&p.Counts.Quarantines, 1); e != nil {
				return st, e
			}
		} else {
			spec.Kind = "origin"
			p.Stage = "boundaries"
			if e = addOutputCount(&p.Counts.Origins, 1); e != nil {
				return st, e
			}
			n := closurePageCount(export.ExpectedWindows)
			if n > ^uint64(0)/2 || addOutputCount(&p.ClosurePages, n*2) != nil {
				return st, ErrOwnedEvidence
			}
		}
		return m.saveOutputPlan(ctx, h, p, prior, spec, []namespaceinventory.DerivedMutation{change})
	case "boundaries", "prospective":
		var export materializedNamespaceExport
		_, found, e := m.readDerived(ctx, "namespace-export-plans", m.snapshot.Fingerprint(), sha([]byte(p.CID)), &export)
		if e != nil || !found || export.Origin == nil || export.CID != p.CID {
			return st, ErrOwnedEvidence
		}
		kind := "effective-boundaries"
		if p.Stage == "prospective" {
			if !export.Prospective {
				p.Stage = "namespaces"
				p.Cursor = ""
				p.CID = ""
				return m.saveOutputPlan(ctx, h, p, prior, nil, nil)
			}
			kind = "roster-members"
		}
		rows, next, done, total, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID(kind, p.CID), p.Cursor, 1)
		if e != nil {
			return st, e
		}
		expected := export.ExpectedWindows
		if p.Stage == "prospective" {
			expected = export.ExpectedProspectives
		}
		if total != expected {
			return st, ErrOwnedEvidence
		}
		if len(rows) == 0 {
			if !done {
				return st, ErrOwnedEvidence
			}
			if p.Stage == "boundaries" {
				if p.NamespaceWindows != expected {
					return st, ErrOwnedEvidence
				}
				p.Stage = "prospective"
			} else {
				if p.NamespaceProspectives != expected {
					return st, ErrOwnedEvidence
				}
				p.Stage = "namespaces"
				p.CID = ""
			}
			p.Cursor = ""
			return m.saveOutputPlan(ctx, h, p, prior, nil, nil)
		}
		spec := &materializedOutputSpec{Kind: p.Stage, CID: p.CID, Key: rows[0].Key}
		if p.Stage == "boundaries" {
			var b namespaceimport.EffectiveOwnerBoundary
			if namespaceinventory.DecodeStrict(rows[0].Value, &b) != nil {
				return st, ErrOwnedEvidence
			}
			sum, e := namespaceimport.EffectiveBoundarySHA256(b)
			if e != nil || sum != rows[0].Key || b.Window.CID != p.CID {
				return st, ErrOwnedEvidence
			}
			if addOutputCount(&p.Counts.Windows, 1) != nil || addOutputCount(&p.NamespaceWindows, 1) != nil {
				return st, ErrOwnedEvidence
			}
		} else {
			var fact HistoricalFact
			if namespaceinventory.DecodeStrict(rows[0].Value, &fact) != nil || sha([]byte(fact.OwnerID)) != rows[0].Key || m.recheckFact(ctx, &fact) != nil || fact.Kind != "held-current-member" || fact.GroupID != export.Origin.Origin.GroupID {
				return st, ErrOwnedEvidence
			}
			if addOutputCount(&p.ProspectiveCount, 1) != nil || addOutputCount(&p.NamespaceProspectives, 1) != nil {
				return st, ErrOwnedEvidence
			}
		}
		p.Cursor = next
		return m.saveOutputPlan(ctx, h, p, prior, spec, nil)
	case "segments":
		rows, next, done, total, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("preserved-history-segments", m.snapshot.Fingerprint()), p.Cursor, 1)
		if e != nil {
			return st, e
		}
		if len(rows) == 0 {
			if !done || total != p.SegmentCount {
				return st, ErrOwnedEvidence
			}
			p.Stage = "final-counts"
			p.Cursor = ""
			return m.saveOutputPlan(ctx, h, p, prior, nil, nil)
		}
		var segment namespaceimport.OwnerHistorySegment
		if namespaceinventory.DecodeStrict(rows[0].Value, &segment) != nil {
			return st, ErrOwnedEvidence
		}
		sum, e := namespaceimport.OwnerHistorySegmentSHA256(segment)
		if e != nil || sum != rows[0].Key {
			return st, ErrOwnedEvidence
		}
		if addOutputCount(&p.SegmentCount, 1) != nil {
			return st, ErrOwnedEvidence
		}
		p.Cursor = next
		return m.saveOutputPlan(ctx, h, p, prior, &materializedOutputSpec{Kind: "history-segment", CID: segment.CID, Key: sum}, nil)
	case "final-counts":
		_, actual, e := m.workspace.DerivedSetIdentity(ctx, namespaceinventory.DerivedSetID("output-specs", m.snapshot.Fingerprint()))
		if e != nil || actual != p.DataPages {
			return st, ErrOwnedEvidence
		}
		if e = addOutputCount(&p.ClosurePages, closurePageCount(p.Counts.Origins)); e != nil {
			return st, e
		}
		if p.ProspectiveCount > 0 {
			if e = addOutputCount(&p.ClosurePages, closurePageCount(p.ProspectiveCount)); e != nil {
				return st, e
			}
		}
		if p.SegmentCount > 0 {
			if e = addOutputCount(&p.ClosurePages, closurePageCount(p.SegmentCount)); e != nil {
				return st, e
			}
		}
		if p.DataPages > ^uint64(0)-p.ClosurePages {
			return st, ErrOwnedEvidence
		}
		p.PageCount = p.DataPages + p.ClosurePages
		if p.PageCount == 0 {
			return st, ErrOwnedEvidence
		}
		p.Stage = "complete"
		return m.saveOutputPlan(ctx, h, p, prior, nil, nil)
	case "complete":
		return st, ErrCaptureProgressPending
	}
	return st, ErrOwnedEvidence
}
