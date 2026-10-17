package messagedelivery

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// Only the private, real source-owned transition adapter implements this.
// Nil is the unchanged original first-epoch path. It grants no runtime Ready.
type namespaceEpochPublisher interface {
	prepareEpochPublish(context.Context, *namespaceimport.Permit, namespaceimport.VerifiedManifest) error
	publishEpochCutover(context.Context, *namespaceimport.Permit, namespaceimport.VerifiedManifest, NamespaceCutover) error
}

func (w *NamespaceImportWriter) installEpochPublisher(value namespaceEpochPublisher) error {
	if w == nil || value == nil {
		return ErrInvalid
	}
	w.epochPublisherMu.Lock()
	defer w.epochPublisherMu.Unlock()
	if w.epochPublisher != nil {
		return ErrConflict
	}
	w.epochPublisher = value
	return nil
}
func (w *NamespaceImportWriter) epochPublisherSnapshot() namespaceEpochPublisher {
	if w == nil {
		return nil
	}
	w.epochPublisherMu.Lock()
	defer w.epochPublisherMu.Unlock()
	return w.epochPublisher
}
