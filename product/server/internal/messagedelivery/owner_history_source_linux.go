//go:build linux

package messagedelivery

import (
	"context"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// The private operator calls this once with its exact reviewed trust file and
// ACTUAL held materializer/validator. The same concrete verifier is used for
// discovery and for Controller semantic validation; no bare DTO writer port.
type ReviewedOwnerHistorySource struct {
	validator *ownedcontrol.HistoricalSegmentSnapshotValidator
	original  *ownedcontrol.HistoricalMaterializer
	verifier  *OriginalHistoryVerifier
	header    namespaceimport.Header
}

func NewReviewedOwnerHistorySource(base *ownedcontrol.SnapshotValidator, original *ownedcontrol.HistoricalMaterializer, h namespaceimport.Header, trustFile, trustSHA string) (*ReviewedOwnerHistorySource, error) {
	verifier, e := LoadOriginalHistoryVerifier(trustFile, trustSHA, h)
	if e != nil {
		return nil, e
	}
	validator, e := ownedcontrol.WithOriginalHistorySegments(base, original, verifier)
	if e != nil {
		return nil, e
	}
	return &ReviewedOwnerHistorySource{validator, original, verifier, h}, nil
}
func (s *ReviewedOwnerHistorySource) Validator() *ownedcontrol.HistoricalSegmentSnapshotValidator {
	if s == nil {
		return nil
	}
	return s.validator
}
func (s *ReviewedOwnerHistorySource) Producer() namespaceimport.OriginalHistorySegmentProducer {
	if s == nil {
		return nil
	}
	return s.verifier
}
func (s *ReviewedOwnerHistorySource) Verifier() namespaceimport.OriginalHistorySegmentVerifier {
	if s == nil {
		return nil
	}
	return s.verifier
}
func (s *ReviewedOwnerHistorySource) Step(ctx context.Context) (namespaceinventory.DerivedCheckpoint, error) {
	if s == nil || s.original == nil || s.verifier == nil {
		return namespaceinventory.DerivedCheckpoint{}, ErrUnprepared
	}
	if e := s.verifier.checkConfig(s.header); e != nil {
		return namespaceinventory.DerivedCheckpoint{}, e
	}
	return s.original.StepOriginalHistorySegments(ctx, s.header, s.verifier, s.verifier)
}
