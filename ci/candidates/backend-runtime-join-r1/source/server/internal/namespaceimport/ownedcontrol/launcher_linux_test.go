//go:build linux

package ownedcontrol

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func launcherFixture(t *testing.T) (*Launcher, string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	l, e := OpenOwnedLauncher(dir, strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	return l, dir
}
func TestOwnedLauncherHoldPersistsAndCannotPrepareMetadata(t *testing.T) {
	l, dir := launcherFixture(t)
	hold, e := l.Hold(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if hold.Validate() != nil || hold.ValidateExclusion() == nil {
		t.Fatal("bare lock became completed exclusion proof")
	}
	first := hold.RecordSHA256()
	if e = l.Close(); e != nil {
		t.Fatal(e)
	}
	reopened, e := OpenOwnedLauncher(dir, strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	same, e := reopened.Hold(context.Background())
	if e != nil || same.RecordSHA256() != first || same.LeaseID() != hold.LeaseID() {
		t.Fatal("reopen renewed cut/lease")
	}
	if service, e := reopened.Start(context.Background(), ServiceSpec{Role: "old-producer"}); e == nil || service != nil {
		t.Fatal("durable hold admitted writer")
	}
	_, state, e := reopened.load()
	if e != nil || len(state.Entries) != 0 {
		t.Fatal("rejected writer changed owner inventory")
	}
}
func TestOwnedLauncherRejectsOrphanStateAndForeignHeader(t *testing.T) {
	t.Run("foreign-header", func(t *testing.T) {
		l, dir := launcherFixture(t)
		l.Close()
		if other, e := OpenOwnedLauncher(dir, strings.Repeat("b", 64)); e == nil {
			other.Close()
			t.Fatal("foreign target adopted launcher")
		}
	})
	t.Run("orphan-state", func(t *testing.T) {
		l, dir := launcherFixture(t)
		h, _, e := l.load()
		if e != nil {
			t.Fatal(e)
		}
		l.Close()
		name := filepath.Join(dir, "state-00000000000000000002.json")
		if h.Sequence != 1 {
			t.Fatal("fixture sequence")
		}
		if e = os.WriteFile(name, []byte("{}\n"), 0600); e != nil {
			t.Fatal(e)
		}
		if other, e := OpenOwnedLauncher(dir, strings.Repeat("a", 64)); e == nil {
			other.Close()
			t.Fatal("orphan transaction was silently forked")
		}
	})
	t.Run("lock-alias", func(t *testing.T) {
		l, dir := launcherFixture(t)
		l.Close()
		if e := os.Remove(filepath.Join(dir, "launcher.lock")); e != nil {
			t.Fatal(e)
		}
		if e := os.Symlink("HEAD", filepath.Join(dir, "launcher.lock")); e != nil {
			t.Fatal(e)
		}
		if other, e := OpenOwnedLauncher(dir, strings.Repeat("a", 64)); e == nil {
			other.Close()
			t.Fatal("symlink launcher lock accepted")
		}
	})
}
