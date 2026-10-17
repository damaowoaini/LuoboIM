package namespaceinventory

import (
	"bytes"
	"context"
	"encoding/json"
	"go.mongodb.org/mongo-driver/bson"
	"sort"
)

func OriginalMongoCatalogCollections() []string {
	out := append([]string(nil), collections...)
	sort.Strings(out)
	return out
}

// Exactly twelve real @catalog objects, re-resolved through the sealed source
// set and original RawRoot/RowsRoot. Derived lookup entries are locators only.
// Missing/duplicate/redirected entries cannot turn into a fresh live catalog.
func (w *ImportWorkspace) ReadOriginalMongoCatalog(ctx context.Context) ([]ImportRawRow, error) {
	if w == nil {
		return nil, ErrInvalid
	}
	seal, e := w.readOriginalIndexSeal(ctx)
	if e != nil {
		return nil, e
	}
	entries, _, done, total, e := w.ReadEntries(ctx, DerivedSetID("collection-rows", "@catalog"), "", len(collections)+1)
	if e != nil || !done || total != uint64(len(collections)) || len(entries) != len(collections) {
		return nil, ErrChanged
	}
	names := map[string]ImportRawRow{}
	for _, entry := range entries {
		var ref originalIndexReference
		if DecodeStrict(entry.Value, &ref) != nil {
			return nil, ErrChanged
		}
		source, found, e := w.ReadEntry(ctx, DerivedSetID("source-rows", w.snapshot.Fingerprint()), digest([]byte(ref.Kind)))
		if e != nil || !found || entry.Key != source.Key || !bytes.Equal(source.Value, entry.Value) {
			return nil, ErrChanged
		}
		row, e := w.snapshot.ReadReference(ctx, w.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position)
		if e != nil {
			return nil, e
		}
		raw, ok := row.Mongo()
		if !ok || raw.Collection != "@catalog" || !uniqueBSON(bson.Raw(raw.Raw), 0) {
			return nil, ErrChanged
		}
		name, ok := bson.Raw(raw.Raw).Lookup("name").StringValueOK()
		if !ok || !originalCollectionAllowed(name) || raw.KeySHA != digest([]byte(name)) {
			return nil, ErrChanged
		}
		if _, exists := names[name]; exists {
			return nil, ErrChanged
		}
		names[name] = row
	}
	current, e := w.readOriginalIndexSeal(ctx)
	a, _ := json.Marshal(seal)
	b, _ := json.Marshal(current)
	if e != nil || !bytes.Equal(a, b) {
		return nil, ErrChanged
	}
	var out []ImportRawRow
	for _, name := range OriginalMongoCatalogCollections() {
		row, ok := names[name]
		if !ok {
			return nil, ErrChanged
		}
		out = append(out, row)
	}
	return out, nil
}
