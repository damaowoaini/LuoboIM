package msgtransfer

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestManagedOwnerAnnouncementRequiresExactPairAndFreshPrivateOutput(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ready.json")
	nonce := strings.Repeat("b", 64)
	for _, tc := range []struct {
		name, path, nonce string
		ok                bool
	}{
		{"both-absent", "", "", true}, {"fresh-pair", path, nonce, true}, {"no-nonce", path, "", false}, {"no-path", "", nonce, false}, {"bad-nonce", path, strings.Repeat("B", 64), false}, {"relative-path", "ready.json", nonce, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(ManagedOwnerAnnouncementPathEnv, tc.path)
			t.Setenv(ManagedOwnerNonceEnv, tc.nonce)
			p, n, err := LoadManagedOwnerAnnouncement()
			if (err == nil) != tc.ok {
				t.Fatal("paired private output admission differs", err)
			}
			if err == nil && (p != tc.path || n != tc.nonce) {
				t.Fatal("source owner values changed")
			}
		})
	}
	if err = os.WriteFile(path, []byte("reserved"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(ManagedOwnerAnnouncementPathEnv, path)
	t.Setenv(ManagedOwnerNonceEnv, nonce)
	if _, _, err = LoadManagedOwnerAnnouncement(); err == nil {
		t.Fatal("preexisting announcement could be adopted")
	}
}

func TestManagedOwnerAnnouncementCannotClaimStartOrDrainAndClonesOwnedNames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newManagedOrdinaryRuntime(cancel)
	if _, err := r.OwnerAnnouncement(ctx); err == nil {
		t.Fatal("unstarted resources announced ready")
	}
	base := filepath.Join(string(filepath.Separator), "owned", "runtime")
	r.bootstrap = OwnedOrdinaryBootstrap{Version: "owned-ordinary-runtime-v1", Database: "owned-fixture", Epoch: uuid.NewString(), ActivationSHA256: strings.Repeat("a", 64), BadgeEvidenceSHA256: strings.Repeat("b", 64), Topic: "ordinary", ConsumerGroup: "owned-group", PolicySHA256: strings.Repeat("c", 64), VendorPath: filepath.Join(base, "vendor.json"), VendorSHA256: strings.Repeat("d", 64), DrainPath: filepath.Join(base, "drain.json"), inputSHA: strings.Repeat("e", 64)}
	r.instance = uuid.NewString()
	r.intake = &ManagedOrdinaryKafka{}
	r.offline = &OwnedOrdinaryOfflineRuntime{}
	r.intakeStarted = true
	r.steps = []ordinaryStopStep{{name: "intake"}, {name: "online"}, {name: "offline"}, {name: "mongo"}}
	announcement, err := r.OwnerAnnouncement(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if announcement.Version != "managed-ordinary-owner-ready-v1" || announcement.InstanceID != r.instance || announcement.BootstrapSHA256 != r.bootstrap.inputSHA || announcement.DrainPath != r.bootstrap.DrainPath {
		t.Fatal("original runtime binding lost")
	}
	announcement.OwnedStopStepNames[0] = "foreign"
	again, err := r.OwnerAnnouncement(ctx)
	if err != nil || again.OwnedStopStepNames[0] != "intake" {
		t.Fatal("announcement mutated owned shutdown order", err)
	}
	r.completed = 1
	if _, err = r.OwnerAnnouncement(ctx); err == nil {
		t.Fatal("partly stopping runtime announced ready")
	}
	r.completed = 0
	r.complete = true
	if _, err = r.OwnerAnnouncement(ctx); err == nil {
		t.Fatal("completed drain renewed ready intent")
	}
}
