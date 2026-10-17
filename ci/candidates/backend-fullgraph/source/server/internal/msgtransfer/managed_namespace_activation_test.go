package msgtransfer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

func TestManagedOrdinaryNamespaceBootstrapPreservesIsolatedDefaultAndRejectsSupervisedFallback(t *testing.T) {
	for _, mode := range []string{"original-isolated-default", "old-supervised-version", "partial-new-ticket", "shape-is-not-runtime-permission"} {
		t.Run(mode, func(t *testing.T) {
			b, path := managedOrdinaryBootstrapFixture(t)
			t.Setenv(ManagedOwnerAnnouncementPathEnv, "")
			t.Setenv(ManagedOwnerNonceEnv, "")
			t.Setenv(ownedcontrol.NamespaceRuntimePreparationPathEnv, "")
			t.Setenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv, "")
			if mode != "original-isolated-default" {
				t.Setenv(ManagedOwnerAnnouncementPathEnv, filepath.Join(filepath.Dir(path), "owner.json"))
				t.Setenv(ManagedOwnerNonceEnv, strings.Repeat("f", 64))
			}
			if mode == "partial-new-ticket" || mode == "shape-is-not-runtime-permission" {
				b.Version = ownedcontrol.NamespaceRuntimeManagedBootstrapVersion
				t.Setenv(ownedcontrol.NamespaceRuntimePreparationPathEnv, filepath.Join(filepath.Dir(path), "not-created-runtime.json"))
			}
			if mode == "shape-is-not-runtime-permission" {
				t.Setenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv, strings.Repeat("a", 64))
			}
			writeManagedBootstrapFixture(t, path, b)
			_, err := LoadOwnedOrdinaryBootstrap(b.Database)
			valid := mode == "original-isolated-default" || mode == "shape-is-not-runtime-permission"
			if (err == nil) != valid {
				t.Fatal("private version/admission contract changed", err)
			}
			if mode == "shape-is-not-runtime-permission" {
				if g, err := ownedcontrol.NewNamespaceRuntimeStartupGate(os.Getenv(ownedcontrol.NamespaceRuntimePreparationPathEnv), strings.Repeat("a", 64), "msgtransfer-managed", b.Epoch, strings.Repeat("a", 64), strings.Repeat("a", 64), strings.Repeat("a", 64), strings.Repeat("f", 64)); err == nil || g != nil {
					t.Fatal("loaded bootstrap shape manufactured actual runtime capability")
				}
			}
			if _, err := os.Stat(b.DrainPath); !os.IsNotExist(err) {
				t.Fatal("bootstrap manufactured drain")
			}
		})
	}
}
