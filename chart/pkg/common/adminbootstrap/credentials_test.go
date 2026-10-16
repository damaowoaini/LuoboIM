package adminbootstrap

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBootstrapCredentialsRequireExplicitPrivateSecret(t *testing.T) {
	t.Setenv(FileEnv, "")
	if _, err := Load([]string{"admin"}); err == nil {
		t.Fatal("predictable fallback accepted")
	}
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	t.Setenv(FileEnv, path)
	for _, test := range []struct {
		body    string
		mode    os.FileMode
		allowed bool
	}{{`{"admin":"admin"}`, 0600, false}, {`{"other":"a-strong-private-initial-password"}`, 0600, false}, {`{"admin":"a-strong-private-initial-password"}`, 0644, false}, {`{"admin":"a-strong-private-initial-password"}`, 0600, true}} {
		if err := os.WriteFile(path, []byte(test.body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, test.mode); err != nil {
			t.Fatal(err)
		}
		values, err := Load([]string{"admin"})
		if (err == nil) != test.allowed {
			t.Fatal("invalid bootstrap acceptance result")
		}
		if err == nil && (len(values["admin"]) != 32 || strings.Contains(values["admin"], "private")) {
			t.Fatal("wire credential derivation invalid")
		}
	}
}
