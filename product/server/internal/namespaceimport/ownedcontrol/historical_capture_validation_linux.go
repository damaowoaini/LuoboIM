//go:build linux

package ownedcontrol

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

func (s *HistoricalCaptureSource) sourceReferences(ctx context.Context, refs []namespaceimport.SourceRef) error {
	if len(refs) == 0 {
		return ErrOwnedEvidence
	}
	for _, ref := range refs {
		if _, e := s.materializer.snapshot.ReadReference(ctx, s.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position); e != nil {
			return e
		}
	}
	return nil
}
func (s *HistoricalCaptureSource) sourceNamespace(ctx context.Context, cid string) (materializerNamespacePlan, namespaceReduction, error) {
	m := s.materializer
	var plan materializerNamespacePlan
	_, found, e := m.readDerived(ctx, "namespace-plans", m.snapshot.Fingerprint(), sha([]byte(cid)), &plan)
	if e != nil || !found || plan.CID != cid {
		return plan, namespaceReduction{}, ErrOwnedEvidence
	}
	var original namespaceReduction
	_, found, e = m.readDerived(ctx, "namespace-reductions", m.snapshot.Fingerprint(), sha([]byte(cid)), &original)
	if e != nil || !found || !original.Complete || original.CID != cid {
		return plan, original, ErrOwnedEvidence
	}
	for _, f := range []*HistoricalFact{original.Claim, original.Principals, original.MongoHead, original.RedisHead} {
		if e = m.recheckFact(ctx, f); e != nil {
			return plan, original, e
		}
	}
	rebuilt, e := m.finalizeNamespace(ctx, &original)
	if e != nil || historicalJSONSHA(rebuilt) != historicalJSONSHA(plan) {
		return plan, original, ErrOwnedEvidence
	}
	return plan, original, nil
}
func (s *HistoricalCaptureSource) ValidateOrigin(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, r namespaceimport.OriginRecord) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return e
	}
	plan, _, e := s.sourceNamespace(ctx, r.Origin.CID)
	if e != nil {
		return e
	}
	export, e := s.materializer.exportNamespacePlan(ctx, h, plan)
	if e != nil || export.Origin == nil {
		return ErrOwnedEvidence
	}
	expected := *export.Origin
	expected.SeedEvidenceSHA256, e = namespaceimport.SourceSeedEvidenceSHA256(h, i, expected)
	if e != nil {
		return e
	}
	if historicalJSONSHA(expected) != historicalJSONSHA(r) {
		return ErrOwnedEvidence
	}
	refs := appendHistoricalRefs(nil, r.Evidence...)
	refs = appendHistoricalRefs(refs, r.MongoHead.Evidence...)
	refs = appendHistoricalRefs(refs, r.RedisHead.Evidence...)
	return s.sourceReferences(ctx, refs)
}
func (s *HistoricalCaptureSource) originalOwner(ctx context.Context, cid, owner string) (materializerOwnerPlan, error) {
	m := s.materializer
	var raw historicalOwnerReduction
	_, found, e := m.readDerived(ctx, "owner-reductions", m.snapshot.Fingerprint(), namespaceimport.EffectiveOwnerKey(cid, owner), &raw)
	if e != nil || !found || raw.CID != cid || raw.OwnerID != owner {
		return materializerOwnerPlan{}, ErrOwnedEvidence
	}
	plan, _, e := s.sourceNamespace(ctx, cid)
	if e != nil {
		return materializerOwnerPlan{}, e
	}
	return m.effectiveOwner(ctx, raw, plan)
}
func (s *HistoricalCaptureSource) ValidateWindow(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, w namespaceimport.OwnerWindow) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return e
	}
	expected, e := s.originalOwner(ctx, w.CID, w.OwnerID)
	if e != nil || expected.Window == nil || historicalJSONSHA(*expected.Window) != historicalJSONSHA(w) {
		return ErrOwnedEvidence
	}
	return s.sourceReferences(ctx, w.Evidence)
}
func (s *HistoricalCaptureSource) ValidateQuarantine(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, q namespaceimport.Quarantine) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return e
	}
	plan, _, e := s.sourceNamespace(ctx, q.CID)
	if e != nil {
		return e
	}
	export, e := s.materializer.exportNamespacePlan(ctx, h, plan)
	if e != nil || export.Quarantine == nil || historicalJSONSHA(*export.Quarantine) != historicalJSONSHA(q) {
		return ErrOwnedEvidence
	}
	return s.sourceReferences(ctx, q.Evidence)
}
func (s *HistoricalCaptureSource) ValidateLegacyHeadAbsence(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, r namespaceimport.OriginRecord) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return e
	}
	return s.materializer.VerifyLegacyCounterAbsence(ctx, s.materializer.snapshot, h, i, r)
}
func (s *HistoricalCaptureSource) ValidateCoverage(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, r namespaceimport.OriginRecord, c namespaceimport.OriginCoverage) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return e
	}
	export, e := s.namespaceExport(ctx, r.Origin.CID)
	if e != nil || export.Origin == nil || historicalJSONSHA(export.Origin.Origin) != historicalJSONSHA(r.Origin) {
		return ErrOwnedEvidence
	}
	expected, e := s.canonicalCoverage(ctx, i, export)
	if e != nil || historicalJSONSHA(expected) != historicalJSONSHA(c) {
		return ErrOwnedEvidence
	}
	return s.sourceReferences(ctx, c.Evidence)
}
func (s *HistoricalCaptureSource) ValidateEffectiveBoundary(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, b namespaceimport.EffectiveOwnerBoundary, c namespaceimport.OriginCoverage) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return e
	}
	m := s.materializer
	owner, e := s.originalOwner(ctx, b.Window.CID, b.Window.OwnerID)
	if e != nil || owner.Window == nil || historicalJSONSHA(*owner.Window) != historicalJSONSHA(b.Window) {
		return ErrOwnedEvidence
	}
	root, e := m.rootState(ctx, "owner-original-records", namespaceimport.EffectiveOwnerKey(b.Window.CID, b.Window.OwnerID))
	if e != nil || root.Count != owner.OriginalRecords {
		return ErrOwnedEvidence
	}
	expected := namespaceimport.EffectiveOwnerBoundary{Window: *owner.Window, HistoryPolicySHA256: m.policySHA, OriginalOwnerWindowsRootSHA256: root.RootSHA256, FloorEvidence: append([]namespaceimport.SourceRef(nil), owner.Window.Evidence...)}
	if historicalJSONSHA(expected) != historicalJSONSHA(b) {
		return ErrOwnedEvidence
	}
	export, e := s.namespaceExport(ctx, b.Window.CID)
	if e != nil {
		return e
	}
	coverage, e := s.canonicalCoverage(ctx, i, export)
	if e != nil || historicalJSONSHA(coverage) != historicalJSONSHA(c) {
		return ErrOwnedEvidence
	}
	return s.sourceReferences(ctx, appendHistoricalRefs(append([]namespaceimport.SourceRef(nil), b.Window.Evidence...), b.FloorEvidence...))
}
func (s *HistoricalCaptureSource) ValidateProspectiveAdmission(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, a namespaceimport.ProspectiveOwnerAdmission) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return e
	}
	if a.Kind != "held-cut-roster-v1" {
		return ErrOwnedEvidence
	}
	export, e := s.namespaceExport(ctx, a.CID)
	if e != nil {
		return e
	}
	var member HistoricalFact
	_, found, e := s.materializer.readDerived(ctx, "roster-members", a.CID, sha([]byte(a.OwnerID)), &member)
	if e != nil || !found || member.OwnerID != a.OwnerID {
		return ErrOwnedEvidence
	}
	expected, e := s.canonicalProspective(ctx, i, export, member)
	if e != nil || historicalJSONSHA(expected) != historicalJSONSHA(a) {
		return ErrOwnedEvidence
	}
	refs := appendHistoricalRefs(nil, a.CutEvidence...)
	refs = appendHistoricalRefs(refs, a.HeadEvidence...)
	refs = appendHistoricalRefs(refs, a.MemberEvidence...)
	return s.sourceReferences(ctx, refs)
}
func (s *HistoricalCaptureSource) ValidateOwnerHistorySegment(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, a namespaceimport.OwnerHistorySegment) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return e
	}
	if historyPortNil(s.verifier) {
		return namespaceimport.ErrUntrusted
	}
	sum, e := namespaceimport.OwnerHistorySegmentSHA256(a)
	if e != nil {
		return e
	}
	var expected namespaceimport.OwnerHistorySegment
	_, found, e := s.materializer.readDerived(ctx, "preserved-history-segments", s.materializer.snapshot.Fingerprint(), sum, &expected)
	if e != nil || !found || historicalJSONSHA(expected) != historicalJSONSHA(a) {
		return ErrOwnedEvidence
	}
	if e = s.sourceReferences(ctx, a.Evidence); e != nil {
		return e
	}
	return s.verifier.VerifyOriginalHistorySegment(ctx, h, i, a, s.materializer)
}

// This is the executable private assembly point. Writer remains the real Store
// adapter supplied by its package (no cycle); the SAME real source performs
// capture/read/revalidation/semantic validation. No operator JSON can replace
// any typed source method or construct a page/Permit. Begin acquires the actual
// owned agent BEFORE the deferred raw capture first starts.
func NewOwnedHistoricalImportController(config namespaceimport.Config, writer namespaceimport.Writer, journal namespaceimport.Journal, agent *OwnedAgent, live *namespaceinventory.LiveSource, describe DescribeHistoricalNamespace, options HistoricalCaptureOptions, producer namespaceimport.OriginalHistorySegmentProducer, verifier namespaceimport.OriginalHistorySegmentVerifier) (*namespaceimport.Controller, *HistoricalCaptureSource, error) {
	if config.CaptureAgent != options.CaptureAgent || config.CaptureKeyID != options.CaptureKeyID || !config.CapturePublicKey.Equal(options.CapturePublicKey) || config.PageBytes != options.PageBytes || config.PageRows != options.PageRows {
		return nil, nil, ErrOwnedEvidence
	}
	source, e := NewHistoricalCaptureSource(agent, config.Header, live, describe, options, producer, verifier)
	if e != nil {
		return nil, nil, e
	}
	controller, e := namespaceimport.NewController(config, writer, source, source, agent, journal)
	if e != nil {
		return nil, nil, e
	}
	return controller, source, nil
}

var _ namespaceimport.OriginValidator = (*HistoricalCaptureSource)(nil)
var _ namespaceimport.CoverageValidator = (*HistoricalCaptureSource)(nil)
var _ namespaceimport.ProspectiveValidator = (*HistoricalCaptureSource)(nil)
var _ namespaceimport.OwnerHistorySegmentValidator = (*HistoricalCaptureSource)(nil)
