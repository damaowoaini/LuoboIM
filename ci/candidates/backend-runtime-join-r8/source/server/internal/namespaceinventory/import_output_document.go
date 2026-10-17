package namespaceinventory

import (
	"context"
	"encoding/json"
)

// Output documents are private, immutable, content-addressed and fsynced before
// a derived index/cursor references them. Interrupted writes can only replay
// the same body. They never replace raw custody or create a trust capability.
func (w *ImportWorkspace) StoreOutputDocument(ctx context.Context, body json.RawMessage, maximum int) (sum string, resultErr error) {
	if maximum < 1024 || maximum > 8<<20 || len(body) == 0 || len(body) > maximum || !json.Valid(body) {
		return "", ErrLimit
	}
	if e := w.check(ctx); e != nil {
		return "", e
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return "", e
	}
	defer func() {
		if lock.Close() != nil {
			sum = ""
			resultErr = ErrPrivate
		}
	}()
	sum, e = (progressStore{w.dir}).save("import-output", body)
	if e != nil {
		return "", e
	}
	if e = w.check(ctx); e != nil {
		return "", e
	}
	return sum, nil
}
func (w *ImportWorkspace) ReadOutputDocument(ctx context.Context, sum string, maximum int) (body json.RawMessage, resultErr error) {
	if !hexText(sum, 64) || maximum < 1024 || maximum > 8<<20 {
		return nil, ErrInvalid
	}
	if e := w.check(ctx); e != nil {
		return nil, e
	}
	lock, e := lockJournal(w.dir)
	if e != nil {
		return nil, e
	}
	defer func() {
		if lock.Close() != nil {
			body = nil
			resultErr = ErrPrivate
		}
	}()
	value, e := ReadPrivateDocument((progressStore{w.dir}).objectPath("import-output", sum), maximum+1)
	if e != nil || len(value) > maximum+1 || digest(value) != sum || !json.Valid(value) {
		return nil, ErrChanged
	}
	if e = w.check(ctx); e != nil {
		return nil, e
	}
	return append(json.RawMessage(nil), value...), nil
}
