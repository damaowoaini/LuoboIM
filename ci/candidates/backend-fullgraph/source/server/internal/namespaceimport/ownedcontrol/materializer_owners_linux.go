//go:build linux

package ownedcontrol

import (
	"context"
	"math"
	"strings"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

type historicalRosterProgress struct {
	CID         string          `json:"cid"`
	GroupID     string          `json:"group_id"`
	Group       *HistoricalFact `json:"group,omitempty"`
	Members     uint64          `json:"members"`
	Owners      uint64          `json:"owners"`
	Conflicting bool            `json:"conflicting"`
	Complete    bool            `json:"complete"`
}
type materializerOwnerPlan struct {
	CID                    string                                      `json:"cid"`
	OwnerID                string                                      `json:"owner_id"`
	Window                 *namespaceimport.OwnerWindow                `json:"window,omitempty"`
	HistoricalGap          *namespaceimport.HistoricalAuthorizationGap `json:"historical_gap,omitempty"`
	OriginalLineagePending bool                                        `json:"original_lineage_pending,omitempty"`
	OriginalRecordSetID    string                                      `json:"original_record_set_id"`
	OriginalRecords        uint64                                      `json:"original_records"`
	HistoricalPolicySHA256 string                                      `json:"historical_policy_sha256"`
	CurrentMembership      *HistoricalFact                             `json:"current_membership,omitempty"`
}

// Group/current roster facts are a separate pass, independent of the number of
// original messages. A last-left date is retained as missing domain-specific
// history evidence. It never creates a sequence window or a historical union.
func (m *HistoricalMaterializer) StepGroups(ctx context.Context) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil || m.hold.ValidateExclusion() != nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, expected, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	if st.Phase != "reduce" || !strings.HasPrefix(st.Subcursor, "groups:") {
		return st, ErrCaptureProgressPending
	}
	ns, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("namespace-plans", m.snapshot.Fingerprint()), st.Cursor, 1)
	if e != nil {
		return st, e
	}
	if len(ns) == 0 {
		if !done {
			return st, ErrOwnedEvidence
		}
		updated, _, e := m.workspace.Commit(ctx, expected, "reduce", "", "owners:", nil)
		return updated, e
	}
	var plan materializerNamespacePlan
	if namespaceinventory.DecodeStrict(ns[0].Value, &plan) != nil || sha([]byte(plan.CID)) != next {
		return st, ErrOwnedEvidence
	}
	if plan.Origin == nil || plan.Origin.Kind != "group" {
		updated, _, e := m.workspace.Commit(ctx, expected, "reduce", next, "groups:", nil)
		return updated, e
	}
	pieces := strings.Split(st.Subcursor, ":")
	if len(pieces) != 2 && len(pieces) != 3 {
		return st, ErrOwnedEvidence
	}
	after := ""
	if len(pieces) == 3 {
		if pieces[1] != next {
			return st, ErrOwnedEvidence
		}
		after = pieces[2]
	} else if pieces[1] != "" {
		return st, ErrOwnedEvidence
	}
	var roster historicalRosterProgress
	prior, exists, e := m.readDerived(ctx, "roster-progress", m.snapshot.Fingerprint(), next, &roster)
	if e != nil {
		return st, e
	}
	if !exists {
		roster.CID = plan.CID
		roster.GroupID = plan.Origin.GroupID
	} else if roster.Complete || roster.CID != plan.CID || roster.GroupID != plan.Origin.GroupID {
		return st, ErrOwnedEvidence
	}
	items, fnext, fdone, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("group-metadata", roster.GroupID), after, 1)
	if e != nil {
		return st, e
	}
	changes := []namespaceinventory.DerivedMutation{}
	cursor, sub := st.Cursor, "groups:"+next+":"+fnext
	if len(items) == 0 {
		if !fdone {
			return st, ErrOwnedEvidence
		}
		roster.Complete = true
		cursor = next
		sub = "groups:"
	} else {
		fact, e := m.originalFact(ctx, items[0])
		if e != nil || fact.GroupID != roster.GroupID {
			return st, ErrOwnedEvidence
		}
		if fact.Kind == "held-current-group" {
			if roster.Group != nil {
				roster.Conflicting = true
			} else {
				copy := fact
				roster.Group = &copy
			}
		}
		if fact.Kind == "held-current-member" || fact.Kind == "original-membership-calendar-only" {
			key := namespaceimport.EffectiveOwnerKey(plan.CID, fact.OwnerID)
			var owner historicalOwnerReduction
			old, ok, e := m.readDerived(ctx, "owner-reductions", m.snapshot.Fingerprint(), key, &owner)
			if e != nil {
				return st, e
			}
			if !ok {
				owner.CID = plan.CID
				owner.OwnerID = fact.OwnerID
			} else if owner.CID != plan.CID || owner.OwnerID != fact.OwnerID {
				return st, ErrOwnedEvidence
			}
			if fact.Kind == "held-current-member" {
				if owner.CurrentMembership != nil {
					roster.Conflicting = true
				} else {
					copy := fact
					owner.CurrentMembership = &copy
					if e = incrementHistorical(&roster.Members); e != nil {
						return st, e
					}
					if fact.Role != nil && *fact.Role == constant.GroupOwner {
						if e = incrementHistorical(&roster.Owners); e != nil {
							return st, e
						}
					}
				}
				// A duplicate original member identity is retained in raw/group audit
				// and disables prospective roster admission. Do not overwrite the first
				// immutable per-owner entry or turn one malformed group into a global
				// capture CAS failure; current roster never grants past history here.
				_, memberExists, e := m.workspace.ReadEntry(ctx, namespaceinventory.DerivedSetID("roster-members", plan.CID), sha([]byte(fact.OwnerID)))
				if e != nil {
					return st, e
				}
				if !memberExists {
					change, e := derivedUpdate("roster-members", plan.CID, sha([]byte(fact.OwnerID)), namespaceinventory.DerivedEntry{}, fact)
					if e != nil {
						return st, e
					}
					changes = append(changes, change)
				}
			} else if owner.Calendar == nil {
				copy := fact
				owner.Calendar = &copy
			}
			change, e := derivedUpdate("owner-reductions", m.snapshot.Fingerprint(), key, old, owner)
			if e != nil {
				return st, e
			}
			changes = append(changes, change)
		}
	}
	change, e := derivedUpdate("roster-progress", m.snapshot.Fingerprint(), next, prior, roster)
	if e != nil {
		return st, e
	}
	changes = append(changes, change)
	updated, _, e := m.workspace.Commit(ctx, expected, "reduce", cursor, sub, changes)
	return updated, e
}
func actualPlanHead(p materializerNamespacePlan) (int64, bool) {
	known := false
	head := int64(0)
	for _, v := range []*namespaceimport.Head{p.MongoHead, p.RedisHead} {
		if v == nil {
			continue
		}
		if v.State != "present" || v.Value < 0 {
			return 0, false
		}
		known = true
		if head < v.Value {
			head = v.Value
		}
	}
	return head, known
}
func appendHistoricalRefs(out []namespaceimport.SourceRef, refs ...namespaceimport.SourceRef) []namespaceimport.SourceRef {
	for _, ref := range refs {
		present := false
		for _, old := range out {
			if old == ref {
				present = true
				break
			}
		}
		if !present {
			out = append(out, ref)
		}
	}
	return out
}
func (m *HistoricalMaterializer) recheckFact(ctx context.Context, f *HistoricalFact) error {
	if f == nil {
		return nil
	}
	body, e := materializerBody(*f)
	if e != nil {
		return e
	}
	_, e = m.originalFact(ctx, namespaceinventory.DerivedEntry{Key: sha(body), Value: body})
	return e
}
func (m *HistoricalMaterializer) effectiveOwner(ctx context.Context, owner historicalOwnerReduction, plan materializerNamespacePlan) (materializerOwnerPlan, error) {
	out := materializerOwnerPlan{CID: owner.CID, OwnerID: owner.OwnerID, OriginalRecordSetID: namespaceinventory.DerivedSetID("owner-original-records", namespaceimport.EffectiveOwnerKey(owner.CID, owner.OwnerID)), OriginalRecords: owner.OriginalRecords, HistoricalPolicySHA256: m.policySHA, CurrentMembership: owner.CurrentMembership}
	for _, f := range []*HistoricalFact{owner.First, owner.Conflict, owner.Floor, owner.Calendar, owner.CurrentMembership, owner.PriorProspective} {
		if e := m.recheckFact(ctx, f); e != nil {
			return out, e
		}
	}
	refs := []namespaceimport.SourceRef{}
	for _, f := range []*HistoricalFact{owner.First, owner.Conflict, owner.Floor, owner.Calendar, owner.CurrentMembership, owner.PriorProspective} {
		if f != nil {
			refs = appendHistoricalRefs(refs, f.Source)
		}
	}
	gap := func(reason string) (materializerOwnerPlan, error) {
		if len(refs) == 0 {
			return out, ErrOwnedEvidence
		}
		out.HistoricalGap = &namespaceimport.HistoricalAuthorizationGap{CID: owner.CID, OwnerID: owner.OwnerID, Reason: reason, Evidence: refs}
		return out, nil
	}
	if plan.Origin == nil || plan.NamespaceQuarantineReason != "" {
		return gap("origin-not-proven-namespace-separately-quarantined")
	}
	if owner.PriorProspective != nil {
		if owner.SegmentSources > 0 {
			out.OriginalLineagePending = true
			return out, nil
		}
		return gap("prior-history-policy-has-no-original-source-locator")
	}
	if owner.First == nil || owner.First.Window == nil {
		if owner.Calendar != nil {
			return gap("calendar-only-no-domain-specific-sequence-bound")
		}
		return gap("held-roster-does-not-prove-original-history")
	}
	if owner.Ambiguous {
		return gap("conflicting-original-owner-windows; no-union")
	}
	w := *owner.First.Window
	if w.CID != owner.CID || w.OwnerID != owner.OwnerID || w.OriginSHA256 != plan.Origin.SHA256 || w.GroupID != plan.Origin.GroupID {
		return gap("original-owner-window-origin-mismatch")
	}
	if plan.Origin.Kind != "group" {
		member := false
		for _, id := range plan.Origin.OriginIDs {
			member = member || id == owner.OwnerID
		}
		if !member {
			return gap("original-window-owner-not-an-original-principal")
		}
	}
	head, known := actualPlanHead(plan)
	if !known {
		return gap("original-domain-head-unavailable")
	}
	if w.Closed && w.Upper > head {
		return gap("original-closed-bound-exceeds-actual-domain-head")
	}
	// Base is a CLOSED historical interval frozen at this held cut. An original
	// open window retains its original raw/window hash in audit; it is not mutated
	// into a new business leave event. Prospective ownership is independent.
	originallyOpen := !w.Closed
	ownerFloor := int64(0)
	if owner.Floor != nil && owner.Floor.Floor != nil {
		ownerFloor = *owner.Floor.Floor
	}
	w, e := freezeHistoricalBase(w, head, ownerFloor, 0)
	if e != nil {
		return out, e
	}
	if originallyOpen {
		for _, h := range []*namespaceimport.Head{plan.MongoHead, plan.RedisHead} {
			if h != nil {
				refs = appendHistoricalRefs(refs, h.Evidence...)
			}
		}
	}
	var global HistoricalFact
	_, exists, e := m.readDerived(ctx, "global-floor", m.snapshot.Fingerprint(), sha([]byte(owner.CID)), &global)
	if e != nil {
		return out, e
	}
	if exists {
		if global.Floor == nil || m.recheckFact(ctx, &global) != nil {
			return out, ErrOwnedEvidence
		}
		w.Lower = max(w.Lower, *global.Floor)
		refs = appendHistoricalRefs(refs, global.Source)
	}
	// H=0 or Lower>H stays explicitly closed-empty. No overflow normalization,
	// fresh cut, min/max union, current-roster or open=0 interpretation occurs.
	w.Evidence = refs
	out.Window = &w
	return out, nil
}
func (m *HistoricalMaterializer) StepOwners(ctx context.Context) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil || m.hold.ValidateExclusion() != nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, expected, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	if st.Phase != "reduce" || st.Subcursor != "owners:" {
		return st, ErrCaptureProgressPending
	}
	rows, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("owner-reductions", m.snapshot.Fingerprint()), st.Cursor, 1)
	if e != nil {
		return st, e
	}
	if len(rows) == 0 {
		if !done {
			return st, ErrOwnedEvidence
		}
		updated, _, e := m.workspace.Commit(ctx, expected, "roots", "", "", nil)
		return updated, e
	}
	var owner historicalOwnerReduction
	if namespaceinventory.DecodeStrict(rows[0].Value, &owner) != nil || namespaceimport.EffectiveOwnerKey(owner.CID, owner.OwnerID) != next {
		return st, ErrOwnedEvidence
	}
	var plan materializerNamespacePlan
	_, exists, e := m.readDerived(ctx, "namespace-plans", m.snapshot.Fingerprint(), sha([]byte(owner.CID)), &plan)
	if e != nil || !exists {
		return st, ErrOwnedEvidence
	}
	result, e := m.effectiveOwner(ctx, owner, plan)
	if e != nil {
		return st, e
	}
	changes := []namespaceinventory.DerivedMutation{}
	change, e := derivedUpdate("owner-plans", owner.CID, next, namespaceinventory.DerivedEntry{}, result)
	if e != nil {
		return st, e
	}
	changes = append(changes, change)
	if result.HistoricalGap != nil {
		value, e := namespaceimport.HistoricalGapSHA256(*result.HistoricalGap)
		if e != nil {
			return st, e
		}
		change, e = derivedUpdate("history-gaps", owner.CID, value, namespaceinventory.DerivedEntry{}, result.HistoricalGap)
		if e != nil {
			return st, e
		}
		changes = append(changes, change)
	}
	updated, _, e := m.workspace.Commit(ctx, expected, "reduce", next, "owners:", changes)
	return updated, e
}

func freezeHistoricalBase(original namespaceimport.OwnerWindow, head, ownerFloor, globalFloor int64) (namespaceimport.OwnerWindow, error) {
	if head < 0 || head >= math.MaxInt64 || ownerFloor < 0 || ownerFloor >= math.MaxInt64 || globalFloor < 0 || globalFloor >= math.MaxInt64 || original.Lower < 0 || original.Lower >= math.MaxInt64 || original.Upper < 0 || original.Upper >= math.MaxInt64 || !original.Closed && original.Upper != 0 || original.Closed && original.Upper > head {
		return namespaceimport.OwnerWindow{}, ErrOwnedEvidence
	}
	out := original
	out.Evidence = append([]namespaceimport.SourceRef(nil), original.Evidence...)
	if !out.Closed {
		out.Closed = true
		out.Upper = head
	}
	out.Lower = max(int64(1), out.Lower, ownerFloor, globalFloor)
	return out, nil
}
