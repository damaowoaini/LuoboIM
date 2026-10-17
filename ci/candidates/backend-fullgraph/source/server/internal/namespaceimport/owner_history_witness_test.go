package namespaceimport

import (
	"context"
	"errors"
	"testing"
)

type historySegmentFixtureValidator struct {
	*fixtureOrigins
	received []OwnerHistorySegment
	reject   bool
}

func (v *historySegmentFixtureValidator) ValidateOwnerHistorySegment(_ context.Context, _ Header, _ Inventory, s OwnerHistorySegment) error {
	if v.reject {
		return ErrUntrusted
	}
	v.received = append(v.received, s)
	return nil
}
func historySegmentPageFixture(t *testing.T) (controllerFixture, State, CapturedPage, *historySegmentFixtureValidator) {
	t.Helper()
	f := newFixture(t)
	v := &historySegmentFixtureValidator{fixtureOrigins: f.origins}
	f.controller.origins = v
	i := Inventory{HeaderSHA256: f.config.Header.SHA256(), LeaseID: "synthetic-held-cut", RawRootSHA256: digest("original-raw"), HeldAdmissionGuardSHA256: digest("held-guard"), OwnerHistorySegmentCount: 1, PageCount: 1}
	segment := OwnerHistorySegment{Kind: "preserved-owner-history-v1", CID: "sg_group", OwnerID: "owner", OriginSHA256: digest("origin"), Lower: 11, Upper: 14, SourceEpoch: "12345678-1234-1234-1234-123456789002", SourceKind: "namespace-owner-prospective-v1", SourceID: "original-source-row", Evidence: syntheticRefs()}
	if segment.SourceEpoch == f.config.Header.Epoch {
		segment.SourceEpoch = "12345678-1234-1234-1234-123456789003"
	}
	sum, e := OwnerHistorySegmentSHA256(segment)
	if e != nil {
		t.Fatal(e)
	}
	i.OwnerHistorySegmentSetRootSHA256 = fixtureSetRoot([]string{sum})
	p := PageData{HeaderSHA256: i.HeaderSHA256, InventoryBindingSHA256: i.BindingSHA256(), RawSourceRootSHA256: i.RawRootSHA256, OwnerHistorySegments: []OwnerHistorySegmentWitness{{Segment: segment, HeaderSHA256: i.HeaderSHA256, InventoryBindingSHA256: i.BindingSHA256()}}}
	i.PagesRootSHA256 = digest([]string{"namespace-import-page-leaf-v1", digest(p)})
	return f, State{Header: f.config.Header, Inventory: i}, CapturedPage{Data: p}, v
}
func TestOwnerHistorySegmentPageRetainsOnlyOriginalPrivateProofIntervals(t *testing.T) {
	t.Run("actual source validator and private deep copy", func(t *testing.T) {
		f, st, p, source := historySegmentPageFixture(t)
		v, e := f.controller.verifyPage(context.Background(), st, p)
		if e != nil || len(source.received) != 1 {
			t.Fatal("source validation not used", e)
		}
		copy := v.OwnerHistorySegments()
		copy[0].Segment.Lower = 1
		if v.OwnerHistorySegments()[0].Segment.Lower != 11 {
			t.Fatal("private row changed")
		}
		var bare VerifiedPage
		if (&Permit{}).ValidatePage(f.config.Header, bare) == nil {
			t.Fatal("bare JSON minted page authority")
		}
	})
	t.Run("actual source rejection", func(t *testing.T) {
		f, st, p, source := historySegmentPageFixture(t)
		source.reject = true
		if _, e := f.controller.verifyPage(context.Background(), st, p); !errors.Is(e, ErrUntrusted) {
			t.Fatal(e)
		}
	})
	t.Run("missing semantic source port", func(t *testing.T) {
		f, st, p, _ := historySegmentPageFixture(t)
		f.controller.origins = f.origins
		if _, e := f.controller.verifyPage(context.Background(), st, p); !errors.Is(e, ErrCoverageIncomplete) {
			t.Fatal(e)
		}
	})
	t.Run("target epoch is not retained original history", func(t *testing.T) {
		f, st, p, _ := historySegmentPageFixture(t)
		p.Data.OwnerHistorySegments[0].Segment.SourceEpoch = f.config.Header.Epoch
		if p.Data.OwnerHistorySegments[0].ValidateBinding(f.config.Header, st.Inventory) == nil {
			t.Fatal("fresh target epoch renewed past")
		}
	})
	t.Run("foreign header and binding rejected", func(t *testing.T) {
		f, st, p, _ := historySegmentPageFixture(t)
		w := p.Data.OwnerHistorySegments[0]
		w.HeaderSHA256 = digest("foreign")
		if w.ValidateBinding(f.config.Header, st.Inventory) == nil {
			t.Fatal("foreign header")
		}
		w = p.Data.OwnerHistorySegments[0]
		w.InventoryBindingSHA256 = digest("foreign")
		if w.ValidateBinding(f.config.Header, st.Inventory) == nil {
			t.Fatal("foreign inventory")
		}
	})
	t.Run("signed original interval root rejected when changed", func(t *testing.T) {
		f, st, p, _ := historySegmentPageFixture(t)
		st.Inventory.OwnerHistorySegmentSetRootSHA256 = digest("changed")
		if p.Data.OwnerHistorySegments[0].ValidateBinding(f.config.Header, st.Inventory) == nil {
			t.Fatal("unbound root")
		}
	})
	t.Run("removed self notice entitlement stays one point", func(t *testing.T) {
		f, st, p, _ := historySegmentPageFixture(t)
		w := p.Data.OwnerHistorySegments[0]
		w.Segment.SourceKind = "member-notice-entitlement-v1"
		if w.ValidateBinding(f.config.Header, st.Inventory) == nil {
			t.Fatal("point entitlement widened to interval")
		}
	})
	t.Run("root excluded only from binding not complete signed identity", func(t *testing.T) {
		_, st, _, _ := historySegmentPageFixture(t)
		before, binding := st.Inventory.SHA256(), st.Inventory.BindingSHA256()
		st.Inventory.OwnerHistorySegmentSetRootSHA256 = digest("another-root")
		if before == st.Inventory.SHA256() || binding != st.Inventory.BindingSHA256() {
			t.Fatal("unsigned root or circular binding")
		}
	})
}
