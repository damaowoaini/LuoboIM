//go:build linux

package ownedcontrol

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// A real source policy derives COMPLETE original sets and final effective
// boundaries from the sealed snapshot's raw-bound index. No external JSON/SHA,
// current group roster, arbitrary signature, maximum or absence is a substitute.
type HistoricalCoveragePolicy interface {
	HistoricalSourcePolicy
	VerifyCoverage(context.Context, *namespaceinventory.ImportSnapshot, namespaceimport.OriginRecord, namespaceimport.OriginCoverage, []namespaceinventory.ImportRawRow) error
	VerifyEffectiveBoundary(context.Context, *namespaceinventory.ImportSnapshot, namespaceimport.EffectiveOwnerBoundary, namespaceimport.OriginCoverage, []namespaceinventory.ImportRawRow) error
}

func (s *SnapshotValidator) ValidateCoverage(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, origin namespaceimport.OriginRecord, coverage namespaceimport.OriginCoverage) error {
	if e := s.Revalidate(ctx, h, i); e != nil {
		return e
	}
	if e := coverage.ValidateBinding(h, i, origin.Origin); e != nil {
		return e
	}
	policy, ok := s.policy.(HistoricalCoveragePolicy)
	if !ok {
		return namespaceimport.ErrCoverageIncomplete
	}
	rows, e := s.witnesses(ctx, coverage.Evidence)
	if e != nil {
		return e
	}
	return policy.VerifyCoverage(ctx, s.snapshot, origin, coverage, rows)
}
func (s *SnapshotValidator) ValidateEffectiveBoundary(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, boundary namespaceimport.EffectiveOwnerBoundary, coverage namespaceimport.OriginCoverage) error {
	if e := s.Revalidate(ctx, h, i); e != nil {
		return e
	}
	policy, ok := s.policy.(HistoricalCoveragePolicy)
	if !ok {
		return namespaceimport.ErrCoverageIncomplete
	}
	if _, e := namespaceimport.EffectiveBoundarySHA256(boundary); e != nil {
		return e
	}
	refs := append(append([]namespaceimport.SourceRef(nil), boundary.Window.Evidence...), boundary.FloorEvidence...)
	rows, e := s.witnesses(ctx, uniqueRefs(refs))
	if e != nil {
		return e
	}
	return policy.VerifyEffectiveBoundary(ctx, s.snapshot, boundary, coverage, rows)
}
