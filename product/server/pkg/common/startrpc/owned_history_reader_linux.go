//go:build linux

package startrpc

import (
	"context"
	"os"
	"sort"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"google.golang.org/grpc"
)

const HistoryReaderPreparationEnv = "LUOBO_HISTORY_READER_PREPARATION_PATH"
const HistoryReaderPreparationSHAEnv = "LUOBO_HISTORY_READER_PREPARATION_SHA256"

// The concrete MD binding captures the exact actual reader/DB/known private
// preparation. This interface is in the lower startup package to avoid a
// messagedelivery -> startrpc -> messagedelivery import cycle.
type OwnedHistoryReaderBinding interface {
	RecordRegistration(context.Context, string, string, string, string, string, []string) (ownedcontrol.HistoryReaderRegistration, error)
	AwaitReadReady(context.Context, string) error
}
type ownedHistoryStartup struct {
	mu                  sync.Mutex
	path, sha           string
	bootstrap           ownedcontrol.HistoryReaderBootstrap
	binding             OwnedHistoryReaderBinding
	finalCheck          func(context.Context) error
	registrationWritten bool
}

var ownedHistoryStartups sync.Map // exact original *grpc.Server only

func historyReaderRole(role string) bool { return role == "rpc-msg" || role == "rpc-conversation" }

func parseOwnedHistoryStartup(input ownedRPCInput) (*ownedHistoryStartup, error) {
	path, sum := os.Getenv(HistoryReaderPreparationEnv), os.Getenv(HistoryReaderPreparationSHAEnv)
	if path == "" && sum == "" {
		if input.boot.Mode == "required" && historyReaderRole(input.boot.Role) {
			return nil, errOwnedRPC
		}
		return nil, nil
	}
	if path == "" || !ownedHash(sum) || input.boot.Mode != "required" || input.boot.Role != "rpc-msg" && input.boot.Role != "rpc-conversation" {
		return nil, errOwnedRPC
	}
	b, e := ownedcontrol.ReadHistoryReaderBootstrap(path, sum)
	if e != nil || b.Requirement.Role != input.boot.Role || b.Requirement.BootstrapSHA256 != input.rawSHA || b.Requirement.ConfigurationSHA256 != input.configSHA || b.Requirement.EnvironmentSHA256 != input.environmentSHA || b.Header.Epoch != input.boot.Epoch || b.Header.ActivationSHA256 != input.boot.ActivationSHA256 || ownedSHA([]byte(b.Header.Database)) != input.boot.DatabaseSHA256 {
		return nil, errOwnedRPC
	}
	return &ownedHistoryStartup{path: path, sha: sum, bootstrap: b}, nil
}

// Factories obtain preparation input from the actual validated OWNED scope,
// never directly from ambient env. Half-on/unowned/private-role confusion fails.
func OwnedRPCHistoryPreparation(server *grpc.Server) (path, sum string, err error) {
	scope := ownedScope(server)
	if value, ok := ownedHistoryStartups.Load(server); ok {
		if scope == nil {
			return "", "", errOwnedRPC
		}
		state := value.(*ownedHistoryStartup)
		if scope.role != state.bootstrap.Requirement.Role {
			return "", "", errOwnedRPC
		}
		return state.path, state.sha, nil
	}
	if scope != nil && scope.mode == "required" && historyReaderRole(scope.role) {
		return "", "", errOwnedRPC
	}
	if os.Getenv(HistoryReaderPreparationEnv) != "" || os.Getenv(HistoryReaderPreparationSHAEnv) != "" {
		return "", "", errOwnedRPC
	}
	return "", "", nil
}

func OwnRPCHistoryReader(server *grpc.Server, binding OwnedHistoryReaderBinding) error {
	scope := ownedScope(server)
	value, ok := ownedHistoryStartups.Load(server)
	if scope == nil || !ok || binding == nil {
		return errOwnedRPC
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.sealed {
		return errOwnedRPC
	}
	state := value.(*ownedHistoryStartup)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.binding != nil || state.registrationWritten || state.bootstrap.Requirement.Role != scope.role {
		return errOwnedRPC
	}
	state.binding = binding
	return nil
}

// Prepared msg constructors still retain their actual permanent-writer gate.
// Skipping its pre-registration call does NOT activate a writer: this strict
// callback is repeated after ReadReady and before discovery/Serve/admission.
func OwnRPCPreparedWriterFinalCheck(server *grpc.Server, check func(context.Context) error) error {
	value, ok := ownedHistoryStartups.Load(server)
	if !ok || check == nil {
		return errOwnedRPC
	}
	scope := ownedScope(server)
	if scope == nil {
		return errOwnedRPC
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.sealed {
		return errOwnedRPC
	}
	state := value.(*ownedHistoryStartup)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.finalCheck != nil {
		return errOwnedRPC
	}
	state.finalCheck = check
	return nil
}

func bindOwnedHistoryAndAwaitReadiness(ctx context.Context, server *grpc.Server, input ownedRPCInput, configurationSHA, environmentSHA string) error {
	value, ok := ownedHistoryStartups.Load(server)
	scope := ownedScope(server)
	if !ok {
		if scope != nil && scope.mode == "required" && historyReaderRole(scope.role) {
			return errOwnedRPC
		}
		return nil
	}
	state := value.(*ownedHistoryStartup)
	if scope == nil || !scope.sealed || ctx == nil {
		return errOwnedRPC
	}
	state.mu.Lock()
	binding, final, already := state.binding, state.finalCheck, state.registrationWritten
	state.mu.Unlock()
	if binding == nil || already || final == nil {
		return errOwnedRPC
	}
	var names []string
	for name := range server.GetServiceInfo() {
		names = append(names, name)
	}
	sort.Strings(names)
	registration, e := binding.RecordRegistration(ctx, input.boot.InstanceID, input.rawSHA, configurationSHA, environmentSHA, input.nonce, names)
	if e != nil || registration.Role != scope.role || registration.HeaderSHA256 != state.bootstrap.Header.SHA256() {
		return errOwnedRPC
	}
	state.mu.Lock()
	state.registrationWritten = true
	state.mu.Unlock()
	// At this point TCP listen may exist, but neither discovery.Register nor
	// server.Serve has run. No HTTP/gRPC handler can consume the preparation.
	if e = binding.AwaitReadReady(ctx, input.nonce); e != nil {
		return e
	}

	return nil
}

// Retain the original writer check after actual full-graph admission. The data
// phase above cannot open discovery/Serve or turn DataReady into write Ready.
func awaitOwnedHistoryWriterFinal(ctx context.Context, server *grpc.Server) error {
	value, ok := ownedHistoryStartups.Load(server)
	if !ok {
		return nil
	}
	state := value.(*ownedHistoryStartup)
	state.mu.Lock()
	final, registered := state.finalCheck, state.registrationWritten
	state.mu.Unlock()
	if !registered || final == nil {
		return errOwnedRPC
	}
	return final(ctx)
}
