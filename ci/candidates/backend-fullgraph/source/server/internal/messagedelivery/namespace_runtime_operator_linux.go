//go:build linux

package messagedelivery

import (
	"context"
	"crypto/ed25519"
	"errors"
	"path/filepath"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

type ControlledNamespaceRuntimeRun struct {
	preparation     *NamespaceRuntimePreparation
	Processes       *ownedcontrol.NamespaceRuntimeLaunchGroup
	Readers         *ownedcontrol.HistoryReaderLaunchGroup
	DataReadySHA256 string
	AdmissionSHA256 string
	Started         []ownedcontrol.NamespaceRuntimeStartedObservation
}

// Concrete private operator composition. The caller already possesses the
// original held capture, verified signed import, actual published bounds, source
// keys and reviewed executable/build/bootstraps. No production startup creates
// those prerequisites and no client or network DTO can construct this handle.
//
// A non-nil run ALWAYS retains all partial real children. The caller must keep
// it and Stop on cancellation/failure, or use ContinueKnownAdmission after an
// unknown write has been positively inspected; an absent receipt never retries.
func (p *HistoryReadinessPreparation) StartAndActivateNamespaceRuntime(ctx context.Context, readerDir, runtimeDir, sessionPath, keyID string, key ed25519.PrivateKey, requirements []ownedcontrol.NamespaceRuntimeRequirement, specs []ownedcontrol.ServiceSpec) (*ControlledNamespaceRuntimeRun, error) {
	if p == nil || ctx == nil || ctx.Done() == nil || len(specs) != len(ownedcontrol.NamespaceRuntimeRoles()) || p.validate(ctx) != nil {
		return nil, ErrUnprepared
	}
	byRole := map[string]ownedcontrol.ServiceSpec{}
	for _, spec := range specs {
		if byRole[spec.Role].Role != "" {
			return nil, ErrInvalid
		}
		byRole[spec.Role] = spec
	}
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		if byRole[role].Role != role {
			return nil, ErrInvalid
		}
	}
	preparation, e := p.PrepareNamespaceRuntime(ctx, requirements, sessionPath, keyID, key)
	if e != nil {
		return nil, e
	}
	readers, e := p.NewReaderLaunchGroup(ctx, readerDir)
	if e != nil {
		return nil, e
	}
	group, e := ownedcontrol.NewNamespaceRuntimeLaunchGroup(p.hold, p.writer.header, runtimeDir, preparation.requirements, preparation, readers)
	if e != nil {
		return nil, e
	}
	run := &ControlledNamespaceRuntimeRun{preparation: preparation, Processes: group, Readers: readers}
	// Preflight and freeze all original private bootstraps before launching any
	// child. These inputs carry no new authoritative history/Ready information.
	frozen := map[string]ownedcontrol.PreparedNamespaceRuntimeSpec{}
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		spec := byRole[role]
		instance, e := ownedcontrol.NamespaceRuntimeExpectedInstance(spec)
		if e != nil {
			return run, e
		}
		_, sum, e := preparation.WriteRuntimeBootstrap(ctx, role, instance, filepath.Join(spec.Work, "namespace-runtime-bootstrap.json"), filepath.Join(spec.Work, "namespace-runtime-registration.json"), filepath.Join(spec.Work, "namespace-runtime-release.json"))
		if e != nil {
			return run, e
		}
		input := ownedcontrol.PreparedNamespaceRuntimeSpec{Service: spec, RuntimePath: filepath.Join(spec.Work, "namespace-runtime-bootstrap.json"), RuntimeSHA256: sum}
		if role == "rpc-msg" || role == "rpc-conversation" {
			_, sum, e := p.WriteReaderBootstrap(ctx, role, filepath.Join(spec.Work, "history-reader-preparation.json"), filepath.Join(spec.Work, "history-reader-registration.json"))
			if e != nil {
				return run, e
			}
			input.HistoryPath, input.HistorySHA256 = filepath.Join(spec.Work, "history-reader-preparation.json"), sum
		}
		frozen[role] = input
	}
	for _, role := range []string{"rpc-conversation", "rpc-msg"} {
		if _, e = group.Start(ctx, frozen[role]); e != nil {
			return run, e
		}
	}
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, e = p.source.ReadinessSourceCustody(ctx, p.hold, p.writer.header, p.selection.Inventory, p.selection.ImportConfig); e != nil {
			return run, e
		}
		graph, e := readers.Observe(ctx)
		if e == nil {
			sum, e := p.PublishReadReady(ctx, graph)
			if e != nil {
				return run, e
			}
			run.DataReadySHA256 = sum
			break
		}
		if !errors.Is(e, ownedcontrol.ErrHistoryReaderRegistrationPending) {
			return run, e
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-tick.C:
		}
	}
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		if role == "rpc-msg" || role == "rpc-conversation" {
			continue
		}
		if _, e = group.Start(ctx, frozen[role]); e != nil {
			return run, e
		}
	}
	for {
		if _, e = p.source.ReadinessSourceCustody(ctx, p.hold, p.writer.header, p.selection.Inventory, p.selection.ImportConfig); e != nil {
			return run, e
		}
		graph, e := group.Observe(ctx)
		if e == nil {
			sum, e := preparation.PublishRuntimeAdmission(ctx, graph)
			if e != nil {
				return run, e
			}
			run.AdmissionSHA256 = sum
			processes, e := group.Processes()
			if e != nil {
				return run, e
			}
			if _, e = preparation.WriteRuntimeReleases(ctx, graph, processes); e != nil {
				return run, e
			}
			break
		}
		if !errors.Is(e, ownedcontrol.ErrNamespaceRuntimeRegistrationPending) {
			return run, e
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-tick.C:
		}
	}
	if e = run.awaitStarted(ctx); e != nil {
		return run, e
	}
	return run, nil
}
func (r *ControlledNamespaceRuntimeRun) awaitStarted(ctx context.Context) error {
	if r == nil || r.preparation == nil || r.Processes == nil || !validSHA(r.AdmissionSHA256) {
		return ErrUnprepared
	}
	p := r.preparation.history
	for {
		if _, e := p.source.ReadinessSourceCustody(ctx, p.hold, p.writer.header, p.selection.Inventory, p.selection.ImportConfig); e != nil {
			return e
		}
		turn, done := context.WithTimeout(ctx, 3*time.Second)
		rows, e := r.Processes.AwaitStarted(turn, r.AdmissionSHA256)
		done()
		if e == nil {
			r.Started = rows
			return nil
		}
		if !errors.Is(e, context.DeadlineExceeded) {
			return e
		}
		if e = ctx.Err(); e != nil {
			return e
		}
	}
}

// Explicit known-commit recovery, not another publish attempt. Re-observe the
// original children and exact prepared graph, then read the original actual DB
// receipt. Missing/unknown remains nonpermit; no constructor/Start is repeated.
func (r *ControlledNamespaceRuntimeRun) ContinueKnownAdmission(ctx context.Context) error {
	if r == nil || ctx == nil || r.preparation == nil || r.Processes == nil {
		return ErrUnprepared
	}
	p := r.preparation
	if _, e := p.history.source.ReadinessSourceCustody(ctx, p.history.hold, p.history.writer.header, p.history.selection.Inventory, p.history.selection.ImportConfig); e != nil {
		return e
	}
	graph, e := r.Processes.Observe(ctx)
	if e != nil {
		return e
	}
	row, found, e := p.history.writer.readNamespaceRuntimeAdmission(ctx)
	if e != nil {
		return e
	}
	if !found {
		return ErrCommitUnconfirmed
	}
	if e = p.ValidateNamespaceRuntimeStarted(ctx, historyReadinessDigest(row), graph.AuditRows()); e != nil {
		return e
	}
	processes, e := r.Processes.Processes()
	if e != nil {
		return e
	}
	if _, e = p.WriteRuntimeReleases(ctx, graph, processes); e != nil {
		return e
	}
	r.AdmissionSHA256 = historyReadinessDigest(row)
	return r.awaitStarted(ctx)
}
func (r *ControlledNamespaceRuntimeRun) Stop(ctx context.Context) error {
	if r == nil || r.Processes == nil {
		return ErrUnprepared
	}
	return r.Processes.Stop(ctx)
}
