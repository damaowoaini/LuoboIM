//go:build linux

package runtimeacceptance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// Call observations only: state encodes not-entered / attempted-not-returned / returned.
// Stage is last-entered, not a failure predicate. Pending retains the original wait.
type readerCallObservation struct {
	Stage      string
	State      string
	ErrorClass string
}

func graphOperationErrorClass(err error) string {
	switch {
	case err == nil:
		return "none"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(err, os.ErrPermission):
		return "permission"
	case errors.Is(err, os.ErrNotExist):
		return "not-found"
	case errors.Is(err, os.ErrExist):
		return "already-exists"
	case errors.Is(err, ownedcontrol.ErrHistoryReaderRegistrationPending):
		return "registration-pending"
	case errors.Is(err, namespaceimport.ErrCommitUnconfirmed):
		return "commit-unconfirmed"
	case errors.Is(err, ownedcontrol.ErrOwnedEvidence):
		return "owned-evidence"
	case errors.Is(err, namespaceimport.ErrUntrusted):
		return "import-evidence"
	case errors.Is(err, namespaceimport.ErrConflict):
		return "import-conflict"
	case errors.Is(err, namespaceimport.ErrInvalid):
		return "import-invalid"
	case errors.Is(err, messagedelivery.ErrUnprepared):
		return "preparation-unavailable"
	case errors.Is(err, messagedelivery.ErrConflict):
		return "preparation-conflict"
	case errors.Is(err, namespaceinventory.ErrPrivate):
		return "inventory-private"
	case errors.Is(err, namespaceinventory.ErrIdentity):
		return "inventory-identity"
	case errors.Is(err, namespaceinventory.ErrChanged):
		return "inventory-changed"
	case errors.Is(err, namespaceinventory.ErrSource):
		return "inventory-source"
	default:
		return "other"
	}
}

type ReaderPhaseObservation struct {
	Phase                           string                               `json:"phase"`
	Readers                         []ownedcontrol.ObservedHistoryReader `json:"readers"`
	ActualPreparedRoles             int                                  `json:"actualPreparedRoles"`
	DataReadySHA256                 string                               `json:"dataReadySHA256,omitempty"`
	KnownAdmissionSHA256            string                               `json:"knownAdmissionSHA256,omitempty"`
	BothActualConnectionsNotServing bool                                 `json:"bothActualConnectionsNotServing"`
	Endpoints                       []ActualReaderEndpoint               `json:"endpoints"`
}
type ActualGraphRun struct {
	Processes       *ownedcontrol.NamespaceRuntimeLaunchGroup
	Readers         *ownedcontrol.HistoryReaderLaunchGroup
	DataReadySHA256 string
	AdmissionSHA256 string
	Started         []ownedcontrol.NamespaceRuntimeStartedObservation
	ReaderPhases    []ReaderPhaseObservation
	ReaderServed    []ActualReaderEndpoint
	Launched        []ownedcontrol.NamespaceRuntimeProcess
	StoppedAt       string
}

func (r *ActualGraphRun) Stop(ctx context.Context) error {
	if r == nil || r.Processes == nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	return r.Processes.Stop(ctx)
}

// Only an unknown, deliberately unregistered method is used. If the real gRPC
// server has begun Serve it returns Unimplemented; a deadline is required at
// the original, synchronously paused prepare/publish/release boundaries. No
// user read/write, auth identity, health replacement or fake server is used.
func observeReaderFence(ctx context.Context, phase string, readers *ownedcontrol.VerifiedHistoryReaderGraph, ports map[string]int, prepared int, data, admission string) (ReaderPhaseObservation, error) {
	out := ReaderPhaseObservation{Phase: phase, Readers: readers.AuditRows(), ActualPreparedRoles: prepared, DataReadySHA256: data, KnownAdmissionSHA256: admission}
	if len(out.Readers) != 2 || len(ports) != 2 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	for _, row := range out.Readers {
		port, ok := ports[row.Role]
		if !ok {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		endpoint, e := probeActualReaderEndpoint(ctx, readers, row, port, false, "")
		if e != nil {
			return out, e
		}
		out.Endpoints = append(out.Endpoints, endpoint)
	}
	out.BothActualConnectionsNotServing = true
	return out, nil
}

// This explicit acceptance composition uses the SAME existing private
// preparation/launch/graph/Store methods as the production operator. There is
// no new production hook/API, alternate authority or phase DTO that can mint a
// release. Pausing between those original calls makes the negative gRPC
// observations deterministic without racing private-file polling.
func (s *Session) startActualGraph(ctx context.Context, p *messagedelivery.HistoryReadinessPreparation) (*ActualGraphRun, error) {
	in := s.inputs
	s.Phase = "actual-runtime-private-bootstrap-preparation"
	prep, e := p.PrepareNamespaceRuntime(ctx, in.Requirements, filepath.Join(in.Work, "runtime-session.json"), in.ReleaseKeyID, in.ReleasePrivateKey)
	if e != nil {
		return nil, e
	}
	readers, e := p.NewReaderLaunchGroup(ctx, filepath.Join(in.Work, "reader-owners"))
	if e != nil {
		return nil, e
	}
	hold, e := s.Agent.RefreshExclusion(ctx)
	if e != nil {
		return nil, e
	}
	group, e := ownedcontrol.NewNamespaceRuntimeLaunchGroup(hold, in.Config.Header, filepath.Join(in.Work, "runtime-owners"), in.Requirements, prep, readers)
	if e != nil {
		return nil, e
	}
	out := &ActualGraphRun{Processes: group, Readers: readers}
	frozen := map[string]ownedcontrol.PreparedNamespaceRuntimeSpec{}
	for _, spec := range in.Specs {
		instance, e := ownedcontrol.NamespaceRuntimeExpectedInstance(spec)
		if e != nil {
			return out, e
		}
		_, sum, e := prep.WriteRuntimeBootstrap(ctx, spec.Role, instance, filepath.Join(spec.Work, "namespace-runtime-bootstrap.json"), filepath.Join(spec.Work, "namespace-runtime-registration.json"), filepath.Join(spec.Work, "namespace-runtime-release.json"))
		if e != nil {
			return out, e
		}
		input := ownedcontrol.PreparedNamespaceRuntimeSpec{Service: spec, RuntimePath: filepath.Join(spec.Work, "namespace-runtime-bootstrap.json"), RuntimeSHA256: sum}
		if spec.Role == "rpc-msg" || spec.Role == "rpc-conversation" {
			_, sum, e = p.WriteReaderBootstrap(ctx, spec.Role, filepath.Join(spec.Work, "history-reader-preparation.json"), filepath.Join(spec.Work, "history-reader-registration.json"))
			if e != nil {
				return out, e
			}
			input.HistoryPath, input.HistorySHA256 = filepath.Join(spec.Work, "history-reader-preparation.json"), sum
		}
		frozen[spec.Role] = input
	}
	s.Phase = "actual-two-reader-processes-and-original-registration"
	for _, role := range []string{"rpc-conversation", "rpc-msg"} {
		s.readerObservation = readerCallObservation{Stage: "start-" + role, State: "in-progress", ErrorClass: "in-progress"}
		process, e := group.Start(ctx, frozen[role])
		s.readerObservation.State = "returned"
		s.readerObservation.ErrorClass = graphOperationErrorClass(e)
		if process.Service != nil {
			out.Launched = append(out.Launched, process)
		}
		if e != nil {
			return out, e
		}
	}
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	check := func() error {
		_, e := s.Source.ReadinessSourceCustody(ctx, hold, in.Config.Header, s.State.Inventory, in.Config)
		return e
	}
	var readerGraph *ownedcontrol.VerifiedHistoryReaderGraph
	for {
		s.readerObservation = readerCallObservation{Stage: "source-custody", State: "in-progress", ErrorClass: "in-progress"}
		e = check()
		s.readerObservation.State = "returned"
		s.readerObservation.ErrorClass = graphOperationErrorClass(e)
		if e != nil {
			return out, e
		}
		s.readerObservation = readerCallObservation{Stage: "observe-registration", State: "in-progress", ErrorClass: "in-progress"}
		readerGraph, e = readers.Observe(ctx)
		s.readerObservation.State = "returned"
		s.readerObservation.ErrorClass = graphOperationErrorClass(e)
		if e == nil {
			break
		}
		if !errors.Is(e, ownedcontrol.ErrHistoryReaderRegistrationPending) {
			return out, e
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-tick.C:
		}
	}
	s.Phase = "actual-readers-registered-before-readiness"
	phase, e := observeReaderFence(ctx, "registered-not-serving-before-readiness", readerGraph, in.ReaderPorts, 2, "", "")
	if e != nil {
		return out, e
	}
	out.ReaderPhases = append(out.ReaderPhases, phase)
	if in.StopAt == phase.Phase {
		out.StoppedAt = phase.Phase
		return out, nil
	}
	s.Phase = "actual-closed-inventory-readready-publication"
	out.DataReadySHA256, e = p.PublishReadReady(ctx, readerGraph)
	if e != nil {
		return out, e
	}
	s.Phase = "actual-twelve-other-source-constructors"
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		if role != "rpc-msg" && role != "rpc-conversation" {
			process, e := group.Start(ctx, frozen[role])
			if process.Service != nil {
				out.Launched = append(out.Launched, process)
			}
			if e != nil {
				return out, e
			}
		}
	}
	var graph *ownedcontrol.VerifiedNamespaceRuntimeGraph
	for {
		if e = check(); e != nil {
			return out, e
		}
		graph, e = group.Observe(ctx)
		if e == nil {
			break
		}
		if !errors.Is(e, ownedcontrol.ErrNamespaceRuntimeRegistrationPending) {
			return out, e
		}
		select {
		case <-ctx.Done():
			return out, ctx.Err()
		case <-tick.C:
		}
	}
	phase, e = observeReaderFence(ctx, "known-readiness-still-keeps-writer-fence", readerGraph, in.ReaderPorts, len(graph.AuditRows()), out.DataReadySHA256, "")
	if e != nil {
		return out, e
	}
	out.ReaderPhases = append(out.ReaderPhases, phase)
	if in.StopAt == phase.Phase {
		out.StoppedAt = phase.Phase
		return out, nil
	}
	s.Phase = "actual-known-runtime-admission-publication"
	out.AdmissionSHA256, e = prep.PublishRuntimeAdmission(ctx, graph)
	if e != nil {
		return out, e
	}
	phase, e = observeReaderFence(ctx, "known-admission-without-private-release-still-not-serving", readerGraph, in.ReaderPorts, len(graph.AuditRows()), out.DataReadySHA256, out.AdmissionSHA256)
	if e != nil {
		return out, e
	}
	out.ReaderPhases = append(out.ReaderPhases, phase)
	s.Phase = "actual-private-releases-and-fourteen-started-owners"
	processes, e := group.Processes()
	if e != nil {
		return out, e
	}
	if _, e = prep.WriteRuntimeReleases(ctx, graph, processes); e != nil {
		return out, e
	}
	for {
		if e = check(); e != nil {
			return out, e
		}
		turn, cancel := context.WithTimeout(ctx, 3*time.Second)
		out.Started, e = group.AwaitStarted(turn, out.AdmissionSHA256)
		cancel()
		if e == nil {
			break
		}
		if !errors.Is(e, context.DeadlineExceeded) {
			return out, e
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
	}
	for _, row := range readerGraph.AuditRows() {
		originalInode := ""
		for _, r := range out.ReaderPhases[0].Endpoints {
			if r.Role == row.Role {
				originalInode = r.ListenerInode
			}
		}
		if originalInode == "" {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		endpoint, e := probeActualReaderEndpoint(ctx, readerGraph, row, in.ReaderPorts[row.Role], true, originalInode)
		if e != nil {
			return out, e
		}
		out.ReaderServed = append(out.ReaderServed, endpoint)
	}
	return out, nil
}
