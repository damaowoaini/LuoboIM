//go:build darwin

package messagedelivery

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"go.mongodb.org/mongo-driver/mongo"
)

// The real controlled supervisor/graph is Linux-specific. Ordinary unowned
// and legacy Darwin behavior remains the original path; no private required
// runtime capability is manufactured from a schema marker or release JSON.
type NamespaceRuntimeBinding struct{}

func NewNamespaceRuntimeBindingForOwnedContext(ctx context.Context, db *mongo.Database) (*NamespaceRuntimeBinding, error) {
	if ownedcontrol.NamespaceRuntimeStartupFromContext(ctx) != nil || namespaceRuntimeBindingRequired() {
		return nil, ErrUnprepared
	}
	return nil, nil
}
func (*NamespaceRuntimeBinding) BindOriginalReader(*HistoryWindowReader, string) error {
	return ErrUnprepared
}
func (*NamespaceRuntimeBinding) BindOriginalStore(*Store, string) error                 { return ErrUnprepared }
func (*NamespaceRuntimeBinding) BindRuntimeContext(ctx context.Context) context.Context { return ctx }
func (*NamespaceRuntimeBinding) CheckPreparedCatalog(context.Context) error             { return ErrUnprepared }
func (*NamespaceRuntimeBinding) MatchesNamespaceRuntimeGate(*ownedcontrol.NamespaceRuntimeStartupGate) bool {
	return false
}
func (*NamespaceRuntimeBinding) SealNamespaceBindings(context.Context, []string, ownedmongo.Snapshot) (ownedcontrol.NamespaceRuntimeBindings, error) {
	return ownedcontrol.NamespaceRuntimeBindings{}, ErrUnprepared
}
func (*NamespaceRuntimeBinding) ValidateRuntimeStores(context.Context) error { return ErrUnprepared }

func BindPreparedNamespaceRuntime(ctx context.Context, store *Store, name, kind string, owner any, inspect func(context.Context) error) error {
	if namespaceRuntimeBindingRequired() {
		return ErrUnprepared
	}
	return nil
}
func NewRuntimeMembershipStore(ctx context.Context, db *mongo.Database, epoch, activation, label string) (*Store, error) {
	if namespaceRuntimeBindingRequired() {
		return nil, ErrUnprepared
	}
	return NewMembershipBoundStore(db, epoch, activation)
}
