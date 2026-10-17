package startrpc

import (
	"context"
	"encoding/json"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestOwnedPushActualOwnerClosesOriginalResourcesBeforeRedisUnknownNonpermit(t *testing.T) {
	for _, kind := range []string{"ack", "redis-unknown", "required-token-only"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "required-token-only" {
				if fullgraphRequiredTokenRunner == nil {
					t.Fatal("mandatory Linux real fullgraph fixture not linked")
				}
				fullgraphRequiredTokenRunner(t)
				return
			}
			input := ownedRPCInputFixture(t)
			input.boot.Role = "push"
			raw, err := json.Marshal(input.boot)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			if err := os.WriteFile(input.sourcePath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(raw))
			input, err = loadOwnedRPCInput("push")
			if err != nil {
				t.Fatal(err)
			}
			registry := &ownedRPCRegistryFixture{}
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			var mu sync.Mutex
			var order []string
			record := func(name string) { mu.Lock(); order = append(order, name); mu.Unlock() }
			result, finished := make(chan error, 1), make(chan struct{})
			register := func(ctx context.Context, _ discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
				if OwnedRPCMode(server) != input.boot.Mode {
					t.Error("original mode not bound")
				}
				healthpb.RegisterHealthServer(server, health.NewServer())
				names := []string{"push-consumer", "push-provider", "callback-http", "redis"}
				for _, name := range names {
					name := name
					if err := OwnRPCResource(server, name, func(context.Context) error {
						record(name)
						if name == "redis" && kind == "redis-unknown" {
							ownedsource.Attempt(ctx, ownedsource.RedisMutation)(true)
							ownedsource.Attempt(ctx, ownedsource.RedisMutation)(false)
						}
						return nil
					}); err != nil {
						return nil, err
					}
				}
				admissionErr := OwnRPCPushAdmission(server, func() error { record("activate"); return nil }, func(context.Context) error { record("admission"); return nil }, func() error { return nil })
				return nil, admissionErr
			}
			go func() {
				defer close(finished)
				result <- startOwnedRPC(parent, 0, "owned-push-fixture", 0, input, register, func(context.Context, string) (kdisc.OwnedRegistry, error) { return registry, nil })
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("original owner leaked")
				}
			})
			ready := awaitOwnedRPCAnnouncement(t, input)
			expectedFirst := "push-admission"
			if ready.Role != "push" || ready.OwnedStopStepNames[0] != expectedFirst {
				t.Fatal("actual admission missing from ready")
			}
			cancel()
			var outcome error
			select {
			case outcome = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("original owner did not join")
			}
			mu.Lock()
			actual := append([]string{}, order...)
			mu.Unlock()
			expectedOrder := []string{"activate", "admission", "push-consumer", "push-provider", "callback-http", "redis"}
			if !reflect.DeepEqual(actual, expectedOrder) || registry.closes.Load() != 1 {
				t.Fatal("actual close order or discovery ownership differs")
			}
			if kind != "redis-unknown" {
				if outcome != nil {
					t.Fatal(outcome)
				}
				if _, _, err := ownedcontrol.ReadServerRuntimeDrain(input.boot.DrainPath, input.nonce); err != nil {
					t.Fatal(err)
				}
			} else {
				if outcome == nil {
					t.Fatal("unknown source got successful owner outcome")
				}
				if _, err := os.Lstat(input.boot.DrainPath); !os.IsNotExist(err) {
					t.Fatal("unknown Redis source got canonical drain")
				}
				body, err := os.ReadFile(input.boot.DrainPath + ".unconfirmed")
				if err != nil {
					t.Fatal(err)
				}
				var envelope struct {
					OwnerNonce  string                         `json:"ownerNonce"`
					Observation ownedcontrol.SourceObservation `json:"observation"`
				}
				if json.Unmarshal(body, &envelope) != nil || envelope.OwnerNonce != input.nonce || envelope.Observation.BootstrapSHA256 != input.rawSHA || envelope.Observation.PolicySHA256 == "" || !envelope.Observation.External.RedisMutation.Unknown {
					t.Fatal("unknown source custody borrowed another owner")
				}
			}
		})
	}
}
