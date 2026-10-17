//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/ed25519"
	"path/filepath"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// This is the SAME actual capture/page source returned by the real controlled
// import assembly. JSON inventory fields cannot mint it or substitute another
// cut. The caller may use these paths only to forbid new derived/runtime writes
// inside original raw/derived custody, not as a read/write permission.
func (s *HistoricalCaptureSource) ReadinessSourceCustody(ctx context.Context, hold *AdmissionHold, h namespaceimport.Header, i namespaceimport.Inventory, c namespaceimport.Config) ([]string, error) {
	if s == nil || s.agent == nil || s.agent.hold == nil || ctx == nil {
		return nil, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if hold == nil || hold != s.agent.hold || c.Header != h || c.CaptureAgent != s.options.CaptureAgent || c.CaptureKeyID != s.options.CaptureKeyID || len(c.CapturePublicKey) != ed25519.PublicKeySize || !c.CapturePublicKey.Equal(s.options.CapturePublicKey) || c.PageRows != s.options.PageRows || c.PageBytes != s.options.PageBytes {
		return nil, ErrOwnedEvidence
	}
	if e := s.revalidate(ctx, h, i); e != nil {
		return nil, e
	}
	result := []string{s.options.RawDirectory, s.options.DerivedDirectory}
	for _, p := range result {
		at, e := filepath.EvalSymlinks(p)
		if e != nil || at != p {
			return nil, ErrOwnedEvidence
		}
	}
	return result, nil
}
