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

func TestOwnedRPCActualDatabaseStaysBoundToOriginalBootstrapBeforeWrites(t *testing.T) {
	for _, variant := range []string{"foreign-handle", "configuration-before-bind", "configuration-after-bind"} {
		t.Run(variant, func(t *testing.T) {
			input := ownedRPCInputFixture(t)
			registry := &ownedRPCRegistryFixture{}
			var closed, initialized atomic.Int32
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			register := func(_ context.Context, _ discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
				healthpb.RegisterHealthServer(server, health.NewServer())
				for _, name := range []string{"redis", "mongo"} {
					if err := OwnRPCResource(server, name, func(context.Context) error { closed.Add(1); return nil }); err != nil {
						return nil, err
					}
				}
				actualDatabase := config.Config.Mongo.Database
				if variant == "foreign-handle" {
					actualDatabase = "foreign-tenant"
				} else if variant == "configuration-before-bind" {
					config.Config.Mongo.Database = "foreign-tenant"
					actualDatabase = config.Config.Mongo.Database
				}
				if err := OwnRPCDatabase(server, actualDatabase); err != nil {
					return nil, err
				}
				// Real constructors perform this identity check before any index or
				// business initialization. This counter marks that exact boundary.
				initialized.Add(1)
				if variant == "configuration-after-bind" {
					config.Config.Mongo.Database = "foreign-tenant"
				}
				return nil, nil
			}
			err := startOwnedRPC(ctx, 0, "owned-fixture", 0, input, register,
				func(context.Context, string) (kdisc.OwnedRegistry, error) { return registry, nil })
			if err == nil || closed.Load() != 2 || registry.closes.Load() != 1 {
				t.Fatal("foreign original-database binding did not reject and close its actual resources", err)
			}
			wantInitialized := int32(0)
			if variant == "configuration-after-bind" {
				wantInitialized = 1
			}
			if initialized.Load() != wantInitialized {
				t.Fatal("foreign handle reached the business initialization boundary")
			}
			for _, path := range []string{input.path, input.boot.DrainPath} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("foreign database startup manufactured original ready/drain evidence", err)
				}
			}
		})
	}
}

// This uses the real startOwnedRPC listener/Serve/stop/private receipts. The
// controlled close callbacks are resource-boundary barriers, not real DB grants;
// actual Redis sensitive loops and Kafka sockets have their own direct controls.
func TestOwnedRPCMsgSensitiveWorkersJoinBeforeProducerPoolsAndDrain(t *testing.T) {
	for _, variant := range []string{"joined", "sensitive-close-failure", "partial-constructor-failure"} {
		t.Run(variant, func(t *testing.T) {
			input := ownedRPCInputFixture(t)
			input.boot.Role = "rpc-msg"
			raw, err := json.Marshal(input.boot)
			if err != nil {
				t.Fatal(err)
			}
			raw = append(raw, '\n')
			if err := os.WriteFile(input.sourcePath, raw, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(raw))
			input, err = loadOwnedRPCInput("rpc-msg")
			if err != nil {
				t.Fatal(err)
			}
			registry := &ownedRPCRegistryFixture{}
			ctx, cancel := context.WithCancel(context.Background())
			release, closing, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			var mu sync.Mutex
			var order []string
			record := func(name string) { mu.Lock(); order = append(order, name); mu.Unlock() }
			result := make(chan error, 1)
			register := func(_ context.Context, _ discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
				healthpb.RegisterHealthServer(server, health.NewServer())
				if err := OwnRPCDatabase(server, config.Config.Mongo.Database); err != nil {
					return nil, err
				}
				// Intentionally register the pools first, matching actual msg Start.
				// The production fixed order, not insertion order, must govern close.
				for _, name := range []string{"redis", "mongo", "kafka-producers"} {
					name := name
					if err := OwnRPCResource(server, name, func(context.Context) error { record(name); return nil }); err != nil {
						return nil, err
					}
				}
				if err := OwnRPCResource(server, "sensitive-word-workers", func(call context.Context) error {
					if OwnedRPCContext(server).Err() == nil {
						return errors.New("actual RPC work was not canceled before sensitive stop")
					}
					close(closing)
					select {
					case <-release:
					case <-call.Done():
						return call.Err()
					}
					record("sensitive-word-workers")
					if variant == "sensitive-close-failure" {
						return context.DeadlineExceeded
					}
					return nil
				}); err != nil {
					return nil, err
				}
				if variant == "partial-constructor-failure" {
					return nil, errors.New("synthetic actual later constructor failure")
				}
				return nil, nil
			}
			t.Cleanup(func() {
				cancel()
				releaseOnce.Do(func() { close(release) })
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
					t.Error("msg lifecycle did not join after releasing its original barrier")
				}
			})
			go func() {
				defer close(finished)
				result <- startOwnedRPC(ctx, 0, "owned-msg-fixture", 0, input, register, func(context.Context, string) (kdisc.OwnedRegistry, error) { return registry, nil })
			}()
			var ready ownedcontrol.ServerRuntimeAnnouncement
			if variant != "partial-constructor-failure" {
				ready = awaitOwnedRPCAnnouncement(t, input)
				cancel()
			}
			select {
			case <-closing:
			case <-time.After(3 * time.Second):
				t.Fatal("original sensitive close was not reached")
			}
			mu.Lock()
			premature := len(order) != 0
			mu.Unlock()
			if premature || registry.closes.Load() != 0 {
				t.Fatal("a pool or registry closed before actual sensitive join")
			}
			if _, err := os.Lstat(input.boot.DrainPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unjoined sensitive worker produced a drain", err)
			}
			if variant == "partial-constructor-failure" {
				if _, err := os.Lstat(input.path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("partial msg construction produced ready", err)
				}
			}
			releaseOnce.Do(func() { close(release) })
			select {
			case err = <-result:
			case <-time.After(3 * time.Second):
				t.Fatal("msg close did not return")
			}
			mu.Lock()
			actual := append([]string{}, order...)
			mu.Unlock()
			if variant == "sensitive-close-failure" {
				if !errors.Is(err, context.DeadlineExceeded) || !reflect.DeepEqual(actual, []string{"sensitive-word-workers"}) || registry.closes.Load() != 0 {
					t.Fatal("failed sensitive join was hidden or later pools closed", err, actual)
				}
			} else {
				if !reflect.DeepEqual(actual, []string{"sensitive-word-workers", "kafka-producers", "redis", "mongo"}) || registry.closes.Load() != 1 {
					t.Fatal("actual msg resource close order changed", actual)
				}
				if variant == "joined" {
					if err != nil {
						t.Fatal(err)
					}
					drain, _, readErr := ownedcontrol.ReadServerRuntimeDrain(input.boot.DrainPath, input.nonce)
					if readErr != nil || !reflect.DeepEqual(drain.JoinedAndClosed, ready.OwnedStopStepNames) {
						t.Fatal("drain omitted the exact sensitive obligation", readErr)
					}
				} else if err == nil {
					t.Fatal("partial constructor failure was lost")
				}
			}
			if variant != "joined" {
				if _, err := os.Lstat(input.boot.DrainPath); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed msg stop/start produced successful drain", err)
				}
			}
		})
	}
}
