package namespaceinventory

import "context"

// Validate the COMPLETE finite index seal, including absence lookups. Reading
// only the present evidence would not detect a changed floor-absence index.
// This checks fixed set roots/counts, not their entries or a history prefix.
// Every public workspace operation retains the original private/live checks.
func (w *ImportWorkspace) OriginalHistoryIndexBinding(ctx context.Context) (string, error) {
	seal, e := w.readOriginalIndexSeal(ctx)
	if e != nil {
		return "", e
	}
	for _, row := range seal.Collections {
		root, count, e := w.DerivedSetIdentity(ctx, DerivedSetID("mongo-objects", row.Collection))
		if e != nil {
			return "", e
		}
		if root != row.Root || count != row.Count {
			return "", ErrChanged
		}
	}
	for _, row := range []struct {
		Kind, Root string
		Count      uint64
	}{{"original-history-sequences", seal.SequenceRoot, seal.SequenceCount}, {"original-history-documents", seal.MessageRoot, seal.MessageCount}} {
		root, count, e := w.DerivedSetIdentity(ctx, DerivedSetID(row.Kind, w.snapshot.Fingerprint()))
		if e != nil {
			return "", e
		}
		if root != row.Root || count != row.Count {
			return "", ErrChanged
		}
	}
	if e = w.check(ctx); e != nil {
		return "", e
	}
	return jsonSHA(seal), nil
}
