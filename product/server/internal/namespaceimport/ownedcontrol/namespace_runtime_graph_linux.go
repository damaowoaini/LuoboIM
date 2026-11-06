//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

var ErrNamespaceRuntimeRegistrationPending = errors.New("owned namespace runtime registration pending")

// Invocation-local labels only. These contain no owner identity or authority;
// the original observed graph/error still determines every admission decision.
type NamespaceRuntimeCallObservation struct {
	Role      string
	Predicate string
}

func (o *NamespaceRuntimeCallObservation) enter(role, predicate string) {
	if o == nil {
		return
	}
	o.Role = "none"
	for _, allowed := range NamespaceRuntimeRoles() {
		if role == allowed {
			o.Role = role
			break
		}
	}
	o.Predicate = predicate
}

type NamespaceRuntimeProcess struct {
	Service   *OwnedService
	Bootstrap NamespaceRuntimeBootstrap
}
type namespaceRuntimeExecution struct {
	path, sha string
	bootstrap NamespaceRuntimeBootstrap
}
type NamespaceRuntimeLaunchValidator interface {
	ValidateNamespaceRuntimeLaunch(context.Context, NamespaceRuntimeBootstrap) error
}

// There is deliberately no JSON constructor. The full graph comes only from
// actual retained exec.Cmd handles, reviewed build/source/image identities and
// the original private registration nonce. Merely registered gRPC services,
// resource names, a RuntimeReady bool or a canonical-drain DTO cannot mint it.
type VerifiedNamespaceRuntimeGraph struct {
	mu                                                     sync.Mutex
	hold                                                   *AdmissionHold
	header, preparation, inventory, manifest, requirements string
	processes                                              []NamespaceRuntimeProcess
	rows                                                   []ObservedNamespaceRuntime
	observedAt                                             time.Time
}

func ObserveNamespaceRuntimeGraph(ctx context.Context, hold *AdmissionHold, h namespaceimport.Header, preparation, inventory, manifest string, requirements []NamespaceRuntimeRequirement, processes []NamespaceRuntimeProcess) (*VerifiedNamespaceRuntimeGraph, error) {
	return observeNamespaceRuntimeGraph(ctx, hold, h, preparation, inventory, manifest, requirements, processes, nil)
}

func observeNamespaceRuntimeGraph(ctx context.Context, hold *AdmissionHold, h namespaceimport.Header, preparation, inventory, manifest string, requirements []NamespaceRuntimeRequirement, processes []NamespaceRuntimeProcess, observation *NamespaceRuntimeCallObservation) (*VerifiedNamespaceRuntimeGraph, error) {
	observation.enter("", "graph-preconditions")
	requirementsSHA, e := NamespaceRuntimeRequirementsSHA256(requirements)
	if e != nil || ctx == nil || ctx.Err() != nil || hold == nil || hold.ValidateExclusion() != nil || h.Validate() != nil || hold.HeaderSHA256() != h.SHA256() || !isSHA(preparation) || !isSHA(inventory) || !isSHA(manifest) || len(processes) != len(requirements) {
		return nil, ErrOwnedEvidence
	}
	g := &VerifiedNamespaceRuntimeGraph{hold: hold, header: h.SHA256(), preparation: preparation, inventory: inventory, manifest: manifest, requirements: requirementsSHA, processes: append([]NamespaceRuntimeProcess(nil), processes...)}
	for at, p := range processes {
		b, r := p.Bootstrap, requirements[at]
		observation.enter(r.Role, "bootstrap")
		if b.ValidateShape() != nil || b.Header != h || b.RuntimePreparationSHA256 != preparation || b.InventorySHA256 != inventory || b.ManifestSHA256 != manifest || b.RequirementsSHA256 != requirementsSHA {
			return nil, ErrOwnedEvidence
		}
		a, _ := json.Marshal(b.Requirement)
		z, _ := json.Marshal(r)
		observation.enter(r.Role, "requirement")
		if string(a) != string(z) {
			return nil, ErrOwnedEvidence
		}
		row, e := observeNamespaceRuntimeWithObservation(ctx, p, observation)
		if e != nil {
			return nil, e
		}
		g.rows = append(g.rows, row)
	}
	g.observedAt = time.Now()
	observation.enter("", "complete")
	return g, nil
}
func observeNamespaceRuntime(ctx context.Context, p NamespaceRuntimeProcess) (ObservedNamespaceRuntime, error) {
	return observeNamespaceRuntimeWithObservation(ctx, p, nil)
}

func observeNamespaceRuntimeWithObservation(ctx context.Context, p NamespaceRuntimeProcess, observation *NamespaceRuntimeCallObservation) (ObservedNamespaceRuntime, error) {
	var empty ObservedNamespaceRuntime
	s, b := p.Service, p.Bootstrap
	observation.enter(b.Requirement.Role, "input-state")
	if s == nil || ctx == nil || ctx.Err() != nil {
		return empty, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	required := b.Requirement
	observation.enter(required.Role, "owner")
	if s.cmd == nil || s.cmd.Process == nil || s.joined || s.owner.Role != required.Role || s.owner.BinarySHA256 != required.BinarySHA256 || s.owner.SourceSHA256 != required.SourceSHA256 || s.owner.BuildReceiptSHA256 != required.BuildReceiptSHA256 || s.owner.BootstrapSHA256 != required.BootstrapSHA256 {
		if observation != nil {
			if s.cmd == nil || s.cmd.Process == nil {
				observation.enter(required.Role, "handle")
			} else if s.joined {
				observation.enter(required.Role, "joined")
			}
		}
		return empty, ErrOwnedEvidence
	}
	observation.enter(required.Role, "done")
	select {
	case <-s.done:
		return empty, ErrOwnedEvidence
	default:
	}
	observation.enter(required.Role, "process-before")
	before, e := processIdentity(s.owner.PID)
	if e != nil || !sameProcess(before, s.owner) || actualImage(s.owner.PID, s.owner.Executable, s.owner.BinarySHA256) != nil {
		if observation != nil && e == nil && sameProcess(before, s.owner) {
			// The original short circuit reached actualImage; do not observe it again.
			observation.enter(required.Role, "image")
		}
		return empty, ErrOwnedEvidence
	}
	observation.enter(required.Role, "envelope")
	var envelope namespaceRuntimeRegistrationEnvelope
	sum, e := readServerEnvelope(b.RegistrationPath, &envelope)
	if errors.Is(e, os.ErrNotExist) {
		observation.enter(required.Role, "registration-absent")
		return empty, ErrNamespaceRuntimeRegistrationPending
	}
	if e != nil || envelope.Schema != 1 || envelope.OwnerNonce != s.owner.OwnerNonce {
		return empty, ErrOwnedEvidence
	}
	r := envelope.Registration
	observation.enter(required.Role, "instance")
	if b.ExpectedInstanceID != "" && r.InstanceID != b.ExpectedInstanceID {
		return empty, ErrOwnedEvidence
	}
	observation.enter(required.Role, "tuple")
	if r.ValidateShape() != nil || r.Role != required.Role || r.HeaderSHA256 != b.Header.SHA256() || r.RuntimePreparationSHA256 != b.RuntimePreparationSHA256 || r.RuntimeTokenSHA256 != sha([]byte(b.RuntimeToken)) || r.InventorySHA256 != b.InventorySHA256 || r.ManifestSHA256 != b.ManifestSHA256 || r.RequirementsSHA256 != b.RequirementsSHA256 || r.BootstrapSHA256 != required.BootstrapSHA256 || r.ConfigurationSHA256 != required.ConfigurationSHA256 || r.EnvironmentSHA256 != required.EnvironmentSHA256 || r.ContractSHA256 != required.ContractSHA256 {
		return empty, ErrOwnedEvidence
	}
	observation.enter(required.Role, "mongo-binding")
	if validateNamespaceRuntimeRegistrationMongo(r, b) != nil {
		return empty, ErrOwnedEvidence
	}
	a, _ := json.Marshal(r.Services)
	z, _ := json.Marshal(required.Services)
	observation.enter(required.Role, "services")
	if string(a) != string(z) {
		return empty, ErrOwnedEvidence
	}
	a, _ = json.Marshal(r.Components)
	z, _ = json.Marshal(required.Components)
	observation.enter(required.Role, "components")
	if string(a) != string(z) {
		return empty, ErrOwnedEvidence
	}
	observation.enter(required.Role, "process-after")
	after, e := processIdentity(s.owner.PID)
	if e != nil || !sameProcess(before, after) {
		return empty, ErrOwnedEvidence
	}
	return ObservedNamespaceRuntime{Role: required.Role, PID: s.owner.PID, UID: s.owner.UID, StartToken: s.owner.StartToken, BootID: s.owner.BootID, BinarySHA256: s.owner.BinarySHA256, SourceSHA256: s.owner.SourceSHA256, BuildReceiptSHA256: s.owner.BuildReceiptSHA256, OwnerNonceSHA256: sha([]byte(s.owner.OwnerNonce)), RegistrationSHA256: sum, Registration: r}, nil
}
func (g *VerifiedNamespaceRuntimeGraph) Refresh(ctx context.Context) error {
	if g == nil {
		return ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hold == nil || g.hold.ValidateExclusion() != nil || len(g.rows) != len(NamespaceRuntimeRoles()) || len(g.processes) != len(g.rows) {
		return ErrOwnedEvidence
	}
	for at, p := range g.processes {
		row, e := observeNamespaceRuntime(ctx, p)
		if e != nil {
			return e
		}
		a, _ := json.Marshal(row)
		z, _ := json.Marshal(g.rows[at])
		if string(a) != string(z) {
			return ErrOwnedEvidence
		}
	}
	g.observedAt = time.Now()
	return nil
}
func (g *VerifiedNamespaceRuntimeGraph) ValidateLocal(hold *AdmissionHold, h, preparation, inventory, manifest, requirements string) error {
	if g == nil {
		return ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	age := time.Since(g.observedAt)
	if hold == nil || g.hold != hold || hold.ValidateExclusion() != nil || g.header != h || g.preparation != preparation || g.inventory != inventory || g.manifest != manifest || g.requirements != requirements || len(g.rows) != len(NamespaceRuntimeRoles()) || age < 0 || age > 20*time.Second {
		return ErrOwnedEvidence
	}
	return nil
}
func (g *VerifiedNamespaceRuntimeGraph) AuditRows() []ObservedNamespaceRuntime {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	raw, _ := json.Marshal(g.rows)
	var out []ObservedNamespaceRuntime
	_ = json.Unmarshal(raw, &out)
	return out
}
