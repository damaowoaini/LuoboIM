//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Explicit, narrow held-start exception. Launcher.Start still refuses EVERY
// ordinary start while HELD. This group accepts only source-pinned reader
// binaries carrying the private prepare protocol, and those RPCs cannot Serve
// until their exact registration is in the actual readiness publication.
// It never puts these replacement processes in the retired legacy writer set.
type PreparedHistoryReaderSpec struct {
	Service                            ServiceSpec
	PreparationPath, PreparationSHA256 string
}
type historyReaderExecution struct {
	path, sha string
	bootstrap HistoryReaderBootstrap
}
type historyReaderServerBootstrap struct {
	Version             string `json:"version"`
	Role                string `json:"role"`
	Mode                string `json:"mode"`
	InstanceID          string `json:"instanceID"`
	DatabaseSHA256      string `json:"databaseSHA256"`
	Epoch               string `json:"epoch"`
	ActivationSHA256    string `json:"activationSHA256"`
	ConfigurationSHA256 string `json:"configurationSHA256"`
	EnvironmentSHA256   string `json:"environmentSHA256"`
	DrainPath           string `json:"drainPath"`
}

func validateHistoryReaderServerBootstrap(spec ServiceSpec, b HistoryReaderBootstrap) error {
	return validateHistoryReaderServerBootstrapForRuntime(spec, b, false)
}
func validateHistoryReaderServerBootstrapForRuntime(spec ServiceSpec, b HistoryReaderBootstrap, runtime bool) error {
	var actual historyReaderServerBootstrap
	sum, e := readServerEnvelope(spec.BootstrapPath, &actual)
	version := "owned-server-bootstrap-v1"
	if runtime {
		version = NamespaceRuntimeServerBootstrapVersion
	}
	if e != nil || sum != spec.BootstrapSHA256 || actual.Version != version || actual.Role != spec.Role || actual.Mode != "required" || !uuidPattern.MatchString(actual.InstanceID) || actual.DatabaseSHA256 != sha([]byte(b.Header.Database)) || actual.Epoch != b.Header.Epoch || actual.ActivationSHA256 != b.Header.ActivationSHA256 || actual.ConfigurationSHA256 != b.Requirement.ConfigurationSHA256 || actual.EnvironmentSHA256 != b.Requirement.EnvironmentSHA256 || filepath.Dir(actual.DrainPath) != spec.Work {
		return ErrOwnedEvidence
	}
	return nil
}

type HistoryReaderLaunchValidator interface {
	ValidateHistoryReaderLaunch(context.Context, HistoryReaderBootstrap) error
}
type HistoryReaderLaunchGroup struct {
	mu           sync.Mutex
	hold         *AdmissionHold
	dir          string
	requirements []HistoryReaderRequirement
	validator    HistoryReaderLaunchValidator
	entries      map[string]HistoryReaderProcess
	order        []string
	stopping     bool
}

func NewHistoryReaderLaunchGroup(hold *AdmissionHold, dir string, requirements []HistoryReaderRequirement, validator HistoryReaderLaunchValidator) (*HistoryReaderLaunchGroup, error) {
	if hold == nil || hold.ValidateExclusion() != nil || validator == nil {
		return nil, ErrOwnedEvidence
	}
	if _, e := HistoryReaderRequirementsSHA256(requirements); e != nil {
		return nil, e
	}
	canonical, e := filepath.EvalSymlinks(dir)
	if e != nil || canonical != dir {
		return nil, ErrOwnedEvidence
	}
	info, e := os.Lstat(dir)
	if e != nil || !private(info, true) {
		return nil, ErrOwnedEvidence
	}
	body, e := json.Marshal(requirements)
	if e != nil {
		return nil, e
	}
	var copied []HistoryReaderRequirement
	if json.Unmarshal(body, &copied) != nil {
		return nil, ErrOwnedEvidence
	}
	return &HistoryReaderLaunchGroup{hold: hold, dir: dir, requirements: copied, validator: validator, entries: map[string]HistoryReaderProcess{}}, nil
}
func (g *HistoryReaderLaunchGroup) Start(ctx context.Context, spec PreparedHistoryReaderSpec) (HistoryReaderProcess, error) {
	return g.start(ctx, spec, nil)
}
func (g *HistoryReaderLaunchGroup) StartForNamespaceRuntime(ctx context.Context, spec PreparedHistoryReaderSpec, path, sum string) (HistoryReaderProcess, error) {
	b, e := ReadNamespaceRuntimeBootstrap(path, sum)
	if e != nil {
		return HistoryReaderProcess{}, e
	}
	return g.start(ctx, spec, &namespaceRuntimeExecution{path, sum, b})
}
func (g *HistoryReaderLaunchGroup) start(ctx context.Context, spec PreparedHistoryReaderSpec, runtime *namespaceRuntimeExecution) (HistoryReaderProcess, error) {
	var empty HistoryReaderProcess
	if g == nil || ctx == nil || ctx.Err() != nil {
		return empty, ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stopping || g.hold.ValidateExclusion() != nil {
		return empty, ErrOwnedEvidence
	}
	b, e := ReadHistoryReaderBootstrap(spec.PreparationPath, spec.PreparationSHA256)
	if e != nil || b.Header.SHA256() != g.hold.HeaderSHA256() {
		return empty, ErrOwnedEvidence
	}
	if e = g.validator.ValidateHistoryReaderLaunch(ctx, b); e != nil {
		return empty, e
	}
	if runtime != nil {
		validator, ok := g.validator.(NamespaceRuntimeLaunchValidator)
		if !ok || runtime.bootstrap.Header != b.Header || validator.ValidateNamespaceRuntimeLaunch(ctx, runtime.bootstrap) != nil {
			return empty, ErrOwnedEvidence
		}
	}
	selected := false
	for _, r := range g.requirements {
		left, _ := json.Marshal(r)
		right, _ := json.Marshal(b.Requirement)
		if string(left) == string(right) {
			selected = true
		}
	}
	s := spec.Service
	r := b.Requirement
	if !selected || g.entries[r.Role].Service != nil || s.Role != r.Role || s.BinarySHA256 != r.BinarySHA256 || s.SourceSHA256 != r.SourceSHA256 || s.BuildReceiptSHA256 != r.BuildReceiptSHA256 || s.BootstrapSHA256 != r.BootstrapSHA256 || filepath.Dir(b.BindingPath) != s.Work || s.BootstrapEnvironmentKey != "LUOBO_OWNED_SERVER_BOOTSTRAP_PATH" || s.BootstrapSHAEnvironmentKey != "LUOBO_OWNED_SERVER_BOOTSTRAP_SHA256" {
		return empty, ErrOwnedEvidence
	}
	if e = validateHistoryReaderServerBootstrapForRuntime(s, b, runtime != nil); e != nil {
		return empty, e
	}
	// Reserve the role before exec. Any partial/unknown launch is inspect-only;
	// a new caller cannot repeat Start over an existing intent or adopt a PID.
	intent := struct{ Version, Header, Lease, Preparation, Role, Binary, Source, Build, ServerBootstrap, ReaderBootstrap string }{"held-reader-launch-v1", b.Header.SHA256(), g.hold.LeaseID(), b.PreparationSHA256, s.Role, s.BinarySHA256, s.SourceSHA256, s.BuildReceiptSHA256, s.BootstrapSHA256, spec.PreparationSHA256}
	if _, e = privateJSON(filepath.Join(g.dir, s.Role+"-launch-intent.json"), intent); e != nil {
		return empty, e
	}
	service, e := startOwnedServiceRuntimeEnvironment(ctx, s, &historyReaderExecution{spec.PreparationPath, spec.PreparationSHA256, b}, runtime)
	if service != nil {
		service.launcher = g.hold.launcher
		value := HistoryReaderProcess{service, b}
		g.entries[s.Role] = value
		g.order = append(g.order, s.Role)
		if _, pe := privateJSON(filepath.Join(g.dir, s.Role+"-actual-owner.json"), service.owner); pe != nil {
			return value, errors.Join(e, pe)
		}
		return value, e
	}
	return empty, e
}
func (g *HistoryReaderLaunchGroup) Observe(ctx context.Context) (*VerifiedHistoryReaderGraph, error) {
	if g == nil {
		return nil, ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	var rows []HistoryReaderProcess
	for _, r := range g.requirements {
		p := g.entries[r.Role]
		if p.Service == nil {
			return nil, ErrOwnedEvidence
		}
		rows = append(rows, p)
	}
	first := rows[0].Bootstrap
	return ObserveHistoryReaderGraph(ctx, g.hold, first.Header, first.PreparationSHA256, first.InventorySHA256, first.ManifestSHA256, g.requirements, rows)
}

// This joins actual replacement children. The exit record explicitly is NOT a
// legacy-writer drain, remote-effect ACK, namespace permission or Ready token.
func (g *HistoryReaderLaunchGroup) Stop(ctx context.Context) error {
	if g == nil || ctx == nil {
		return ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.stopping = true
	for at := len(g.order) - 1; at >= 0; at-- {
		role := g.order[at]
		s := g.entries[role].Service
		if s == nil {
			return ErrOwnedEvidence
		}
		s.mu.Lock()
		owner, cmd, done := s.owner, s.cmd, s.done
		s.mu.Unlock()
		if cmd == nil || cmd.Process == nil {
			return ErrOwnedEvidence
		}
		select {
		case <-done:
		default:
			current, e := processIdentity(owner.PID)
			if e != nil || !sameProcess(current, owner) || actualImage(owner.PID, owner.Executable, owner.BinarySHA256) != nil {
				return ErrOwnedEvidence
			}
			if e = cmd.Process.Signal(syscall.SIGTERM); e != nil {
				return e
			}
			select {
			case <-done:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if _, e := processIdentity(owner.PID); e == nil {
			return ErrOwnedEvidence
		}
		if s.waitErr != nil {
			return errors.Join(ErrOwnedEvidence, s.waitErr)
		}
		path := filepath.Join(g.dir, role+"-prepared-child-exit.json")
		type exitRecord struct {
			Version          string
			Owner            ProcessOwner
			ObservedAbsentAt time.Time
			Claim            string
		}
		if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
			if _, e = privateJSON(path, exitRecord{"prepared-reader-child-exit-v1", owner, time.Now().UTC(), "local-child-joined-only-not-legacy-drain"}); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else {
			var saved exitRecord
			if _, e = readServerEnvelope(path, &saved); e != nil || saved.Version != "prepared-reader-child-exit-v1" || saved.Owner != owner || saved.ObservedAbsentAt.IsZero() || saved.ObservedAbsentAt.After(time.Now()) || saved.Claim != "local-child-joined-only-not-legacy-drain" {
				return ErrOwnedEvidence
			}
		}
	}
	return nil
}
