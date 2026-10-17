//go:build linux

package startrpc

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	kdisc "github.com/openimsdk/open-im-server/v3/pkg/common/discoveryregister"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type historyReaderPhaseRegistry struct {
	ownedRPCRegistryFixture
	registers atomic.Int32
}

func (r *historyReaderPhaseRegistry) Register(string, string, int, ...grpc.DialOption) error {
	r.registers.Add(1)
	return nil
}

// Explicit lifecycle seam only: this does NOT attest to Mongo publication,
// actual MD semantic binding, real owned process graph or production Ready.
type historyReaderPhaseBinding struct {
	bootstrap ownedcontrol.HistoryReaderBootstrap
	recorded  chan struct{}
	allow     chan struct{}
	awaited   atomic.Bool
}

func (b *historyReaderPhaseBinding) RecordRegistration(_ context.Context, instance, boot, configuration, environment, nonce string, services []string) (ownedcontrol.HistoryReaderRegistration, error) {
	close(b.recorded)
	h := b.bootstrap.Header
	return ownedcontrol.HistoryReaderRegistration{Version: "history-reader-registered-not-serving-v1", Role: "rpc-conversation", Mode: "required", InstanceID: instance, HeaderSHA256: h.SHA256(), DatabaseSHA256: ownedSHA([]byte(h.Database)), Epoch: h.Epoch, ActivationSHA256: h.ActivationSHA256, PreparationID: b.bootstrap.PreparationID, PreparationSHA256: b.bootstrap.PreparationSHA256, PreparationTokenSHA256: ownedSHA([]byte(b.bootstrap.PreparationToken)), InventorySHA256: b.bootstrap.InventorySHA256, ManifestSHA256: b.bootstrap.ManifestSHA256, RequirementsSHA256: b.bootstrap.RequirementsSHA256, BootstrapSHA256: boot, ConfigurationSHA256: configuration, EnvironmentSHA256: environment, ReadContractSHA256: b.bootstrap.Requirement.ReadContractSHA256, Services: services}, nil
}
func (b *historyReaderPhaseBinding) AwaitReadReady(ctx context.Context, _ string) error {
	b.awaited.Store(true)
	select {
	case <-b.allow:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Unpublished shape fixture only: the real v2 bootstrap/parser is exercised,
// but no Mongo target, DAO capability, graph registration or release is minted.
func preparedHistoryRPCFixture(t *testing.T, role string) (ownedRPCInput, ownedcontrol.HistoryReaderBootstrap) {
	t.Helper()
	if role != "rpc-conversation" && role != "rpc-msg" {
		t.Fatal("explicit history-reader role required")
	}
	t.Setenv(ownedcontrol.NamespaceRuntimePreparationPathEnv, "")
	t.Setenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv, "")
	input := ownedRPCInputFixture(t)
	epoch, activation := uuid.NewString(), strings.Repeat("c", 64)
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_V1", "required")
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH", epoch)
	t.Setenv("LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256", activation)
	configuration, environment, e := OwnedRPCConfigurationHashes()
	if e != nil {
		t.Fatal(e)
	}
	input.boot.Version = ownedcontrol.NamespaceRuntimeServerBootstrapVersion
	input.boot.Role = role
	input.boot.Mode = "required"
	input.boot.Epoch = epoch
	input.boot.ActivationSHA256 = activation
	input.boot.ConfigurationSHA256 = configuration
	input.boot.EnvironmentSHA256 = environment
	raw, e := json.Marshal(input.boot)
	if e != nil {
		t.Fatal(e)
	}
	raw = append(raw, '\n')
	if e = os.WriteFile(input.sourcePath, raw, 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv(OwnedRPCBootstrapSHAEnv, ownedSHA(raw))
	h := namespaceimport.Header{Tenant: "phase-test", Database: config.Config.Mongo.Database, StorageInstance: uuid.NewString(), ReplicaSet: "test-rs", MongoProcess: strings.Repeat("b", 24), MongoTopology: 1, RedisRunID: strings.Repeat("c", 40), RedisDB: 15, KafkaClusterID: "synthetic-cluster", Epoch: epoch, ActivationSHA256: activation, MigrationID: uuid.NewString(), DeploymentPolicySHA256: strings.Repeat("d", 64)}
	sum := strings.Repeat("e", 64)
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	namespace := ownedcontrol.NamespaceRuntimeBootstrap{Version: "namespace-runtime-private-bootstrap-v1", Header: h, HistoryPreparationID: sum, HistoryPreparationSHA256: sum, InventorySHA256: sum, ManifestSHA256: sum, RuntimePreparationID: sum, RuntimePreparationSHA256: sum, RuntimeToken: sum, RequirementsSHA256: sum, Requirement: ownedcontrol.NamespaceRuntimeRequirement{Role: role, BinarySHA256: sum, SourceSHA256: sum, BuildReceiptSHA256: sum, BootstrapSHA256: ownedSHA(raw), ConfigurationSHA256: configuration, EnvironmentSHA256: environment, ContractSHA256: ownedcontrol.NamespaceRuntimeContractSHA256(role), Services: []string{"grpc.health.v1.Health"}, Components: []string{"history-reader", "namespace-writer", "rpc-handlers", "source-ownership"}}, ExpectedInstanceID: input.boot.InstanceID, RegistrationPath: filepath.Join(filepath.Dir(input.path), "namespace-registration.json"), ReleasePath: filepath.Join(filepath.Dir(input.path), "namespace-release.json"), ReleaseKeyID: "synthetic-unpublished-key", ReleasePublicKey: key.Public().(ed25519.PublicKey)}
	// Exact synthetic catalog BSON is shape evidence only. The real gate still
	// requires its original Mongo target and concrete same-instance DAO binding.
	for at, name := range namespaceinventory.OriginalMongoCatalogCollections() {
		id := make([]byte, 16)
		id[15] = byte(at + 1)
		catalog, e := bson.Marshal(bson.D{{Key: "name", Value: name}, {Key: "type", Value: "collection"}, {Key: "info", Value: bson.D{{Key: "uuid", Value: primitive.Binary{Subtype: 4, Data: id}}}}})
		if e != nil {
			t.Fatal(e)
		}
		catalogSHA := ownedSHA(catalog)
		namespace.MongoCatalog = append(namespace.MongoCatalog, ownedcontrol.NamespaceRuntimeMongoCollection{Name: name, UUID: hex.EncodeToString(id), RawSHA256: catalogSHA, Source: namespaceimport.SourceRef{ObjectSHA256: ownedSHA([]byte("synthetic-catalog-object-" + name)), RecordSHA256: catalogSHA, Kind: "mongo:@catalog:" + ownedSHA([]byte(name)), Position: uint64(at)}})
	}
	namespace.MongoCatalogSHA256, e = ownedcontrol.NamespaceRuntimeMongoCatalogSHA256(namespace.MongoCatalog)
	if e != nil {
		t.Fatal(e)
	}
	namespacePath := filepath.Join(filepath.Dir(input.path), "namespace-prepare.json")
	namespaceSHA, e := ownedcontrol.WriteNamespaceRuntimeBootstrap(namespacePath, namespace)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv(ownedcontrol.NamespaceRuntimePreparationPathEnv, namespacePath)
	t.Setenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv, namespaceSHA)
	input, e = loadOwnedRPCInput(role)
	if e != nil {
		t.Fatal(e)
	}
	b := ownedcontrol.HistoryReaderBootstrap{Version: "private-history-reader-prepare-v1", Header: h, PreparationID: sum, PreparationSHA256: sum, PreparationToken: sum, InventorySHA256: sum, ManifestSHA256: sum, RequirementsSHA256: sum, Requirement: ownedcontrol.HistoryReaderRequirement{Role: role, BinarySHA256: sum, SourceSHA256: sum, BuildReceiptSHA256: sum, BootstrapSHA256: input.rawSHA, ConfigurationSHA256: configuration, EnvironmentSHA256: environment, Services: []string{"grpc.health.v1.Health"}, ReadContractSHA256: sum}, BindingPath: filepath.Join(filepath.Dir(input.path), "reader-registration.json")}
	path := filepath.Join(filepath.Dir(input.path), "reader-prepare.json")
	at, e := ownedcontrol.WriteHistoryReaderBootstrap(path, b)
	if e != nil {
		t.Fatal(e)
	}
	t.Setenv(HistoryReaderPreparationEnv, path)
	t.Setenv(HistoryReaderPreparationSHAEnv, at)
	// Both private preparation pairs leave the original business env hash intact.
	current, e := loadOwnedRPCInput(role)
	if e != nil || current.environmentSHA != input.environmentSHA {
		t.Fatal("private preparation changed business environment binding", e)
	}
	return current, b
}
func TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence(t *testing.T) {
	for _, allow := range []bool{false, true} {
		name := "registered-not-serving-before-readiness"
		if allow {
			name = "known-readiness-still-keeps-writer-fence"
		}
		t.Run(name, func(t *testing.T) {
			input, boot := preparedHistoryRPCFixture(t, "rpc-conversation")
			registry := &historyReaderPhaseRegistry{}
			binding := &historyReaderPhaseBinding{bootstrap: boot, recorded: make(chan struct{}), allow: make(chan struct{})}
			if allow {
				close(binding.allow)
			}
			finalCalled := atomic.Bool{}
			closed := atomic.Int32{}
			blocked := errors.New("selected writer gate remains closed")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			register := func(_ context.Context, _ discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (ManagedStop, error) {
				healthpb.RegisterHealthServer(server, health.NewServer())
				if e := OwnRPCDatabase(server, config.Config.Mongo.Database); e != nil {
					return nil, e
				}
				for _, role := range []string{"redis", "mongo"} {
					if e := OwnRPCResource(server, role, func(context.Context) error { closed.Add(1); return nil }); e != nil {
						return nil, e
					}
				}
				if e := OwnRPCHistoryReader(server, binding); e != nil {
					return nil, e
				}
				if e := OwnRPCPreparedWriterFinalCheck(server, func(context.Context) error { finalCalled.Store(true); return blocked }); e != nil {
					return nil, e
				}
				return nil, nil
			}
			go func() {
				done <- startOwnedRPC(ctx, 0, "private-history-phase", 0, input, register, func(context.Context, string) (kdisc.OwnedRegistry, error) { return registry, nil })
				close(done)
			}()
			t.Cleanup(func() {
				cancel()
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					t.Error("actual owned phase goroutine did not finish cleanup")
				}
			})
			select {
			case <-binding.recorded:
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatal("actual owned start never reached registered-not-serving binding")
			}
			if !allow {
				cancel()
			}
			select {
			case e := <-done:
				if e == nil {
					t.Fatal("unpublished or writer-unbound source served successfully")
				}
				if allow && !errors.Is(e, blocked) {
					t.Fatal("strict final writer predicate was not consumed", e)
				}
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatal("owned pending startup failed to join")
			}
			if registry.registers.Load() != 0 || registry.closes.Load() != 1 || closed.Load() != 2 || !binding.awaited.Load() || finalCalled.Load() != allow {
				t.Fatal("discovery/public admission occurred early or real resources were not joined")
			}
			if _, e := os.Lstat(input.path); !os.IsNotExist(e) {
				t.Fatal("pending registration became a runtime ready announcement")
			}
			if _, e := os.Lstat(input.boot.DrainPath); !os.IsNotExist(e) {
				t.Fatal("partial startup fabricated a full runtime drain")
			}
		})
	}
}
