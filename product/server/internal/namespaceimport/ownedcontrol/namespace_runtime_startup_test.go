//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// A private final release file is expected to be absent before the producer's
// signed publication. Absence is still closed evidence for every other reader;
// only AwaitAdmission's existing context/ticker branch may wait for it.
func TestNamespaceRuntimeMissingReleaseWaitsWithoutWeakeningPrivateEvidence(t *testing.T) {
	fixture := func(t *testing.T) (*NamespaceRuntimeStartupGate, NamespaceRuntimeBootstrap, string) {
		t.Helper()
		b, original, _ := namespaceRuntimeFixture(t)
		b.Requirement.Role = "msggateway"
		b.Requirement.ContractSHA256 = NamespaceRuntimeContractSHA256("msggateway")
		b.Requirement.Services = []string{"OpenIMServer.msggateway.msgGateway"}
		b.Requirement.Components = []string{"provider-callbacks", "rpc-handlers", "source-ownership", "websocket-admission", "websocket-runtime"}
		path := filepath.Join(filepath.Dir(original), "gateway-bootstrap.json")
		pin, e := WriteNamespaceRuntimeBootstrap(path, b)
		if e != nil {
			t.Fatal("gateway fixture bootstrap rejected", e)
		}
		nonce := strings.Repeat("d", 64)
		r := b.Requirement
		gate, e := NewNamespaceRuntimeStartupGate(path, pin, r.Role, b.ExpectedInstanceID, r.BootstrapSHA256, r.ConfigurationSHA256, r.EnvironmentSHA256, nonce)
		if e != nil {
			t.Fatal("gateway fixture gate rejected", e)
		}
		bindings := NamespaceRuntimeBindings{Role: r.Role, HeaderSHA256: b.Header.SHA256(), Resources: []string{"callback-http", "redis", "websocket-runtime"}}
		if _, e = gate.RecordPrepared(context.Background(), r.Services, r.Components, bindings); e != nil {
			t.Fatal("actual private registration rejected", e)
		}
		return gate, b, nonce
	}
	t.Run("missing-release-waits-and-cancels-without-ready", func(t *testing.T) {
		gate, b, _ := fixture(t)
		var release NamespaceRuntimeRelease
		_, e := readServerEnvelope(b.ReleasePath, &release)
		if !errors.Is(e, ErrOwnedEvidence) || !errors.Is(e, os.ErrNotExist) || strings.Contains(e.Error(), filepath.Dir(b.ReleasePath)) {
			t.Fatal("missing final file lost closed evidence or exposed its private path")
		}
		original, e := os.ReadFile(b.RegistrationPath)
		if e != nil {
			t.Fatal("original registration absent")
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- gate.AwaitAdmission(ctx) }()
		select {
		case <-done:
			t.Fatal("absence ended the original admission wait")
		case <-time.After(150 * time.Millisecond):
		}
		if _, _, e := gate.KnownPublication(); !errors.Is(e, ErrOwnedEvidence) {
			t.Fatal("missing release became a publication")
		}
		cancel()
		select {
		case e := <-done:
			if !errors.Is(e, context.Canceled) {
				t.Fatal("original cancellation did not end the wait", e)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("original admission wait ignored cancellation")
		}
		if _, _, e := gate.KnownPublication(); !errors.Is(e, ErrOwnedEvidence) {
			t.Fatal("cancelled wait became a publication")
		}
		current, e := os.ReadFile(b.RegistrationPath)
		if e != nil || string(current) != string(original) {
			t.Fatal("waiting replayed or changed the original registration")
		}
		if _, e := os.Lstat(b.ReleasePath); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("waiting fabricated a release")
		}
	})
	for _, kind := range []string{"malformed-release", "empty-release", "public-mode-release", "symlink-release", "hardlink-release", "directory-release"} {
		t.Run(kind, func(t *testing.T) {
			gate, b, _ := fixture(t)
			private := filepath.Join(filepath.Dir(b.ReleasePath), "private-target.json")
			switch kind {
			case "malformed-release":
				if e := os.WriteFile(b.ReleasePath, []byte("{private malformed data"), 0600); e != nil {
					t.Fatal(e)
				}
			case "empty-release":
				if e := os.WriteFile(b.ReleasePath, nil, 0600); e != nil {
					t.Fatal(e)
				}
			case "public-mode-release":
				if _, e := writeServerEnvelope(b.ReleasePath, NamespaceRuntimeRelease{}); e != nil {
					t.Fatal(e)
				}
				if e := os.Chmod(b.ReleasePath, 0644); e != nil {
					t.Fatal(e)
				}
			case "symlink-release":
				if _, e := writeServerEnvelope(private, NamespaceRuntimeRelease{}); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(private, b.ReleasePath); e != nil {
					t.Fatal(e)
				}
			case "hardlink-release":
				if _, e := writeServerEnvelope(private, NamespaceRuntimeRelease{}); e != nil {
					t.Fatal(e)
				}
				if e := os.Link(private, b.ReleasePath); e != nil {
					t.Fatal(e)
				}
			case "directory-release":
				if e := os.Mkdir(b.ReleasePath, 0700); e != nil {
					t.Fatal(e)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var release NamespaceRuntimeRelease
			if _, e := readServerEnvelope(b.ReleasePath, &release); !errors.Is(e, ErrOwnedEvidence) || errors.Is(e, os.ErrNotExist) {
				t.Fatal("invalid private file escaped the original reader", e)
			}
			if e := gate.AwaitAdmission(ctx); !errors.Is(e, ErrOwnedEvidence) || errors.Is(e, os.ErrNotExist) {
				t.Fatal("existing invalid release was treated as pending", e)
			}
		})
	}
	t.Run("absent-parent-is-not-release-pending", func(t *testing.T) {
		_, b, _ := fixture(t)
		var release NamespaceRuntimeRelease
		_, e := readServerEnvelope(filepath.Join(filepath.Dir(b.ReleasePath), "absent", "release.json"), &release)
		if !errors.Is(e, ErrOwnedEvidence) || errors.Is(e, os.ErrNotExist) {
			t.Fatal("missing parent became pending", e)
		}
	})
	t.Run("aliased-parent-is-not-release-pending", func(t *testing.T) {
		_, b, _ := fixture(t)
		parent := filepath.Dir(b.ReleasePath)
		alias := filepath.Join(parent, "alias")
		if e := os.Symlink(parent, alias); e != nil {
			t.Fatal(e)
		}
		var release NamespaceRuntimeRelease
		_, e := readServerEnvelope(filepath.Join(alias, "absent-release.json"), &release)
		if !errors.Is(e, ErrOwnedEvidence) || errors.Is(e, os.ErrNotExist) {
			t.Fatal("aliased parent became pending", e)
		}
	})
	t.Run("missing-announcement-cannot-be-ready", func(t *testing.T) {
		_, b, nonce := fixture(t)
		_, _, e := ReadServerRuntimeAnnouncement(filepath.Join(filepath.Dir(b.ReleasePath), "absent-ready.json"), nonce)
		if !errors.Is(e, ErrOwnedEvidence) || errors.Is(e, os.ErrNotExist) {
			t.Fatal("missing announcement escaped closed evidence", e)
		}
	})
}
