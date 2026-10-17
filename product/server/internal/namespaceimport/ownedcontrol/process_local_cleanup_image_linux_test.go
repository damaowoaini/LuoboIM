//go:build linux

package ownedcontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOwnedLocalCleanupCancelledValidationAndOriginalDoneStayLocal(t *testing.T) {
	t.Run("cancelled-validation-keeps-live-child-unsignalled", func(t *testing.T) {
		s, release, count := cleanupSignalChild(t, 7)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		exit, e := s.StopForLocalCleanup(ctx)
		if !errors.Is(e, context.Canceled) || exit.Version != "" || s.stopSignalAttempted || s.joined {
			t.Fatal("cancelled validation performed an OS stop or claimed completion")
		}
		if _, e = os.Stat(count); !os.IsNotExist(e) {
			t.Fatal("cancelled validation signalled the original child")
		}
		if e = os.WriteFile(release, []byte("release"), 0600); e != nil {
			t.Fatal("original child release unavailable")
		}
		wait, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		select {
		case <-s.done:
		case <-wait.Done():
			t.Fatal("original Wait did not finish")
		}
		exit, e = s.StopForLocalCleanup(ctx)
		if !errors.Is(e, ErrOwnedEvidence) || exit.Version != "owned-local-child-exited-v1" || exit.Owner != s.owner || exit.ObservedAbsentAt.IsZero() || exit.WaitSucceeded || exit.WaitErrorSHA256 == "" || s.stopSignalAttempted || s.joined {
			t.Fatal("known original nonzero Wait became canonical or remained unobserved")
		}
		if _, e = os.Stat(count); !os.IsNotExist(e) {
			t.Fatal("completed original child was signalled")
		}
	})
	t.Run("same-original-wait-during-image-validation-is-local-only", func(t *testing.T) {
		s, release, count := cleanupSignalChild(t, 7)
		wait, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		observed := false
		gate := &imageBoundaryContext{Context: wait, ready: func() bool { return true }, action: func() {
			// Stop has already selected the live original handle. This real
			// context boundary finishes that child before its image check.
			if os.WriteFile(release, []byte("release"), 0600) != nil {
				t.Fatal("original child release unavailable")
			}
			select {
			case <-s.done:
				observed = true
			case <-wait.Done():
				t.Fatal("original Wait did not finish during validation")
			}
		}}
		exit, e := s.StopForLocalCleanup(gate)
		if !observed || !errors.Is(e, ErrOwnedEvidence) || exit.Version != "owned-local-child-exited-v1" || exit.Owner != s.owner || exit.ObservedAbsentAt.IsZero() || exit.WaitSucceeded || s.stopSignalAttempted || s.joined {
			t.Fatal("validation exit race lost original Wait or fabricated authority")
		}
		if _, e = processIdentity(s.owner.PID); !os.IsNotExist(e) {
			t.Fatal("local exit did not prove actual original PID absence")
		}
		if _, e = os.Stat(count); !os.IsNotExist(e) {
			t.Fatal("exited original child got another signal")
		}
		if _, e = os.Stat(filepath.Join(filepath.Dir(release), "drain.json")); !os.IsNotExist(e) {
			t.Fatal("local exit fabricated canonical drain")
		}
	})
}
