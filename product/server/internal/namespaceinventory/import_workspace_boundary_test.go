package namespaceinventory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func snapshotEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, e := os.ReadDir(dir)
	if e != nil {
		t.Fatal(e)
	}
	out := []string{}
	for _, entry := range entries {
		out = append(out, entry.Name())
	}
	sort.Strings(out)
	return out
}
func TestImportWorkspaceRejectsSnapshotChildrenAndAliasesBeforeWrite(t *testing.T) {
	source, rawDir, snapshot := importSnapshotFixture(t)
	ctx := context.Background()
	header, policy := jsonSHA("header"), jsonSHA("policy")
	aliases := t.TempDir()
	reject := func(t *testing.T, path string, resume bool) {
		t.Helper()
		before := snapshotEntryNames(t, rawDir)
		if _, e := OpenImportWorkspace(ctx, snapshot, source, path, header, policy, resume); !errors.Is(e, ErrInvalid) {
			t.Fatal("snapshot path was not rejected before create/load", e)
		}
		if !reflect.DeepEqual(before, snapshotEntryNames(t, rawDir)) {
			t.Fatal("rejected workspace added original snapshot entries")
		}
	}
	t.Run("fresh same original root", func(t *testing.T) { reject(t, rawDir, false) })
	t.Run("fresh child under original root", func(t *testing.T) {
		path := filepath.Join(rawDir, "forbidden-new-workspace")
		reject(t, path, false)
		if _, e := os.Lstat(path); !errors.Is(e, os.ErrNotExist) {
			t.Fatal("fresh rejection created a directory")
		}
	})
	t.Run("resume preexisting original child", func(t *testing.T) {
		path := filepath.Join(rawDir, "preexisting-owned-child")
		if e := os.Mkdir(path, 0700); e != nil {
			t.Fatal(e)
		}
		reject(t, path, true)
	})
	t.Run("fresh and resume aliases into original root", func(t *testing.T) {
		alias := filepath.Join(aliases, "raw-alias")
		if e := os.Symlink(rawDir, alias); e != nil {
			t.Fatal(e)
		}
		reject(t, filepath.Join(alias, "new-via-alias"), false)
		reject(t, alias, true)
	})
	t.Run("legal independent sibling create and resume", func(t *testing.T) {
		path := filepath.Join(filepath.Dir(rawDir), filepath.Base(rawDir)+"-independent-derived")
		before := snapshotEntryNames(t, rawDir)
		w, e := OpenImportWorkspace(ctx, snapshot, source, path, header, policy, false)
		if e != nil {
			t.Fatal("legal sibling rejected", e)
		}
		_, expected, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		again, e := OpenImportWorkspace(ctx, snapshot, source, path, header, policy, true)
		if e != nil {
			t.Fatal(e)
		}
		_, actual, e := again.Checkpoint(ctx)
		if e != nil || actual != expected {
			t.Fatal("legal sibling resume changed", e)
		}
		if !reflect.DeepEqual(before, snapshotEntryNames(t, rawDir)) {
			t.Fatal("legal sibling changed original snapshot")
		}
	})
}
