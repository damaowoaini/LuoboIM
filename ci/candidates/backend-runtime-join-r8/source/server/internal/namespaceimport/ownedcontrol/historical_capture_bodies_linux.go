//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

type materializedBodyState struct {
	Inventory          namespaceimport.Inventory `json:"inventory"`
	DescriptorSHA256   string                    `json:"descriptor_sha256"`
	Stage              string                    `json:"stage"`
	Cursor             string                    `json:"cursor,omitempty"`
	NamespaceCursor    string                    `json:"namespace_cursor,omitempty"`
	CID                string                    `json:"cid,omitempty"`
	KindIndex          int                       `json:"kind_index"`
	LeafCursor         string                    `json:"leaf_cursor,omitempty"`
	LeafProcessed      uint64                    `json:"leaf_processed"`
	NextPage           uint64                    `json:"next_page"`
	PreviousPageSHA256 string                    `json:"previous_page_sha256,omitempty"`
}
type materializedPageReference struct {
	Index          uint64 `json:"index"`
	PageSHA256     string `json:"page_sha256"`
	DocumentSHA256 string `json:"document_sha256"`
}

func (s *HistoricalCaptureSource) bodyState(ctx context.Context) (materializedBodyState, namespaceinventory.DerivedEntry, bool, error) {
	m := s.materializer
	var v materializedBodyState
	prior, found, e := m.readDerived(ctx, "source-body-state", m.snapshot.Fingerprint(), m.snapshot.Fingerprint(), &v)
	if e != nil {
		return v, prior, found, e
	}
	if found && (v.DescriptorSHA256 != s.descriptorSHA || v.Inventory.HeaderSHA256 != s.header.SHA256()) {
		return v, prior, false, ErrOwnedEvidence
	}
	return v, prior, found, nil
}
func (s *HistoricalCaptureSource) saveBodyState(ctx context.Context, v materializedBodyState, prior namespaceinventory.DerivedEntry, changes []namespaceinventory.DerivedMutation, complete bool) error {
	m := s.materializer
	if v.DescriptorSHA256 != s.descriptorSHA || v.Inventory.HeaderSHA256 != s.header.SHA256() {
		return ErrOwnedEvidence
	}
	change, e := derivedUpdate("source-body-state", m.snapshot.Fingerprint(), m.snapshot.Fingerprint(), prior, v)
	if e != nil {
		return e
	}
	changes = append(changes, change)
	st, expected, e := m.workspace.Checkpoint(ctx)
	if e != nil || (st.Phase != "roots" && st.Phase != "pages") {
		return ErrOwnedEvidence
	}
	phase := "pages"
	if complete {
		phase = "complete"
	}
	_, _, e = m.workspace.Commit(ctx, expected, phase, "", "source-bodies:"+v.Stage, changes)
	return e
}
func (s *HistoricalCaptureSource) beginBodies(ctx context.Context) error {
	m := s.materializer
	// Unknown records without ANY source CID cannot be silently omitted from a
	// publishable namespace set. Unlike an owner-only historical gap on a known
	// origin/head, this requires explicit source review before signing. All raw
	// rows and the bounded unattributed-gaps index remain privately inspectable.
	_, unattributed, e := m.workspace.DerivedSetIdentity(ctx, namespaceinventory.DerivedSetID("unattributed-gaps", m.snapshot.Fingerprint()))
	if e != nil {
		return e
	}
	if unattributed > 0 {
		return namespaceimport.ErrUntrusted
	}
	plan, _, found, e := m.outputPlan(ctx, s.header)
	if e != nil || !found || plan.Stage != "complete" || plan.PageCount == 0 {
		return ErrOwnedEvidence
	}
	if _, _, exists, e := s.bodyState(ctx); e != nil || exists {
		return ErrOwnedEvidence
	}
	identity, e := m.captureIdentity(ctx)
	if e != nil {
		return e
	}
	i := namespaceimport.Inventory{HeaderSHA256: s.header.SHA256(), LeaseID: m.hold.LeaseID(), CaptureID: identity.CaptureID, CaptureAgent: s.options.CaptureAgent, CapturedAt: identity.CapturedAt, OriginalTrust: "UNTRUSTED", MongoRootSHA256: identity.MongoRootSHA256, RedisRootSHA256: identity.RedisRootSHA256, RawRootSHA256: identity.RawRootSHA256, CoverageCount: plan.Counts.Origins, ProspectiveCount: plan.ProspectiveCount, OwnerHistorySegmentCount: plan.SegmentCount, HeldAdmissionGuardSHA256: m.hold.RecordSHA256(), PageCount: plan.PageCount, Counts: plan.Counts, KeyID: s.options.CaptureKeyID}
	return s.saveBodyState(ctx, materializedBodyState{Inventory: i, DescriptorSHA256: s.descriptorSHA, Stage: "coverage"}, namespaceinventory.DerivedEntry{}, nil, false)
}
func (s *HistoricalCaptureSource) namespaceExport(ctx context.Context, cid string) (materializedNamespaceExport, error) {
	m := s.materializer
	var v materializedNamespaceExport
	_, found, e := m.readDerived(ctx, "namespace-export-plans", m.snapshot.Fingerprint(), sha([]byte(cid)), &v)
	if e != nil || !found || v.CID != cid {
		return v, ErrOwnedEvidence
	}
	return v, nil
}
func (s *HistoricalCaptureSource) canonicalCoverage(ctx context.Context, i namespaceimport.Inventory, export materializedNamespaceExport) (namespaceimport.OriginCoverage, error) {
	m := s.materializer
	if export.Origin == nil {
		return namespaceimport.OriginCoverage{}, ErrOwnedEvidence
	}
	var v materializerCoverageInputs
	_, found, e := m.readDerived(ctx, "coverage-inputs", m.snapshot.Fingerprint(), sha([]byte(export.CID)), &v)
	if e != nil || !found || v.CID != export.CID || v.EffectiveOwners != export.ExpectedWindows {
		return namespaceimport.OriginCoverage{}, ErrOwnedEvidence
	}
	for _, job := range []struct {
		kind, root string
		count      uint64
	}{{"namespace-facts", v.SourceFactsRootSHA256, 0}, {"owner-window-audit", v.OriginalWindowSetRootSHA256, v.OriginalWindows}, {"effective-owners", v.OwnerSetRootSHA256, v.EffectiveOwners}, {"effective-boundaries", v.EffectiveBoundarySetRootSHA256, v.EffectiveOwners}, {"history-gaps", v.HistoricalGapSetRootSHA256, v.HistoricalGapCount}, {"roster-members", v.CurrentRosterRootSHA256, v.CurrentRosterCount}} {
		tree, e := m.rootState(ctx, job.kind, export.CID)
		if e != nil || tree.RootSHA256 != job.root || job.kind != "namespace-facts" && tree.Count != job.count {
			return namespaceimport.OriginCoverage{}, ErrOwnedEvidence
		}
	}
	refs := appendHistoricalRefs(nil, export.Origin.Evidence...)
	refs = appendHistoricalRefs(refs, export.Origin.MongoHead.Evidence...)
	refs = appendHistoricalRefs(refs, export.Origin.RedisHead.Evidence...)
	c := namespaceimport.OriginCoverage{HeaderSHA256: s.header.SHA256(), InventoryBindingSHA256: i.BindingSHA256(), CID: export.CID, OriginSHA256: export.Origin.Origin.SHA256, Domain: export.Origin.Origin.Kind, OriginalWindows: v.OriginalWindows, EffectiveOwners: v.EffectiveOwners, OriginalWindowSetRootSHA256: v.OriginalWindowSetRootSHA256, OwnerSetRootSHA256: v.OwnerSetRootSHA256, EffectiveBoundarySetRootSHA256: v.EffectiveBoundarySetRootSHA256, SourceCoverageRootSHA256: v.SourceFactsRootSHA256, HistoryPolicySHA256: m.policySHA, Evidence: refs}
	if v.HistoricalGapCount > 0 {
		c.HistoricalGapCount = v.HistoricalGapCount
		c.HistoricalGapSetRootSHA256 = v.HistoricalGapSetRootSHA256
	}
	if c.ValidateBinding(s.header, i, export.Origin.Origin) != nil {
		return c, ErrOwnedEvidence
	}
	return c, nil
}
func (s *HistoricalCaptureSource) canonicalProspective(ctx context.Context, i namespaceimport.Inventory, export materializedNamespaceExport, member HistoricalFact) (namespaceimport.ProspectiveOwnerAdmission, error) {
	m := s.materializer
	if export.Origin == nil || !export.Prospective || export.Origin.Origin.Kind != "group" || member.Kind != "held-current-member" || member.GroupID != export.Origin.Origin.GroupID || m.recheckFact(ctx, &member) != nil {
		return namespaceimport.ProspectiveOwnerAdmission{}, ErrOwnedEvidence
	}
	var roster historicalRosterProgress
	_, found, e := m.readDerived(ctx, "roster-progress", m.snapshot.Fingerprint(), sha([]byte(export.CID)), &roster)
	if e != nil || !found || !roster.Complete || roster.Conflicting || roster.Owners != 1 || roster.Members != export.ExpectedProspectives || roster.Group == nil || m.recheckFact(ctx, roster.Group) != nil {
		return namespaceimport.ProspectiveOwnerAdmission{}, ErrOwnedEvidence
	}
	root, e := m.rootState(ctx, "roster-members", export.CID)
	if e != nil || root.Count != roster.Members {
		return namespaceimport.ProspectiveOwnerAdmission{}, ErrOwnedEvidence
	}
	maximum, e := export.Origin.EffectiveSeedMaximum()
	if e != nil {
		return namespaceimport.ProspectiveOwnerAdmission{}, e
	}
	heads := appendHistoricalRefs(nil, export.Origin.MongoHead.Evidence...)
	heads = appendHistoricalRefs(heads, export.Origin.RedisHead.Evidence...)
	cut := appendHistoricalRefs(nil, roster.Group.Source)
	cut = appendHistoricalRefs(cut, export.Origin.Evidence...)
	a := namespaceimport.ProspectiveOwnerAdmission{Kind: "held-cut-roster-v1", CID: export.CID, OwnerID: member.OwnerID, OriginSHA256: export.Origin.Origin.SHA256, SeedMaximum: maximum, Lower: maximum + 1, HeaderSHA256: s.header.SHA256(), InventoryBindingSHA256: i.BindingSHA256(), CutSHA256: namespaceimport.HeldProspectiveCutSHA256(s.header, i), CutEvidence: cut, HeadEvidence: heads, MemberEvidence: []namespaceimport.SourceRef{member.Source}, RosterSHA256: root.RootSHA256, RosterMembers: root.Count}
	if a.ValidateShape() != nil {
		return a, ErrOwnedEvidence
	}
	return a, nil
}
func (s *HistoricalCaptureSource) coverageWitness(ctx context.Context, i namespaceimport.Inventory, cid string) (namespaceimport.OriginCoverageWitness, error) {
	m := s.materializer
	var index struct {
		SHA256 string `json:"sha256"`
	}
	_, found, e := m.readDerived(ctx, "canonical-coverage-cid", m.snapshot.Fingerprint(), sha([]byte(cid)), &index)
	if e != nil || !found {
		return namespaceimport.OriginCoverageWitness{}, ErrOwnedEvidence
	}
	var c namespaceimport.OriginCoverage
	_, found, e = m.readDerived(ctx, "canonical-coverages", m.snapshot.Fingerprint(), index.SHA256, &c)
	if e != nil || !found || c.CID != cid || namespaceimport.CoverageSHA256(c) != index.SHA256 {
		return namespaceimport.OriginCoverageWitness{}, ErrOwnedEvidence
	}
	root, proof, e := m.workspace.MerkleProof(ctx, materializerTreeID("canonical-coverages", m.snapshot.Fingerprint()), index.SHA256)
	if e != nil || root != i.CoverageSetRootSHA256 {
		return namespaceimport.OriginCoverageWitness{}, ErrOwnedEvidence
	}
	out := namespaceimport.OriginCoverageWitness{Coverage: c}
	for _, v := range proof {
		out.Proof = append(out.Proof, namespaceimport.MerkleSibling{SHA256: v.SHA256, Left: v.Left})
	}
	return out, nil
}
func (s *HistoricalCaptureSource) sourceSetProof(ctx context.Context, kind, scope, key, expectedRoot string) ([]namespaceimport.MerkleSibling, error) {
	root, proof, e := s.materializer.workspace.MerkleProof(ctx, materializerTreeID(kind, scope), key)
	if e != nil || root != expectedRoot {
		return nil, ErrOwnedEvidence
	}
	out := make([]namespaceimport.MerkleSibling, 0, len(proof))
	for _, v := range proof {
		out = append(out, namespaceimport.MerkleSibling{SHA256: v.SHA256, Left: v.Left})
	}
	return out, nil
}
func (s *HistoricalCaptureSource) canonicalBodyStep(ctx context.Context, v materializedBodyState, prior namespaceinventory.DerivedEntry) error {
	m := s.materializer
	switch v.Stage {
	case "coverage":
		rows, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("namespace-export-plans", m.snapshot.Fingerprint()), v.Cursor, 1)
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			if !done {
				return ErrOwnedEvidence
			}
			v.Stage = "prospective"
			v.Cursor = ""
			return s.saveBodyState(ctx, v, prior, nil, false)
		}
		var export materializedNamespaceExport
		if namespaceinventory.DecodeStrict(rows[0].Value, &export) != nil || sha([]byte(export.CID)) != next {
			return ErrOwnedEvidence
		}
		changes := []namespaceinventory.DerivedMutation{}
		if export.Origin != nil {
			c, e := s.canonicalCoverage(ctx, v.Inventory, export)
			if e != nil {
				return e
			}
			sum := namespaceimport.CoverageSHA256(c)
			a, e := derivedUpdate("canonical-coverages", m.snapshot.Fingerprint(), sum, namespaceinventory.DerivedEntry{}, c)
			if e != nil {
				return e
			}
			b, e := derivedUpdate("canonical-coverage-cid", m.snapshot.Fingerprint(), next, namespaceinventory.DerivedEntry{}, struct {
				SHA256 string `json:"sha256"`
			}{sum})
			if e != nil {
				return e
			}
			changes = append(changes, a, b)
		}
		v.Cursor = next
		return s.saveBodyState(ctx, v, prior, changes, false)
	case "prospective":
		rows, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("output-specs", m.snapshot.Fingerprint()), v.Cursor, 1)
		if e != nil {
			return e
		}
		if len(rows) == 0 {
			if !done {
				return ErrOwnedEvidence
			}
			v.Stage = "canonical-roots"
			v.KindIndex = 0
			v.Cursor = ""
			return s.saveBodyState(ctx, v, prior, nil, false)
		}
		var spec materializedOutputSpec
		if namespaceinventory.DecodeStrict(rows[0].Value, &spec) != nil || outputPlanKey(spec.Index) != next {
			return ErrOwnedEvidence
		}
		changes := []namespaceinventory.DerivedMutation{}
		if spec.Kind == "prospective" {
			export, e := s.namespaceExport(ctx, spec.CID)
			if e != nil {
				return e
			}
			var member HistoricalFact
			_, found, e := m.readDerived(ctx, "roster-members", spec.CID, spec.Key, &member)
			if e != nil || !found || sha([]byte(member.OwnerID)) != spec.Key {
				return ErrOwnedEvidence
			}
			a, e := s.canonicalProspective(ctx, v.Inventory, export, member)
			if e != nil {
				return e
			}
			sum, e := namespaceimport.ProspectiveAdmissionSHA256(a)
			if e != nil {
				return e
			}
			one, e := derivedUpdate("canonical-prospectives", m.snapshot.Fingerprint(), sum, namespaceinventory.DerivedEntry{}, a)
			if e != nil {
				return e
			}
			two, e := derivedUpdate("canonical-prospective-owner", m.snapshot.Fingerprint(), namespaceimport.EffectiveOwnerKey(spec.CID, member.OwnerID), namespaceinventory.DerivedEntry{}, struct {
				SHA256 string `json:"sha256"`
			}{sum})
			if e != nil {
				return e
			}
			changes = append(changes, one, two)
		}
		v.Cursor = next
		return s.saveBodyState(ctx, v, prior, changes, false)
	case "canonical-roots":
		jobs := []struct {
			kind  string
			count uint64
		}{{"canonical-coverages", v.Inventory.CoverageCount}, {"canonical-prospectives", v.Inventory.ProspectiveCount}, {"preserved-history-segments", v.Inventory.OwnerHistorySegmentCount}}
		if v.KindIndex < 0 || v.KindIndex > len(jobs) {
			return ErrOwnedEvidence
		}
		if v.KindIndex == len(jobs) {
			v.Stage = "data-pages"
			v.Cursor = ""
			return s.saveBodyState(ctx, v, prior, nil, false)
		}
		job := jobs[v.KindIndex]
		tree, ready, e := m.advanceTree(ctx, job.kind, m.snapshot.Fingerprint())
		if e != nil {
			return e
		}
		if !ready {
			return nil
		}
		if tree.Count != job.count {
			return ErrOwnedEvidence
		}
		switch v.KindIndex {
		case 0:
			v.Inventory.CoverageSetRootSHA256 = tree.RootSHA256
		case 1:
			if job.count > 0 {
				v.Inventory.ProspectiveSetRootSHA256 = tree.RootSHA256
			}
		case 2:
			if job.count > 0 {
				v.Inventory.OwnerHistorySegmentSetRootSHA256 = tree.RootSHA256
			}
		}
		// The canonical roots are excluded from binding; every body already built
		// must remain bound to the SAME immutable count/capture skeleton.
		v.KindIndex++
		return s.saveBodyState(ctx, v, prior, nil, false)
	}
	return ErrOwnedEvidence
}
func encodeOutput(v any) (json.RawMessage, error) {
	b, e := json.Marshal(v)
	if e != nil {
		return nil, e
	}
	return b, nil
}
