package namespaceimport

import (
	"context"
	"testing"
)

func TestVerifiedImportExposesDeepCopiesOfOriginalSignedCustody(t *testing.T) {
	f := newFixture(t)
	beginCaptured(t, f)
	state := f.journal.state
	captured, e := f.source.ReadPage(context.Background(), state.Inventory, 0)
	if e != nil {
		t.Fatal(e)
	}
	page, e := f.controller.verifyPage(context.Background(), state, captured)
	if e != nil {
		t.Fatal(e)
	}
	inventory := page.Inventory()
	if inventory.SHA256() != state.Inventory.SHA256() || inventory.RawRootSHA256 != state.Inventory.RawRootSHA256 {
		t.Fatal("signed inventory/root lost")
	}
	inventory.Signature[0] ^= 1
	if page.Inventory().Signature[0] == inventory.Signature[0] {
		t.Fatal("inventory signature aliases caller")
	}
	permit := newPermit(f.config, state.Fence, state.Inventory.SHA256(), page.SHA256())
	defer permit.active.Store(false)
	if e = permit.ValidatePage(f.config.Header, page); e != nil {
		t.Fatal(e)
	}
	fence := permit.FenceObservation()
	fence.Joined[0].JoinRecord = "foreign"
	fence.Signature[0] ^= 1
	if permit.FenceObservation().Joined[0].JoinRecord == "foreign" || permit.FenceObservation().SHA256() != state.Fence.SHA256() {
		t.Fatal("original live fence evidence aliases caller")
	}
	forged := page
	forged.inventory.RawRootSHA256 = digest("foreign")
	if permit.ValidatePage(f.config.Header, forged) == nil {
		t.Fatal("mismatched roots admitted")
	}
	manifest := manifestFor(State{Header: state.Header, Inventory: state.Inventory, NextPage: state.Inventory.PageCount, Cumulative: state.Inventory.Counts, LastPageSHA256: page.SHA256()})
	value := VerifiedManifest{data: manifest, sha256: digest(manifest), inventory: clone(state.Inventory)}
	manifestPermit := newPermit(f.config, state.Fence, state.Inventory.SHA256(), value.SHA256())
	defer manifestPermit.active.Store(false)
	if e = manifestPermit.ValidateManifest(f.config.Header, value); e != nil {
		t.Fatal(e)
	}
	copy := value.Inventory()
	copy.Counts.Windows++
	if value.Inventory().Counts != state.Inventory.Counts {
		t.Fatal("manifest inventory aliases copy")
	}
}
