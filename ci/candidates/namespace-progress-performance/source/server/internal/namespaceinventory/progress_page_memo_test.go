package namespaceinventory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
)

func progressMemoFixture(t *testing.T) (progressStore, progressState, progressPage, Limits) {
	t.Helper()
	s, dir := pageFixture(t), chunkDir(t)
	l := chunkFixtureLimits()
	if _, e := BeginResumableCapture(context.Background(), s, s.id.Binding, dir, l, ProgressBudget{Steps: 1, SourcePages: 1}); e != nil {
		t.Fatal(e)
	}
	_, p := progressAt(t, dir)
	if p.Pending == nil {
		t.Fatal("committed pending page fixture missing")
	}
	return progressStore{dir}, p, *p.Pending, l
}

func TestNamespaceProgressMemoKeepsCustody(t *testing.T) {
	for _, kind := range []string{"raw-tamper", "private-mode", "foreign-reference", "limits", "copy-isolation", "invocation-local", "bounded-eviction", "strict-redecode"} {
		t.Run(kind, func(t *testing.T) {
			s, st, meta, limits := progressMemoFixture(t)
			ctx := newProgressPageContext(context.Background())
			memo := ctx.Value(progressPageMemoContextKey{}).(*progressPageMemo)
			original, e := s.page(ctx, st, meta, limits)
			if e != nil || len(memo.Entries) != 1 {
				t.Fatal("strict parse did not populate invocation-local memo", e)
			}
			path := filepath.Join(s.dir, meta.File)
			raw, e := ReadPrivateDocument(path, MaxPrivateSnapshot)
			if e != nil {
				t.Fatal(e)
			}
			switch kind {
			case "raw-tamper":
				if os.WriteFile(path, append(raw, ' '), 0600) != nil {
					t.Fatal("tamper setup")
				}
				if _, e := s.page(ctx, st, meta, limits); e == nil {
					t.Fatal("memo bypassed freshly reread raw SHA")
				}
			case "private-mode":
				if os.Chmod(path, 0644) != nil {
					t.Fatal("mode setup")
				}
				if _, e := s.page(ctx, st, meta, limits); e == nil {
					t.Fatal("memo bypassed private-file custody")
				}
			case "foreign-reference":
				st.RawRoot = ""
				if _, e := s.page(ctx, st, meta, limits); e == nil {
					t.Fatal("memo bypassed current committed RawRoot")
				}
			case "limits":
				strict := limits
				strict.Bytes = 1
				if _, e := s.page(ctx, st, meta, strict); e == nil || len(memo.Entries) != 1 {
					t.Fatal("page validated under looser limits was reused")
				}
			case "copy-isolation":
				changed, e := s.page(ctx, st, meta, limits)
				if e != nil || len(changed.Mongo) == 0 || len(changed.Mongo[0].Raw) == 0 {
					t.Fatal("copy fixture", e)
				}
				changed.Mongo[0].Raw[0] ^= 1
				changed.Mongo[0].KeySHA = strings.Repeat("0", 64)
				again, e := s.page(ctx, st, meta, limits)
				if e != nil || !reflect.DeepEqual(again, original) {
					t.Fatal("returned page mutation poisoned a verified memo", e)
				}
				cursor := PageCursor{AfterID: []byte{1, 2}, Pending: []string{"old"}}
				copied := copyProgressCursor(cursor)
				copied.AfterID[0], copied.Pending[0] = 9, "changed"
				if cursor.AfterID[0] != 1 || cursor.Pending[0] != "old" {
					t.Fatal("cursor slices escaped memo ownership")
				}
			case "invocation-local":
				next := newProgressPageContext(ctx)
				nextMemo := next.Value(progressPageMemoContextKey{}).(*progressPageMemo)
				if nextMemo == memo || len(nextMemo.Entries) != 0 {
					t.Fatal("new invocation inherited parsed pages")
				}
				if _, e := s.page(next, st, meta, limits); e != nil || len(nextMemo.Entries) != 1 {
					t.Fatal("new invocation failed its own strict parse", e)
				}
				closeProgressPageContext(ctx)
				if !memo.Closed || memo.Entries != nil || memo.Order != nil || memo.Bytes != 0 {
					t.Fatal("returned invocation retained private parsed pages")
				}
				if _, e := s.page(ctx, st, meta, limits); e != nil || memo.Entries != nil {
					t.Fatal("closed invocation was reused or rejected valid uncached bytes", e)
				}
			case "bounded-eviction":
				for n := 0; n < progressPageMemoEntries+1; n++ {
					page := copyProgressPage(original)
					page.Mongo[0].KeySHA = digest([]byte(fmt.Sprint(n)))
					b, e := json.Marshal(page)
					if e != nil {
						t.Fatal(e)
					}
					if _, e := decodeProgressPage(ctx, digest(b), b, limits); e != nil {
						t.Fatal(e)
					}
					if len(memo.Entries) > progressPageMemoEntries || memo.Bytes > progressPageMemoBytes || memo.Bytes < 0 {
						t.Fatal("memo retained unbounded history")
					}
				}
				if _, e := s.page(ctx, st, meta, limits); e != nil {
					t.Fatal("eviction changed valid page admission", e)
				}
				large := copyProgressPage(original)
				large.Mongo[0] = rawRow(t, "group_member", "large", bson.M{"_id": "large", "payload": strings.Repeat("x", progressPageMemoBytes)})
				b, e := json.Marshal(large)
				if e != nil || len(b) <= progressPageMemoBytes {
					t.Fatal("uncached valid page setup", e)
				}
				beforeEntries, beforeBytes := len(memo.Entries), memo.Bytes
				if _, e := decodeProgressPage(ctx, digest(b), b, DefaultLimits()); e != nil || len(memo.Entries) != beforeEntries || memo.Bytes != beforeBytes {
					t.Fatal("memo size became a new page/history cap", e)
				}
			case "strict-redecode":
				duplicate := append([]byte("{\"mongo\":[],"), raw[1:]...)
				if _, e := decodeProgressPage(ctx, digest(duplicate), duplicate, limits); e == nil {
					t.Fatal("a new hash skipped strict duplicate-key parsing")
				}
			}
		})
	}
}

func TestNamespaceProgressRowSelectionPreservesEvidence(t *testing.T) {
	s := pageFixture(t)
	page := Page{Mongo: s.rows["msg"], Redis: s.keys}
	want := indexRows(page)
	if len(page.Mongo) == 0 || len(page.Redis) == 0 {
		t.Fatal("BSON/Redis evidence fixture missing")
	}
	for _, kind := range []string{"mongo-erased-and-ambiguous", "redis-head-and-reasons", "invalid-position"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "invalid-position" {
				for _, n := range []int{-1, len(want)} {
					if _, e := progressIndexRow(page, n); e == nil {
						t.Fatal("invalid row admitted")
					}
				}
				return
			}
			start, end := 0, len(page.Mongo)
			if kind == "redis-head-and-reasons" {
				start, end = len(page.Mongo), len(want)
			}
			for n := start; n < end; n++ {
				got, e := progressIndexRow(page, n)
				if e != nil || !reflect.DeepEqual(got, want[n]) {
					t.Fatal("selected row differs from frozen whole-page classifier", n, e)
				}
			}
		})
	}
}
