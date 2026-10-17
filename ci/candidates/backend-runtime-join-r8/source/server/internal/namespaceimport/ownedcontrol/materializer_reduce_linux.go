//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// Reductions are private progress, NOT grants. Each step re-resolves its raw
// SourceRef and rederives the factual atom before updating one persistent CID
// or owner. The number of namespaces, historical rows or owners is not a cap.
type namespaceReduction struct {
	CID                       string                                     `json:"cid"`
	ObservedOrigin            *namespaceimport.NamespaceOriginDescriptor `json:"observed_origin,omitempty"`
	Claim                     *HistoricalFact                            `json:"claim,omitempty"`
	Principals                *HistoricalFact                            `json:"principals,omitempty"`
	MongoHead                 *HistoricalFact                            `json:"mongo_head,omitempty"`
	RedisHead                 *HistoricalFact                            `json:"redis_head,omitempty"`
	MongoCounterRows          uint64                                     `json:"mongo_counter_rows"`
	Facts                     uint64                                     `json:"facts"`
	OriginalWindows           uint64                                     `json:"original_windows"`
	ErasedSlots               uint64                                     `json:"erased_slots"`
	IntegrityReason           string                                     `json:"integrity_reason,omitempty"`
	OriginalOwnershipVerified bool                                       `json:"original_ownership_verified"`
	Complete                  bool                                       `json:"complete"`
}
type historicalOwnerReduction struct {
	CID               string          `json:"cid"`
	OwnerID           string          `json:"owner_id"`
	First             *HistoricalFact `json:"first,omitempty"`
	OriginalRecords   uint64          `json:"original_records"`
	SegmentSources    uint64          `json:"segment_sources"`
	Ambiguous         bool            `json:"ambiguous"`
	Conflict          *HistoricalFact `json:"conflict,omitempty"`
	Calendar          *HistoricalFact `json:"calendar,omitempty"`
	CurrentMembership *HistoricalFact `json:"current_membership,omitempty"`
	PriorProspective  *HistoricalFact `json:"prior_prospective,omitempty"`
	Floor             *HistoricalFact `json:"floor,omitempty"`
}
type materializerNamespacePlan struct {
	CID                             string                                     `json:"cid"`
	Origin                          *namespaceimport.NamespaceOriginDescriptor `json:"origin,omitempty"`
	OwnershipEvidence               []namespaceimport.SourceRef                `json:"ownership_evidence,omitempty"`
	MongoHead                       *namespaceimport.Head                      `json:"mongo_head,omitempty"`
	RedisHead                       *namespaceimport.Head                      `json:"redis_head,omitempty"`
	NeedsVerifiedLegacyMongoAbsence bool                                       `json:"needs_verified_legacy_mongo_absence"`
	NamespaceQuarantineReason       string                                     `json:"namespace_quarantine_reason,omitempty"`
	OriginalWindows                 uint64                                     `json:"original_windows"`
	ErasedSlots                     uint64                                     `json:"erased_slots"`
}

func (m *HistoricalMaterializer) readDerived(ctx context.Context, kind, scope, key string, out any) (namespaceinventory.DerivedEntry, bool, error) {
	entry, ok, e := m.workspace.ReadEntry(ctx, namespaceinventory.DerivedSetID(kind, scope), key)
	if e != nil || !ok {
		return entry, ok, e
	}
	if namespaceinventory.DecodeStrict(entry.Value, out) != nil {
		return entry, false, ErrOwnedEvidence
	}
	return entry, true, nil
}
func derivedUpdate(kind, scope, key string, prior namespaceinventory.DerivedEntry, value any) (namespaceinventory.DerivedMutation, error) {
	b, e := materializerBody(value)
	if e != nil {
		return namespaceinventory.DerivedMutation{}, e
	}
	return namespaceinventory.DerivedMutation{SetID: namespaceinventory.DerivedSetID(kind, scope), Key: key, ExpectedSHA256: prior.SHA256, Value: b}, nil
}
func (m *HistoricalMaterializer) originalFact(ctx context.Context, entry namespaceinventory.DerivedEntry) (HistoricalFact, error) {
	var value HistoricalFact
	if namespaceinventory.DecodeStrict(entry.Value, &value) != nil || historicalJSONSHA(value) != entry.Key {
		return value, ErrOwnedEvidence
	}
	r := value.Source
	raw, e := m.snapshot.ReadReference(ctx, m.live, r.Kind, r.ObjectSHA256, r.RecordSHA256, r.Position)
	if e != nil {
		return value, e
	}
	all, e := DecodeHistoricalRow(raw, m.describe)
	if e != nil {
		return value, e
	}
	for _, actual := range all {
		if historicalJSONSHA(actual) == entry.Key {
			return actual, nil
		}
	}
	return value, ErrOwnedEvidence
}
func incrementHistorical(n *uint64) error {
	if *n == ^uint64(0) {
		return ErrOwnedEvidence
	}
	*n++
	return nil
}
func observeNamespace(s *namespaceReduction, f HistoricalFact) error {
	if f.CID != s.CID {
		return ErrOwnedEvidence
	}
	if e := incrementHistorical(&s.Facts); e != nil {
		return e
	}
	if f.Origin != nil {
		if s.ObservedOrigin != nil && historicalJSONSHA(*s.ObservedOrigin) != historicalJSONSHA(*f.Origin) {
			s.IntegrityReason = "multiple-original-tuples-share-CID"
		} else {
			d := *f.Origin
			d.OriginIDs = append([]string(nil), d.OriginIDs...)
			s.ObservedOrigin = &d
		}
	}
	switch f.Kind {
	case "namespace-integrity-gap", "unresolved-source-row":
		s.IntegrityReason = f.Reason
	case "original-permanent-claim":
		if s.Claim != nil && historicalJSONSHA(s.Claim) != historicalJSONSHA(f) {
			s.IntegrityReason = "conflicting-original-permanent-claim"
		} else {
			copy := f
			s.Claim = &copy
		}
	case "original-permanent-principals":
		if s.Principals != nil && historicalJSONSHA(s.Principals) != historicalJSONSHA(f) {
			s.IntegrityReason = "conflicting-original-principals"
		} else {
			copy := f
			s.Principals = &copy
		}
	case "raw-mongo-head":
		if f.Head == nil || *f.Head < 0 {
			return ErrOwnedEvidence
		}
		if e := incrementHistorical(&s.MongoCounterRows); e != nil {
			return e
		}
		if s.MongoHead == nil || *s.MongoHead.Head < *f.Head {
			copy := f
			s.MongoHead = &copy
		}
	case "raw-redis-head":
		if f.Head == nil || *f.Head < 0 {
			return ErrOwnedEvidence
		}
		if s.RedisHead != nil && historicalJSONSHA(s.RedisHead) != historicalJSONSHA(f) {
			s.IntegrityReason = "conflicting-original-Redis-head"
		} else {
			copy := f
			s.RedisHead = &copy
		}
	case "erased-original-slot":
		return incrementHistorical(&s.ErasedSlots)
	case "original-explicit-owner-window", "original-history-segment-source":
		return incrementHistorical(&s.OriginalWindows)
	}
	return nil
}
func (m *HistoricalMaterializer) originalSequenceObject(ctx context.Context, id string) (HistoricalFact, error) {
	var ref namespaceimport.SourceRef
	_, exists, e := m.readDerived(ctx, "sequence-objects", m.snapshot.Fingerprint(), sha([]byte(id)), &ref)
	if e != nil || !exists {
		return HistoricalFact{}, ErrOwnedEvidence
	}
	raw, e := m.snapshot.ReadReference(ctx, m.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position)
	if e != nil {
		return HistoricalFact{}, e
	}
	facts, e := DecodeHistoricalRow(raw, m.describe)
	if e != nil || len(facts) != 1 {
		return HistoricalFact{}, ErrOwnedEvidence
	}
	return facts[0], nil
}
func (m *HistoricalMaterializer) finalizeNamespace(ctx context.Context, s *namespaceReduction) (materializerNamespacePlan, error) {
	plan := materializerNamespacePlan{CID: s.CID, OriginalWindows: s.OriginalWindows, ErasedSlots: s.ErasedSlots}
	if s.IntegrityReason != "" {
		plan.NamespaceQuarantineReason = s.IntegrityReason
		return plan, nil
	}
	if s.Claim == nil || s.Claim.Claim == nil || s.Principals == nil || s.Principals.Principals == nil {
		plan.NamespaceQuarantineReason = "original-origin-lifecycle-proof-unavailable"
		return plan, nil
	}
	c, p := s.Claim.Claim, s.Principals.Principals
	d := p.Origin
	if c.CID != s.CID || d.CID != s.CID || c.OriginKind != d.Kind || c.GroupID != d.GroupID || c.OriginSHA256 != d.SHA256 || c.CanonicalSHA256 != p.ClaimSHA256 || c.Epoch != p.Epoch || c.ActivationSHA256 != p.ActivationSHA256 || !c.CreatedAt.Equal(p.CreatedAt) || s.ObservedOrigin != nil && historicalJSONSHA(*s.ObservedOrigin) != historicalJSONSHA(d) {
		plan.NamespaceQuarantineReason = "original-claim-principals-lineage-conflict"
		return plan, nil
	}
	plan.OwnershipEvidence = []namespaceimport.SourceRef{s.Claim.Source, s.Principals.Source}
	if d.Kind == "group" {
		root, e := m.originalSequenceObject(ctx, "permanent-group-origin:"+sha([]byte(d.GroupID)))
		if e != nil || root.Claim == nil || root.Claim.GroupID != d.GroupID || root.Claim.OriginSHA256 != d.SHA256 || c.RequiredGroupReservationSHA256 != "" && root.Claim.CanonicalSHA256 != c.RequiredGroupReservationSHA256 {
			plan.NamespaceQuarantineReason = "missing-or-conflicting-original-group-reservation"
			return plan, nil
		}
		plan.OwnershipEvidence = append(plan.OwnershipEvidence, root.Source)
	}
	plan.Origin = &d
	s.OriginalOwnershipVerified = true
	if s.MongoHead != nil {
		plan.MongoHead = &namespaceimport.Head{State: "present", Value: *s.MongoHead.Head, Evidence: []namespaceimport.SourceRef{s.MongoHead.Source}}
	} else {
		plan.NeedsVerifiedLegacyMongoAbsence = true
	}
	if s.RedisHead != nil {
		plan.RedisHead = &namespaceimport.Head{State: "present", Value: *s.RedisHead.Head, Evidence: []namespaceimport.SourceRef{s.RedisHead.Source}}
	}
	// A missing Redis row is NOT a zero head. It is a separate unresolved source
	// requirement and cannot be silently repaired from retained message maxima.
	return plan, nil
}
func sameOriginalWindow(a, b *namespaceimport.OwnerWindow) bool {
	if a == nil || b == nil {
		return false
	}
	x, y := *a, *b
	x.Evidence = nil
	y.Evidence = nil
	return historicalJSONSHA(x) == historicalJSONSHA(y)
}
func (m *HistoricalMaterializer) ownerFactChanges(ctx context.Context, f HistoricalFact) ([]namespaceinventory.DerivedMutation, error) {
	if f.OwnerID == "" || f.Kind != "original-explicit-owner-window" && f.Kind != "original-visibility-floor" && f.Kind != "original-prospective-history-needs-lineage" && f.Kind != "original-history-segment-source" {
		return nil, nil
	}
	key := namespaceimport.EffectiveOwnerKey(f.CID, f.OwnerID)
	var owner historicalOwnerReduction
	prior, found, e := m.readDerived(ctx, "owner-reductions", m.snapshot.Fingerprint(), key, &owner)
	if e != nil {
		return nil, e
	}
	if !found {
		owner = historicalOwnerReduction{CID: f.CID, OwnerID: f.OwnerID}
	} else if owner.CID != f.CID || owner.OwnerID != f.OwnerID {
		return nil, ErrOwnedEvidence
	}
	changes := []namespaceinventory.DerivedMutation{}
	if f.Kind == "original-explicit-owner-window" || f.Kind == "original-history-segment-source" {
		if e = incrementHistorical(&owner.OriginalRecords); e != nil {
			return nil, e
		}
		if f.Kind == "original-history-segment-source" {
			if e = incrementHistorical(&owner.SegmentSources); e != nil {
				return nil, e
			}
		}
		if f.Kind == "original-explicit-owner-window" {
			if f.Window == nil {
				return nil, ErrOwnedEvidence
			}
			if owner.First == nil {
				copy := f
				owner.First = &copy
			} else if !sameOriginalWindow(owner.First.Window, f.Window) {
				owner.Ambiguous = true
				if owner.Conflict == nil {
					copy := f
					owner.Conflict = &copy
				}
			}
		}
		change, e := derivedUpdate("owner-window-audit", f.CID, historicalJSONSHA(f), namespaceinventory.DerivedEntry{}, f)
		if e != nil {
			return nil, e
		}
		changes = append(changes, change)
		change, e = derivedUpdate("owner-original-records", namespaceimport.EffectiveOwnerKey(f.CID, f.OwnerID), historicalJSONSHA(f), namespaceinventory.DerivedEntry{}, f)
		if e != nil {
			return nil, e
		}
		changes = append(changes, change)
	} else if f.Kind == "original-prospective-history-needs-lineage" {
		if owner.PriorProspective == nil {
			copy := f
			owner.PriorProspective = &copy
		}
	} else {
		if f.Floor == nil {
			return nil, ErrOwnedEvidence
		}
		if owner.Floor == nil || *owner.Floor.Floor < *f.Floor {
			copy := f
			owner.Floor = &copy
		}
	}
	change, e := derivedUpdate("owner-reductions", m.snapshot.Fingerprint(), key, prior, owner)
	if e != nil {
		return nil, e
	}
	return append(changes, change), nil
}

// StepReduce advances exactly ONE CID fact (or a CID close). It preserves the
// exact fact cursor across process restarts. It never gathers a complete
// group's owners or scans old pages again to reconstruct progress.
func (m *HistoricalMaterializer) StepReduce(ctx context.Context) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil || m.hold.ValidateExclusion() != nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, expected, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	if st.Phase != "reduce" || strings.HasPrefix(st.Subcursor, "owners:") || strings.HasPrefix(st.Subcursor, "groups:") {
		return st, ErrCaptureProgressPending
	}
	entries, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("namespaces", m.snapshot.Fingerprint()), st.Cursor, 1)
	if e != nil {
		return st, e
	}
	if len(entries) == 0 {
		if !done || st.Subcursor != "" {
			return st, ErrOwnedEvidence
		}
		updated, _, e := m.workspace.Commit(ctx, expected, "reduce", "", "groups:", nil)
		return updated, e
	}
	var ns struct {
		ID string `json:"id"`
	}
	if namespaceinventory.DecodeStrict(entries[0].Value, &ns) != nil || sha([]byte(ns.ID)) != entries[0].Key {
		return st, ErrOwnedEvidence
	}
	factCursor := ""
	if st.Subcursor != "" {
		parts := strings.Split(st.Subcursor, ":")
		if len(parts) != 2 || parts[0] != next {
			return st, ErrOwnedEvidence
		}
		factCursor = parts[1]
	}
	var reduced namespaceReduction
	prior, found, e := m.readDerived(ctx, "namespace-reductions", m.snapshot.Fingerprint(), next, &reduced)
	if e != nil {
		return st, e
	}
	if !found {
		reduced.CID = ns.ID
	} else if reduced.CID != ns.ID || reduced.Complete {
		return st, ErrOwnedEvidence
	}
	facts, fnext, fdone, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("namespace-facts", ns.ID), factCursor, 1)
	if e != nil {
		return st, e
	}
	changes := []namespaceinventory.DerivedMutation{}
	cursor, sub := st.Cursor, next+":"+fnext
	if len(facts) > 0 {
		fact, e := m.originalFact(ctx, facts[0])
		if e != nil {
			return st, e
		}
		if e = observeNamespace(&reduced, fact); e != nil {
			return st, e
		}
		extra, e := m.ownerFactChanges(ctx, fact)
		if e != nil {
			return st, e
		}
		changes = append(changes, extra...)
		if fact.Kind == "original-visibility-floor" && fact.OwnerID == "" {
			var floor HistoricalFact
			fprior, present, e := m.readDerived(ctx, "global-floor", m.snapshot.Fingerprint(), next, &floor)
			if e != nil {
				return st, e
			}
			if !present || floor.Floor == nil || *floor.Floor < *fact.Floor {
				change, e := derivedUpdate("global-floor", m.snapshot.Fingerprint(), next, fprior, fact)
				if e != nil {
					return st, e
				}
				changes = append(changes, change)
			}
		}
	} else {
		if !fdone {
			return st, ErrOwnedEvidence
		}
		plan, e := m.finalizeNamespace(ctx, &reduced)
		if e != nil {
			return st, e
		}
		reduced.Complete = true
		change, e := derivedUpdate("namespace-plans", m.snapshot.Fingerprint(), next, namespaceinventory.DerivedEntry{}, plan)
		if e != nil {
			return st, e
		}
		changes = append(changes, change)
		cursor = next
		sub = ""
	}
	change, e := derivedUpdate("namespace-reductions", m.snapshot.Fingerprint(), next, prior, reduced)
	if e != nil {
		return st, e
	}
	changes = append(changes, change)
	updated, _, e := m.workspace.Commit(ctx, expected, "reduce", cursor, sub, changes)
	if e != nil {
		return st, errors.Join(ErrCaptureProgressPending, e)
	}
	return updated, nil
}

// Private inspection returns factual progress only. It does not sign Inventory,
// mint VerifiedPage/Permit, fill an unknown legacy plane, or publish Ready.
func (m *HistoricalMaterializer) InspectNamespacePlan(ctx context.Context, cid string) (json.RawMessage, error) {
	if m == nil || m.hold.ValidateExclusion() != nil || !historicalIdentity(cid) {
		return nil, ErrOwnedEvidence
	}
	value, found, e := m.workspace.ReadEntry(ctx, namespaceinventory.DerivedSetID("namespace-plans", m.snapshot.Fingerprint()), sha([]byte(cid)))
	if e != nil || !found {
		return nil, ErrCaptureProgressPending
	}
	return append(json.RawMessage(nil), value.Value...), nil
}
