//go:build linux

package startrpc

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

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

// The original named entry is now an integration consumer, not a disabled
// legacy-v1 fake. Both cancelled real graphs are mandatory in focused AND full
// regression preparation; absent evidence fails instead of skipping this top.
func TestOwnedPreparedReaderRegistersBeforeServeAndRetainsFinalWriterFence(t *testing.T) {
	actual := readActualFullgraphEvidence(t, "ack")
	t.Run("registered-not-serving-before-readiness", func(t *testing.T) {
		assertActualCancelledReaderPhase(t, "reader-registered-cancel", "registered-not-serving-before-readiness", actual)
	})
	t.Run("known-readiness-still-keeps-writer-fence", func(t *testing.T) {
		assertActualCancelledReaderPhase(t, "reader-writer-fence-cancel", "known-readiness-still-keeps-writer-fence", actual)
	})
}
