//go:build linux

package ownedcontrol

import (
	"context"
	"strconv"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// Private deterministic inputs for coverage/page construction. Root strings
// alone are not permission; original sealed refs and semantic source validation
// remain mandatory before a controller can mint any VerifiedPage.
type materializerCoverageInputs struct {
	CID                            string `json:"cid"`
	OriginalWindows                uint64 `json:"original_windows"`
	OriginalWindowSetRootSHA256    string `json:"original_window_set_root_sha256"`
	EffectiveOwners                uint64 `json:"effective_owners"`
	OwnerSetRootSHA256             string `json:"owner_set_root_sha256"`
	EffectiveBoundarySetRootSHA256 string `json:"effective_boundary_set_root_sha256"`
	HistoricalGapCount             uint64 `json:"historical_gap_count"`
	HistoricalGapSetRootSHA256     string `json:"historical_gap_set_root_sha256"`
	SourceFactsRootSHA256          string `json:"source_facts_root_sha256"`
	CurrentRosterRootSHA256        string `json:"current_roster_root_sha256"`
	CurrentRosterCount             uint64 `json:"current_roster_count"`
}

func (m *HistoricalMaterializer) advanceTree(ctx context.Context, kind, scope string) (namespaceinventory.DerivedMerkleState, bool, error) {
	tree := materializerTreeID(kind, scope)
	state, _, exists, e := m.workspace.MerkleState(ctx, tree)
	if e != nil {
		return state, false, e
	}
	if !exists {
		state, e = m.workspace.BeginMerkle(ctx, tree, namespaceinventory.DerivedSetID(kind, scope), "set")
	} else if state.Phase != "complete" {
		state, e = m.workspace.StepMerkle(ctx, tree)
	}
	return state, state.Phase == "complete", e
}
func (m *HistoricalMaterializer) rootState(ctx context.Context, kind, scope string) (namespaceinventory.DerivedMerkleState, error) {
	state, _, exists, e := m.workspace.MerkleState(ctx, materializerTreeID(kind, scope))
	if e != nil || !exists || state.Phase != "complete" || state.SetID != namespaceinventory.DerivedSetID(kind, scope) {
		return state, ErrCaptureProgressPending
	}
	actual, count, e := m.workspace.DerivedSetIdentity(ctx, state.SetID)
	if e != nil || actual != state.SetRoot || count != state.Count {
		return state, ErrOwnedEvidence
	}
	return state, nil
}
func (m *HistoricalMaterializer) advanceRootCursor(ctx context.Context, cursor, sub string, changes []namespaceinventory.DerivedMutation) (namespaceinventory.DerivedCheckpoint, error) {
	// A tree step committed its own immutable progress, so obtain the new exact
	// journal state before advancing the enclosing orchestration cursor.
	current, expected, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return current, e
	}
	if current.Phase != "roots" {
		return current, ErrOwnedEvidence
	}
	next, _, e := m.workspace.Commit(ctx, expected, "roots", cursor, sub, changes)
	return next, e
}

// StepRoots wires the actual persisted Merkle jobs into the materializer. One
// invocation does at most one <=30 leaf/parent job or one owner/CID output. It
// does not run a last full summary or restart a finished owner's source scan.
func (m *HistoricalMaterializer) stepHistoricalRoots(ctx context.Context) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil || m.hold.ValidateExclusion() != nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, _, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	if st.Phase != "roots" {
		return st, ErrCaptureProgressPending
	}
	sub := st.Subcursor
	if sub == "" {
		sub = "global:0"
	}
	if strings.HasPrefix(sub, "global:") {
		phase, e := strconv.Atoi(strings.TrimPrefix(sub, "global:"))
		if e != nil || phase < 0 || phase > 3 {
			return st, ErrOwnedEvidence
		}
		globals := [][2]string{{"source-rows", m.snapshot.Fingerprint()}, {"collection-rows", "message_delivery_sequences_v1"}, {"facts", m.snapshot.Fingerprint()}}
		if phase == len(globals) {
			return m.advanceRootCursor(ctx, "", "owner-roots", nil)
		}
		_, ready, e := m.advanceTree(ctx, globals[phase][0], globals[phase][1])
		if e != nil {
			return st, e
		}
		if !ready {
			current, _, e := m.workspace.Checkpoint(ctx)
			return current, e
		}
		return m.advanceRootCursor(ctx, "", "global:"+strconv.Itoa(phase+1), nil)
	}
	if sub == "owner-roots" {
		entries, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("owner-reductions", m.snapshot.Fingerprint()), st.Cursor, 1)
		if e != nil {
			return st, e
		}
		if len(entries) == 0 {
			if !done {
				return st, ErrOwnedEvidence
			}
			return m.advanceRootCursor(ctx, "", "namespace-roots:0", nil)
		}
		var owner historicalOwnerReduction
		if namespaceinventory.DecodeStrict(entries[0].Value, &owner) != nil || namespaceimport.EffectiveOwnerKey(owner.CID, owner.OwnerID) != next {
			return st, ErrOwnedEvidence
		}
		root, ready, e := m.advanceTree(ctx, "owner-original-records", next)
		if e != nil {
			return st, e
		}
		if !ready {
			current, _, e := m.workspace.Checkpoint(ctx)
			return current, e
		}
		var plan materializerOwnerPlan
		_, exists, e := m.readDerived(ctx, "owner-plans", owner.CID, next, &plan)
		if e != nil || !exists || plan.OriginalRecords != root.Count {
			return st, ErrOwnedEvidence
		}
		changes := []namespaceinventory.DerivedMutation{}
		if plan.Window != nil {
			// The source lower itself is an original floor witness when no later
			// visibility-floor row exists; this never invents a missing floor row.
			boundary := namespaceimport.EffectiveOwnerBoundary{Window: *plan.Window, HistoryPolicySHA256: m.policySHA, OriginalOwnerWindowsRootSHA256: root.RootSHA256, FloorEvidence: append([]namespaceimport.SourceRef(nil), plan.Window.Evidence...)}
			sum, e := namespaceimport.EffectiveBoundarySHA256(boundary)
			if e != nil {
				return st, e
			}
			for _, v := range []struct {
				kind, scope, key string
				value            any
			}{
				{"effective-boundaries", owner.CID, sum, boundary},
				{"effective-owners", owner.CID, next, struct{ CID, OwnerID string }{owner.CID, owner.OwnerID}},
				{"boundary-index", m.snapshot.Fingerprint(), next, struct{ CID, SHA256 string }{owner.CID, sum}},
			} {
				change, e := derivedUpdate(v.kind, v.scope, v.key, namespaceinventory.DerivedEntry{}, v.value)
				if e != nil {
					return st, e
				}
				changes = append(changes, change)
			}
		}
		return m.advanceRootCursor(ctx, next, "owner-roots", changes)
	}
	if strings.HasPrefix(sub, "namespace-roots:") {
		stage, e := strconv.Atoi(strings.TrimPrefix(sub, "namespace-roots:"))
		if e != nil || stage < 0 || stage > 6 {
			return st, ErrOwnedEvidence
		}
		entries, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("namespace-plans", m.snapshot.Fingerprint()), st.Cursor, 1)
		if e != nil {
			return st, e
		}
		if len(entries) == 0 {
			if !done || stage != 0 {
				return st, ErrOwnedEvidence
			}
			return m.advanceRootCursor(ctx, "", "prepared", nil)
		}
		var plan materializerNamespacePlan
		if namespaceinventory.DecodeStrict(entries[0].Value, &plan) != nil || sha([]byte(plan.CID)) != next {
			return st, ErrOwnedEvidence
		}
		jobs := []string{"namespace-facts", "owner-window-audit", "history-gaps", "roster-members", "effective-owners", "effective-boundaries"}
		if stage < len(jobs) {
			_, ready, e := m.advanceTree(ctx, jobs[stage], plan.CID)
			if e != nil {
				return st, e
			}
			if !ready {
				current, _, e := m.workspace.Checkpoint(ctx)
				return current, e
			}
			return m.advanceRootCursor(ctx, st.Cursor, "namespace-roots:"+strconv.Itoa(stage+1), nil)
		}
		facts, e := m.rootState(ctx, "namespace-facts", plan.CID)
		if e != nil {
			return st, e
		}
		original, e := m.rootState(ctx, "owner-window-audit", plan.CID)
		if e != nil || original.Count != plan.OriginalWindows {
			return st, ErrOwnedEvidence
		}
		gaps, e := m.rootState(ctx, "history-gaps", plan.CID)
		if e != nil {
			return st, e
		}
		roster, e := m.rootState(ctx, "roster-members", plan.CID)
		if e != nil {
			return st, e
		}
		owners, e := m.rootState(ctx, "effective-owners", plan.CID)
		if e != nil {
			return st, e
		}
		boundaries, e := m.rootState(ctx, "effective-boundaries", plan.CID)
		if e != nil || owners.Count != boundaries.Count {
			return st, ErrOwnedEvidence
		}
		value := materializerCoverageInputs{CID: plan.CID, OriginalWindows: original.Count, OriginalWindowSetRootSHA256: original.RootSHA256, EffectiveOwners: owners.Count, OwnerSetRootSHA256: owners.RootSHA256, EffectiveBoundarySetRootSHA256: boundaries.RootSHA256, HistoricalGapCount: gaps.Count, HistoricalGapSetRootSHA256: gaps.RootSHA256, SourceFactsRootSHA256: facts.RootSHA256, CurrentRosterRootSHA256: roster.RootSHA256, CurrentRosterCount: roster.Count}
		change, e := derivedUpdate("coverage-inputs", m.snapshot.Fingerprint(), next, namespaceinventory.DerivedEntry{}, value)
		if e != nil {
			return st, e
		}
		return m.advanceRootCursor(ctx, next, "namespace-roots:0", []namespaceinventory.DerivedMutation{change})
	}
	if sub == "prepared" {
		return st, ErrCaptureProgressPending
	}
	return st, ErrOwnedEvidence
}

// Step is the actual bounded orchestration entry. Its terminal "prepared"
// reduction state is deliberately NOT a signed source inventory, a write permit
// or Ready. The page/signature continuation must consume these exact original
// roots and source identities, without minting a fresh capture or history cut.
func (m *HistoricalMaterializer) Step(ctx context.Context) (namespaceinventory.DerivedCheckpoint, error) {
	if m == nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrOwnedEvidence
	}
	st, _, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return st, e
	}
	switch st.Phase {
	case "raw":
		return m.StepRaw(ctx)
	case "reduce":
		if strings.HasPrefix(st.Subcursor, "groups:") {
			return m.StepGroups(ctx)
		}
		if st.Subcursor == "owners:" {
			return m.StepOwners(ctx)
		}
		return m.StepReduce(ctx)
	case "roots":
		return m.StepRoots(ctx)
	}
	return st, ErrCaptureProgressPending
}
