//go:build darwin

package messagedelivery

import "context"

// Owned epoch migration uses the real Linux supervisor. The ordinary Darwin
// inspector retains its original path; it does not fabricate an archive grant.
func (w *NamespaceImportWriter) readPublishedNamespaceCutover(ctx context.Context, expected string) (NamespaceCutover, string, error) {
	return w.store.namespaceCutover(ctx)
}
