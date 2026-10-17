//go:build linux

package ownedcontrol

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// A narrowly selected original metadata observation from the SAME sealed
// target capture. No BSON/body or permission is returned, only its actual
// source locator. The original global cutover remains in the live DB until
// the new signed manifest is genuinely ready to publish in one strong TX.
func (s *HistoricalCaptureSource) ValidateCapturedEpochCutover(ctx context.Context, hold *AdmissionHold, h namespaceimport.Header, i namespaceimport.Inventory, c namespaceimport.Config, expectedRawSHA string) (namespaceimport.SourceRef, error) {
	var empty namespaceimport.SourceRef
	if s == nil || !isSHA(expectedRawSHA) {
		return empty, ErrOwnedEvidence
	}
	if _, e := s.ReadinessSourceCustody(ctx, hold, h, i, c); e != nil {
		return empty, e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, h, i); e != nil {
		return empty, e
	}
	raw, ref, e := s.materializer.ReadOriginalHistoryRecord(ctx, "message_delivery_sequences_v1", "schema:group-namespace-cutover-v1")
	if e != nil || sha(raw) != expectedRawSHA || !isSHA(ref.ObjectSHA256) || !isSHA(ref.RecordSHA256) {
		return empty, ErrOwnedEvidence
	}
	return ref, nil
}
