package namespaceinventory

import "context"

// This is a parse optimization, never a custody or authorization cache. Each
// caller has already checked the current RawRoot, metadata and private path;
// decodeProgressPage also hashes the freshly reread bytes even on a memo hit.
// No parsed page survives ResumeResumableCapture's return. A fresh context is
// installed even if the caller passes a context from an earlier invocation.
type progressPageMemoKey struct {
	SHA    string
	Limits Limits
}
type progressPageMemoEntry struct {
	Page  Page
	Bytes int
}
type progressPageMemo struct {
	Entries map[progressPageMemoKey]progressPageMemoEntry
	Order   []progressPageMemoKey
	Bytes   int
	Closed  bool
}
type progressPageMemoContextKey struct{}

// Both limits are independent of history size. Bytes bounds retained encoded
// page bytes; decoded pages also obey the existing <=4096 rows/page and cursor
// bounds. This is not an exact Go heap/RSS limit. Oversized pages remain valid
// and follow the original uncached strict decoder, rather than losing rows.
const progressPageMemoEntries = 8
const progressPageMemoBytes = 2 << 20

func newProgressPageContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, progressPageMemoContextKey{}, &progressPageMemo{Entries: map[progressPageMemoKey]progressPageMemoEntry{}})
}

func closeProgressPageContext(ctx context.Context) {
	if memo, ok := ctx.Value(progressPageMemoContextKey{}).(*progressPageMemo); ok {
		memo.Entries, memo.Order, memo.Bytes, memo.Closed = nil, nil, 0, true
	}
}

func decodeProgressPage(ctx context.Context, sha string, raw []byte, limits Limits) (Page, error) {
	if digest(raw) != sha {
		return Page{}, ErrInvalid
	}
	memo, _ := ctx.Value(progressPageMemoContextKey{}).(*progressPageMemo)
	if memo != nil && memo.Closed {
		memo = nil
	}
	key := progressPageMemoKey{SHA: sha, Limits: limits}
	if memo != nil {
		if entry, ok := memo.Entries[key]; ok {
			return copyProgressPage(entry.Page), nil
		}
	}
	var page Page
	if DecodeStrict(raw, &page) != nil || validatePage(page, limits) != nil {
		return Page{}, ErrInvalid
	}
	if memo != nil && len(raw) <= progressPageMemoBytes {
		for len(memo.Order) >= progressPageMemoEntries || memo.Bytes > progressPageMemoBytes-len(raw) {
			old := memo.Order[0]
			memo.Bytes -= memo.Entries[old].Bytes
			delete(memo.Entries, old)
			memo.Order = memo.Order[1:]
		}
		memo.Entries[key] = progressPageMemoEntry{Page: copyProgressPage(page), Bytes: len(raw)}
		memo.Order = append(memo.Order, key)
		memo.Bytes += len(raw)
	}
	return page, nil
}

// The selected row goes through the exact existing classifier, including
// malformed/erased BSON and Redis reason handling. Other rows were page-bound
// validated but need not all be classified again for this one work quantum.
func progressIndexRow(page Page, row int) (indexRecord, error) {
	if row < 0 || row >= len(page.Mongo)+len(page.Redis) {
		return indexRecord{}, ErrInvalid
	}
	if row < len(page.Mongo) {
		return indexRows(Page{Mongo: page.Mongo[row : row+1]})[0], nil
	}
	row -= len(page.Mongo)
	return indexRows(Page{Redis: page.Redis[row : row+1]})[0], nil
}

func copyProgressBytes(v []byte) []byte {
	if v == nil {
		return nil
	}
	out := make([]byte, len(v))
	copy(out, v)
	return out
}
func copyProgressCursor(v PageCursor) PageCursor {
	v.AfterID = copyProgressBytes(v.AfterID)
	if v.Pending != nil {
		out := make([]string, len(v.Pending))
		copy(out, v.Pending)
		v.Pending = out
	}
	return v
}
func copyProgressPage(v Page) Page {
	v.Before = copyProgressCursor(v.Before)
	v.After = copyProgressCursor(v.After)
	if v.Mongo != nil {
		out := make([]MongoRow, len(v.Mongo))
		copy(out, v.Mongo)
		for n := range out {
			out[n].Raw = copyProgressBytes(out[n].Raw)
		}
		v.Mongo = out
	}
	if v.Redis != nil {
		out := make([]RedisRow, len(v.Redis))
		copy(out, v.Redis)
		for n := range out {
			out[n].Dump = copyProgressBytes(out[n].Dump)
		}
		v.Redis = out
	}
	return v
}
