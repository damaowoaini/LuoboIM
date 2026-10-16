package groupeffects

import (
	"testing"
	"time"
)

func TestRoleCallbackTimeoutPreservesLegacyClientCap(t *testing.T) {
	for _, fixture := range []struct {
		configured int
		effective  time.Duration
	}{{0, 15 * time.Second}, {-1, 15 * time.Second}, {2, 2 * time.Second}, {30, 15 * time.Second}} {
		if got := roleCallbackTimeout(fixture.configured); got != fixture.effective {
			t.Fatalf("configured=%d effective=%s", fixture.configured, got)
		}
	}
}
