//go:build linux

// Package runtimeacceptance composes the real private owned deployment path.
// It has no RPC endpoint, marker setter or replacement ControlPlane/Writer port.
package runtimeacceptance

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"go.mongodb.org/mongo-driver/mongo"
)

// Handles come from the fresh private resource bootstrap. These concrete types
// cannot be supplied as a JSON verified page/hold or a self-reported Ready flag.
// The loader independently verifies the fresh owned kernel/container plane;
// all subsequent authority comes from the real Agent/Controller/Store methods.
type Inputs struct {
	Work              string
	StopAt            string // acceptance-only finite pause; never a permit or runtime input
	Config            namespaceimport.Config
	Database          *mongo.Database
	Live              *namespaceinventory.LiveSource
	Launcher          *ownedcontrol.Launcher
	Kafka             *ownedcontrol.KafkaProbe
	MongoExclusions   []ownedcontrol.MongoExclusion
	RedisExclusions   []ownedcontrol.RedisExclusion
	KafkaExclusions   []ownedcontrol.KafkaExclusion
	LegacyWriters     []ownedcontrol.OwnedWriter
	LegacySpecs       []ownedcontrol.ServiceSpec
	ProbePrivateKey   ed25519.PrivateKey
	Capture           ownedcontrol.HistoricalCaptureOptions
	ReaderPorts       map[string]int
	Readers           []ownedcontrol.HistoryReaderRequirement
	Requirements      []ownedcontrol.NamespaceRuntimeRequirement
	Specs             []ownedcontrol.ServiceSpec
	ReleaseKeyID      string
	ReleasePrivateKey ed25519.PrivateKey
}

type Session struct {
	inputs                        Inputs
	Phase                         string
	Agent                         *ownedcontrol.OwnedAgent
	Controller                    *namespaceimport.Controller
	Source                        *ownedcontrol.HistoricalCaptureSource
	Writer                        *messagedelivery.NamespaceImportWriter
	State                         namespaceimport.State
	Runtime                       *ActualGraphRun
	Legacy                        []*ownedcontrol.OwnedService
	legacyStartupStage            string
	legacyChildRetained           bool
	acquireObservation            ownedcontrol.AgentAcquireObservation
	readerObservation             readerCallObservation
	constructorObservation        constructorCallObservation
	journal                       namespaceimport.Journal
	cleanupMu                     sync.Mutex
	localExited                   map[*ownedcontrol.OwnedService]bool
	journalClosed, launcherClosed bool
	cleanupFailure                error
}

// Start calls every original source path. Any nonnil returned Session retains
// its actual partial children/custody even on failure; callers must Cleanup and
// preserve failure artifacts. This function never converts a prerequisite or
// cleanup failure into a successful observation.
func Start(ctx context.Context, in Inputs) (*Session, error) {
	if ctx == nil || ctx.Done() == nil || in.Database == nil || in.Live == nil || in.Launcher == nil || in.Kafka == nil || in.Config.Header.Validate() != nil || in.Database.Name() != in.Config.Header.Database || !filepath.IsAbs(in.Work) || filepath.Clean(in.Work) != in.Work || len(in.LegacySpecs) != 2 || len(in.LegacyWriters) != 2 || len(in.Specs) != 14 || len(in.Requirements) != 14 || len(in.ProbePrivateKey) != ed25519.PrivateKeySize || len(in.ReleasePrivateKey) != ed25519.PrivateKeySize {
		return nil, ownedcontrol.ErrOwnedEvidence
	}
	if in.StopAt != "" && in.StopAt != "registered-not-serving-before-readiness" && in.StopAt != "known-readiness-still-keeps-writer-fence" {
		return nil, ownedcontrol.ErrOwnedEvidence
	}
	if _, e := ownedcontrol.NamespaceRuntimeRequirementsSHA256(in.Requirements); e != nil {
		return nil, e
	}
	if _, e := ownedcontrol.HistoryReaderRequirementsSHA256(in.Readers); e != nil {
		return nil, e
	}
	// These are the complete original writers ONLY in a separately proven fresh
	// task-owned service plane. They never represent an adopted old906 deployment.
	if in.LegacySpecs[0].Role != "rpc-msg" || in.LegacySpecs[1].Role != "msgtransfer-legacy" || in.LegacyWriters[0].Role != "rpc-msg" || in.LegacyWriters[0].Phase != "producer" || in.LegacyWriters[1].Role != "msgtransfer-legacy" || in.LegacyWriters[1].Phase != "consumer" {
		return nil, ownedcontrol.ErrOwnedEvidence
	}
	selected := map[string]bool{}
	for _, s := range in.Specs {
		if selected[s.Role] {
			return nil, ownedcontrol.ErrOwnedEvidence
		}
		selected[s.Role] = true
	}
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		if !selected[role] {
			return nil, ownedcontrol.ErrOwnedEvidence
		}
	}
	out := &Session{inputs: in, Phase: "construct-actual-import-store"}
	store, e := messagedelivery.NewStore(in.Database, in.Config.Header.Epoch, in.Config.Header.ActivationSHA256)
	if e != nil {
		return out, e
	}
	writer, e := messagedelivery.NewNamespaceImportWriter(store, in.Config.Header)
	if e != nil {
		return out, e
	}
	out.Writer = writer
	// Actual original executables, Start/Serve/owner announcements and joins.
	for _, spec := range in.LegacySpecs {
		out.Phase = "actual-owned-legacy-start-" + spec.Role
		out.legacyChildRetained = false
		child, observation, e := in.Launcher.StartWithObservation(ctx, spec)
		out.legacyStartupStage = string(observation.Stage)
		if child != nil {
			out.Legacy = append(out.Legacy, child)
			out.legacyChildRetained = true
		}
		if e != nil {
			return out, e
		}
		_, _, observation, e = child.AwaitAnnouncementWithObservation(ctx)
		out.legacyStartupStage = string(observation.Stage)
		if e != nil {
			return out, e
		}
	}
	agent, e := ownedcontrol.NewOwnedAgent(in.Config.Header, in.Launcher, in.LegacyWriters, in.Kafka, in.MongoExclusions, in.RedisExclusions, in.KafkaExclusions, writer, in.Config.ProbeKeyID, in.ProbePrivateKey)
	if e != nil {
		return out, e
	}
	out.Agent = agent
	dir := filepath.Join(in.Work, "controller")
	if e = os.Mkdir(dir, 0700); e != nil {
		return out, e
	}
	journal, e := namespaceimport.OpenPrivateJournal(dir)
	if e != nil {
		return out, e
	}
	out.journal = journal
	controller, source, e := messagedelivery.NewReviewedHistoricalImportController(in.Config, writer, journal, agent, in.Live, in.Capture, in.Capture.OriginalHistoryTrustPath, in.Capture.OriginalHistoryTrustSHA256)
	if e != nil {
		return out, e
	}
	out.Controller, out.Source = controller, source
	// Begin really stops the owned producer, observes both actual broker queue
	// planes, joins the original consumer, revokes/live-tests all three legacy
	// principal families, and runs real held metadata bootstrap. No fake hold.
	out.Phase = "actual-agent-stop-dual-drain-and-credential-exclusion"
	out.State, e = controller.Begin(ctx)
	out.acquireObservation = agent.AcquireObservation()
	if e != nil {
		return out, e
	}
	for out.State.Phase != "published" {
		if e = ctx.Err(); e != nil {
			return out, e
		}
		out.Phase = "actual-signed-import-" + out.State.Phase
		out.State, e = controller.Step(ctx)
		if errors.Is(e, namespaceimport.ErrPending) {
			continue
		}
		if e != nil {
			return out, e
		} // unknown commit is retained; never dispatch replay
	}
	out.Phase = "actual-published-source-history-readiness-preparation"
	hold, e := agent.RefreshExclusion(ctx)
	if e != nil {
		return out, e
	}
	selection := messagedelivery.HistoryReadinessSelection{ImportConfig: in.Config, Inventory: out.State.Inventory, ManifestSHA256: out.State.Cutover.ManifestSHA256, Readers: in.Readers, PrivateSessionPath: filepath.Join(in.Work, "history-readiness-session.json")}
	preparation, e := writer.PrepareHistoryReadiness(ctx, hold, source, selection)
	if e != nil {
		return out, e
	}
	for _, dir := range []string{"reader-owners", "runtime-owners"} {
		if e = os.Mkdir(filepath.Join(in.Work, dir), 0700); e != nil {
			return out, e
		}
	}
	// Actual two reader processes -> closed data publication -> twelve more
	// actual mains -> original graph -> known Mongo admission -> signed releases.
	out.Runtime, e = out.startActualGraph(ctx, preparation)
	if e != nil {
		return out, e
	}
	if in.StopAt != "" {
		if out.Runtime.StoppedAt != in.StopAt {
			return out, ownedcontrol.ErrOwnedEvidence
		}
		return out, nil
	}
	if len(out.Runtime.Started) != 14 {
		return out, ownedcontrol.ErrOwnedEvidence
	}
	return out, nil
}

// Close is intentionally not a business-drain or acceptance predicate. A
// runtime Redis unknown still returns nonpermit even when all PIDs disappear.
// The fresh service-plane collector checks source receipts and OS absence.
func (s *Session) Cleanup(ctx context.Context) error {
	if s == nil || ctx == nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.cleanupCompleteLocked() {
		return s.cleanupFailure
	}
	var result error
	if s.Runtime != nil {
		result = errors.Join(result, s.Runtime.Stop(ctx))
		// A noncanonical runtime error cannot strand this or any other retained
		// child. Shared per-service signal state prevents another SIGTERM here.
		for _, p := range s.Runtime.Launched {
			if p.Service == nil {
				result = errors.Join(result, ownedcontrol.ErrOwnedEvidence)
				continue
			}
			if s.localExited[p.Service] {
				continue
			}
			exit, e := p.Service.StopForLocalCleanup(ctx)
			s.noteLocalExitLocked(p.Service, exit)
			result = errors.Join(result, e)
		}
	}
	result = errors.Join(result, s.cleanupRetainedImportLocked(ctx))
	s.cleanupFailure = errors.Join(s.cleanupFailure, result)
	return s.cleanupFailure
}
func (s *Session) noteLocalExit(service *ownedcontrol.OwnedService, exit ownedcontrol.LocalChildExit) {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	s.noteLocalExitLocked(service, exit)
}
func (s *Session) noteLocalExitLocked(service *ownedcontrol.OwnedService, exit ownedcontrol.LocalChildExit) {
	if service != nil && !exit.ObservedAbsentAt.IsZero() && (exit.Version == "owned-local-child-exited-v1" || exit.Version == "owned-local-unbound-child-exited-v1") {
		if s.localExited == nil {
			s.localExited = map[*ownedcontrol.OwnedService]bool{}
		}
		s.localExited[service] = true
	}
}
func (s *Session) allLocalExitedLocked() bool {
	if s.Runtime != nil {
		for _, p := range s.Runtime.Launched {
			if p.Service == nil || !s.localExited[p.Service] {
				return false
			}
		}
	}
	for _, child := range s.Legacy {
		if child == nil || !s.localExited[child] {
			return false
		}
	}
	return true
}
func (s *Session) cleanupCompleteLocked() bool {
	return s.allLocalExitedLocked() && (s.journal == nil || s.journalClosed) && (s.inputs.Launcher == nil || s.launcherClosed)
}

// Completion describes local retained Wait/Close only. cleanupFailure remains
// sticky: it is never a canonical business drain, Ready or writer permission.
func (s *Session) CleanupComplete() bool {
	if s == nil {
		return false
	}
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	return s.cleanupCompleteLocked()
}
func (s *Session) cleanupRetainedImport(ctx context.Context) error {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	return s.cleanupRetainedImportLocked(ctx)
}
func (s *Session) cleanupRetainedImportLocked(ctx context.Context) error {
	var result error
	for at := len(s.Legacy) - 1; at >= 0; at-- {
		child := s.Legacy[at]
		if child == nil {
			result = errors.Join(result, ownedcontrol.ErrOwnedEvidence)
			continue
		}
		if s.localExited[child] {
			continue
		}
		exit, e := child.StopForLocalCleanup(ctx)
		s.noteLocalExitLocked(child, exit)
		result = errors.Join(result, e)
	}
	if s.journal != nil && !s.journalClosed {
		if closer, ok := s.journal.(interface{ Close() error }); ok {
			e := closer.Close()
			result = errors.Join(result, e)
			if e == nil {
				s.journalClosed = true
			}
		} else {
			s.journalClosed = true
		}
	}
	// Retain the original exclusion lock while any original child is unjoined.
	if s.inputs.Launcher != nil && !s.launcherClosed && s.allLocalExitedLocked() {
		e := s.inputs.Launcher.Close()
		result = errors.Join(result, e)
		if e == nil {
			s.launcherClosed = true
		}
	}
	return result
}
