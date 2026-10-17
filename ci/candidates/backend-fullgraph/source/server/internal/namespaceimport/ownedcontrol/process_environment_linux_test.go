//go:build linux

package ownedcontrol

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func environmentFixtureFile(t *testing.T, v OwnedServiceEnvironment) (ServiceSpec, []byte) {
	t.Helper()
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private environment directory unavailable")
	}
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	b = append(b, '\n')
	path := filepath.Join(dir, "environment.json")
	if os.WriteFile(path, b, 0600) != nil {
		t.Fatal("private environment write failed")
	}
	return ServiceSpec{Role: v.Role, PrivateEnvironmentPath: path, PrivateEnvironmentSHA256: sha(b)}, b
}
func TestOwnedServicePrivateEnvironmentKeepsIdentityAndRejectsOwnerOverrides(t *testing.T) {
	valid := OwnedServiceEnvironment{Version: "owned-service-environment-v1", Role: "rpc-rtc", Values: map[string]string{"LUOBO_RTC_API_KEY": "synthetic-key", "LUOBO_RTC_API_SECRET": "synthetic-secret-not-a-credential", "LUOBO_RTC_LIVE_URL": "ws://127.0.0.1:47891", "LUOBO_RTC_ALLOW_LOOPBACK": "true"}}
	t.Run("default-without-private-file-keeps-original-environment", func(t *testing.T) {
		rows, e := loadOwnedServiceEnvironment(ServiceSpec{Role: "rpc-rtc"})
		if e != nil || rows != nil {
			t.Fatal("default path changed")
		}
	})
	t.Run("actual-private-file-exact-SHA-and-sorted-constructor-keys", func(t *testing.T) {
		spec, _ := environmentFixtureFile(t, valid)
		rows, e := loadOwnedServiceEnvironment(spec)
		if e != nil {
			t.Fatal("reviewed private environment rejected")
		}
		want := []string{"LUOBO_RTC_ALLOW_LOOPBACK=true", "LUOBO_RTC_API_KEY=synthetic-key", "LUOBO_RTC_API_SECRET=synthetic-secret-not-a-credential", "LUOBO_RTC_LIVE_URL=ws://127.0.0.1:47891"}
		if !reflect.DeepEqual(rows, want) {
			t.Fatal("exact constructor environment changed")
		}
	})
	t.Run("partial-pair-wrong-SHA-or-foreign-role-rejects", func(t *testing.T) {
		spec, _ := environmentFixtureFile(t, valid)
		bad := []ServiceSpec{spec, spec, spec, spec}
		bad[0].PrivateEnvironmentPath = ""
		bad[1].PrivateEnvironmentSHA256 = ""
		bad[2].PrivateEnvironmentSHA256 = strings.Repeat("a", 64)
		bad[3].Role = "rpc-user"
		for _, v := range bad {
			if rows, e := loadOwnedServiceEnvironment(v); e == nil || rows != nil {
				t.Fatal("unbound private input accepted")
			}
		}
	})
	t.Run("HOME-PATH-TMP-and-admission-keys-cannot-be-overridden", func(t *testing.T) {
		for _, key := range []string{"HOME", "PATH", "TMPDIR", "CODEX_HOME", "LUOBO_GROUP_HISTORY_WINDOW_V1", "LUOBO_GROUP_HISTORY_WINDOW_EPOCH", "LUOBO_NAMESPACE_RUNTIME_PREPARATION_PATH", "LUOBO_OWNED_SERVER_BOOTSTRAP_PATH"} {
			v := OwnedServiceEnvironment{Version: valid.Version, Role: valid.Role, Values: map[string]string{key: "synthetic"}}
			spec, _ := environmentFixtureFile(t, v)
			if rows, e := loadOwnedServiceEnvironment(spec); e == nil || rows != nil {
				t.Fatal("source owner key override accepted")
			}
		}
	})
	t.Run("duplicate-JSON-key-and-nonprivate-file-reject", func(t *testing.T) {
		spec, _ := environmentFixtureFile(t, valid)
		b := []byte(`{"version":"owned-service-environment-v1","role":"rpc-rtc","values":{"LUOBO_RTC_API_KEY":"one","LUOBO_RTC_API_KEY":"two"}}` + "\n")
		if os.WriteFile(spec.PrivateEnvironmentPath, b, 0600) != nil {
			t.Fatal("private fixture update failed")
		}
		spec.PrivateEnvironmentSHA256 = sha(b)
		if _, e := loadOwnedServiceEnvironment(spec); e == nil {
			t.Fatal("duplicate configuration accepted")
		}
		spec, _ = environmentFixtureFile(t, valid)
		if os.Chmod(spec.PrivateEnvironmentPath, 0644) != nil {
			t.Fatal("fixture mode change failed")
		}
		if _, e := loadOwnedServiceEnvironment(spec); e == nil {
			t.Fatal("public credential file accepted")
		}
	})
	t.Run("unknown-constructor-key-NUL-newline-and-size-reject", func(t *testing.T) {
		for _, value := range []string{"bad\x00value", "bad\nvalue", strings.Repeat("v", 4097)} {
			v := valid
			v.Values = map[string]string{"LUOBO_RTC_API_KEY": value}
			spec, _ := environmentFixtureFile(t, v)
			if _, e := loadOwnedServiceEnvironment(spec); e == nil {
				t.Fatal("unbounded or ambiguous value accepted")
			}
		}
		v := valid
		v.Values = map[string]string{"LUOBO_FAKE_READY": "true"}
		spec, _ := environmentFixtureFile(t, v)
		if _, e := loadOwnedServiceEnvironment(spec); e == nil {
			t.Fatal("unknown runtime override accepted")
		}
	})
	t.Run("group-secondary-bootstrap-is-role-bound-complete-private-and-exact", func(t *testing.T) {
		dir := t.TempDir()
		if os.Chmod(dir, 0700) != nil {
			t.Fatal("private group source directory unavailable")
		}
		path := filepath.Join(dir, "group-input.json")
		raw := []byte("{\"version\":\"source-loader-still-validates-the-full-original-manifest\"}\n")
		if os.WriteFile(path, raw, 0600) != nil {
			t.Fatal("private source write failed")
		}
		value := OwnedServiceEnvironment{Version: valid.Version, Role: "rpc-group", Values: map[string]string{"LUOBO_GROUP_OWNED_RECEIVER_INPUT": path, "LUOBO_GROUP_OWNED_RECEIVER_INPUT_SHA256": sha(raw)}}
		spec, _ := environmentFixtureFile(t, value)
		actual, e := loadOwnedServiceEnvironment(spec)
		if e != nil || !reflect.DeepEqual(actual, []string{"LUOBO_GROUP_OWNED_RECEIVER_INPUT=" + path, "LUOBO_GROUP_OWNED_RECEIVER_INPUT_SHA256=" + sha(raw)}) {
			t.Fatal("actual group constructor source cannot reach its existing child loader")
		}
		// No semantic grant is minted here: deliberately incomplete input is
		// retained verbatim for the actual child parser, never treated as Ready.
		for _, kind := range []string{"path-only", "sha-only", "wrong-role", "wrong-body", "public-file", "symlink", "foreign-owner-key"} {
			copy := OwnedServiceEnvironment{Version: value.Version, Role: value.Role, Values: map[string]string{}}
			for key, v := range value.Values {
				copy.Values[key] = v
			}
			switch kind {
			case "path-only":
				delete(copy.Values, "LUOBO_GROUP_OWNED_RECEIVER_INPUT_SHA256")
			case "sha-only":
				delete(copy.Values, "LUOBO_GROUP_OWNED_RECEIVER_INPUT")
			case "wrong-role":
				copy.Role = "rpc-user"
			case "wrong-body":
				copy.Values["LUOBO_GROUP_OWNED_RECEIVER_INPUT_SHA256"] = strings.Repeat("a", 64)
			case "public-file":
				if os.Chmod(path, 0644) != nil {
					t.Fatal("fixture mode update failed")
				}
			case "symlink":
				alias := filepath.Join(dir, "alias.json")
				if os.Symlink(path, alias) != nil {
					t.Fatal("fixture alias unavailable")
				}
				copy.Values["LUOBO_GROUP_OWNED_RECEIVER_INPUT"] = alias
			case "foreign-owner-key":
				copy.Values["LUOBO_NAMESPACE_RUNTIME_PREPARATION_PATH"] = path
			}
			bad, _ := environmentFixtureFile(t, copy)
			if rows, e := loadOwnedServiceEnvironment(bad); e == nil || rows != nil {
				t.Fatal("unbound group secondary source was admitted: " + kind)
			}
			if os.Chmod(path, 0600) != nil {
				t.Fatal("fixture mode restore failed")
			}
		}
	})

}
