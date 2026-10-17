//go:build linux

package ownedcontrol

import (
	"context"
	"reflect"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// Embedding preserves the actual existing coverage/prospective/legacy source
// validators; it does not mask them behind a reduced interface. The concrete MD
// verifier is explicitly injected by the private operator, never resolved from
// an event or the current business DB. Old sources without segments use the
// unchanged SnapshotValidator. A source with segments and no verifier rejects.
type HistoricalSegmentSnapshotValidator struct {
	*SnapshotValidator
	original *HistoricalMaterializer
	verifier namespaceimport.OriginalHistorySegmentVerifier
}

func historyPortNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return r.IsNil()
	}
	return false
}
func WithOriginalHistorySegments(base *SnapshotValidator, original *HistoricalMaterializer, verifier namespaceimport.OriginalHistorySegmentVerifier) (*HistoricalSegmentSnapshotValidator, error) {
	if base == nil || original == nil || original.hold == nil || original.workspace == nil || original.snapshot == nil || historyPortNil(verifier) || base.hold != original.hold || base.snapshot != original.snapshot || base.header.SHA256() != original.hold.HeaderSHA256() {
		return nil, ErrOwnedEvidence
	}
	return &HistoricalSegmentSnapshotValidator{base, original, verifier}, nil
}
func (s *HistoricalSegmentSnapshotValidator) ValidateOwnerHistorySegment(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, value namespaceimport.OwnerHistorySegment) error {
	if s == nil || s.SnapshotValidator == nil || s.original == nil || historyPortNil(s.verifier) {
		return namespaceimport.ErrUntrusted
	}
	if e := s.Revalidate(ctx, h, i); e != nil {
		return e
	}
	if s.hold.ValidateExclusion() != nil || value.SourceEpoch == h.Epoch {
		return ErrOwnedEvidence
	}
	if _, e := namespaceimport.OwnerHistorySegmentSHA256(value); e != nil {
		return e
	}
	if value.SourceKind == "member-notice-entitlement-v1" && value.Lower != value.Upper {
		return namespaceimport.ErrInvalid
	}
	if _, e := s.witnesses(ctx, value.Evidence); e != nil {
		return e
	}
	if e := s.original.workspace.CheckOriginalHistoryIndex(ctx); e != nil {
		return e
	}
	if e := s.verifier.VerifyOriginalHistorySegment(ctx, h, i, value, s.original); e != nil {
		return e
	}
	return s.hold.ValidateExclusion()
}
