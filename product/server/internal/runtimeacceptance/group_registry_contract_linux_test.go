//go:build linux

package runtimeacceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/msgtransfer"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
)

// This contract reads the actual full fixture writer's private role inputs
// through the unchanged production bootstrap and registry readers. Selectors
// below never execute a binary, contact a service or grant runtime admission.
func TestFreshFixtureGroupReceiverRegistryMatchesProductionReader(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	header := namespaceimport.Header{Tenant: "synthetic-group-registry", Database: "synthetic_group_registry", StorageInstance: "synthetic-storage", ReplicaSet: "synthetic-replica", MongoProcess: "synthetic-process", MongoTopology: 1, RedisRunID: "synthetic-redis", RedisDB: 0, KafkaClusterID: "synthetic-kafka", Epoch: "11111111-1111-4111-8111-111111111111", ActivationSHA256: strings.Repeat("a", 64), MigrationID: "22222222-2222-4222-8222-222222222222", DeploymentPolicySHA256: strings.Repeat("b", 64)}
	credentials := map[string]string{"serviceSecret": "synthetic-fixture-secret", "minioUser": "synthetic_minio", "minioSecret": "synthetic-fixture-minio"}
	for _, database := range []string{"mongo", "redis", "kafka"} {
		for _, scope := range []string{"Legacy", "Runtime"} {
			credentials[database+scope+"User"] = "synthetic_" + strings.ToLower(database+scope)
			credentials[database+scope+"Password"] = "synthetic-fixture-password"
		}
	}
	boot := &Bootstrap{Plan: PrivatePlan{Work: root}, Header: header, Resource: ResourcePlane{Nonce: strings.Repeat("a", 32)}, ManagerID: "synthetic_group_manager", Credential: credentials, Ports: map[string]int{"api": 31000, "msggateway": 31002, "push": 31019, "rpc-auth": 31010, "rpc-conversation": 31011, "rpc-encryption": 31012, "rpc-friend": 31013, "rpc-group": 31014, "rpc-msg": 31015, "rpc-rtc": 31016, "rpc-third": 31017, "rpc-user": 31018}}
	runtimeDir, err := boot.writeConfiguration("runtime-config", false, "127.0.0.1:6379", []string{"synthetic_online", "synthetic_offline", "synthetic_push"}, []string{"synthetic_online_group", "synthetic_offline_group", "synthetic_push_group"})
	if err != nil {
		t.Fatal(err)
	}
	legacyDir, err := boot.writeConfiguration("legacy-config", true, "127.0.0.1:6379", []string{"synthetic_online", "synthetic_offline", "synthetic_push"}, []string{"synthetic_online_group", "synthetic_offline_group", "synthetic_push_group"})
	if err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "services")
	if err := os.Mkdir(work, 0700); err != nil {
		t.Fatal(err)
	}
	artifact := func(role string) BinaryArtifact {
		return BinaryArtifact{Role: role, Path: filepath.Join(root, "synthetic-"+role), SHA256: strings.Repeat("c", 64), SourceSHA256: strings.Repeat("d", 64), ReceiptPath: filepath.Join(root, "synthetic-"+role+".json"), ReceiptSHA256: strings.Repeat("e", 64), Services: []string{"synthetic-service"}, Components: []string{"synthetic-component"}}
	}
	var binaries []BinaryArtifact
	for _, role := range ownedcontrol.NamespaceRuntimeRoles() {
		binaries = append(binaries, artifact(role))
	}
	plan, err := PrepareServicePlan(ServicePlanInputs{Work: work, Header: header, Binaries: binaries, LegacyMessageTransfer: artifact("msgtransfer-legacy"), RuntimeConfigDirectory: runtimeDir, LegacyConfigDirectory: legacyDir, BadgeEvidenceSHA256: strings.Repeat("f", 64), LegacyInstanceIDs: map[string]string{"rpc-msg": "33333333-3333-4333-8333-333333333333", "msgtransfer-legacy": "44444444-4444-4444-8444-444444444444"}})
	if err != nil {
		t.Fatal("actual service-plan fixture failed", err)
	}
	var groupSpec, ordinarySpec ownedcontrol.ServiceSpec
	for _, spec := range plan.Specs {
		if spec.Role == "rpc-group" {
			groupSpec = spec
		}
		if spec.Role == "msgtransfer-managed" {
			ordinarySpec = spec
		}
	}
	raw, err := os.ReadFile(groupSpec.PrivateEnvironmentPath)
	if err != nil {
		t.Fatal(err)
	}
	if digestBytes(raw) != groupSpec.PrivateEnvironmentSHA256 {
		t.Fatal("group private environment pin differs")
	}
	var environment ownedcontrol.OwnedServiceEnvironment
	if err := json.Unmarshal(raw, &environment); err != nil {
		t.Fatal(err)
	}
	if environment.Role != "rpc-group" || len(environment.Values) != 2 {
		t.Fatal("fixture expanded group environment")
	}
	for _, key := range []string{msgtransfer.OwnedGroupBootstrapPathEnv, msgtransfer.OwnedGroupBootstrapSHAEnv} {
		t.Setenv(key, environment.Values[key])
	}
	groupBoot, err := msgtransfer.LoadOwnedGroupReceiverBootstrap(header.Database)
	if err != nil {
		t.Fatal("production group bootstrap rejected writer output", err)
	}
	ordinaryBoot, err := ownedcontrol.ReadNamespaceRuntimeManagedInput(ordinarySpec.BootstrapPath, ordinarySpec.BootstrapSHA256)
	if err != nil {
		t.Fatal("production ordinary bootstrap rejected writer output", err)
	}
	groupInput := aliasvendor.OwnedInputFile{Path: groupBoot.VendorPath, ExpectedSHA256: groupBoot.VendorSHA256}
	ordinaryInput := aliasvendor.OwnedInputFile{Path: ordinaryBoot.VendorPath, ExpectedSHA256: ordinaryBoot.VendorSHA256}
	ctx := context.Background()
	t.Run("actual-role-inputs-accepted-and-separated", func(t *testing.T) {
		group, err := aliasvendor.LoadOwnedActionOfflineRegistry(ctx, groupInput, header.Database, header.Epoch, header.ActivationSHA256)
		if err != nil {
			t.Fatal("production group registry reader rejected actual fixture", err)
		}
		ordinary, err := aliasvendor.LoadOwnedOfflineRegistry(ctx, ordinaryInput, header.Database, header.Epoch, header.ActivationSHA256)
		if err != nil {
			t.Fatal("production ordinary reader rejected preserved fixture", err)
		}
		if groupInput.Path == ordinaryInput.Path || groupInput.ExpectedSHA256 == ordinaryInput.ExpectedSHA256 || len(group.Entries) != 1 || len(ordinary.Entries) != 1 || ordinary.Entries[0].GroupProfile != nil {
			t.Fatal("role registries were not kept distinct")
		}
		entry := group.Entries[0]
		if entry.GroupProfile == nil || entry.Policy.Version != "group-effect-v1" || entry.GroupProfile.Transport.Version != "group-effect-v1" || entry.BindingSeedSHA256 != ordinary.Entries[0].Policy.ConfigurationSHA256 || group.CurrentPolicySHA256 != entry.Fingerprint {
			t.Fatal("group current transport/profile/seed contract differs")
		}
		if groupBoot.MessageCallbacksPath != "" || groupBoot.MessageCallbacksSHA256 != "" || groupBoot.OfflineCallbacksPath != "" || groupBoot.OfflineCallbacksSHA256 != "" || groupBoot.RoleCallbacksPath != "" || groupBoot.RoleCallbacksSHA256 != "" {
			t.Fatal("disabled fixture unexpectedly needs callback material")
		}
		if err := withPrivateConfiguration(runtimeDir, func(string) error {
			profile, err := deliveryprofile.FreezeGroup(header.Database, header.Epoch, header.ActivationSHA256, "")
			if err != nil {
				return err
			}
			if !exactJSON(profile, *entry.GroupProfile) || messagedelivery.KafkaDeliveryPolicy(profile.Transport) != entry.Policy {
				t.Fatal("fixture did not retain actual frozen group profile")
			}
			seed, err := msgtransfer.FreezeOwnedKafkaPolicy(header.Database, header.Epoch, header.ActivationSHA256)
			if err != nil {
				return err
			}
			if seed != ordinary.Entries[0].Policy || entry.BindingSeedSHA256 != seed.ConfigurationSHA256 {
				t.Fatal("ordinary seed or group seed binding changed")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("cross-role-registry-is-rejected", func(t *testing.T) {
		if _, err := aliasvendor.LoadOwnedActionOfflineRegistry(ctx, ordinaryInput, header.Database, header.Epoch, header.ActivationSHA256); err == nil {
			t.Fatal("group reader accepted the old ordinary fixture")
		}
		if _, err := aliasvendor.LoadOwnedOfflineRegistry(ctx, groupInput, header.Database, header.Epoch, header.ActivationSHA256); err == nil {
			t.Fatal("ordinary reader accepted group action material")
		}
	})
	t.Run("profile-absence-and-transport-drift-are-rejected", func(t *testing.T) {
		raw, err := os.ReadFile(groupInput.Path)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"missing-profile", "transport-drift"} {
			var changed aliasvendor.OwnedOfflinePolicyRegistry
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			if name == "missing-profile" {
				changed.Entries[0].GroupProfile = nil
			} else {
				changed.Entries[0].GroupProfile.Transport.ConfigurationSHA256 = strings.Repeat("9", 64)
			}
			path := filepath.Join(work, name+".json")
			pin, err := writePrivate(path, changed)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := aliasvendor.LoadOwnedActionOfflineRegistry(ctx, aliasvendor.OwnedInputFile{Path: path, ExpectedSHA256: pin}, header.Database, header.Epoch, header.ActivationSHA256); err == nil {
				t.Fatal("group reader accepted invalid typed profile", name)
			}
		}
	})
}
