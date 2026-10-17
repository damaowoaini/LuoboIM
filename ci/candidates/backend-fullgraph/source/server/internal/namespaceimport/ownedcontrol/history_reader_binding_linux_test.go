//go:build linux

package ownedcontrol

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

func TestHistoryReadinessPrivateSessionRetainsIdentityAndUnknownIntent(t *testing.T) {
	ctx := context.Background()
	h := namespaceimport.Header{Tenant: "session-test", Database: "session_test", StorageInstance: "11111111-1111-4111-8111-111111111111", ReplicaSet: "test-rs", MongoProcess: strings.Repeat("b", 24), MongoTopology: 1, RedisRunID: strings.Repeat("c", 40), RedisDB: 15, KafkaClusterID: "synthetic-cluster", Epoch: "22222222-2222-4222-8222-222222222222", ActivationSHA256: strings.Repeat("d", 64), MigrationID: "33333333-3333-4333-8333-333333333333", DeploymentPolicySHA256: strings.Repeat("e", 64)}
	top := t.TempDir()
	if e := os.Chmod(top, 0700); e != nil {
		t.Fatal(e)
	}
	launcherPath := filepath.Join(top, "launcher")
	if e := os.Mkdir(launcherPath, 0700); e != nil {
		t.Fatal(e)
	}
	launcher, e := OpenOwnedLauncher(launcherPath, h.SHA256())
	if e != nil {
		t.Fatal(e)
	}
	defer launcher.Close()
	hold, e := launcher.Hold(ctx)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(top, "session.json")
	inventory, manifest, graph := strings.Repeat("1", 64), strings.Repeat("2", 64), strings.Repeat("3", 64)
	t.Run("bare hold cannot create a session or graph", func(t *testing.T) {
		if _, _, e := OpenHistoryReadinessSession(path, hold, h, inventory, manifest, graph); e == nil {
			t.Fatal("bare lock became completed exclusion")
		}
		if _, e := os.Lstat(path); !os.IsNotExist(e) {
			t.Fatal("refused hold created private state")
		}
		if _, e := ObserveHistoryReaderGraph(ctx, hold, h, graph, inventory, manifest, nil, nil); e == nil {
			t.Fatal("bare JSON/hold minted observed process graph")
		}
	})
	// Explicit synthetic exclusion ONLY to test private file durability. This does
	// not prove a real service stop, Kafka drain, credential exclusion or Ready.
	hold.exclusionSHA = strings.Repeat("f", 64)
	hold.exclusionAt = time.Now().UTC()
	original, sum, e := OpenHistoryReadinessSession(path, hold, h, inventory, manifest, graph)
	if e != nil {
		t.Fatal(e)
	}
	t.Run("same reopen never renews identity", func(t *testing.T) {
		again, at, e := OpenHistoryReadinessSession(path, hold, h, inventory, manifest, graph)
		if e != nil || again != original || at != sum {
			t.Fatal("session/token/cut renewed", e)
		}
		if _, _, e = OpenHistoryReadinessSession(path, hold, h, strings.Repeat("4", 64), manifest, graph); e == nil {
			t.Fatal("changed original inventory reused session")
		}
	})
	t.Run("unknown dispatch intent cannot authorize replay", func(t *testing.T) {
		intent := path + ".publish-intent"
		created, e := BeginHistoryReadinessIntent(intent, sum, "publish", graph)
		if e != nil || !created {
			t.Fatal(e)
		}
		created, e = BeginHistoryReadinessIntent(intent, sum, "publish", graph)
		if e != nil || created {
			t.Fatal("pending intent permitted replay", e)
		}
		if _, e = BeginHistoryReadinessIntent(intent, sum, "publish", manifest); e == nil {
			t.Fatal("different write replaced pending identity")
		}
	})
	t.Run("private alias and lost exclusion reject", func(t *testing.T) {
		alias := filepath.Join(top, "session-alias.json")
		if e := os.Link(path, alias); e != nil {
			t.Fatal(e)
		}
		if _, _, e = OpenHistoryReadinessSession(path, hold, h, inventory, manifest, graph); e == nil {
			t.Fatal("hardlinked private session accepted")
		}
		if e = os.Remove(alias); e != nil {
			t.Fatal(e)
		}
		hold.exclusionSHA = ""
		if _, _, e = OpenHistoryReadinessSession(path, hold, h, inventory, manifest, graph); e == nil {
			t.Fatal("expired/lost exclusion renewed session")
		}
	})
}
