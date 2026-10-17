//go:build darwin || linux

package ownedcontrol

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOwnedServerRuntimeEvidenceDoesNotInventLegacyEpoch(t *testing.T) {
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	nonce := strings.Repeat("a", 64)
	path := filepath.Join(dir, "ready.json")
	a := ServerRuntimeAnnouncement{Version: "owned-server-owner-ready-v1", Role: "rpc-group", Mode: "legacy", InstanceID: "e3d61bfc-643c-42dc-a65e-9acf0f726156", DatabaseSHA256: strings.Repeat("b", 64), BootstrapSHA256: strings.Repeat("c", 64), PolicySHA256: strings.Repeat("d", 64), DrainPath: filepath.Join(dir, "drain.json"), OwnedStopStepNames: []string{"accept", "handlers", "workers", "pools"}}
	t.Run("legacy actual identity round trip", func(t *testing.T) {
		sum, e := WriteServerRuntimeAnnouncement(path, nonce, a)
		if e != nil {
			t.Fatal(e)
		}
		got, actual, e := ReadServerRuntimeAnnouncement(path, nonce)
		if e != nil || actual != sum || got.Epoch != "" || got.ActivationSHA256 != "" {
			t.Fatal("legacy runtime was relabelled")
		}
		if _, _, e = ReadRuntimeAnnouncement(path, nonce); e == nil {
			t.Fatal("server identity impersonated managed")
		}
	})
	t.Run("legacy target epoch rejected", func(t *testing.T) {
		v := a
		v.Epoch = a.InstanceID
		v.ActivationSHA256 = strings.Repeat("e", 64)
		if v.validate() == nil {
			t.Fatal("legacy target epoch accepted")
		}
	})
	t.Run("required must carry actual runtime identity", func(t *testing.T) {
		v := a
		v.Mode = "required"
		if v.validate() == nil {
			t.Fatal("missing required identity")
		}
		v.Epoch = a.InstanceID
		v.ActivationSHA256 = strings.Repeat("e", 64)
		if v.validate() != nil {
			t.Fatal("actual required identity rejected")
		}
	})
	t.Run("unknown role cannot self enroll", func(t *testing.T) {
		v := a
		v.Role = "arbitrary-child"
		if v.validate() == nil {
			t.Fatal("unknown role accepted")
		}
	})
	t.Run("drain exact nonce and complete ordered join", func(t *testing.T) {
		d := ServerRuntimeDrain{Version: "owned-server-drained-v1", Role: a.Role, Mode: a.Mode, InstanceID: a.InstanceID, DatabaseSHA256: a.DatabaseSHA256, BootstrapSHA256: a.BootstrapSHA256, PolicySHA256: a.PolicySHA256, JoinedAndClosed: a.OwnedStopStepNames}
		if _, e := WriteServerRuntimeDrain(a.DrainPath, nonce, d); e != nil {
			t.Fatal(e)
		}
		if _, _, e := ReadServerRuntimeDrain(a.DrainPath, strings.Repeat("f", 64)); e == nil {
			t.Fatal("foreign nonce accepted")
		}
		d.SourceOutcomeUnconfirmed = true
		if d.validate() == nil {
			t.Fatal("unconfirmed drain accepted")
		}
	})
}
