package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

type emptyReadSource struct{ binding namespaceinventory.Binding }

func (s emptyReadSource) Identity(context.Context) (namespaceinventory.Identity, error) {
	return namespaceinventory.Identity{Binding: s.binding, MongoProcess: strings.Repeat("b", 24), MongoTopology: 1, RedisReplication: strings.Repeat("c", 40)}, nil
}
func (emptyReadSource) Mongo(context.Context, func(namespaceinventory.MongoRow) error) error {
	return nil
}
func (emptyReadSource) Redis(context.Context, func(namespaceinventory.RedisRow) error) error {
	return nil
}

func TestNamespaceCaptureCLIInspectAndSafeErrors(t *testing.T) {
	b := namespaceinventory.Binding{Tenant: "private-tenant", Database: "private-db", StorageInstance: "1cfad324-14cd-40bf-b688-2736144cbf32", ReplicaSet: "synthetic-rs", RedisRunID: strings.Repeat("a", 40), RedisDB: 15}
	snapshot, e := namespaceinventory.Capture(context.Background(), emptyReadSource{binding: b}, b, namespaceinventory.DefaultLimits())
	if e != nil {
		t.Fatal(e)
	}
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(dir, 0700) != nil {
		t.Fatal("private directory unavailable")
	}
	path := filepath.Join(dir, "snapshot.json")
	if e := namespaceinventory.SaveExclusive(path, snapshot); e != nil {
		t.Fatal(e)
	}
	t.Run("default-inspect-no-connection", func(t *testing.T) {
		var output bytes.Buffer
		if e := run(context.Background(), []string{"-snapshot", path}, &output); e != nil {
			t.Fatal(e)
		}
		var summary namespaceinventory.PublicSummary
		if json.Unmarshal(output.Bytes(), &summary) != nil || summary.State != namespaceinventory.State || summary.SnapshotSHA != snapshot.SHA {
			t.Fatal("inspect output contract")
		}
		if strings.Contains(output.String(), "private-tenant") || strings.Contains(output.String(), "private-db") {
			t.Fatal("private identity leaked")
		}
	})
	t.Run("no-implicit-capture", func(t *testing.T) {
		var output bytes.Buffer
		if e := run(context.Background(), []string{"-snapshot", path, "-connection", "/must-not-open"}, &output); e == nil || output.Len() != 0 {
			t.Fatal("default mode opened a connection")
		}
	})
	t.Run("no-activation-or-credential-flags", func(t *testing.T) {
		for _, args := range [][]string{{"-mode", "apply", "-snapshot", path}, {"-mongo-uri", "never-echo-this-credential", "-snapshot", path}} {
			var output bytes.Buffer
			if e := run(context.Background(), args, &output); e == nil || output.Len() != 0 {
				t.Fatal("unknown/write mode accepted or echoed")
			}
		}
	})
}
