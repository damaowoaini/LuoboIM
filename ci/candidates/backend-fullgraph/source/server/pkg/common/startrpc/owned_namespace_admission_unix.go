//go:build linux || darwin

package startrpc

import (
	"context"
	"os"
	"sort"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"google.golang.org/grpc"
)

// The implementation lives beside the actual namespace Store/DAO. The lower
// startup owner never constructs one from a JSON identity or database name.
type OwnedNamespaceRuntimeBinding interface {
	BindRuntimeContext(context.Context) context.Context
	MatchesNamespaceRuntimeGate(*ownedcontrol.NamespaceRuntimeStartupGate) bool
	SealNamespaceBindings(context.Context, []string, ownedmongo.Snapshot) (ownedcontrol.NamespaceRuntimeBindings, error)
	ValidateRuntimeStores(context.Context) error
}
type ownedNamespaceStartup struct {
	gate        *ownedcontrol.NamespaceRuntimeStartupGate
	mu          sync.Mutex
	binding     OwnedNamespaceRuntimeBinding
	registered  bool
	activations []func() error
}

var ownedNamespaceStartups sync.Map // original exact *grpc.Server only

func parseOwnedNamespaceStartup(input ownedRPCInput) (*ownedNamespaceStartup, error) {
	path, sum := os.Getenv(ownedcontrol.NamespaceRuntimePreparationPathEnv), os.Getenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv)
	if input.boot.Mode == "legacy" {
		if path != "" || sum != "" || input.boot.Version != "owned-server-bootstrap-v1" {
			return nil, errOwnedRPC
		}
		return nil, nil
	}
	if input.boot.Mode != "required" || input.boot.Version != ownedcontrol.NamespaceRuntimeServerBootstrapVersion || path == "" || !ownedHash(sum) {
		return nil, errOwnedRPC
	}
	if e := checkOwnedNamespacePlatform(); e != nil {
		return nil, e
	}
	gate, e := ownedcontrol.NewNamespaceRuntimeStartupGate(path, sum, input.boot.Role, input.boot.InstanceID, input.rawSHA, input.configSHA, input.environmentSHA, input.nonce)
	if e != nil {
		return nil, e
	}
	return &ownedNamespaceStartup{gate: gate}, nil
}
func OwnRPCNamespaceRuntimeBinding(server *grpc.Server, binding OwnedNamespaceRuntimeBinding) error {
	scope := ownedScope(server)
	value, ok := ownedNamespaceStartups.Load(server)
	if scope == nil || !ok || binding == nil {
		return errOwnedRPC
	}
	state := value.(*ownedNamespaceStartup)
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.sealed || !binding.MatchesNamespaceRuntimeGate(state.gate) {
		return errOwnedRPC
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.binding != nil || state.registered {
		return errOwnedRPC
	}
	state.binding = binding
	scope.workers.ctx = binding.BindRuntimeContext(scope.workers.ctx)
	return nil
}
func ownedNamespaceComponents(scope *ownedRPCScope, bindings ownedcontrol.NamespaceRuntimeBindings) []string {
	set := map[string]bool{"rpc-handlers": true, "source-ownership": true}
	for _, local := range bindings.Local {
		switch local.Kind {
		case "namespace-store":
			set["namespace-writer"] = true
		case "history-reader":
			set["history-reader"] = true
		case "frozen-online-runtime":
			set["online-runtime"] = true
		case "frozen-offline-runtime":
			set["offline-runtime"] = true
		case "frozen-group-runtime":
			set["receiver-runtime"] = true
		case "retained-callback-provider":
			set["provider-callbacks"] = true
		}
	}
	for _, name := range bindings.Resources {
		if name == "kafka-producers" {
			set["kafka-producer"] = true
		}
		if name == "websocket-runtime" {
			set["websocket-runtime"] = true
		}
		if name == "object-provider" || name == "callback-http" {
			set["provider-callbacks"] = true
		}
		if name == "sensitive-word-workers" {
			set["sensitive-word-workers"] = true
		}
	}
	if scope.admission != nil {
		set[scope.admission.name] = true
	}
	var out []string
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
func bindOwnedNamespaceAndAwaitAdmission(ctx context.Context, server *grpc.Server, input ownedRPCInput) error {
	value, ok := ownedNamespaceStartups.Load(server)
	if !ok {
		if input.boot.Mode == "required" {
			return errOwnedRPC
		}
		return nil
	}
	state, scope := value.(*ownedNamespaceStartup), ownedScope(server)
	if scope == nil || !scope.sealed || ctx == nil || scope.mongoSource == nil {
		return errOwnedRPC
	}
	state.mu.Lock()
	binding, already := state.binding, state.registered
	state.mu.Unlock()
	if already {
		return errOwnedRPC
	}
	needsDAO := scope.role == "rpc-msg" || scope.role == "rpc-conversation" || scope.role == "rpc-group"
	if needsDAO && binding == nil {
		return errOwnedRPC
	}
	resources := scope.resourceNames()
	sort.Strings(resources)
	observation := scope.mongoSource.Snapshot()
	if observation.SourceOutcomeUnconfirmed || observation.External.Unconfirmed() || observation.Overflow || observation.InFlight != 0 {
		return errOwnedRPC
	}
	var bindings ownedcontrol.NamespaceRuntimeBindings
	var e error
	if binding != nil {
		bindings, e = binding.SealNamespaceBindings(ctx, resources, observation)
		if e != nil {
			return e
		}
	} else {
		bindings = ownedcontrol.NamespaceRuntimeBindings{Role: scope.role, HeaderSHA256: state.gate.Bootstrap().Header.SHA256(), Resources: resources, Source: observation}
	}
	components := ownedNamespaceComponents(scope, bindings)
	var services []string
	for name := range server.GetServiceInfo() {
		services = append(services, name)
	}
	sort.Strings(services)
	if _, e = state.gate.RecordPrepared(ctx, services, components, bindings); e != nil {
		return e
	}
	state.mu.Lock()
	state.registered = true
	state.mu.Unlock()
	if e = state.gate.AwaitAdmission(ctx); e != nil {
		return e
	}
	if binding != nil {
		return binding.ValidateRuntimeStores(ctx)
	}
	return nil
}

func OwnRPCNamespaceActivation(server *grpc.Server, start func() error) error {
	scope := ownedScope(server)
	v, ok := ownedNamespaceStartups.Load(server)
	if scope == nil || !ok || start == nil {
		return errOwnedRPC
	}
	state := v.(*ownedNamespaceStartup)
	scope.mu.Lock()
	defer scope.mu.Unlock()
	state.mu.Lock()
	defer state.mu.Unlock()
	if scope.sealed || state.registered {
		return errOwnedRPC
	}
	state.activations = append(state.activations, start)
	return nil
}
func activateOwnedNamespaceResources(ctx context.Context, server *grpc.Server) error {
	v, ok := ownedNamespaceStartups.Load(server)
	if !ok {
		return nil
	}
	state := v.(*ownedNamespaceStartup)
	if _, _, e := state.gate.KnownPublication(); e != nil {
		return e
	}
	state.mu.Lock()
	starts := append([]func() error(nil), state.activations...)
	state.activations = nil
	state.mu.Unlock()
	for _, start := range starts {
		if e := ctx.Err(); e != nil {
			return e
		}
		if e := start(); e != nil {
			return e
		}
	}
	return nil
}
