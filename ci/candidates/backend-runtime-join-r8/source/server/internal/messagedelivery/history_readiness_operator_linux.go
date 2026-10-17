//go:build linux

package messagedelivery

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

// A live owner handle survives all partial outcomes. Callers MUST retain it
// and Stop/Join on failure/cancellation; no timeout is reported as process exit.
// DataReady is deliberately distinct from runtime/Serve/producer activation.
type HistoryReadinessRun struct {
	Readers                    *ownedcontrol.HistoryReaderLaunchGroup
	DataReadyPublicationSHA256 string
}

// Executable composition used by the same private operator that already owns
// the real import source/hold. It writes exact private per-role bootstraps,
// starts only pinned actual exec.Cmd children, observes their REAL original
// process/start/exe and registered-not-serving evidence, then publishes the
// actual source/bounds/reader proof in Mongo. No command self-reported Ready,
// fake PID adoption, external network event or current-roster history inference.
func (p *HistoryReadinessPreparation) StartAndPublishReadReady(ctx context.Context, ownershipDirectory string, specs []ownedcontrol.ServiceSpec) (*HistoryReadinessRun, error) {
	if p == nil || ctx == nil || ctx.Done() == nil || len(specs) != len(p.selection.Readers) || p.validate(ctx) != nil {
		return nil, ErrUnprepared
	}
	byRole := map[string]ownedcontrol.ServiceSpec{}
	for _, s := range specs {
		if s.Role == "" || byRole[s.Role].Role != "" {
			return nil, ErrInvalid
		}
		byRole[s.Role] = s
	}
	for _, r := range p.selection.Readers {
		if byRole[r.Role].Role == "" {
			return nil, ErrInvalid
		}
	}
	group, e := p.NewReaderLaunchGroup(ctx, ownershipDirectory)
	if e != nil {
		return nil, e
	}
	run := &HistoryReadinessRun{Readers: group}
	for _, r := range p.selection.Readers {
		spec := byRole[r.Role]
		path := filepath.Join(spec.Work, "history-reader-preparation.json")
		bindingPath := filepath.Join(spec.Work, "history-reader-registration.json")
		_, sum, e := p.WriteReaderBootstrap(ctx, r.Role, path, bindingPath)
		if e != nil {
			return run, e
		}
		if _, e = group.Start(ctx, ownedcontrol.PreparedHistoryReaderSpec{Service: spec, PreparationPath: path, PreparationSHA256: sum}); e != nil {
			return run, e
		}
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		// A slow real constructor may outlive the short observation. The SAME
		// actual source/Agent rechecks process joins, credentials and Kafka outside
		// Mongo retry callbacks. This never renews capture/cut/lease/raw input.
		if e := p.hold.ValidateExclusion(); e != nil {
			if _, e = p.source.ReadinessSourceCustody(ctx, p.hold, p.writer.header, p.selection.Inventory, p.selection.ImportConfig); e != nil {
				return run, e
			}
		}
		graph, e := group.Observe(ctx)
		if e == nil {
			sum, e := p.PublishReadReady(ctx, graph)
			if e != nil {
				return run, e
			}
			run.DataReadyPublicationSHA256 = sum
			return run, nil
		}
		if !errors.Is(e, ownedcontrol.ErrHistoryReaderRegistrationPending) {
			return run, e
		}
		select {
		case <-ctx.Done():
			return run, ctx.Err()
		case <-ticker.C:
		}
	}
}
