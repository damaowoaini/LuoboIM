package namespaceinventory

import (
	"context"
	"encoding/json"

	"go.mongodb.org/mongo-driver/bson"
)

type originalIndexReference struct {
	ObjectSHA256 string `json:"object_sha256"`
	RecordSHA256 string `json:"record_sha256"`
	Kind         string `json:"kind"`
	Position     uint64 `json:"position"`
}
type originalCollectionIndex struct {
	Collection string `json:"collection"`
	Root       string `json:"root"`
	Count      uint64 `json:"count"`
}
type originalIndexSeal struct {
	Version        string                    `json:"version"`
	SnapshotSHA256 string                    `json:"snapshot_sha256"`
	RawRootSHA256  string                    `json:"raw_root_sha256"`
	RowsRootSHA256 string                    `json:"rows_root_sha256"`
	SourceRowsRoot string                    `json:"source_rows_root"`
	UniqueRows     uint64                    `json:"unique_rows"`
	Collections    []originalCollectionIndex `json:"collections"`
	SequenceRoot   string                    `json:"sequence_root,omitempty"`
	SequenceCount  uint64                    `json:"sequence_count,omitempty"`
	MessageRoot    string                    `json:"message_root,omitempty"`
	MessageCount   uint64                    `json:"message_count,omitempty"`
}

func originalIndexSealKey(snapshot string) string {
	return jsonSHA([]string{"original-history-index-seal-v1", snapshot})
}
func originalIndexMutation(set, key string, v any) (DerivedMutation, error) {
	b, e := json.Marshal(v)
	if e != nil || len(b) > 64<<10 {
		return DerivedMutation{}, ErrLimit
	}
	return DerivedMutation{SetID: set, Key: key, Value: b}, nil
}
func originalCollectionAllowed(name string) bool {
	for _, actual := range collections {
		if name == actual {
			return true
		}
	}
	return false
}

// OriginalRawIndexChanges re-resolves the actual row through the original
// RowsRoot/RawRoot, then returns at most four journal mutations. The caller
// commits these atomically with that raw row's completed fact cursor. String
// _id objects get exact lookup entries; nonstring _id remains raw evidence and
// is never guessed/stringified into an original receipt identity.
func (w *ImportWorkspace) OriginalRawIndexChanges(ctx context.Context, row ImportRawRow) ([]DerivedMutation, error) {
	st, _, e := w.Checkpoint(ctx)
	if e != nil || st.Phase != "raw" {
		return nil, ErrChanged
	}
	object, record, position := row.Reference()
	actual, e := w.snapshot.ReadReference(ctx, w.live, row.Source(), object, record, position)
	if e != nil {
		return nil, e
	}
	ref := originalIndexReference{object, record, actual.Source(), position}
	out := []DerivedMutation{}
	add := func(kind, scope, key string) error {
		v, e := originalIndexMutation(DerivedSetID(kind, scope), key, ref)
		if e != nil {
			return e
		}
		out = append(out, v)
		return nil
	}
	if e = add("source-rows", w.snapshot.Fingerprint(), digest([]byte(actual.Source()))); e != nil {
		return nil, e
	}
	if mongo, ok := actual.Mongo(); ok {
		if e = add("collection-rows", mongo.Collection, digest([]byte(actual.Source()))); e != nil {
			return nil, e
		}
		raw := bson.Raw(mongo.Raw)
		if mongo.Collection == "message_delivery_effects_v1" {
			cid, cok := raw.Lookup("conversation_id").StringValueOK()
			seq, sok := raw.Lookup("seq").Int64OK()
			persist, pok := raw.Lookup("persist").BooleanOK()
			epoch, eok := raw.Lookup("epoch").StringValueOK()
			branch, bok := raw.Lookup("branch_id").StringValueOK()
			_, iok := raw.Lookup("source_id").StringValueOK()
			id, idok := raw.Lookup("_id").StringValueOK()
			if cok && sok && pok && persist && seq > 0 && eok && bok && iok && idok && id == jsonSHA([]string{epoch, "branch", branch}) {
				if e = add("original-history-sequences", w.snapshot.Fingerprint(), jsonSHA([]any{cid, seq})); e != nil {
					return nil, e
				}
			}
		}
		if mongo.Collection == "msg" {
			if id, ok := raw.Lookup("doc_id").StringValueOK(); ok && id != "" {
				if e = add("original-history-documents", w.snapshot.Fingerprint(), digest([]byte(id))); e != nil {
					return nil, e
				}
			}
		}
		if id, ok := bson.Raw(mongo.Raw).Lookup("_id").StringValueOK(); ok {
			if !originalCollectionAllowed(mongo.Collection) {
				return nil, ErrInvalid
			}
			if e = add("mongo-objects", mongo.Collection, digest([]byte(id))); e != nil {
				return nil, e
			}
			if mongo.Collection == "message_delivery_sequences_v1" {
				if e = add("sequence-objects", w.snapshot.Fingerprint(), digest([]byte(id))); e != nil {
					return nil, e
				}
			}
		}
	}
	return out, nil
}

// CompleteOriginalRawIndex is the real raw->reduce transition. It cannot seal
// until the original cursor is exhausted and every original source row has an
// indexed entry. It pins all finite collection lookup roots, so a later derived
// write cannot turn a previously present original object into sealed absence.
func (w *ImportWorkspace) CompleteOriginalRawIndex(ctx context.Context, expected string) (DerivedCheckpoint, error) {
	st, sum, e := w.Checkpoint(ctx)
	if e != nil || sum != expected || st.Phase != "raw" || st.Subcursor != "" {
		return st, ErrChanged
	}
	rows, _, done, e := w.snapshot.ReadRows(ctx, w.live, st.Cursor, 1)
	if e != nil || len(rows) != 0 || !done {
		return st, ErrChanged
	}
	info := w.snapshot.Identity()
	if info.UniqueRows < 0 {
		return st, ErrChanged
	}
	root, count, e := w.DerivedSetIdentity(ctx, DerivedSetID("source-rows", w.snapshot.Fingerprint()))
	if e != nil || count != uint64(info.UniqueRows) {
		return st, ErrChanged
	}
	seal := originalIndexSeal{Version: "original-history-index-seal-v1", SnapshotSHA256: w.snapshot.Fingerprint(), RawRootSHA256: info.RawRootSHA256, RowsRootSHA256: info.RowsRootSHA256, SourceRowsRoot: root, UniqueRows: count}
	for _, collection := range collections {
		root, count, e := w.DerivedSetIdentity(ctx, DerivedSetID("mongo-objects", collection))
		if e != nil {
			return st, e
		}
		seal.Collections = append(seal.Collections, originalCollectionIndex{collection, root, count})
	}
	seal.SequenceRoot, seal.SequenceCount, e = w.DerivedSetIdentity(ctx, DerivedSetID("original-history-sequences", w.snapshot.Fingerprint()))
	if e != nil {
		return st, e
	}
	seal.MessageRoot, seal.MessageCount, e = w.DerivedSetIdentity(ctx, DerivedSetID("original-history-documents", w.snapshot.Fingerprint()))
	if e != nil {
		return st, e
	}

	change, e := originalIndexMutation(DerivedSetID("original-index-seals", w.snapshot.Fingerprint()), originalIndexSealKey(w.snapshot.Fingerprint()), seal)
	if e != nil {
		return st, e
	}
	next, _, e := w.Commit(ctx, expected, "reduce", "", "", []DerivedMutation{change})
	return next, e
}
func (w *ImportWorkspace) readOriginalIndexSeal(ctx context.Context) (originalIndexSeal, error) {
	st, _, e := w.Checkpoint(ctx)
	if e != nil || st.Phase == "raw" {
		return originalIndexSeal{}, ErrChanged
	}
	entry, found, e := w.ReadEntry(ctx, DerivedSetID("original-index-seals", w.snapshot.Fingerprint()), originalIndexSealKey(w.snapshot.Fingerprint()))
	var seal originalIndexSeal
	if e != nil || !found || DecodeStrict(entry.Value, &seal) != nil {
		return seal, ErrChanged
	}
	info := w.snapshot.Identity()
	if info.UniqueRows < 0 || seal.Version != "original-history-index-seal-v1" || seal.SnapshotSHA256 != w.snapshot.Fingerprint() || seal.RawRootSHA256 != info.RawRootSHA256 || seal.RowsRootSHA256 != info.RowsRootSHA256 || seal.UniqueRows != uint64(info.UniqueRows) || len(seal.Collections) != len(collections) {
		return seal, ErrChanged
	}
	root, count, e := w.DerivedSetIdentity(ctx, DerivedSetID("source-rows", w.snapshot.Fingerprint()))
	if e != nil || root != seal.SourceRowsRoot || count != seal.UniqueRows {
		return seal, ErrChanged
	}
	for n, row := range seal.Collections {
		if row.Collection != collections[n] {
			return seal, ErrChanged
		}
	}
	return seal, nil
}

// ReadOriginalMongoRecord returns found=false only under the completed source
// index seal. It validates the current lookup root AND each found raw page and
// exact _id on every invocation. It does not scan/requery Mongo or decode/cache
// a complete history/group, and caller mutation cannot alter private custody.
func (w *ImportWorkspace) ReadOriginalMongoRecord(ctx context.Context, collection, id string) (ImportRawRow, bool, error) {
	if w == nil || !originalCollectionAllowed(collection) || id == "" {
		return ImportRawRow{}, false, ErrInvalid
	}
	seal, e := w.readOriginalIndexSeal(ctx)
	if e != nil {
		return ImportRawRow{}, false, e
	}
	var expected originalCollectionIndex
	for _, row := range seal.Collections {
		if row.Collection == collection {
			expected = row
			break
		}
	}
	root, count, e := w.DerivedSetIdentity(ctx, DerivedSetID("mongo-objects", collection))
	if e != nil || expected.Collection != collection || root != expected.Root || count != expected.Count {
		return ImportRawRow{}, false, ErrChanged
	}
	entry, found, e := w.ReadEntry(ctx, DerivedSetID("mongo-objects", collection), digest([]byte(id)))
	if e != nil || !found {
		return ImportRawRow{}, found, e
	}
	var ref originalIndexReference
	if DecodeStrict(entry.Value, &ref) != nil {
		return ImportRawRow{}, false, ErrChanged
	}
	row, e := w.snapshot.ReadReference(ctx, w.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position)
	if e != nil {
		return ImportRawRow{}, false, e
	}
	raw, ok := row.Mongo()
	if !ok || raw.Collection != collection {
		return ImportRawRow{}, false, ErrChanged
	}
	actual, ok := bson.Raw(raw.Raw).Lookup("_id").StringValueOK()
	if !ok || actual != id {
		return ImportRawRow{}, false, ErrChanged
	}
	return row, true, nil
}

// CheckOriginalHistoryIndex validates only sealed raw-index readiness. It is
// not a history grant and exposes no caller-selected absence/floor value.
func (w *ImportWorkspace) CheckOriginalHistoryIndex(ctx context.Context) error {
	_, e := w.readOriginalIndexSeal(ctx)
	return e
}

// A locator proves which original record to re-read, never ownership. Its
// index root is sealed with the complete original RawRoot/RowsRoot. Duplicate
// CID/seq or doc_id cannot overwrite an immutable journal entry.
func (w *ImportWorkspace) readOriginalScopedRecord(ctx context.Context, kind, key, collection string) (ImportRawRow, bool, error) {
	seal, e := w.readOriginalIndexSeal(ctx)
	if e != nil {
		return ImportRawRow{}, false, e
	}
	expected, count := seal.SequenceRoot, seal.SequenceCount
	if kind == "original-history-documents" {
		expected, count = seal.MessageRoot, seal.MessageCount
	}
	if len(expected) != 64 {
		return ImportRawRow{}, false, ErrChanged
	}
	root, n, e := w.DerivedSetIdentity(ctx, DerivedSetID(kind, w.snapshot.Fingerprint()))
	if e != nil || root != expected || n != count {
		return ImportRawRow{}, false, ErrChanged
	}
	entry, found, e := w.ReadEntry(ctx, DerivedSetID(kind, w.snapshot.Fingerprint()), key)
	if e != nil || !found {
		return ImportRawRow{}, found, e
	}
	var ref originalIndexReference
	if DecodeStrict(entry.Value, &ref) != nil {
		return ImportRawRow{}, false, ErrChanged
	}
	row, e := w.snapshot.ReadReference(ctx, w.live, ref.Kind, ref.ObjectSHA256, ref.RecordSHA256, ref.Position)
	if e != nil {
		return ImportRawRow{}, false, e
	}
	raw, ok := row.Mongo()
	if !ok || raw.Collection != collection {
		return ImportRawRow{}, false, ErrChanged
	}
	return row, true, nil
}
func (w *ImportWorkspace) ReadOriginalHistorySourceAtSequence(ctx context.Context, cid string, seq int64) (ImportRawRow, bool, error) {
	if w == nil || cid == "" || seq < 1 {
		return ImportRawRow{}, false, ErrInvalid
	}
	row, found, e := w.readOriginalScopedRecord(ctx, "original-history-sequences", jsonSHA([]any{cid, seq}), "message_delivery_effects_v1")
	if e != nil || !found {
		return row, found, e
	}
	original, _ := row.Mongo()
	raw := bson.Raw(original.Raw)
	actual, ok := raw.Lookup("conversation_id").StringValueOK()
	number, nok := raw.Lookup("seq").Int64OK()
	if !ok || actual != cid || !nok || number != seq {
		return ImportRawRow{}, false, ErrChanged
	}
	return row, true, nil
}
func (w *ImportWorkspace) ReadOriginalHistoryMessageDocument(ctx context.Context, id string) (ImportRawRow, bool, error) {
	if w == nil || id == "" {
		return ImportRawRow{}, false, ErrInvalid
	}
	row, found, e := w.readOriginalScopedRecord(ctx, "original-history-documents", digest([]byte(id)), "msg")
	if e != nil || !found {
		return row, found, e
	}
	original, _ := row.Mongo()
	actual, ok := bson.Raw(original.Raw).Lookup("doc_id").StringValueOK()
	if !ok || actual != id {
		return ImportRawRow{}, false, ErrChanged
	}
	return row, true, nil
}
