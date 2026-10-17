//go:build darwin || linux

package namespaceimport

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImportPrivateJournalReopensExactPendingAndRejectsFork(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	j, err := OpenPrivateJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := State{Revision: 1, Phase: "pending-page", PendingAttemptID: "synthetic-attempt", PendingPage: PageIdentity{SHA256: digest("page")}}
	if err = j.CompareAndSwap(context.Background(), "", s); err != nil {
		t.Fatal(err)
	}
	if err = j.CompareAndSwap(context.Background(), "", s); !errors.Is(err, ErrConflict) {
		t.Fatal("fork accepted")
	}
	if _, err = OpenPrivateJournal(dir); !errors.Is(err, ErrConflict) {
		t.Fatal("concurrent journal owner admitted")
	}
	if err = j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = OpenPrivateJournal(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer j.Close()
	got, found, err := j.Load(context.Background())
	if err != nil || !found || digest(got) != digest(s) {
		t.Fatal("pending state not durable")
	}
	if err = os.Remove(filepath.Join(dir, "HEAD")); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(filepath.Join(dir, eventName(s.Revision, digest(s))), filepath.Join(dir, "HEAD")); err != nil {
		t.Fatal(err)
	}
	if _, _, err = j.Load(context.Background()); err == nil {
		t.Fatal("symlink head accepted")
	}
}
func TestImportStrictPrivateJSONRejectsNestedDuplicateAndOverflow(t *testing.T) {
	for _, body := range []string{`{"phase":"captured","phase":"published"}`, `{"header":{"tenant":"one","tenant":"two"}}`, `{"revision":1e400}`, `{"unknown":1}`} {
		var s State
		if strictJSON([]byte(body), &s) == nil {
			t.Fatalf("ambiguous record accepted: %s", body)
		}
	}
}
