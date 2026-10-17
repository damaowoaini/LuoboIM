package namespaceimport

import (
	"context"
	"errors"
	"testing"
)

type prospectiveFixtureValidator struct {
	*fixtureOrigins
	received []ProspectiveOwnerAdmission
	reject   bool
}

func (p *prospectiveFixtureValidator) ValidateProspectiveAdmission(_ context.Context, _ Header, _ Inventory, a ProspectiveOwnerAdmission) error {
	if p.reject {
		return ErrUntrusted
	}
	p.received = append(p.received, a)
	return nil
}
func prospectivePageFixture(t *testing.T) (controllerFixture, State, CapturedPage, *prospectiveFixtureValidator) {
	t.Helper()
	f := newFixture(t)
	v := &prospectiveFixtureValidator{fixtureOrigins: f.origins}
	f.controller.origins = v
	i := Inventory{HeaderSHA256: f.config.Header.SHA256(), LeaseID: "synthetic-held-cut", RawRootSHA256: digest("raw"), MongoRootSHA256: digest("mongo"), RedisRootSHA256: digest("redis"), HeldAdmissionGuardSHA256: digest("actual-guard"), ProspectiveCount: 1, PageCount: 1}
	a := ProspectiveOwnerAdmission{Kind: "held-cut-roster-v1", CID: "sg_g", OwnerID: "owner", OriginSHA256: digest("origin"), SeedMaximum: 10, Lower: 11, HeaderSHA256: i.HeaderSHA256, InventoryBindingSHA256: i.BindingSHA256(), CutSHA256: HeldProspectiveCutSHA256(f.config.Header, i), CutEvidence: syntheticRefs(), HeadEvidence: syntheticRefs(), MemberEvidence: syntheticRefs(), RosterSHA256: digest("roster"), RosterMembers: 1}
	sum, e := ProspectiveAdmissionSHA256(a)
	if e != nil {
		t.Fatal(e)
	}
	i.ProspectiveSetRootSHA256 = fixtureSetRoot([]string{sum})
	p := PageData{HeaderSHA256: i.HeaderSHA256, InventoryBindingSHA256: i.BindingSHA256(), RawSourceRootSHA256: i.RawRootSHA256, ProspectiveAdmissions: []ProspectiveOwnerWitness{{Admission: a}}}
	i.PagesRootSHA256 = digest([]string{"namespace-import-page-leaf-v1", digest(p)})
	return f, State{Header: f.config.Header, Inventory: i}, CapturedPage{Data: p}, v
}
func TestProspectivePageNeedsOriginalRootAndActualSemanticValidation(t *testing.T) {
	t.Run("held bound private page and deep copy", func(t *testing.T) {
		f, state, p, semantic := prospectivePageFixture(t)
		v, e := f.controller.verifyPage(context.Background(), state, p)
		if e != nil || len(semantic.received) != 1 {
			t.Fatal("actual verifier not invoked", e)
		}
		copy := v.ProspectiveAdmissions()
		copy[0].Admission.Lower = 1
		if v.ProspectiveAdmissions()[0].Admission.Lower != 11 {
			t.Fatal("copy mutated private page")
		}
		var bare VerifiedPage
		if (&Permit{}).ValidatePage(f.config.Header, bare) == nil {
			t.Fatal("JSON-mintable write permit")
		}
	})
	t.Run("root drift", func(t *testing.T) {
		f, state, p, _ := prospectivePageFixture(t)
		state.Inventory.ProspectiveSetRootSHA256 = digest("wrong")
		if _, e := f.controller.verifyPage(context.Background(), state, p); e == nil {
			t.Fatal("wrong signed set root")
		}
	})
	t.Run("semantic evidence rejection", func(t *testing.T) {
		f, state, p, v := prospectivePageFixture(t)
		v.reject = true
		if _, e := f.controller.verifyPage(context.Background(), state, p); !errors.Is(e, ErrUntrusted) {
			t.Fatal("failed semantics granted page", e)
		}
	})
	t.Run("missing source policy", func(t *testing.T) {
		f, state, p, _ := prospectivePageFixture(t)
		f.controller.origins = f.origins
		if _, e := f.controller.verifyPage(context.Background(), state, p); !errors.Is(e, ErrCoverageIncomplete) {
			t.Fatal("bare shape granted page", e)
		}
	})
	t.Run("foreign held cut", func(t *testing.T) {
		f, state, p, _ := prospectivePageFixture(t)
		state.Inventory.HeldAdmissionGuardSHA256 = digest("otherguard")
		if p.Data.ProspectiveAdmissions[0].ValidateBinding(f.config.Header, state.Inventory) == nil {
			t.Fatal("foreign guard renewed cut")
		}
	})
	t.Run("actual source cannot be imported as held", func(t *testing.T) {
		f, state, p, _ := prospectivePageFixture(t)
		p.Data.ProspectiveAdmissions[0].Admission.Kind = "actual-positive-source-v1"
		if p.Data.ProspectiveAdmissions[0].ValidateBinding(f.config.Header, state.Inventory) == nil {
			t.Fatal("runtime-only source accepted as roster admission")
		}
	})
	t.Run("prospective root excluded only from binding", func(t *testing.T) {
		_, state, _, _ := prospectivePageFixture(t)
		i := state.Inventory
		old := i.BindingSHA256()
		whole := i.SHA256()
		i.ProspectiveSetRootSHA256 = digest("other")
		if old != i.BindingSHA256() || whole == i.SHA256() {
			t.Fatal("root circularity or unsigned root")
		}
	})
	t.Run("negative and max head never wrap", func(t *testing.T) {
		_, _, p, _ := prospectivePageFixture(t)
		a := p.Data.ProspectiveAdmissions[0].Admission
		for _, head := range []int64{-1, 9223372036854775806, 9223372036854775807} {
			a.SeedMaximum = head
			a.Lower = head + 1
			if a.ValidateShape() == nil {
				t.Fatal("invalid source head admitted")
			}
		}
	})
}
