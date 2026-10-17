package msgtransfer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
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
