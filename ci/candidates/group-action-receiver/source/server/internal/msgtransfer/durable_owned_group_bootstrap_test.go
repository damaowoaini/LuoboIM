package msgtransfer

import (
	"encoding/json"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedGroupBootstrapPrivateExactBindingAndDisabledCallbacks(t *testing.T) {
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private fixture unavailable")
	}
	value := OwnedGroupReceiverBootstrap{Version: "owned-group-receiver-bootstrap-v1", Database: "synthetic_bootstrap", Epoch: "41b33802-7959-4296-82e1-8d817bd8bf34", ActivationSHA256: strings.Repeat("a", 64), BadgeEvidenceSHA256: strings.Repeat("b", 64), VendorPath: filepath.Join(dir, "vendor.json"), VendorSHA256: strings.Repeat("c", 64)}
	raw, _ := json.Marshal(value)
	path := filepath.Join(dir, "bootstrap.json")
	if os.WriteFile(path, raw, 0600) != nil {
		t.Fatal("private input unavailable")
	}
	t.Setenv(OwnedGroupBootstrapPathEnv, path)
	t.Setenv(OwnedGroupBootstrapSHAEnv, deliveryprofile.SHA(raw))
	actual, err := LoadOwnedGroupReceiverBootstrap(value.Database)
	if err != nil || actual != value {
		t.Fatal("disabled callbacks could not retain exact private bootstrap", err)
	}
	if _, err = LoadOwnedGroupReceiverBootstrap("foreign"); err == nil {
		t.Fatal("bootstrap borrowed a different database")
	}
	if os.Chmod(path, 0644) != nil {
		t.Fatal("mode fixture unavailable")
	}
	if _, err = LoadOwnedGroupReceiverBootstrap(value.Database); err == nil {
		t.Fatal("public mode accepted")
	}
}
