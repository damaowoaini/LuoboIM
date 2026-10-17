//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/ed25519"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

func (s *HistoricalCaptureSource) pageEnvelope(v materializedBodyState) namespaceimport.PageData {
	return namespaceimport.PageData{HeaderSHA256: s.header.SHA256(), InventoryBindingSHA256: v.Inventory.BindingSHA256(), Index: v.NextPage, PreviousSHA256: v.PreviousPageSHA256, RawSourceRootSHA256: v.Inventory.RawRootSHA256}
}
func (s *HistoricalCaptureSource) writePage(ctx context.Context, v materializedBodyState, prior namespaceinventory.DerivedEntry, page namespaceimport.PageData) error {
	m := s.materializer
	if page.Index != v.NextPage || v.NextPage >= v.Inventory.PageCount || page.HeaderSHA256 != s.header.SHA256() || page.InventoryBindingSHA256 != v.Inventory.BindingSHA256() || page.PreviousSHA256 != v.PreviousPageSHA256 || page.RawSourceRootSHA256 != v.Inventory.RawRootSHA256 {
		return ErrOwnedEvidence
	}
	rows := len(page.Origins) + len(page.OwnerWindows) + len(page.Quarantines) + len(page.CoverageClosures) + len(page.ProspectiveAdmissions) + len(page.OwnerHistorySegments)
	if rows < 1 || rows > s.options.PageRows {
		return ErrOwnedEvidence
	}
	body, e := json.Marshal(page)
	if e != nil || len(body) > s.options.PageBytes {
		return ErrOwnedEvidence
	}
	document, e := m.workspace.StoreOutputDocument(ctx, body, s.options.PageBytes)
	if e != nil {
		return e
	}
	sum := page.Identity().SHA256
	ref := materializedPageReference{Index: page.Index, PageSHA256: sum, DocumentSHA256: document}
	one, e := derivedUpdate("source-pages", m.snapshot.Fingerprint(), outputPlanKey(page.Index), namespaceinventory.DerivedEntry{}, ref)
	if e != nil {
		return e
	}
	two, e := derivedUpdate("source-page-hashes", m.snapshot.Fingerprint(), sum, namespaceinventory.DerivedEntry{}, ref)
	if e != nil {
		return e
	}
	v.NextPage++
	v.PreviousPageSHA256 = sum
	return s.saveBodyState(ctx, v, prior, []namespaceinventory.DerivedMutation{one, two}, false)
}
func (s *HistoricalCaptureSource) dataPage(ctx context.Context, v materializedBodyState, spec materializedOutputSpec) (namespaceimport.PageData, error) {
	m := s.materializer
	p := s.pageEnvelope(v)
	switch spec.Kind {
	case "origin":
		export, e := s.namespaceExport(ctx, spec.CID)
		if e != nil || export.Origin == nil {
			return p, ErrOwnedEvidence
		}
		r := *export.Origin
		r.SeedEvidenceSHA256, e = namespaceimport.SourceSeedEvidenceSHA256(s.header, v.Inventory, r)
		if e != nil {
			return p, e
		}
		c, e := s.coverageWitness(ctx, v.Inventory, spec.CID)
		if e != nil {
			return p, e
		}
		p.Origins = []namespaceimport.OriginRecord{r}
		p.OriginCoverages = []namespaceimport.OriginCoverageWitness{c}
	case "quarantine":
		export, e := s.namespaceExport(ctx, spec.CID)
		if e != nil || export.Quarantine == nil {
			return p, ErrOwnedEvidence
		}
		p.Quarantines = []namespaceimport.Quarantine{*export.Quarantine}
	case "boundaries":
		var b namespaceimport.EffectiveOwnerBoundary
		_, found, e := m.readDerived(ctx, "effective-boundaries", spec.CID, spec.Key, &b)
		if e != nil || !found || b.Window.CID != spec.CID {
			return p, ErrOwnedEvidence
		}
		sum, e := namespaceimport.EffectiveBoundarySHA256(b)
		if e != nil || sum != spec.Key {
			return p, ErrOwnedEvidence
		}
		c, e := s.coverageWitness(ctx, v.Inventory, spec.CID)
		if e != nil {
			return p, e
		}
		owners, e := s.sourceSetProof(ctx, "effective-owners", spec.CID, namespaceimport.EffectiveOwnerKey(spec.CID, b.Window.OwnerID), c.Coverage.OwnerSetRootSHA256)
		if e != nil {
			return p, e
		}
		boundaries, e := s.sourceSetProof(ctx, "effective-boundaries", spec.CID, spec.Key, c.Coverage.EffectiveBoundarySetRootSHA256)
		if e != nil {
			return p, e
		}
		p.OwnerWindows = []namespaceimport.OwnerWindow{b.Window}
		p.EffectiveBoundaries = []namespaceimport.EffectiveBoundaryWitness{{Boundary: b, Coverage: c, OwnerProof: owners, BoundaryProof: boundaries}}
	case "prospective":
		var member HistoricalFact
		_, found, e := m.readDerived(ctx, "roster-members", spec.CID, spec.Key, &member)
		if e != nil || !found || sha([]byte(member.OwnerID)) != spec.Key || m.recheckFact(ctx, &member) != nil {
			return p, ErrOwnedEvidence
		}
		var index struct {
			SHA256 string `json:"sha256"`
		}
		_, found, e = m.readDerived(ctx, "canonical-prospective-owner", m.snapshot.Fingerprint(), namespaceimport.EffectiveOwnerKey(spec.CID, member.OwnerID), &index)
		if e != nil || !found {
			return p, ErrOwnedEvidence
		}
		var a namespaceimport.ProspectiveOwnerAdmission
		_, found, e = m.readDerived(ctx, "canonical-prospectives", m.snapshot.Fingerprint(), index.SHA256, &a)
		if e != nil || !found || a.CID != spec.CID || a.OwnerID != member.OwnerID {
			return p, ErrOwnedEvidence
		}
		sum, e := namespaceimport.ProspectiveAdmissionSHA256(a)
		if e != nil || sum != index.SHA256 {
			return p, ErrOwnedEvidence
		}
		proof, e := s.sourceSetProof(ctx, "canonical-prospectives", m.snapshot.Fingerprint(), sum, v.Inventory.ProspectiveSetRootSHA256)
		if e != nil {
			return p, e
		}
		p.ProspectiveAdmissions = []namespaceimport.ProspectiveOwnerWitness{{Admission: a, Proof: proof}}
	case "history-segment":
		var a namespaceimport.OwnerHistorySegment
		_, found, e := m.readDerived(ctx, "preserved-history-segments", m.snapshot.Fingerprint(), spec.Key, &a)
		if e != nil || !found || a.CID != spec.CID {
			return p, ErrOwnedEvidence
		}
		sum, e := namespaceimport.OwnerHistorySegmentSHA256(a)
		if e != nil || sum != spec.Key {
			return p, ErrOwnedEvidence
		}
		proof, e := s.sourceSetProof(ctx, "preserved-history-segments", m.snapshot.Fingerprint(), sum, v.Inventory.OwnerHistorySegmentSetRootSHA256)
		if e != nil {
			return p, e
		}
		p.OwnerHistorySegments = []namespaceimport.OwnerHistorySegmentWitness{{Segment: a, HeaderSHA256: s.header.SHA256(), InventoryBindingSHA256: v.Inventory.BindingSHA256(), Proof: proof}}
	default:
		return p, ErrOwnedEvidence
	}
	return p, nil
}
func (s *HistoricalCaptureSource) dataPageStep(ctx context.Context, v materializedBodyState, prior namespaceinventory.DerivedEntry) error {
	m := s.materializer
	plan, _, found, e := m.outputPlan(ctx, s.header)
	if e != nil || !found || plan.Stage != "complete" {
		return ErrOwnedEvidence
	}
	rows, next, done, total, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("output-specs", m.snapshot.Fingerprint()), v.Cursor, 1)
	if e != nil || total != plan.DataPages {
		return ErrOwnedEvidence
	}
	if len(rows) == 0 {
		if !done || v.NextPage != plan.DataPages {
			return ErrOwnedEvidence
		}
		v.Stage = "namespace-closures"
		v.Cursor = ""
		v.NamespaceCursor = ""
		v.CID = ""
		v.KindIndex = 0
		return s.saveBodyState(ctx, v, prior, nil, false)
	}
	var spec materializedOutputSpec
	if namespaceinventory.DecodeStrict(rows[0].Value, &spec) != nil || spec.Index != v.NextPage || outputPlanKey(spec.Index) != next {
		return ErrOwnedEvidence
	}
	page, e := s.dataPage(ctx, v, spec)
	if e != nil {
		return e
	}
	v.Cursor = next
	return s.writePage(ctx, v, prior, page)
}

// These are the real immutable receipt _id prefixes emitted by the Store's
// namespace_import_boundaries.go importSetPrefix, never account/CID parsing.
func sourceClosureCursor(h namespaceimport.Header, kind, cid, leaf string) string {
	if leaf == "" {
		return ""
	}
	return "namespace-import-set:" + h.SHA256() + ":" + kind + ":" + sha([]byte(cid)) + ":" + leaf
}
func (s *HistoricalCaptureSource) closurePage(ctx context.Context, v materializedBodyState, kind, cid, sourceKind, scope string) (namespaceimport.PageData, string, uint64, bool, error) {
	m := s.materializer
	p := s.pageEnvelope(v)
	tree, e := m.rootState(ctx, sourceKind, scope)
	if e != nil {
		return p, "", 0, false, e
	}
	if v.LeafProcessed > tree.Count || (v.LeafProcessed == 0) != (v.LeafCursor == "") {
		return p, "", 0, false, ErrOwnedEvidence
	}
	// Read one verified private value at a time. ReadEntries has a separate
	// 256KiB byte budget, so requesting64 and treating a short byte-bounded
	// reply as missing leaves would permanently reject valid large witnesses.
	// This consumes at most64 leaves/4MiB serially, retaining only one value.
	next := v.LeafCursor
	processed := v.LeafProcessed
	for count := 0; count < 64 && processed < tree.Count; count++ {
		rows, after, _, total, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID(sourceKind, scope), next, 1)
		if e != nil || total != tree.Count || len(rows) != 1 || after <= next {
			return p, "", 0, false, ErrOwnedEvidence
		}
		next = after
		processed++
	}
	complete := processed == tree.Count
	p.CoverageClosures = []namespaceimport.CoverageClosure{{Kind: kind, CID: cid, PreviousCursor: sourceClosureCursor(s.header, kind, cid, v.LeafCursor)}}
	return p, next, processed, complete, nil
}
func (s *HistoricalCaptureSource) closureStep(ctx context.Context, v materializedBodyState, prior namespaceinventory.DerivedEntry) error {
	m := s.materializer
	if v.Stage == "namespace-closures" {
		if v.CID == "" {
			rows, next, done, _, e := m.workspace.ReadEntries(ctx, namespaceinventory.DerivedSetID("namespace-export-plans", m.snapshot.Fingerprint()), v.NamespaceCursor, 1)
			if e != nil {
				return e
			}
			if len(rows) == 0 {
				if !done {
					return ErrOwnedEvidence
				}
				v.Stage = "global-closures"
				v.KindIndex = 0
				return s.saveBodyState(ctx, v, prior, nil, false)
			}
			var export materializedNamespaceExport
			if namespaceinventory.DecodeStrict(rows[0].Value, &export) != nil || sha([]byte(export.CID)) != next {
				return ErrOwnedEvidence
			}
			v.NamespaceCursor = next
			if export.Origin == nil {
				return s.saveBodyState(ctx, v, prior, nil, false)
			}
			v.CID = export.CID
			v.KindIndex = 0
			v.LeafCursor = ""
			v.LeafProcessed = 0
			return s.saveBodyState(ctx, v, prior, nil, false)
		}
		jobs := []struct{ kind, source string }{{"owners", "effective-owners"}, {"boundaries", "effective-boundaries"}}
		if v.KindIndex < 0 || v.KindIndex >= len(jobs) {
			return ErrOwnedEvidence
		}
		job := jobs[v.KindIndex]
		page, next, processed, done, e := s.closurePage(ctx, v, job.kind, v.CID, job.source, v.CID)
		if e != nil {
			return e
		}
		v.LeafCursor = next
		v.LeafProcessed = processed
		if done {
			v.KindIndex++
			v.LeafCursor = ""
			v.LeafProcessed = 0
			if v.KindIndex == len(jobs) {
				v.CID = ""
				v.KindIndex = 0
			}
		}
		return s.writePage(ctx, v, prior, page)
	}
	if v.Stage == "global-closures" {
		jobs := []struct {
			kind, source string
			count        uint64
		}{{"coverage", "canonical-coverages", v.Inventory.CoverageCount}, {"prospective", "canonical-prospectives", v.Inventory.ProspectiveCount}, {"history-segments", "preserved-history-segments", v.Inventory.OwnerHistorySegmentCount}}
		if v.KindIndex < 0 || v.KindIndex > len(jobs) {
			return ErrOwnedEvidence
		}
		if v.KindIndex == len(jobs) {
			if v.NextPage != v.Inventory.PageCount {
				return ErrOwnedEvidence
			}
			v.Stage = "pages-root"
			return s.saveBodyState(ctx, v, prior, nil, false)
		}
		job := jobs[v.KindIndex]
		if v.KindIndex > 0 && job.count == 0 {
			v.KindIndex++
			return s.saveBodyState(ctx, v, prior, nil, false)
		}
		page, next, processed, done, e := s.closurePage(ctx, v, job.kind, "", job.source, m.snapshot.Fingerprint())
		if e != nil {
			return e
		}
		v.LeafCursor = next
		v.LeafProcessed = processed
		if done {
			v.KindIndex++
			v.LeafCursor = ""
			v.LeafProcessed = 0
		}
		return s.writePage(ctx, v, prior, page)
	}
	return ErrOwnedEvidence
}
func (s *HistoricalCaptureSource) pageRootStep(ctx context.Context, v materializedBodyState, prior namespaceinventory.DerivedEntry) error {
	m := s.materializer
	treeID := materializerTreeID("source-page-hashes", m.snapshot.Fingerprint())
	tree, _, found, e := m.workspace.MerkleState(ctx, treeID)
	if e != nil {
		return e
	}
	if !found {
		tree, e = m.workspace.BeginMerkle(ctx, treeID, namespaceinventory.DerivedSetID("source-page-hashes", m.snapshot.Fingerprint()), "page")
	} else if tree.Phase != "complete" {
		tree, e = m.workspace.StepMerkle(ctx, treeID)
	}
	if e != nil {
		return e
	}
	if tree.Phase != "complete" {
		return nil
	}
	if tree.Count != v.Inventory.PageCount || tree.Domain != "page" || v.NextPage != v.Inventory.PageCount {
		return ErrOwnedEvidence
	}
	v.Inventory.PagesRootSHA256 = tree.RootSHA256
	body, e := json.Marshal(v.Inventory)
	if e != nil {
		return e
	}
	v.Inventory.Signature = ed25519.Sign(s.options.CapturePrivateKey, body)
	document, e := m.workspace.StoreOutputDocument(ctx, mustOutput(v.Inventory), s.options.PageBytes)
	if e != nil {
		return e
	}
	change, e := derivedUpdate("signed-source-inventory", m.snapshot.Fingerprint(), m.snapshot.Fingerprint(), namespaceinventory.DerivedEntry{}, struct {
		DocumentSHA256 string `json:"document_sha256"`
	}{document})
	if e != nil {
		return e
	}
	v.Stage = "complete"
	return s.saveBodyState(ctx, v, prior, []namespaceinventory.DerivedMutation{change}, true)
}
func mustOutput(v any) json.RawMessage {
	body, e := json.Marshal(v)
	if e != nil {
		panic("historical source internal non-JSON value")
	}
	return body
}
func (s *HistoricalCaptureSource) stepBodies(ctx context.Context) error {
	v, prior, found, e := s.bodyState(ctx)
	if e != nil || !found {
		return ErrOwnedEvidence
	}
	switch v.Stage {
	case "coverage", "prospective", "canonical-roots":
		return s.canonicalBodyStep(ctx, v, prior)
	case "data-pages":
		return s.dataPageStep(ctx, v, prior)
	case "namespace-closures", "global-closures":
		return s.closureStep(ctx, v, prior)
	case "pages-root":
		return s.pageRootStep(ctx, v, prior)
	}
	return ErrOwnedEvidence
}
func (s *HistoricalCaptureSource) finalInventory(ctx context.Context) (namespaceimport.Inventory, error) {
	m := s.materializer
	st, _, e := m.workspace.Checkpoint(ctx)
	if e != nil || st.Phase != "complete" {
		return namespaceimport.Inventory{}, ErrCaptureProgressPending
	}
	v, _, found, e := s.bodyState(ctx)
	if e != nil || !found || v.Stage != "complete" {
		return namespaceimport.Inventory{}, ErrOwnedEvidence
	}
	var ref struct {
		DocumentSHA256 string `json:"document_sha256"`
	}
	_, found, e = m.readDerived(ctx, "signed-source-inventory", m.snapshot.Fingerprint(), m.snapshot.Fingerprint(), &ref)
	if e != nil || !found {
		return namespaceimport.Inventory{}, ErrOwnedEvidence
	}
	body, e := m.workspace.ReadOutputDocument(ctx, ref.DocumentSHA256, s.options.PageBytes)
	var i namespaceimport.Inventory
	if e != nil || namespaceinventory.DecodeStrict(body, &i) != nil || historicalJSONSHA(i) != historicalJSONSHA(v.Inventory) {
		return i, ErrOwnedEvidence
	}
	signature := append([]byte(nil), i.Signature...)
	i.Signature = nil
	canonical, e := json.Marshal(i)
	i.Signature = signature
	if e != nil || !ed25519.Verify(s.options.CapturePublicKey, canonical, signature) || i.HeaderSHA256 != s.header.SHA256() || i.LeaseID != m.hold.LeaseID() || i.HeldAdmissionGuardSHA256 != m.hold.RecordSHA256() {
		return i, ErrOwnedEvidence
	}
	identity, e := m.captureIdentity(ctx)
	if e != nil || i.CaptureID != identity.CaptureID || !i.CapturedAt.Equal(identity.CapturedAt) || i.RawRootSHA256 != identity.RawRootSHA256 || i.MongoRootSHA256 != identity.MongoRootSHA256 || i.RedisRootSHA256 != identity.RedisRootSHA256 {
		return i, ErrOwnedEvidence
	}
	plan, _, found, e := m.outputPlan(ctx, s.header)
	if e != nil || !found || i.PageCount != plan.PageCount || i.Counts != plan.Counts || i.CoverageCount != plan.Counts.Origins || i.ProspectiveCount != plan.ProspectiveCount || i.OwnerHistorySegmentCount != plan.SegmentCount {
		return i, ErrOwnedEvidence
	}
	tree, _, found, e := m.workspace.MerkleState(ctx, materializerTreeID("source-page-hashes", m.snapshot.Fingerprint()))
	if e != nil || !found || tree.Phase != "complete" || tree.RootSHA256 != i.PagesRootSHA256 || tree.Count != i.PageCount {
		return i, ErrOwnedEvidence
	}
	actual, count, e := m.workspace.DerivedSetIdentity(ctx, tree.SetID)
	if e != nil || actual != tree.SetRoot || count != tree.Count {
		return i, ErrOwnedEvidence
	}
	return i, nil
}
