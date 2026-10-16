package namespaceinventory

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func privateFixtureDir(t *testing.T) string {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil || os.Chmod(dir, 0700) != nil {
		t.Fatal("private fixture directory unavailable")
	}
	return dir
}
func writeFixture(t *testing.T, path string, raw []byte) {
	t.Helper()
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal("private fixture write failed")
	}
}

func TestInventoryPrivateRoundTripAndNoClobber(t *testing.T) {
	snapshot := captureFixture(t, &scriptedSource{})
	path := filepath.Join(privateFixtureDir(t), "snapshot.json")
	if e := CheckFreshPrivatePath(path); e != nil {
		t.Fatal(e)
	}
	if e := SaveExclusive(path, snapshot); e != nil {
		t.Fatal(e)
	}
	public, e := Inspect(path)
	if e != nil || public.SnapshotSHA != snapshot.SHA || public.State != State {
		t.Fatal("private round trip failed")
	}
	before, e := ReadPrivateDocument(path, MaxPrivateSnapshot)
	if e != nil {
		t.Fatal(e)
	}
	if !errors.Is(SaveExclusive(path, snapshot), ErrPrivate) || !errors.Is(CheckFreshPrivatePath(path), ErrPrivate) {
		t.Fatal("existing private evidence was overwritten")
	}
	after, e := ReadPrivateDocument(path, MaxPrivateSnapshot)
	if e != nil || string(after) != string(before) {
		t.Fatal("no-clobber changed evidence")
	}
}

func TestInventoryRejectsPrivateTamperAndPathAliases(t *testing.T) {
	for _, name := range []string{"changed-derived-data", "duplicate-json-field", "world-readable", "hard-link", "symlink-parent"} {
		t.Run(name, func(t *testing.T) {
			snapshot := captureFixture(t, &scriptedSource{})
			dir := privateFixtureDir(t)
			path := filepath.Join(dir, "snapshot.json")
			if e := SaveExclusive(path, snapshot); e != nil {
				t.Fatal(e)
			}
			switch name {
			case "changed-derived-data":
				snapshot.Observations = []Observation{{Source: "pretend", Reasons: []string{"private-secret"}}}
				snapshot.SHA = snapshotDigest(*snapshot)
				raw, _ := json.Marshal(snapshot)
				writeFixture(t, path, raw)
			case "duplicate-json-field":
				raw, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				raw = append([]byte(`{"Version":1,`), raw[1:]...)
				writeFixture(t, path, raw)
			case "world-readable":
				if os.Chmod(path, 0644) != nil {
					t.Fatal("chmod failed")
				}
			case "hard-link":
				if os.Link(path, filepath.Join(dir, "second.json")) != nil {
					t.Fatal("link failed")
				}
			case "symlink-parent":
				link := filepath.Join(privateFixtureDir(t), "alias")
				if os.Symlink(dir, link) != nil {
					t.Fatal("symlink failed")
				}
				path = filepath.Join(link, "snapshot.json")
			}
			if _, e := Inspect(path); e == nil {
				t.Fatal("invalid private evidence accepted")
			}
		})
	}
}

func TestInventoryPrivateConnectionIsExplicitBoundInput(t *testing.T) {
	base := PrivateConnection{Version: 1, Binding: fixtureIdentity().Binding, MongoURI: "mongodb://127.0.0.1:38417/synthetic_db?replicaSet=synthetic-rs", RedisURL: "redis://127.0.0.1:36379/15"}
	for _, tc := range []struct {
		name   string
		mutate func(*PrivateConnection)
		valid  bool
	}{
		{"valid-explicit", func(*PrivateConnection) {}, true},
		{"wrong-database", func(c *PrivateConnection) { c.MongoURI = strings.Replace(c.MongoURI, "/synthetic_db?", "/other?", 1) }, false},
		{"wrong-replica-set", func(c *PrivateConnection) { c.MongoURI = strings.Replace(c.MongoURI, "=synthetic-rs", "=other", 1) }, false},
		{"direct-primary-ambiguity", func(c *PrivateConnection) { c.MongoURI += "&directConnection=true" }, false},
		{"wrong-redis-database", func(c *PrivateConnection) { c.RedisURL = "redis://127.0.0.1:36379/0" }, false},
		{"duplicate-uri-option", func(c *PrivateConnection) { c.MongoURI += "&REPLICASET=synthetic-rs" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.mutate(&c)
			if c.valid() != tc.valid {
				t.Fatal("explicit connection binding contract")
			}
		})
	}
	path := filepath.Join(privateFixtureDir(t), "connection.json")
	raw, _ := json.Marshal(base)
	writeFixture(t, path, raw)
	if _, e := LoadPrivateConnection(path); e != nil {
		t.Fatal("valid private connection rejected")
	}
	raw = append([]byte(`{"mongoURI":"never-echo-this-credential",`), raw[1:]...)
	writeFixture(t, path, raw)
	if _, e := LoadPrivateConnection(path); e == nil {
		t.Fatal("duplicate connection alias accepted")
	}
	if strings.Contains(SafeFailure(errors.New("never-echo-this-credential")), "never-echo-this-credential") {
		t.Fatal("driver error leaked")
	}
}
