//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

func namespaceRuntimeFixture(t *testing.T) (NamespaceRuntimeBootstrap, string, string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	sum := strings.Repeat("a", 64)
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	h := namespaceimport.Header{Tenant: "synthetic-runtime", Database: "synthetic_runtime", StorageInstance: "11111111-1111-4111-8111-111111111111", ReplicaSet: "synthetic-rs", MongoProcess: strings.Repeat("b", 24), MongoTopology: 1, RedisRunID: strings.Repeat("c", 40), RedisDB: 15, KafkaClusterID: "synthetic-cluster", Epoch: "22222222-2222-4222-8222-222222222222", ActivationSHA256: sum, MigrationID: "33333333-3333-4333-8333-333333333333", DeploymentPolicySHA256: sum}
	r := NamespaceRuntimeRequirement{Role: "rpc-auth", BinarySHA256: sum, SourceSHA256: sum, BuildReceiptSHA256: sum, BootstrapSHA256: sum, ConfigurationSHA256: sum, EnvironmentSHA256: sum, ContractSHA256: NamespaceRuntimeContractSHA256("rpc-auth"), Services: []string{"synthetic.rpc"}, Components: []string{"source-ownership"}}
	b := NamespaceRuntimeBootstrap{Version: "namespace-runtime-private-bootstrap-v1", Header: h, HistoryPreparationID: sum, HistoryPreparationSHA256: sum, InventorySHA256: sum, ManifestSHA256: sum, RuntimePreparationID: sum, RuntimePreparationSHA256: sum, RuntimeToken: sum, RequirementsSHA256: sum, Requirement: r, ExpectedInstanceID: "44444444-4444-4444-8444-444444444444", RegistrationPath: filepath.Join(dir, "registration.json"), ReleasePath: filepath.Join(dir, "release.json"), ReleaseKeyID: "synthetic-key", ReleasePublicKey: key.Public().(ed25519.PublicKey)}
	b.MongoCatalog = namespaceRuntimeCatalogFixture(t)
	b.MongoCatalogSHA256, _ = NamespaceRuntimeMongoCatalogSHA256(b.MongoCatalog)
	if e := b.ValidateShape(); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "bootstrap.json")
	pin, e := WriteNamespaceRuntimeBootstrap(path, b)
	if e != nil {
		t.Fatal(e)
	}
	return b, path, pin
}
func TestNamespaceRuntimeRegistrationIsPrivateOnceAndNeverAReadyPermit(t *testing.T) {
	b, path, pin := namespaceRuntimeFixture(t)
	nonce := strings.Repeat("d", 64)
	r := b.Requirement
	gate, e := NewNamespaceRuntimeStartupGate(path, pin, r.Role, b.ExpectedInstanceID, r.BootstrapSHA256, r.ConfigurationSHA256, r.EnvironmentSHA256, nonce)
	if e != nil {
		t.Fatal(e)
	}
	bindings := NamespaceRuntimeBindings{Role: r.Role, HeaderSHA256: b.Header.SHA256(), Resources: []string{"rpc-server"}}
	t.Run("actual-exclusive-registration-still-not-permission", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if _, e = gate.RecordPrepared(ctx, r.Services, r.Components, bindings); e != nil {
			t.Fatal(e)
		}
		if _, _, e = gate.KnownPublication(); e == nil {
			t.Fatal("prepared evidence was treated as Mongo commit")
		}
		if known, e := gate.InspectPreparedRegistration(ctx); e != nil || !known {
			t.Fatal("exact fsynced registration was not inspectable", e)
		}
	})
	t.Run("second-registration-cannot-change-original-resource-identity", func(t *testing.T) {
		bindings.Resources = []string{"another"}
		if _, e := gate.RecordPrepared(context.Background(), r.Services, r.Components, bindings); e == nil {
			t.Fatal("registration overwritten")
		}
		raw, e := os.ReadFile(b.RegistrationPath)
		if e != nil || len(raw) == 0 {
			t.Fatal("original private record disappeared")
		}
	})
	t.Run("unsigned-or-foreign-release-remains-closed", func(t *testing.T) {
		if gate.acceptRelease(NamespaceRuntimeRelease{Version: "namespace-runtime-release-v1", Role: r.Role, InstanceID: b.ExpectedInstanceID}) == nil {
			t.Fatal("unknown external release accepted")
		}
	})
	t.Run("retirement-is-sticky", func(t *testing.T) {
		gate.Retire()
		if gate.CheckConstructionCurrent() == nil {
			t.Fatal("retired constructor revived")
		}
		if _, _, e := gate.KnownPublication(); e == nil {
			t.Fatal("retired publication revived")
		}
	})
}
func TestNamespaceRuntimeCompleteRoleAndActualOwnerHandlesAreMandatory(t *testing.T) {
	b, _, _ := namespaceRuntimeFixture(t)
	var requirements []NamespaceRuntimeRequirement
	for _, role := range NamespaceRuntimeRoles() {
		r := b.Requirement
		r.Role = role
		r.ContractSHA256 = NamespaceRuntimeContractSHA256(role)
		requirements = append(requirements, r)
	}
	t.Run("all-fourteen-in-exact-order", func(t *testing.T) {
		if _, e := NamespaceRuntimeRequirementsSHA256(requirements); e != nil {
			t.Fatal(e)
		}
		if _, e := NamespaceRuntimeRequirementsSHA256(requirements[:len(requirements)-1]); e == nil {
			t.Fatal("missing role was optional")
		}
	})
	t.Run("source-change-cannot-reuse-requirements-root", func(t *testing.T) {
		first, _ := NamespaceRuntimeRequirementsSHA256(requirements)
		other := append([]NamespaceRuntimeRequirement(nil), requirements...)
		other[0].SourceSHA256 = strings.Repeat("e", 64)
		second, e := NamespaceRuntimeRequirementsSHA256(other)
		if e != nil || first == second {
			t.Fatal("source substitution preserved graph root")
		}
	})
	t.Run("bare-hold-is-not-exclusion-and-cannot-exec", func(t *testing.T) {
		l, _ := launcherFixture(t)
		defer l.Close()
		hold, e := l.Hold(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		g := &NamespaceRuntimeLaunchGroup{hold: hold}
		if _, e = g.Start(context.Background(), PreparedNamespaceRuntimeSpec{}); e == nil {
			t.Fatal("bare lock admitted an actual child")
		}
	})
	t.Run("DTO-or-PID-cannot-transfer-an-unobserved-runtime", func(t *testing.T) {
		g := &NamespaceRuntimeLaunchGroup{}
		if cap, e := g.TransferToNextEpoch(context.Background(), nil, b.Header, nil); e == nil || cap != nil {
			t.Fatal("unobserved process graph minted next ownership")
		}
		if rows, e := g.AwaitStarted(context.Background(), strings.Repeat("a", 64)); e == nil || rows != nil {
			t.Fatal("unobserved graph claimed real starts")
		}
	})
}
