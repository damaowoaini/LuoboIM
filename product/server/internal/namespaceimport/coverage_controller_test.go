package namespaceimport

import (
	"context"
	"testing"
)

func TestImportCoverageIsSignedAndCannotBeDropped(t *testing.T) {
	f := newFixture(t)
	beginCaptured(t, f)
	original := clone(f.source.page)
	f.source.page.OriginCoverages = nil
	if _, e := f.controller.Step(context.Background()); e == nil || f.writer.calls != 0 {
		t.Fatal("missing signed coverage reached writer")
	}
	f.source.page = original
	if _, e := f.controller.Step(context.Background()); e != nil || f.writer.calls != 1 {
		t.Fatalf("legitimate covered origin rejected: %v", e)
	}
}
func TestImportEffectiveBoundaryRequiresExactOwnerAndSourcePolicy(t *testing.T) {
	f := newFixture(t)
	r := f.source.page.Origins[0]
	f.source.page.OwnerWindows = []OwnerWindow{{CID: r.Origin.CID, OriginSHA256: r.Origin.SHA256, OwnerID: "a", Lower: 10, Upper: 0, Closed: true, Evidence: syntheticRefs()}}
	beginCaptured(t, f)
	original := clone(f.source.page)
	f.source.page.EffectiveBoundaries[0].Boundary.Window.OwnerID = "foreign"
	if _, e := f.controller.Step(context.Background()); e == nil || f.writer.calls != 0 {
		t.Fatal("foreign owner boundary reached writer")
	}
	f.source.page = original
	if _, e := f.controller.Step(context.Background()); e != nil {
		t.Fatalf("closed empty boundary rejected: %v", e)
	}
	manifest := manifestFor(f.journal.state)
	if manifest.CoverageSetRootSHA256 != f.source.inventory.CoverageSetRootSHA256 || manifest.CoverageCount != 1 {
		t.Fatal("final manifest lost signed source coverage")
	}
}
func TestImportRefusesLegacyValidatorWithoutCoverageAuthority(t *testing.T) {
	f := newFixture(t)
	legacy := onlyLegacyOrigins{f.origins}
	if _, e := NewController(f.config, f.writer, f.source, legacy, f.control, f.journal); e != ErrCoverageIncomplete {
		t.Fatal("legacy validator silently minted complete coverage")
	}
}

type onlyLegacyOrigins struct{ base *fixtureOrigins }

func (o onlyLegacyOrigins) ValidateOrigin(c context.Context, h Header, i Inventory, v OriginRecord) error {
	return o.base.ValidateOrigin(c, h, i, v)
}
func (o onlyLegacyOrigins) ValidateWindow(c context.Context, h Header, i Inventory, v OwnerWindow) error {
	return o.base.ValidateWindow(c, h, i, v)
}
func (o onlyLegacyOrigins) ValidateQuarantine(c context.Context, h Header, i Inventory, v Quarantine) error {
	return o.base.ValidateQuarantine(c, h, i, v)
}
