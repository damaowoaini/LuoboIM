package startrpc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

func TestOwnedGatewayActualRPCOwnerStopsSocketAdmissionBeforeHandlersAndPools(t *testing.T) {
	for _, kind := range []string{"normal", "listener-failure"} {
		t.Run(kind, func(t *testing.T) {
			var failed atomic.Bool
			input := ownedRPCInputFixture(t)
			input.boot.Role = "msggateway"
			raw, err := json.Marshal(input.boot)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			if err := os.WriteFile(input.sourcePath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(raw))
			input, err = loadOwnedRPCInput("msggateway")
			if err != nil {
				t.Fatal(err)
			}
			registry := &ownedRPCRegistryFixture{}
			var mu sync.Mutex
			var order []string
			record := func(name string) { mu.Lock(); order = append(order, name); mu.Unlock() }
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result, finished := make(chan error, 1), make(chan struct{})
			register := func(_ context.Context, _ discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
				healthpb.RegisterHealthServer(server, health.NewServer())
				if err := OwnRPCDatabase(server, config.Config.Mongo.Database); err != nil {
					return nil, err
				}
				for _, name := range []string{"websocket-runtime", "callback-http", "redis"} {
					name := name
					if err := OwnRPCResource(server, name, func(context.Context) error { record(name); return nil }); err != nil {
						return nil, err
					}
				}
				return nil, OwnRPCGatewayAdmission(server, func() error { record("activate"); return nil }, func(context.Context) error { record("admission"); return nil }, func() error {
					if failed.Load() {
						return errors.New("actual websocket Serve failed")
					}
					return nil
				})
			}
			go func() {
				defer close(finished)
				result <- startOwnedRPC(ctx, 0, "gateway-fixture", 0, input, register, func(context.Context, string) (kdisc.OwnedRegistry, error) { return registry, nil })
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("original gateway RPC owner remained active")
				}
			})
			ready := awaitOwnedRPCAnnouncement(t, input)
			if len(ready.OwnedStopStepNames) == 0 || ready.OwnedStopStepNames[0] != "websocket-admission" {
				t.Fatal("ready omitted real admission shutdown")
			}
			if kind == "listener-failure" {
				failed.Store(true)
			}
			cancel()
			select {
			case err := <-result:
				if (err != nil) != (kind == "listener-failure") {
					t.Fatal("actual owner lost runtime failure", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("original owner did not join")
			}
			if kind == "listener-failure" {
				if _, err := os.Lstat(input.boot.DrainPath); !os.IsNotExist(err) {
					t.Fatal("unexpected actual Serve failure emitted canonical drain", err)
				}
			} else {
				drain, _, err := ownedcontrol.ReadServerRuntimeDrain(input.boot.DrainPath, input.nonce)
				if err != nil || !reflect.DeepEqual(drain.JoinedAndClosed, ready.OwnedStopStepNames) {
					t.Fatal("drain did not bind actual completed steps", err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(order, []string{"activate", "admission", "websocket-runtime", "callback-http", "redis"}) || registry.closes.Load() != 1 {
				t.Fatal("gateway used fabricated pools or closed resources before socket work", order)
			}
		})
	}
}
