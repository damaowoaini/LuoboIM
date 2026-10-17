package namespaceinventory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Each real one-leaf get/next visits ctx.Err once. This counts actual lookup
// visits, without replacing the private reader, decoder, writer or filesystem.
type progressMetadataVisits struct {
	context.Context
	visits int
}

func (c *progressMetadataVisits) Err() error {
	c.visits++
	return c.Context.Err()
}

func progressMetadataFixture(t *testing.T) (progressStore, progressState, progressPage, progressRow, Limits) {
	t.Helper()
	s, st, meta, limits := progressMemoFixture(t)
	page, e := s.page(context.Background(), st, meta, limits)
	if e != nil {
		t.Fatal(e)
	}
	rec, e := progressIndexRow(page, 0)
	if e != nil {
		t.Fatal(e)
	}
	ref := progressRef{Number: meta.Number, Row: 0, MetaSHA: jsonSHA(meta)}
	return s, st, meta, storedRow(rec, ref), limits
}

func TestNamespaceProgressMetadataLookupOncePerReference(t *testing.T) {
	for _, kind := range []string{"checked-row", "summary-seen"} {
		t.Run(kind, func(t *testing.T) {
			s, st, meta, row, limits := progressMetadataFixture(t)
			if kind == "summary-seen" {
				second := meta
				second.Number, second.Pass = 2, 2
				root, e := s.put(context.Background(), st.RawRoot, pageNumberKey(2), second)
				if e != nil {
					t.Fatal(e)
				}
				st.RawRoot, st.RawCount = root, 2
				key := digest([]byte(row.Source))
				st.RowsRoot, e = s.put(context.Background(), "", key, row)
				if e != nil {
					t.Fatal(e)
				}
				ref := progressRef{Number: 2, Row: 0, MetaSHA: jsonSHA(second)}
				st.SeenRoot, e = s.put(context.Background(), "", key, ref)
				if e != nil {
					t.Fatal(e)
				}
				st.Phase, st.After, st.RowsCount, st.SeenCount = "summary-seen", "", 1, 1
			}
			for _, root := range []string{st.RawRoot, st.RowsRoot, st.SeenRoot} {
				if root != "" {
					n, e := s.node(root, "")
					if e != nil || len(n.Children) != 0 {
						t.Fatal("lookup-count fixture must use actual one-leaf indexes", e)
					}
				}
			}
			ctx := &progressMetadataVisits{Context: context.Background()}
			if kind == "checked-row" {
				got, e := s.checkedRow(ctx, st, row, limits)
				if e != nil || got.Source != row.Source || got.ContentSHA != row.SHA || ctx.visits != 1 {
					t.Fatalf("checkedRow must resolve once without changing evidence: visits=%d err=%v", ctx.visits, e)
				}
			} else {
				if e := s.summaryStep(ctx, chunkSession{Limits: limits}, &st); e != nil || st.ReducedSeen != 1 || st.After != digest([]byte(row.Source)) || ctx.visits != 4 {
					t.Fatalf("seen next + rows get + exactly two raw lookups required: visits=%d err=%v", ctx.visits, e)
				}
			}
		})
	}
}

func TestNamespaceProgressResolvedMetadataRetainsBindings(t *testing.T) {
	for _, kind := range []string{"pass-and-metaSHA", "number-and-row", "legacy-checkpoint", "private-page-and-row"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "legacy-checkpoint" {
				progressMetadataLegacyBinding(t)
				return
			}
			s, st, meta, row, limits := progressMetadataFixture(t)
			rebind := func(m progressPage) (progressState, progressRow) {
				next, copyRow := st, row
				var e error
				next.RawRoot, e = s.put(context.Background(), st.RawRoot, pageNumberKey(row.Ref.Number), m)
				if e != nil {
					t.Fatal(e)
				}
				copyRow.Ref.MetaSHA = jsonSHA(m)
				return next, copyRow
			}
			reject := func(state progressState, value progressRow) {
				t.Helper()
				if _, e := s.checkedRow(context.Background(), state, value, limits); e == nil {
					t.Fatal("resolved metadata lost an original reference or raw binding")
				}
			}
			switch kind {
			case "pass-and-metaSHA":
				changed := meta
				changed.Pass = 2
				p, value := rebind(changed)
				reject(p, value)
				if _, e := s.hydrateForPass(context.Background(), st, row.Ref, limits, 2); e == nil {
					t.Fatal("summary-seen accepted pass1 as pass2")
				}
				bad := row
				bad.Ref.MetaSHA = strings.Repeat("0", 64)
				reject(st, bad)
				if _, e := s.hydrate(context.Background(), st, row.Ref, limits); e != nil {
					t.Fatal("ordinary valid hydrate changed", e)
				}
			case "number-and-row":
				for _, ref := range []progressRef{{Number: 0, Row: 0, MetaSHA: row.Ref.MetaSHA}, {Number: st.RawCount + 1, Row: 0, MetaSHA: row.Ref.MetaSHA}, {Number: row.Ref.Number, Row: -1, MetaSHA: row.Ref.MetaSHA}, {Number: row.Ref.Number, Row: limits.Rows, MetaSHA: row.Ref.MetaSHA}} {
					bad := row
					bad.Ref = ref
					reject(st, bad)
				}
				changed := meta
				changed.Number = 2
				p, value := rebind(changed)
				var e error
				p.RawRoot, e = s.put(context.Background(), p.RawRoot, pageNumberKey(2), changed)
				if e != nil {
					t.Fatal(e)
				}
				p.RawCount = 2
				reject(p, value)
			case "private-page-and-row":
				for _, field := range []string{"source", "mongo", "SHA", "TTL"} {
					bad := row
					switch field {
					case "source":
						bad.Source += ":foreign"
					case "mongo":
						bad.Mongo = !bad.Mongo
					case "SHA":
						bad.SHA = strings.Repeat("0", 64)
					case "TTL":
						bad.TTL++
					}
					reject(st, bad)
				}
				for _, name := range []string{"../outside", "pagebody-" + strings.Repeat("0", 64) + ".json"} {
					changed := meta
					changed.File = name
					p, value := rebind(changed)
					reject(p, value)
				}
				for _, tamper := range []string{"raw-sha", "mode", "symlink", "hardlink"} {
					store, state, page, value, l := progressMetadataFixture(t)
					ctx := newProgressPageContext(context.Background())
					if _, e := store.checkedRow(ctx, state, value, l); e != nil {
						t.Fatal(e)
					}
					path := filepath.Join(store.dir, page.File)
					switch tamper {
					case "raw-sha":
						b, e := os.ReadFile(path)
						if e != nil || os.WriteFile(path, append(b, ' '), 0600) != nil {
							t.Fatal("raw tamper fixture")
						}
					case "mode":
						if os.Chmod(path, 0644) != nil {
							t.Fatal("mode fixture")
						}
					case "symlink":
						if os.Rename(path, path+".owned") != nil || os.Symlink(path+".owned", path) != nil {
							t.Fatal("symlink fixture")
						}
					case "hardlink":
						if os.Link(path, path+".owned") != nil {
							t.Fatal("hardlink fixture")
						}
					}
					if _, e := store.checkedRow(ctx, state, value, l); e == nil {
						t.Fatal("same-call factoring or existing page memo bypassed current private bytes", tamper)
					}
					closeProgressPageContext(ctx)
				}
			}
		})
	}
}

func progressMetadataLegacyBinding(t *testing.T) {
	t.Helper()
	source, dir := pageFixture(t), chunkDir(t)
	firstPassOnly(t, source, dir)
	session, e := loadSession(dir)
	if e != nil {
		t.Fatal(e)
	}
	var cp chunkCheckpoint
	path := filepath.Join(dir, checkpointName(1))
	if loadPrivateJSON(path, 64<<10, &cp) != nil {
		t.Fatal("real legacy checkpoint missing")
	}
	raw, e := ReadPrivateDocument(filepath.Join(dir, cp.Page), MaxPrivateSnapshot)
	var page Page
	if e != nil || DecodeStrict(raw, &page) != nil {
		t.Fatal("real legacy page missing", e)
	}
	rec, e := progressIndexRow(page, 0)
	if e != nil {
		t.Fatal(e)
	}
	meta := progressPage{Number: 1, Pass: cp.Pass, File: cp.Page, SHA: cp.PageSHA, LegacyCheckpoint: cp.SHA}
	store := progressStore{dir}
	st := initialProgress(session)
	st.RawRoot, e = store.put(context.Background(), "", pageNumberKey(1), meta)
	if e != nil {
		t.Fatal(e)
	}
	st.RawCount = 1
	row := storedRow(rec, progressRef{Number: 1, Row: 0, MetaSHA: jsonSHA(meta)})
	if _, e := store.checkedRow(context.Background(), st, row, session.Limits); e != nil {
		t.Fatal("valid original legacy page was rejected", e)
	}
	cp.PageSHA = strings.Repeat("0", 64)
	cp.SHA = checkpointSHA(cp)
	body, e := json.Marshal(cp)
	if e != nil || os.WriteFile(path, append(body, '\n'), 0600) != nil {
		t.Fatal("owned legacy corruption fixture")
	}
	meta.LegacyCheckpoint = cp.SHA
	st.RawRoot, e = store.put(context.Background(), st.RawRoot, pageNumberKey(1), meta)
	if e != nil {
		t.Fatal(e)
	}
	row.Ref.MetaSHA = jsonSHA(meta)
	if _, e := store.checkedRow(context.Background(), st, row, session.Limits); e == nil {
		t.Fatal("legacy checkpoint/page SHA mismatch was accepted")
	}
}
