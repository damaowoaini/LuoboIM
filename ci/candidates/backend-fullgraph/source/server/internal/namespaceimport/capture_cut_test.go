package namespaceimport

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// This source simulates actual persisted incremental capture: first call seals
// one original cut/time and subsequent calls resume it. New liveness samples
// cannot recapture, alter roots, or renew that source session.
type persistentCutFixtureSource struct {
	base          *fixtureSource
	capturedAt    time.Time
	inventory     Inventory
	calls, builds int
}

func (f *persistentCutFixtureSource) Capture(ctx context.Context, h Header, live FenceObservation) (Inventory, error) {
	f.calls++
	if f.inventory.CaptureID == "" {
		i, e := f.base.Capture(ctx, h, live)
		if e != nil {
			return Inventory{}, e
		}
		f.builds++
		i.CapturedAt = f.capturedAt
		f.base.page.InventoryBindingSHA256 = i.BindingSHA256()
		installSyntheticCoverage(&i, &f.base.page)
		i.PagesRootSHA256 = digest([]string{"namespace-import-page-leaf-v1", digest(f.base.page)})
		b, e := json.Marshal(i.signingValue())
		if e != nil {
			return Inventory{}, e
		}
		i.Signature = ed25519.Sign(f.base.key, b)
		f.inventory = clone(i)
		f.base.inventory = clone(i)
	}
	if live.LeaseID != f.inventory.LeaseID || live.HeaderSHA256 != h.SHA256() {
		return Inventory{}, ErrUntrusted
	}
	if f.calls <= 2 {
		return Inventory{}, ErrPending
	}
	return clone(f.inventory), nil
}
func (f *persistentCutFixtureSource) ReadPage(ctx context.Context, i Inventory, n uint64) (CapturedPage, error) {
	return f.base.ReadPage(ctx, i, n)
}
func (f *persistentCutFixtureSource) Revalidate(ctx context.Context, h Header, i Inventory) error {
	return f.base.Revalidate(ctx, h, i)
}
func TestImportPendingCaptureKeepsOriginalCutWhileLiveFenceAdvances(t *testing.T) {
	for _, name := range []string{"pending resumes the original cut", "current held exclusion invalid", "current lease changed", "original source identity drift", "capture predates original cut"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			initial, e := f.controller.Begin(ctx)
			if e != nil {
				t.Fatal(e)
			}
			at := time.Now().UTC().Add(-time.Second)
			if name == "capture predates original cut" {
				at = at.Add(-time.Minute)
			}
			source := &persistentCutFixtureSource{base: f.source, capturedAt: at}
			f.controller.source = source
			f.control.mutate = func(live *FenceObservation) {
				for n := range live.Drained {
					live.Drained[n].CurrentSampleAt = time.Now().UTC().Add(-250 * time.Millisecond)
					live.Drained[n].PreviousSampleAt = live.Drained[n].CurrentSampleAt.Add(-time.Second)
				}
				if name == "current held exclusion invalid" {
					live.ExclusionMode = "released"
				}
				if name == "current lease changed" {
					live.LeaseID = "another-held-lease"
				}
			}
			if name == "current held exclusion invalid" || name == "current lease changed" {
				if _, e = f.controller.Step(ctx); !errors.Is(e, ErrUntrusted) || source.calls != 0 {
					t.Fatal("bad live fence reached capture", e)
				}
				return
			}
			for n := 0; n < 2; n++ {
				state, e := f.controller.Step(ctx)
				if !errors.Is(e, ErrPending) || state.Phase != "fenced" || state.Revision != initial.Revision {
					t.Fatal("pending capture changed journal/cut", e)
				}
			}
			if source.builds != 1 || !source.inventory.CapturedAt.Equal(at) || source.inventory.LeaseID != initial.Fence.LeaseID {
				t.Fatal("pending source recaptured original cut")
			}
			if name == "original source identity drift" {
				f.source.drift = true
			}
			state, e := f.controller.Step(ctx)
			if name == "original source identity drift" || name == "capture predates original cut" {
				if e == nil || state.Phase != "fenced" || state.Revision != initial.Revision {
					t.Fatal("invalid original source/cut accepted", e)
				}
				return
			}
			if e != nil || state.Phase != "captured" || source.calls != 3 || source.builds != 1 {
				t.Fatal("bounded original capture cannot finish", e)
			}
			if digest(state.CaptureFence) != digest(initial.Fence) || digest(state.Fence) == digest(initial.Fence) || !state.Inventory.CapturedAt.Equal(at) || state.Inventory.RawRootSHA256 != source.inventory.RawRootSHA256 || state.Inventory.BindingSHA256() != source.inventory.BindingSHA256() {
				t.Fatal("capture/live fence or original session replaced")
			}
			if !state.CaptureFence.Drained[0].CurrentSampleAt.Before(state.Inventory.CapturedAt) || !state.Inventory.CapturedAt.Before(state.Fence.Drained[0].CurrentSampleAt) {
				t.Fatal("fixture did not cover advancing current drain sample")
			}
			if e = f.config.verifyInventory(state.Inventory, state.CaptureFence); e != nil {
				t.Fatal(e)
			}
			if e = f.config.verifyInventory(state.Inventory, state.Fence); e == nil {
				t.Fatal("fixture original time unexpectedly satisfies fresh observation")
			}
		})
	}
}
