package namespaceinventory

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginalMongoCatalogRetainsCompleteSealedRawCustody(t *testing.T) {
	ctx := context.Background()
	t.Run("finite-original-catalog-does-not-recapture-history", func(t *testing.T) {
		w, source, _, _ := originalIndexFixture(t, true)
		calls := source.pageCalls
		rows, e := w.ReadOriginalMongoCatalog(ctx)
		if e != nil || len(rows) != len(collections) {
			t.Fatal("complete original catalog unavailable", e)
		}
		names := OriginalMongoCatalogCollections()
		for at, row := range rows {
			raw, ok := row.Mongo()
			if !ok || raw.Collection != "@catalog" || row.Source() != "mongo:@catalog:"+digest([]byte(names[at])) {
				t.Fatal("catalog source identity changed")
			}
			raw.Raw[0] ^= 0xff
		}
		again, e := w.ReadOriginalMongoCatalog(ctx)
		if e != nil || len(again) != len(rows) || source.pageCalls != calls {
			t.Fatal("catalog read recaptured or caller mutated original custody", e)
		}
	})
	t.Run("unsealed-catalog-is-not-runtime-identity", func(t *testing.T) {
		w, _, _, _ := originalIndexFixture(t, false)
		if _, e := w.ReadOriginalMongoCatalog(ctx); e == nil {
			t.Fatal("unsealed source became a target passport")
		}
	})
	t.Run("redirected-derived-catalog-entry-is-rejected", func(t *testing.T) {
		w, _, _, _ := originalIndexFixture(t, true)
		set := DerivedSetID("collection-rows", "@catalog")
		entries, _, _, _, e := w.ReadEntries(ctx, set, "", 1)
		if e != nil || len(entries) != 1 {
			t.Fatal("fixture catalog", e)
		}
		st, sum, e := w.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		entry := entries[0]
		if _, _, e = w.Commit(ctx, sum, st.Phase, st.Cursor, st.Subcursor, []DerivedMutation{{SetID: set, Key: entry.Key, ExpectedSHA256: entry.SHA256, Value: []byte(`{}`)}}); e != nil {
			t.Fatal(e)
		}
		if _, e = w.ReadOriginalMongoCatalog(ctx); e == nil {
			t.Fatal("derived catalog locator became raw authority")
		}
	})
	t.Run("original-body-tamper-remains-visible", func(t *testing.T) {
		w, _, snapshot, _ := originalIndexFixture(t, true)
		rows, e := w.ReadOriginalMongoCatalog(ctx)
		if e != nil {
			t.Fatal(e)
		}
		object, _, _ := rows[0].Reference()
		if e = os.WriteFile(filepath.Join(snapshot.ImportSnapshotPath(), "pagebody-"+object+".json"), []byte("{}\n"), 0600); e != nil {
			t.Fatal(e)
		}
		if _, e = w.ReadOriginalMongoCatalog(ctx); e == nil {
			t.Fatal("changed original catalog body accepted")
		}
	})
}
