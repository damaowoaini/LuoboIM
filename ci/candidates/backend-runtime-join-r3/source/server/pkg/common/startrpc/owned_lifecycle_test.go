package startrpc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type ownedRPCRegistryFixture struct{ closes atomic.Int32 }

func (*ownedRPCRegistryFixture) GetConns(context.Context, string, ...grpc.DialOption) ([]*grpc.ClientConn, error) {
	return nil, errors.New("unexpected external dial")
}
func (*ownedRPCRegistryFixture) GetConn(context.Context, string, ...grpc.DialOption) (*grpc.ClientConn, error) {
	return nil, errors.New("unexpected external dial")
}
func (*ownedRPCRegistryFixture) GetSelfConnTarget() string                          { return "" }
func (*ownedRPCRegistryFixture) AddOption(...grpc.DialOption)                       {}
func (*ownedRPCRegistryFixture) CloseConn(*grpc.ClientConn)                         {}
func (*ownedRPCRegistryFixture) GetClientLocalConns() map[string][]*grpc.ClientConn { return nil }
func (*ownedRPCRegistryFixture) GetUserIdHashGatewayHost(context.Context, string) (string, error) {
	return "", nil
}
func (*ownedRPCRegistryFixture) Register(string, string, int, ...grpc.DialOption) error { return nil }
func (*ownedRPCRegistryFixture) UnRegister() error                                      { return nil }
func (*ownedRPCRegistryFixture) CreateRpcRootNodes([]string) error                      { return nil }
func (*ownedRPCRegistryFixture) RegisterConf2Registry(string, []byte) error             { return nil }
func (*ownedRPCRegistryFixture) GetConfFromRegistry(string) ([]byte, error)             { return nil, nil }
func (r *ownedRPCRegistryFixture) Close()                                               { r.closes.Add(1) }
func (r *ownedRPCRegistryFixture) CloseOwned(context.Context) error                     { r.Close(); return nil }

func ownedRPCInputFixture(t *testing.T) ownedRPCInput {
	t.Helper()
	previous := config.Config
	t.Cleanup(func() { config.Config = previous })
	config.Config.Mongo.Database = "owned-rpc-fixture"
	config.Config.Rpc.ListenIP = "127.0.0.1"
	config.Config.Rpc.RegisterIP = "127.0.0.1"
	config.Config.Prometheus.Enable = false
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_V1", "0")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH", "")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256", "")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(OwnedRPCBootstrapEnv, filepath.Join(dir, "bootstrap.json"))
	t.Setenv(ownedAnnouncementEnv, filepath.Join(dir, "ready.json"))
	t.Setenv(ownedNonceEnv, strings.Repeat("a", 64))
	configSHA, envSHA, err := OwnedRPCConfigurationHashes()
	if err != nil {
		t.Fatal(err)
	}
	boot := OwnedRPCBootstrap{Version: "owned-server-bootstrap-v1", Role: "rpc-conversation", Mode: "legacy", InstanceID: uuid.NewString(), DatabaseSHA256: ownedSHA([]byte(config.Config.Mongo.Database)), ConfigurationSHA256: configSHA, EnvironmentSHA256: envSHA, DrainPath: filepath.Join(dir, "drained.json")}
	body, err := json.Marshal(boot)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv(OwnedRPCBootstrapEnv), append(body, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(append(body, '\n')))
	input, err := loadOwnedRPCInput("rpc-conversation")
	if err != nil {
		t.Fatal(err)
	}
	return input
}
func awaitOwnedRPCAnnouncement(t *testing.T, input ownedRPCInput) ownedcontrol.ServerRuntimeAnnouncement {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		if value, _, err := ownedcontrol.ReadServerRuntimeAnnouncement(input.path, input.nonce); err == nil {
			return value
		}
		select {
		case <-deadline.C:
			t.Fatal("actual lifecycle did not write canonical ready")
		case <-tick.C:
		}
	}
}

func TestOwnedRPCActualStartAnnouncesThenJoinsBeforePrivateDrain(t *testing.T) {
	input := ownedRPCInputFixture(t)
	registry := &ownedRPCRegistryFixture{}
	var mu sync.Mutex
	var order []string
	record := func(name string) { mu.Lock(); order = append(order, name); mu.Unlock() }
	sawCancel, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	t.Cleanup(func() {
		cancel()
		releaseOnce.Do(func() { close(release) })
		select {
		case <-result:
		case <-time.After(3 * time.Second):
			t.Error("owned lifecycle cleanup did not join")
		}
	})
	register := func(_ context.Context, _ discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
		healthpb.RegisterHealthServer(server, health.NewServer())
		if err := OwnRPCDatabase(server, config.Config.Mongo.Database); err != nil {
			return nil, err
		}
		for _, name := range []string{"redis", "mongo"} {
			name := name
			if err := OwnRPCResource(server, name, func(context.Context) error { record(name); return nil }); err != nil {
				return nil, err
			}
		}
		if !OwnedRPCWorkers(server).Run(func(lifetime context.Context) { <-lifetime.Done(); close(sawCancel); <-release; record("worker") }) {
			return nil, errOwnedRPC
		}
		return nil, nil
	}
	go func() {
		result <- startOwnedRPC(ctx, 0, "owned-fixture", 0, input, register, func(context.Context, string) (kdisc.OwnedRegistry, error) { return registry, nil })
		close(result)
	}()
	ready := awaitOwnedRPCAnnouncement(t, input)
	if ready.Role != "rpc-conversation" || ready.Mode != "legacy" || ready.Epoch != "" || ready.ActivationSHA256 != "" {
		t.Fatal("legacy identity was relabeled", ready.Role, ready.Mode)
	}
	cancel()
	select {
	case <-sawCancel:
	case <-time.After(2 * time.Second):
		t.Fatal("owned worker never received stop")
	}
	mu.Lock()
	premature := len(order) != 0
	mu.Unlock()
	if premature || registry.closes.Load() != 0 {
		t.Fatal("shared resource closed before real worker completion")
	}
	if _, err := os.Lstat(input.boot.DrainPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unjoined owner produced drain", err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("actual lifecycle did not join")
	}
	drain, _, err := ownedcontrol.ReadServerRuntimeDrain(input.boot.DrainPath, input.nonce)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(drain.JoinedAndClosed, ready.OwnedStopStepNames) || drain.SourceOutcomeUnconfirmed || registry.closes.Load() != 1 {
		t.Fatal("drain does not match original completed ownership")
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(order, []string{"worker", "redis", "mongo"}) {
		t.Fatal("resources closed out of dependency order", order)
	}
}

func TestOwnedRPCFailedStartNeverAnnouncesAndClosesConstructedResources(t *testing.T) {
	input := ownedRPCInputFixture(t)
	registry := &ownedRPCRegistryFixture{}
	var closes atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	register := func(_ context.Context, _ discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
		if err := OwnRPCResource(server, "redis", func(context.Context) error { closes.Add(1); return nil }); err != nil {
			return nil, err
		}
		// The actual next constructor failed, before Mongo and before registration.
		return nil, errors.New("synthetic actual constructor failure")
	}
	err := startOwnedRPC(ctx, 0, "owned-fixture", 0, input, register, func(context.Context, string) (kdisc.OwnedRegistry, error) { return registry, nil })
	if err == nil || closes.Load() != 1 || registry.closes.Load() != 1 {
		t.Fatal("partial real start did not stop the original handles", err)
	}
	for _, path := range []string{input.path, input.boot.DrainPath} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("failed start manufactured owner evidence", err)
		}
	}
}

func TestOwnedRPCBootstrapRejectsStaleOrFabricatedRuntimeIdentity(t *testing.T) {
	for _, which := range []string{"role", "configuration", "legacy-epoch"} {
		t.Run(which, func(t *testing.T) {
			input := ownedRPCInputFixture(t)
			switch which {
			case "role":
				input.boot.Role = "rpc-group"
			case "configuration":
				config.Config.Mongo.Database = "other-tenant"
			case "legacy-epoch":
				t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH", uuid.NewString())
			}
			body, err := json.Marshal(input.boot)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(input.sourcePath, append(body, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(append(body, '\n')))
			if _, err := loadOwnedRPCInput("rpc-conversation"); err == nil {
				t.Fatal("fabricated/stale source identity accepted")
			}
		})
	}
}
