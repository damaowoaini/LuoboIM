package namespaceinventory

import (
	"context"
	"path/filepath"
)

// ImportSnapshot is private raw custody, never a drain, owner policy, read grant
// or Ready flag. It exposes the same complete two-pass journal without trusting
// operator-supplied summary JSON or copying/reclassifying all old history.
type ImportSnapshot struct {
	dir         string
	session     chunkSession
	head        progressHead
	state       progressState
	fingerprint string
}
type ImportSnapshotIdentity struct {
	Identity                                                       Identity `json:"identity"`
	SessionSHA256, CheckpointSHA256, RawRootSHA256, RowsRootSHA256 string
	RawPages, UniqueRows                                           int64
	Trust                                                          string
}
type ImportRawRow struct {
	source               string
	mongo                *MongoRow
	redis                *RedisRow
	objectSHA, recordSHA string
	position             uint64
}

func (r ImportRawRow) Source() string { return r.source }
func (r ImportRawRow) Reference() (objectSHA, recordSHA string, position uint64) {
	return r.objectSHA, r.recordSHA, r.position
}
func (r ImportRawRow) Mongo() (MongoRow, bool) {
	if r.mongo == nil {
		return MongoRow{}, false
	}
	v := *r.mongo
	v.Raw = append([]byte(nil), v.Raw...)
	return v, true
}
func (r ImportRawRow) Redis() (RedisRow, bool) {
	if r.redis == nil {
		return RedisRow{}, false
	}
	v := *r.redis
	v.Dump = append([]byte(nil), v.Dump...)
	return v, true
}
func (s *ImportSnapshot) Identity() ImportSnapshotIdentity {
	if s == nil {
		return ImportSnapshotIdentity{}
	}
	return ImportSnapshotIdentity{s.session.Identity, s.session.SHA, s.head.StateSHA, s.state.RawRoot, s.state.RowsRoot, s.state.RawCount, s.state.RowsCount, State}
}
func (s *ImportSnapshot) Fingerprint() string {
	if s == nil {
		return ""
	}
	return s.fingerprint
}
func OpenImportSnapshot(ctx context.Context, source PageSource, dir string, binding Binding) (out *ImportSnapshot, resultErr error) {
	if ctx == nil || source == nil || !binding.valid() {
		return nil, ErrInvalid
	}
	lock, e := lockJournal(dir)
	if e != nil {
		return nil, e
	}
	defer func() {
		if lock.Close() != nil {
			out = nil
			resultErr = ErrPrivate
		}
	}()
	session, e := loadSession(dir)
	if e != nil || session.Identity.Binding != binding || !session.Identity.TenantAttested {
		return nil, ErrIdentity
	}
	head, state, e := loadProgress(dir, session)
	if e != nil || state.Phase != "complete" {
		return nil, ErrChanged
	}
	current, e := source.Identity(ctx)
	if e != nil || current != session.Identity {
		return nil, ErrIdentity
	}
	identity := ImportSnapshotIdentity{session.Identity, session.SHA, head.StateSHA, state.RawRoot, state.RowsRoot, state.RawCount, state.RowsCount, State}
	return &ImportSnapshot{dir, session, head, state, jsonSHA(identity)}, nil
}
func (s *ImportSnapshot) check(ctx context.Context, source PageSource) error {
	if s == nil || ctx == nil || source == nil {
		return ErrInvalid
	}
	session, e := loadSession(s.dir)
	if e != nil || session.SHA != s.session.SHA || session.Identity != s.session.Identity {
		return ErrChanged
	}
	head, state, e := loadProgress(s.dir, session)
	if e != nil || head != s.head || state.Phase != "complete" || jsonSHA(state) != jsonSHA(s.state) {
		return ErrChanged
	}
	actual, e := source.Identity(ctx)
	if e != nil || actual != s.session.Identity {
		return ErrIdentity
	}
	return ctx.Err()
}

// ReadRows is bounded by maxRows (execution budget, not history/group size).
// Each returned value was resolved through the current RowsRoot AND RawRoot,
// its original private page path/SHA, and the original exact row classifier.
// The cursor is a hashed index key, never parsed as an account/conversation ID.
func (s *ImportSnapshot) ReadRows(ctx context.Context, source PageSource, after string, maxRows int) (out []ImportRawRow, next string, done bool, resultErr error) {
	if s == nil || maxRows < 1 || maxRows > 1024 || after != "" && !hexText(after, 64) {
		return nil, "", false, ErrInvalid
	}
	lock, e := lockJournal(s.dir)
	if e != nil {
		return nil, "", false, e
	}
	defer func() {
		if lock.Close() != nil {
			out = nil
			next = ""
			done = false
			resultErr = ErrPrivate
		}
	}()
	if e = s.check(ctx, source); e != nil {
		return nil, "", false, e
	}
	store := progressStore{s.dir}
	next = after
	for len(out) < maxRows {
		item, ok, e := store.next(ctx, s.state.RowsRoot, next)
		if e != nil {
			return nil, "", false, e
		}
		if !ok {
			done = true
			break
		}
		var row progressRow
		if DecodeStrict(item.Value, &row) != nil || digest([]byte(row.Source)) != item.Key {
			return nil, "", false, ErrInvalid
		}
		rec, e := store.checkedRow(ctx, s.state, row, s.session.Limits)
		if e != nil || rec.Source != row.Source || rec.ContentSHA != row.SHA {
			return nil, "", false, ErrChanged
		}
		var metadata progressPage
		ok, e = store.get(ctx, s.state.RawRoot, pageNumberKey(row.Ref.Number), &metadata)
		if e != nil || !ok || jsonSHA(metadata) != row.Ref.MetaSHA {
			return nil, "", false, ErrInvalid
		}
		page, e := store.page(ctx, s.state, metadata, s.session.Limits)
		if e != nil {
			return nil, "", false, e
		}
		raw := ImportRawRow{source: row.Source, objectSHA: metadata.SHA, recordSHA: row.SHA, position: uint64(row.Ref.Row)}
		if row.Mongo {
			if row.Ref.Row < 0 || row.Ref.Row >= len(page.Mongo) {
				return nil, "", false, ErrInvalid
			}
			value := page.Mongo[row.Ref.Row]
			value.Raw = append([]byte(nil), value.Raw...)
			raw.mongo = &value
		} else {
			position := row.Ref.Row - len(page.Mongo)
			if position < 0 || position >= len(page.Redis) {
				return nil, "", false, ErrInvalid
			}
			value := page.Redis[position]
			value.Dump = append([]byte(nil), value.Dump...)
			raw.redis = &value
		}
		// checkedRow already verifies the original content hash: Mongo raw BSON;
		// Redis original key/type/DUMP/head tuple (PTTL is intentionally excluded).
		out = append(out, raw)
		next = item.Key
	}
	if e = s.check(ctx, source); e != nil {
		return nil, "", false, e
	}
	return out, next, done, nil
}

// ReadReference refuses an arbitrary private path or JSON value. The exact
// immutable source index key and reference must resolve to the current roots.
func (s *ImportSnapshot) ReadReference(ctx context.Context, source PageSource, sourceKey, objectSHA, recordSHA string, position uint64) (ImportRawRow, error) {
	if s == nil || !text(sourceKey) || !hexText(objectSHA, 64) || !hexText(recordSHA, 64) {
		return ImportRawRow{}, ErrInvalid
	}
	lock, e := lockJournal(s.dir)
	if e != nil {
		return ImportRawRow{}, e
	}
	defer lock.Close()
	if e = s.check(ctx, source); e != nil {
		return ImportRawRow{}, e
	}
	store := progressStore{s.dir}
	var row progressRow
	ok, e := store.get(ctx, s.state.RowsRoot, digest([]byte(sourceKey)), &row)
	if e != nil || !ok || row.Source != sourceKey || row.SHA != recordSHA || uint64(row.Ref.Row) != position {
		return ImportRawRow{}, ErrInvalid
	}
	var meta progressPage
	ok, e = store.get(ctx, s.state.RawRoot, pageNumberKey(row.Ref.Number), &meta)
	if e != nil || !ok || meta.SHA != objectSHA || jsonSHA(meta) != row.Ref.MetaSHA {
		return ImportRawRow{}, ErrInvalid
	}
	if _, e = store.checkedRow(ctx, s.state, row, s.session.Limits); e != nil {
		return ImportRawRow{}, e
	}
	page, e := store.page(ctx, s.state, meta, s.session.Limits)
	if e != nil {
		return ImportRawRow{}, e
	}
	out := ImportRawRow{source: sourceKey, objectSHA: objectSHA, recordSHA: recordSHA, position: position}
	if row.Mongo {
		if row.Ref.Row < 0 || row.Ref.Row >= len(page.Mongo) {
			return out, ErrInvalid
		}
		value := page.Mongo[row.Ref.Row]
		value.Raw = append([]byte(nil), value.Raw...)
		out.mongo = &value
	} else {
		at := row.Ref.Row - len(page.Mongo)
		if at < 0 || at >= len(page.Redis) {
			return out, ErrInvalid
		}
		value := page.Redis[at]
		value.Dump = append([]byte(nil), value.Dump...)
		out.redis = &value
	}
	if e = s.check(ctx, source); e != nil {
		return ImportRawRow{}, e
	}
	return out, nil
}

// ImportSnapshotPath is private operator metadata, never copied to public raw.
func (s *ImportSnapshot) ImportSnapshotPath() string {
	if s == nil {
		return ""
	}
	return filepath.Clean(s.dir)
}
