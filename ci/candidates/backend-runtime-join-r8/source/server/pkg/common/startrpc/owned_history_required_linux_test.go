//go:build linux

package startrpc

import (
	"context"
	"encoding/json"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"google.golang.org/grpc"
)

// A valid original required-mode owned bootstrap without reader evidence must
// fail BEFORE the actual owned Start can open listeners/discovery/resources.
// The explicit old lifecycle seam is not accepted as a production reader.
func TestOwnedRequiredHistoryRolesCannotFallBackToUnboundStores(t *testing.T) {
	for _, role := range []string{"rpc-conversation", "rpc-msg"} {
		t.Run(role+" missing private preparation rejects before resources", func(t *testing.T) {
			input, _ := preparedHistoryRPCFixture(t)
			input.boot.Role = role
			raw, e := json.Marshal(input.boot)
			if e != nil {
				t.Fatal(e)
			}
			raw = append(raw, '\n')
			if e = os.WriteFile(input.sourcePath, raw, 0600); e != nil {
				t.Fatal(e)
			}
			t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(raw))
			input, e = loadOwnedRPCInput(role)
			if e != nil {
				t.Fatal("full original owned input is invalid", e)
			}
			t.Setenv(HistoryReaderPreparationEnv, "")
			t.Setenv(HistoryReaderPreparationSHAEnv, "")
			if state, e := parseOwnedHistoryStartup(input); e == nil || state != nil {
				t.Fatal("owned required role accepted a nil private binding")
			}
			called := atomic.Bool{}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			register := func(context.Context, discoveryregistry.SvcDiscoveryRegistry, *grpc.Server) (ManagedStop, error) {
				called.Store(true)
				return nil, nil
			}
			registry := func(context.Context, string) (kdisc.OwnedRegistry, error) {
				called.Store(true)
				return &ownedRPCRegistryFixture{}, nil
			}
			if e = startOwnedRPC(ctx, 0, "must-not-start-unbound", 0, input, register, registry); e == nil || called.Load() {
				t.Fatal("required reader reached discovery/factory without validated preparation", e)
			}
			if _, e = os.Lstat(input.path); !os.IsNotExist(e) {
				t.Fatal("unbound required role wrote a runtime announcement")
			}
		})
	}
	t.Run("sealed required scope cannot lose its private binding", func(t *testing.T) {
		t.Setenv(HistoryReaderPreparationEnv, "")
		t.Setenv(HistoryReaderPreparationSHAEnv, "")
		server := grpc.NewServer()
		defer server.Stop()
		ownedRPCScopes.Store(server, &ownedRPCScope{role: "rpc-msg", mode: "required", sealed: true})
		defer ownedRPCScopes.Delete(server)
		if _, _, e := OwnedRPCHistoryPreparation(server); e == nil {
			t.Fatal("required scope silently chose the nil Store path")
		}
		if e := bindOwnedHistoryAndAwaitReadiness(context.Background(), server, ownedRPCInput{}, "", ""); e == nil {
			t.Fatal("missing registered state bypassed binding before Serve")
		}
	})
	t.Run("legacy and unowned default paths remain explicit", func(t *testing.T) {
		t.Setenv(HistoryReaderPreparationEnv, "")
		t.Setenv(HistoryReaderPreparationSHAEnv, "")
		input := ownedRPCInput{boot: OwnedRPCBootstrap{Role: "rpc-conversation", Mode: "legacy"}}
		if state, e := parseOwnedHistoryStartup(input); e != nil || state != nil {
			t.Fatal("complete legacy default behavior changed", e)
		}
		server := grpc.NewServer()
		defer server.Stop()
		if path, sum, e := OwnedRPCHistoryPreparation(server); e != nil || path != "" || sum != "" {
			t.Fatal("unowned default reader behavior changed", e)
		}
		ownedRPCScopes.Store(server, &ownedRPCScope{role: "rpc-conversation", mode: "legacy"})
		defer ownedRPCScopes.Delete(server)
		if path, sum, e := OwnedRPCHistoryPreparation(server); e != nil || path != "" || sum != "" {
			t.Fatal("owned legacy behavior changed", e)
		}
		if state, e := parseOwnedHistoryStartup(ownedRPCInput{boot: OwnedRPCBootstrap{Role: "rpc-user", Mode: "required"}}); e != nil || state != nil {
			t.Fatal("unrelated role acquired a reader-specific startup gate", e)
		}
	})
}
