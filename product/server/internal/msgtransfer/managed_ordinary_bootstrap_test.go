package msgtransfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

func managedOrdinaryBootstrapFixture(t *testing.T) (OwnedOrdinaryBootstrap, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	b := OwnedOrdinaryBootstrap{Version: "owned-ordinary-runtime-v1", Database: "synthetic_managed_ordinary", Epoch: uuid.NewString(), ActivationSHA256: deliveryprofile.SHA([]byte("synthetic activation")), BadgeEvidenceSHA256: deliveryprofile.SHA([]byte("synthetic badge")), Topic: "owned-source", ConsumerGroup: "owned-managed", PolicySHA256: deliveryprofile.SHA([]byte("synthetic policy")), VendorPath: filepath.Join(root, "vendors.json"), VendorSHA256: deliveryprofile.SHA([]byte("synthetic registry")), DrainPath: filepath.Join(root, "drain.json")}
	path := filepath.Join(root, "bootstrap.json")
	t.Setenv(OwnedOrdinaryBootstrapPathEnv, path)
	t.Setenv(messagedelivery.HistoryWindowModeEnv, "required")
	t.Setenv(messagedelivery.HistoryWindowEpochEnv, b.Epoch)
	t.Setenv(messagedelivery.HistoryWindowActivationEnv, b.ActivationSHA256)
	return b, path
}

func writeManagedBootstrapFixture(t *testing.T, path string, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(OwnedOrdinaryBootstrapSHAEnv, deliveryprofile.SHA(raw))
	return raw
}

func TestManagedOrdinaryBootstrapLoadsOnlyExactPrivateRequiredScope(t *testing.T) {
	b, path := managedOrdinaryBootstrapFixture(t)
	raw := writeManagedBootstrapFixture(t, path, b)
	got, err := LoadOwnedOrdinaryBootstrap(b.Database)
	if err != nil || got.Epoch != b.Epoch || got.Topic != b.Topic || got.inputSHA != deliveryprofile.SHA(raw) {
		t.Fatal("exact owned manifest rejected", err)
	}
	if _, err := os.Stat(b.DrainPath); !os.IsNotExist(err) {
		t.Fatal("bootstrap manufactured a drain receipt")
	}
	if _, err := LoadOwnedOrdinaryBootstrap(b.Database + "_foreign"); err == nil {
		t.Fatal("foreign database accepted")
	}
}

func TestManagedOrdinaryBootstrapRejectsIncompleteUnsafeOrChangedInputs(t *testing.T) {
	for _, fault := range []string{"legacy-mode", "epoch", "pin", "public-file", "existing-drain", "missing-field", "duplicate-field", "unknown-field"} {
		t.Run(fault, func(t *testing.T) {
			b, path := managedOrdinaryBootstrapFixture(t)
			raw := writeManagedBootstrapFixture(t, path, b)
			switch fault {
			case "legacy-mode":
				t.Setenv(messagedelivery.HistoryWindowModeEnv, "0")
			case "epoch":
				t.Setenv(messagedelivery.HistoryWindowEpochEnv, uuid.NewString())
			case "pin":
				t.Setenv(OwnedOrdinaryBootstrapSHAEnv, deliveryprofile.SHA([]byte("different")))
			case "public-file":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "existing-drain":
				if err := os.WriteFile(b.DrainPath, []byte("incomplete"), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-field":
				var fields map[string]any
				_ = json.Unmarshal(raw, &fields)
				delete(fields, "onlineCallbacksPath")
				writeManagedBootstrapFixture(t, path, fields)
			case "unknown-field":
				var fields map[string]any
				_ = json.Unmarshal(raw, &fields)
				fields["ready"] = true
				writeManagedBootstrapFixture(t, path, fields)
			case "duplicate-field":
				raw = append([]byte(`{"version":"owned-ordinary-runtime-v1",`), raw[1:]...)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(OwnedOrdinaryBootstrapSHAEnv, deliveryprofile.SHA(raw))
			}
			if _, err := LoadOwnedOrdinaryBootstrap(b.Database); err == nil {
				t.Fatal("unsafe or changed bootstrap accepted")
			}
		})
	}
}

func TestManagedOrdinaryProducerMatchesNamespaceLaunchCanonicalShape(t *testing.T) {
	for _, fault := range []string{"producer-and-child-loader-roundtrip", "alphabetic-map-reorder", "unknown-field", "missing-field", "duplicate-field", "wrong-value-type", "null-string", "wrong-pin", "public-file", "symlink-file", "hardlink-file", "trailing-json", "missing-file"} {
		t.Run(fault, func(t *testing.T) {
			b, path := managedOrdinaryBootstrapFixture(t)
			b.Version = ownedcontrol.NamespaceRuntimeManagedBootstrapVersion
			t.Setenv(ManagedOwnerAnnouncementPathEnv, filepath.Join(filepath.Dir(path), "owner.json"))
			t.Setenv(ManagedOwnerNonceEnv, strings.Repeat("f", 64))
			t.Setenv(ownedcontrol.NamespaceRuntimePreparationPathEnv, filepath.Join(filepath.Dir(path), "not-created-runtime.json"))
			t.Setenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv, strings.Repeat("a", 64))
			// Use the real producer type and the actual ServicePlan writer's
			// json.Marshal(struct)+newline wire convention, including empty callbacks.
			raw, e := json.Marshal(b)
			if e != nil {
				t.Fatal(e)
			}
			raw = append(raw, '\n')
			switch fault {
			case "alphabetic-map-reorder":
				var fields map[string]json.RawMessage
				if e := json.Unmarshal(raw, &fields); e != nil {
					t.Fatal(e)
				}
				reordered, e := json.Marshal(fields)
				if e != nil {
					t.Fatal(e)
				}
				reordered = append(reordered, '\n')
				if bytes.Equal(raw, reordered) {
					t.Fatal("fixture lost the struct/map canonical distinction")
				}
				raw = reordered
			case "unknown-field":
				raw = append(append([]byte(nil), raw[:len(raw)-2]...), []byte(",\"unexpected\":\"value\"}\n")...)
			case "missing-field":
				raw = bytes.Replace(raw, []byte(",\"onlineCallbacksPath\":\"\""), nil, 1)
			case "duplicate-field":
				version, e := json.Marshal(b.Version)
				if e != nil {
					t.Fatal(e)
				}
				prefix := append(append([]byte("{\"version\":"), version...), ',')
				raw = append(prefix, raw[1:]...)
			case "wrong-value-type":
				raw = bytes.Replace(raw, []byte("\"onlineCallbacksPath\":\"\""), []byte("\"onlineCallbacksPath\":7"), 1)
			case "null-string":
				raw = bytes.Replace(raw, []byte("\"onlineCallbacksPath\":\"\""), []byte("\"onlineCallbacksPath\":null"), 1)
			case "trailing-json":
				raw = append(raw, []byte("{}\n")...)
			}
			if e := os.WriteFile(path, raw, 0600); e != nil {
				t.Fatal(e)
			}
			pin := deliveryprofile.SHA(raw)
			t.Setenv(OwnedOrdinaryBootstrapSHAEnv, pin)
			switch fault {
			case "wrong-pin":
				pin = deliveryprofile.SHA([]byte("different source"))
			case "public-file":
				if e := os.Chmod(path, 0644); e != nil {
					t.Fatal(e)
				}
			case "symlink-file":
				retained := filepath.Join(filepath.Dir(path), "retained.json")
				if e := os.Rename(path, retained); e != nil {
					t.Fatal(e)
				}
				if e := os.Symlink(retained, path); e != nil {
					t.Fatal(e)
				}
			case "hardlink-file":
				if e := os.Link(path, filepath.Join(filepath.Dir(path), "hardlink.json")); e != nil {
					t.Fatal(e)
				}
			case "missing-file":
				if e := os.Remove(path); e != nil {
					t.Fatal(e)
				}
			}
			got, e := ownedcontrol.ReadNamespaceRuntimeManagedInput(path, pin)
			if fault != "producer-and-child-loader-roundtrip" {
				if !errors.Is(e, ownedcontrol.ErrOwnedEvidence) {
					t.Fatal("unsafe or changed private managed shape accepted", e)
				}
				return
			}
			if e != nil {
				t.Fatal("real producer canonical bytes rejected", e)
			}
			canonical, e := json.Marshal(got)
			if e != nil || !bytes.Equal(append(canonical, '\n'), raw) {
				t.Fatal("consumer shape changed any producer field/order")
			}
			var fields map[string]json.RawMessage
			if e := json.Unmarshal(raw, &fields); e != nil || len(fields) != 15 {
				t.Fatal("producer no longer has the fixed fifteen fields")
			}
			loaded, e := LoadOwnedOrdinaryBootstrap(b.Database)
			if e != nil || loaded.inputSHA != pin {
				t.Fatal("same producer bytes failed the actual child loader", e)
			}
			gate, e := ownedcontrol.NewNamespaceRuntimeStartupGate(os.Getenv(ownedcontrol.NamespaceRuntimePreparationPathEnv), strings.Repeat("a", 64), "msgtransfer-managed", b.Epoch, pin, strings.Repeat("a", 64), strings.Repeat("a", 64), strings.Repeat("f", 64))
			if e == nil || gate != nil {
				t.Fatal("loaded private shape manufactured runtime admission")
			}
			if _, e := os.Stat(b.DrainPath); !os.IsNotExist(e) {
				t.Fatal("shape reader manufactured a drain receipt")
			}
		})
	}
}
