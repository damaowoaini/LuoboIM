//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

type PreparedNamespaceRuntimeSpec struct {
	Service                    ServiceSpec
	RuntimePath, RuntimeSHA256 string
	// Required only for the same original two preparing reader processes.
	HistoryPath, HistorySHA256 string
}
type NamespaceRuntimeLaunchGroup struct {
	mu                         sync.Mutex
	hold                       *AdmissionHold
	header                     namespaceimport.Header
	dir                        string
	requirements               []NamespaceRuntimeRequirement
	validator                  NamespaceRuntimeLaunchValidator
	readers                    *HistoryReaderLaunchGroup
	entries                    map[string]NamespaceRuntimeProcess
	order                      []string
	stopping                   bool
	transferred                bool
	transfer                   *NamespaceRuntimeTransfer
	stopAttempts               map[string]bool
	startedSHA, publicationSHA string
	started                    []NamespaceRuntimeStartedObservation
}

func NewNamespaceRuntimeLaunchGroup(hold *AdmissionHold, h namespaceimport.Header, dir string, requirements []NamespaceRuntimeRequirement, validator NamespaceRuntimeLaunchValidator, readers *HistoryReaderLaunchGroup) (*NamespaceRuntimeLaunchGroup, error) {
	if hold == nil || hold.ValidateExclusion() != nil || hold.HeaderSHA256() != h.SHA256() || h.Validate() != nil || validator == nil || readers == nil {
		return nil, ErrOwnedEvidence
	}
	if _, e := NamespaceRuntimeRequirementsSHA256(requirements); e != nil {
		return nil, e
	}
	actual, e := filepath.EvalSymlinks(dir)
	if e != nil || actual != dir {
		return nil, ErrOwnedEvidence
	}
	info, e := os.Lstat(dir)
	if e != nil || !private(info, true) {
		return nil, ErrOwnedEvidence
	}
	readers.mu.Lock()
	same := readers.hold == hold && !readers.stopping && len(readers.entries) == 0
	readers.mu.Unlock()
	if !same {
		return nil, ErrOwnedEvidence
	}
	raw, e := json.Marshal(requirements)
	if e != nil {
		return nil, e
	}
	var frozen []NamespaceRuntimeRequirement
	if json.Unmarshal(raw, &frozen) != nil {
		return nil, ErrOwnedEvidence
	}
	return &NamespaceRuntimeLaunchGroup{hold: hold, header: h, dir: dir, requirements: frozen, validator: validator, readers: readers, entries: map[string]NamespaceRuntimeProcess{}}, nil
}
func (g *NamespaceRuntimeLaunchGroup) Start(ctx context.Context, spec PreparedNamespaceRuntimeSpec) (NamespaceRuntimeProcess, error) {
	return g.startWithObservation(ctx, spec, nil)
}

// Labels come only from this exact Start invocation. They cannot construct an
// owner, registration, permission or Ready capability, and never re-read a child.
func (g *NamespaceRuntimeLaunchGroup) StartWithObservation(ctx context.Context, spec PreparedNamespaceRuntimeSpec) (NamespaceRuntimeProcess, NamespaceRuntimeCallObservation, error) {
	observation := NamespaceRuntimeCallObservation{Role: "none", Predicate: "start-input"}
	process, e := g.startWithObservation(ctx, spec, &observation)
	return process, observation, e
}

func (g *NamespaceRuntimeLaunchGroup) startWithObservation(ctx context.Context, spec PreparedNamespaceRuntimeSpec, observation *NamespaceRuntimeCallObservation) (NamespaceRuntimeProcess, error) {
	var empty NamespaceRuntimeProcess
	observation.enter(spec.Service.Role, "start-input")
	if g == nil || ctx == nil || ctx.Err() != nil {
		return empty, ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	observation.enter(spec.Service.Role, "start-hold")
	if g.stopping || g.transferred || g.hold.ValidateExclusion() != nil {
		return empty, ErrOwnedEvidence
	}
	observation.enter(spec.Service.Role, "start-bootstrap")
	b, e := ReadNamespaceRuntimeBootstrap(spec.RuntimePath, spec.RuntimeSHA256)
	if e != nil || b.Header != g.header || g.validator.ValidateNamespaceRuntimeLaunch(ctx, b) != nil {
		return empty, ErrOwnedEvidence
	}
	r, s := b.Requirement, spec.Service
	selected := false
	for _, required := range g.requirements {
		a, _ := json.Marshal(required)
		z, _ := json.Marshal(r)
		selected = selected || string(a) == string(z)
	}
	observation.enter(spec.Service.Role, "start-requirement")
	if !selected || g.entries[r.Role].Service != nil || s.Role != r.Role || s.BinarySHA256 != r.BinarySHA256 || s.SourceSHA256 != r.SourceSHA256 || s.BuildReceiptSHA256 != r.BuildReceiptSHA256 || s.BootstrapSHA256 != r.BootstrapSHA256 || filepath.Dir(b.RegistrationPath) != s.Work || filepath.Dir(b.ReleasePath) != s.Work {
		return empty, ErrOwnedEvidence
	}
	if r.Role != "msgtransfer-managed" {
		observation.enter(spec.Service.Role, "start-server-environment")
		if s.BootstrapEnvironmentKey != "LUOBO_OWNED_SERVER_BOOTSTRAP_PATH" || s.BootstrapSHAEnvironmentKey != "LUOBO_OWNED_SERVER_BOOTSTRAP_SHA256" {
			return empty, ErrOwnedEvidence
		}
		var original historyReaderServerBootstrap
		observation.enter(spec.Service.Role, "start-server-bootstrap")
		sum, e := readServerEnvelope(s.BootstrapPath, &original)
		if e != nil || sum != r.BootstrapSHA256 || original.Version != NamespaceRuntimeServerBootstrapVersion || original.Role != r.Role || original.Mode != "required" || original.InstanceID != b.ExpectedInstanceID || original.Epoch != g.header.Epoch || original.ActivationSHA256 != g.header.ActivationSHA256 || original.DatabaseSHA256 != sha([]byte(g.header.Database)) || original.ConfigurationSHA256 != r.ConfigurationSHA256 || original.EnvironmentSHA256 != r.EnvironmentSHA256 || filepath.Dir(original.DrainPath) != s.Work {
			return empty, ErrOwnedEvidence
		}
	} else {
		observation.enter(spec.Service.Role, "start-managed-environment")
		if s.BootstrapEnvironmentKey != "LUOBO_ORDINARY_OWNED_RUNTIME_INPUT" || s.BootstrapSHAEnvironmentKey != "LUOBO_ORDINARY_OWNED_RUNTIME_INPUT_SHA256" {
			return empty, ErrOwnedEvidence
		}
		var original map[string]json.RawMessage
		observation.enter(spec.Service.Role, "start-managed-shape")
		sum, e := readServerEnvelope(s.BootstrapPath, &original)
		if e != nil || sum != r.BootstrapSHA256 || len(original) != 15 {
			return empty, ErrOwnedEvidence
		}
		var version, database, epoch, activation, drain string
		observation.enter(spec.Service.Role, "start-managed-fields")
		if json.Unmarshal(original["version"], &version) != nil || json.Unmarshal(original["database"], &database) != nil || json.Unmarshal(original["epoch"], &epoch) != nil || json.Unmarshal(original["activationSHA256"], &activation) != nil || json.Unmarshal(original["drainPath"], &drain) != nil || version != NamespaceRuntimeManagedBootstrapVersion || database != g.header.Database || epoch != g.header.Epoch || activation != g.header.ActivationSHA256 || filepath.Dir(drain) != s.Work {
			return empty, ErrOwnedEvidence
		}
	}
	isReader := r.Role == "rpc-msg" || r.Role == "rpc-conversation"
	if isReader {
		observation.enter(spec.Service.Role, "start-reader-input")
		if spec.HistoryPath == "" || !isSHA(spec.HistorySHA256) {
			return empty, ErrOwnedEvidence
		}
	} else {
		observation.enter(spec.Service.Role, "start-nonreader-input")
		if spec.HistoryPath != "" || spec.HistorySHA256 != "" {
			return empty, ErrOwnedEvidence
		}
	}
	intent := struct{ Version, Header, Lease, Role, RuntimeBootstrap, Binary, Source, Build, OriginalBootstrap string }{"held-namespace-runtime-launch-v1", g.header.SHA256(), g.hold.LeaseID(), r.Role, spec.RuntimeSHA256, s.BinarySHA256, s.SourceSHA256, s.BuildReceiptSHA256, s.BootstrapSHA256}
	observation.enter(spec.Service.Role, "start-intent")
	if _, e = writeServerEnvelope(filepath.Join(g.dir, r.Role+"-runtime-launch-intent.json"), intent); e != nil {
		return empty, e
	}
	var service *OwnedService
	if isReader {
		observation.enter(spec.Service.Role, "start-reader")
		p, err := g.readers.StartForNamespaceRuntime(ctx, PreparedHistoryReaderSpec{s, spec.HistoryPath, spec.HistorySHA256}, spec.RuntimePath, spec.RuntimeSHA256)
		service, e = p.Service, err
	} else {
		var startup ServiceStartupObservation
		var trace *ServiceStartupObservation
		if observation != nil {
			trace = &startup
		}
		service, e = startOwnedServiceRuntimeEnvironmentObserved(ctx, s, nil, &namespaceRuntimeExecution{spec.RuntimePath, spec.RuntimeSHA256, b}, trace)
		if observation != nil {
			observation.enter(spec.Service.Role, "start-service-"+string(startup.Stage))
		}
	}
	if service == nil {
		return empty, e
	}
	service.launcher = g.hold.launcher
	value := NamespaceRuntimeProcess{service, b}
	g.entries[r.Role] = value
	g.order = append(g.order, r.Role)
	// Preserve the original startup error label while still executing the same
	// required owner write. A later write failure must not hide that first error.
	if observation != nil && e == nil {
		observation.enter(spec.Service.Role, "start-owner-record")
	}
	if _, pe := writeServerEnvelope(filepath.Join(g.dir, r.Role+"-runtime-actual-owner.json"), service.owner); pe != nil {
		return value, errors.Join(e, pe)
	}
	if observation != nil && e == nil {
		observation.enter(spec.Service.Role, "start-complete")
	}
	return value, e
}
func (g *NamespaceRuntimeLaunchGroup) Processes() ([]NamespaceRuntimeProcess, error) {
	return g.processesWithObservation(nil)
}

func (g *NamespaceRuntimeLaunchGroup) processesWithObservation(observation *NamespaceRuntimeCallObservation) ([]NamespaceRuntimeProcess, error) {
	observation.enter("", "process-set")
	if g == nil {
		return nil, ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var rows []NamespaceRuntimeProcess
	for _, r := range g.requirements {
		observation.enter(r.Role, "process-set")
		row := g.entries[r.Role]
		if row.Service == nil {
			return nil, ErrOwnedEvidence
		}
		raw, _ := json.Marshal(row.Bootstrap)
		var b NamespaceRuntimeBootstrap
		_ = json.Unmarshal(raw, &b)
		rows = append(rows, NamespaceRuntimeProcess{row.Service, b})
	}
	return rows, nil
}
func (g *NamespaceRuntimeLaunchGroup) Observe(ctx context.Context) (*VerifiedNamespaceRuntimeGraph, error) {
	return g.observeWithObservation(ctx, nil)
}

// Returns labels from this exact call, never a second observation of the child.
// The labels cannot construct a graph, release, registration or canonical drain.
func (g *NamespaceRuntimeLaunchGroup) ObserveWithObservation(ctx context.Context) (*VerifiedNamespaceRuntimeGraph, NamespaceRuntimeCallObservation, error) {
	observation := NamespaceRuntimeCallObservation{Role: "none", Predicate: "process-set"}
	graph, e := g.observeWithObservation(ctx, &observation)
	return graph, observation, e
}

func (g *NamespaceRuntimeLaunchGroup) observeWithObservation(ctx context.Context, observation *NamespaceRuntimeCallObservation) (*VerifiedNamespaceRuntimeGraph, error) {
	rows, e := g.processesWithObservation(observation)
	if e != nil {
		return nil, e
	}
	first := rows[0].Bootstrap
	return observeNamespaceRuntimeGraph(ctx, g.hold, g.header, first.RuntimePreparationSHA256, first.InventorySHA256, first.ManifestSHA256, g.requirements, rows, observation)
}
func (g *NamespaceRuntimeLaunchGroup) OriginalReaderGroup() *HistoryReaderLaunchGroup {
	if g == nil {
		return nil
	}
	return g.readers
}

// Reads only the pinned original server bootstrap; it cannot choose an instance
// from a later announcement or a guessed requirement. Managed keeps its real
// runtime-generated UUID, subsequently bound by nonce/process observation.
func NamespaceRuntimeExpectedInstance(spec ServiceSpec) (string, error) {
	if spec.Role == "msgtransfer-managed" {
		return "", nil
	}
	var b historyReaderServerBootstrap
	sum, e := readServerEnvelope(spec.BootstrapPath, &b)
	if e != nil || sum != spec.BootstrapSHA256 || b.Version != NamespaceRuntimeServerBootstrapVersion || b.Role != spec.Role || b.Mode != "required" || !uuidPattern.MatchString(b.InstanceID) {
		return "", ErrOwnedEvidence
	}
	return b.InstanceID, nil
}
