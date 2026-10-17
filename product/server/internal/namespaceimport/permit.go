package namespaceimport

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"
)

// Private fields prevent deserialized inventory/JSON/network callers from
// minting an import capability. The permit covers exactly ONE page or manifest.
// It is invalidated when Writer returns; a Mongo retry must recheck it locally.
type Permit struct {
	headerSHA256    string
	inventorySHA256 string
	valueSHA256     string
	fenceSHA256     string
	fence           FenceObservation
	leaseID         string
	attemptID       string
	expires         time.Time
	active          atomic.Bool
}
type VerifiedPage struct {
	data            PageData
	sha256          string
	inventorySHA256 string
	inventory       Inventory
}
type VerifiedManifest struct {
	data      ManifestData
	sha256    string
	inventory Inventory
}

func (v VerifiedPage) Inventory() Inventory     { return clone(v.inventory) }
func (v VerifiedManifest) Inventory() Inventory { return clone(v.inventory) }
func (p *Permit) FenceObservation() FenceObservation {
	if p == nil {
		return FenceObservation{}
	}
	return clone(p.fence)
}
func (v VerifiedPage) Data() PageData                           { return clone(v.data) }
func (v VerifiedPage) SHA256() string                           { return v.sha256 }
func (v VerifiedPage) OriginCoverages() []OriginCoverageWitness { return clone(v.data.OriginCoverages) }
func (v VerifiedPage) EffectiveBoundaries() []EffectiveBoundaryWitness {
	return clone(v.data.EffectiveBoundaries)
}
func (v VerifiedPage) InventorySHA256() string { return v.inventorySHA256 }
func (v VerifiedManifest) Data() ManifestData  { return v.data }
func (v VerifiedManifest) SHA256() string      { return v.sha256 }
func (p *Permit) FenceSHA256() string {
	if p == nil {
		return ""
	}
	return p.fenceSHA256
}
func (p *Permit) LeaseID() string {
	if p == nil {
		return ""
	}
	return p.leaseID
}
func (p *Permit) AttemptID() string {
	if p == nil {
		return ""
	}
	return p.attemptID
}
func (p *Permit) validate(h Header, inventory, value string) error {
	if p == nil || !p.active.Load() || !time.Now().Before(p.expires) || h.SHA256() != p.headerSHA256 || inventory != p.inventorySHA256 || value != p.valueSHA256 || p.fence.SHA256() != p.fenceSHA256 || p.fence.HeaderSHA256 != p.headerSHA256 || p.fence.LeaseID != p.leaseID || !time.Now().Before(p.fence.ValidUntil) {
		return ErrUntrusted
	}
	return nil
}

// These methods perform no I/O. Store calls them INSIDE its actual strong Mongo
// transaction, for every retry, alongside same-client/session/target validation.
func (p *Permit) ValidatePage(h Header, v VerifiedPage) error {
	if v.sha256 == "" || digest(v.data) != v.sha256 || v.inventory.SHA256() != v.inventorySHA256 || v.inventory.HeaderSHA256 != h.SHA256() || v.inventory.BindingSHA256() != v.data.InventoryBindingSHA256 || v.inventory.RawRootSHA256 != v.data.RawSourceRootSHA256 {
		return ErrInvalid
	}
	return p.validate(h, v.inventorySHA256, v.sha256)
}
func (p *Permit) ValidateManifest(h Header, v VerifiedManifest) error {
	if v.sha256 == "" || digest(v.data) != v.sha256 || v.inventory.SHA256() != v.data.InventorySHA256 || v.inventory.HeaderSHA256 != h.SHA256() || v.inventory.CoverageSetRootSHA256 != v.data.CoverageSetRootSHA256 || v.inventory.CoverageCount != v.data.CoverageCount || v.inventory.Counts != v.data.Counts || v.inventory.ProspectiveCount != v.data.ProspectiveCount || v.inventory.ProspectiveSetRootSHA256 != v.data.ProspectiveSetRootSHA256 || v.inventory.OwnerHistorySegmentCount != v.data.OwnerHistorySegmentCount || v.inventory.OwnerHistorySegmentSetRootSHA256 != v.data.OwnerHistorySegmentSetRootSHA256 {
		return ErrInvalid
	}
	return p.validate(h, v.data.InventorySHA256, v.sha256)
}
func newPermit(c Config, f FenceObservation, inventory, value string) *Permit {
	expires := time.Now().Add(c.PermitLifetime)
	if f.ValidUntil.Before(expires) {
		expires = f.ValidUntil
	}
	p := &Permit{headerSHA256: c.Header.SHA256(), inventorySHA256: inventory, valueSHA256: value, fenceSHA256: f.SHA256(), fence: clone(f), leaseID: f.LeaseID, expires: expires}
	p.active.Store(true)
	return p
}
func (c *Controller) verifyPage(ctx context.Context, state State, captured CapturedPage) (VerifiedPage, error) {
	p := clone(captured.Data)
	b, err := json.Marshal(p)
	if err != nil || len(b) > c.config.PageBytes || len(p.Origins)+len(p.OwnerWindows)+len(p.Quarantines)+len(p.CoverageClosures)+len(p.ProspectiveAdmissions)+len(p.OwnerHistorySegments) > c.config.PageRows || p.HeaderSHA256 != c.config.Header.SHA256() || p.InventoryBindingSHA256 != state.Inventory.BindingSHA256() || p.RawSourceRootSHA256 != state.Inventory.RawRootSHA256 || p.Index != state.NextPage || p.PreviousSHA256 != state.LastPageSHA256 || p.Index >= state.Inventory.PageCount {
		return VerifiedPage{}, ErrInvalid
	}
	if err = verifyMembership(digest(p), captured.Proof, state.Inventory.PagesRootSHA256); err != nil {
		return VerifiedPage{}, err
	}
	if len(p.CoverageClosures) > 1 || len(p.CoverageClosures) == 1 && (len(p.Origins) != 0 || len(p.OwnerWindows) != 0 || len(p.Quarantines) != 0 || len(p.OriginCoverages) != 0 || len(p.EffectiveBoundaries) != 0 || len(p.ProspectiveAdmissions) != 0 || len(p.OwnerHistorySegments) != 0) {
		return VerifiedPage{}, ErrInvalid
	}
	for _, closure := range p.CoverageClosures {
		if closure.Kind != "owners" && closure.Kind != "boundaries" && closure.Kind != "coverage" && closure.Kind != "prospective" && closure.Kind != "history-segments" || (closure.Kind == "coverage" || closure.Kind == "prospective" || closure.Kind == "history-segments") && closure.CID != "" || closure.Kind != "coverage" && closure.Kind != "prospective" && closure.Kind != "history-segments" && !identity(closure.CID) || closure.PreviousCursor != "" && !identity(closure.PreviousCursor) {
			return VerifiedPage{}, ErrInvalid
		}
	}
	validator, ok := c.origins.(CoverageValidator)
	if !ok || len(p.OriginCoverages) != len(p.Origins) || len(p.EffectiveBoundaries) != len(p.OwnerWindows) {
		return VerifiedPage{}, ErrCoverageIncomplete
	}
	coverageByCID := map[string]OriginCoverageWitness{}
	for _, witness := range p.OriginCoverages {
		if witness.verify(c.config.Header, state.Inventory) != nil || coverageByCID[witness.Coverage.CID].Coverage.CID != "" {
			return VerifiedPage{}, ErrConflict
		}
		coverageByCID[witness.Coverage.CID] = witness
	}
	seen := map[string]bool{}
	for _, r := range p.Origins {
		key := "origin:" + r.Origin.CID
		if seen[key] || !originValid(r.Origin) || !originHeadsValid(c.config.Header, state.Inventory, r) || !validSHA(r.SeedEvidenceSHA256) || !refsValid(r.Evidence) {
			return VerifiedPage{}, ErrInvalid
		}
		seen[key] = true
		if r.MongoHead.State == LegacyCounterNotAllocatedHere {
			expected, e := SourceSeedEvidenceSHA256(c.config.Header, state.Inventory, r)
			if e != nil || expected != r.SeedEvidenceSHA256 {
				return VerifiedPage{}, ErrConflict
			}
			validator, ok := c.origins.(LegacyHeadValidator)
			if !ok {
				return VerifiedPage{}, ErrUntrusted
			}
			if err = validator.ValidateLegacyHeadAbsence(ctx, c.config.Header, state.Inventory, clone(r)); err != nil {
				return VerifiedPage{}, err
			}
		}
		if err = c.origins.ValidateOrigin(ctx, c.config.Header, state.Inventory, clone(r)); err != nil {
			return VerifiedPage{}, err
		}
		witness, ok := coverageByCID[r.Origin.CID]
		if !ok || witness.Coverage.ValidateBinding(c.config.Header, state.Inventory, r.Origin) != nil {
			return VerifiedPage{}, ErrCoverageIncomplete
		}
		if err = validator.ValidateCoverage(ctx, c.config.Header, state.Inventory, clone(r), clone(witness.Coverage)); err != nil {
			return VerifiedPage{}, err
		}
	}
	for at, w := range p.OwnerWindows {
		key := "window:" + digest([]string{w.CID, w.OwnerID})
		if seen[key] || !identity(w.CID) || !identity(w.OwnerID) || !validSHA(w.OriginSHA256) || w.Lower < 0 || w.Lower >= 9223372036854775807 || w.Upper < 0 || w.Upper >= 9223372036854775807 || !w.Closed && w.Upper != 0 || !refsValid(w.Evidence) {
			return VerifiedPage{}, ErrInvalid
		}
		seen[key] = true
		if err = c.origins.ValidateWindow(ctx, c.config.Header, state.Inventory, clone(w)); err != nil {
			return VerifiedPage{}, err
		}
		boundary := p.EffectiveBoundaries[at]
		if digest(boundary.Boundary.Window) != digest(w) || boundary.verify(c.config.Header, state.Inventory) != nil {
			return VerifiedPage{}, ErrCoverageIncomplete
		}
		if err = validator.ValidateEffectiveBoundary(ctx, c.config.Header, state.Inventory, clone(boundary.Boundary), clone(boundary.Coverage.Coverage)); err != nil {
			return VerifiedPage{}, err
		}
	}
	for _, witness := range p.OwnerHistorySegments {
		if e := witness.ValidateBinding(c.config.Header, state.Inventory); e != nil {
			return VerifiedPage{}, e
		}
		sum, e := OwnerHistorySegmentSHA256(witness.Segment)
		if e != nil {
			return VerifiedPage{}, e
		}
		key := "history-segment:" + sum
		if seen[key] {
			return VerifiedPage{}, ErrConflict
		}
		seen[key] = true
		validator, ok := c.origins.(OwnerHistorySegmentValidator)
		if !ok {
			return VerifiedPage{}, ErrCoverageIncomplete
		}
		if e = validator.ValidateOwnerHistorySegment(ctx, c.config.Header, state.Inventory, clone(witness.Segment)); e != nil {
			return VerifiedPage{}, e
		}
	}
	for _, witness := range p.ProspectiveAdmissions {
		if e := witness.verify(c.config.Header, state.Inventory); e != nil {
			return VerifiedPage{}, e
		}
		admission := witness.Admission
		k := "prospective:" + EffectiveOwnerKey(admission.CID, admission.OwnerID)
		if seen[k] {
			return VerifiedPage{}, ErrConflict
		}
		seen[k] = true
		validator, ok := c.origins.(ProspectiveValidator)
		if !ok {
			return VerifiedPage{}, ErrCoverageIncomplete
		}
		if e := validator.ValidateProspectiveAdmission(ctx, c.config.Header, state.Inventory, clone(admission)); e != nil {
			return VerifiedPage{}, e
		}
	}
	for _, q := range p.Quarantines {
		key := "origin:" + q.CID
		if seen[key] || !identity(q.CID) || !identity(q.Reason) || !refsValid(q.Evidence) {
			return VerifiedPage{}, ErrInvalid
		}
		seen[key] = true
		if err = c.origins.ValidateQuarantine(ctx, c.config.Header, state.Inventory, clone(q)); err != nil {
			return VerifiedPage{}, err
		}
	}
	return VerifiedPage{data: p, sha256: digest(p), inventorySHA256: state.Inventory.SHA256(), inventory: clone(state.Inventory)}, nil
}
