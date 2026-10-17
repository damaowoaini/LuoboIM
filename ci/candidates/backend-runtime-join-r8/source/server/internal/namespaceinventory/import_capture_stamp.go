package namespaceinventory

import (
	"context"
	"time"
)

// ImportCaptureStamp is original session provenance, not a new observation or
// trust upgrade. Returning it never changes Identity/Fingerprint/RawRoot and
// never renews a capture to accommodate a later control-plane liveness sample.
type ImportCaptureStamp struct {
	CaptureID     string    `json:"capture_id"`
	CapturedAt    time.Time `json:"captured_at"`
	SessionSHA256 string    `json:"session_sha256"`
}

func (s *ImportSnapshot) CaptureStamp(ctx context.Context, live PageSource) (ImportCaptureStamp, error) {
	if e := s.check(ctx, live); e != nil {
		return ImportCaptureStamp{}, e
	}
	if !validUUID(s.session.ID) || s.session.Created.IsZero() {
		return ImportCaptureStamp{}, ErrChanged
	}
	return ImportCaptureStamp{s.session.ID, s.session.Created, s.session.SHA}, nil
}
