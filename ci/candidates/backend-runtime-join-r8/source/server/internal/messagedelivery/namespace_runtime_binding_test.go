//go:build linux

package messagedelivery

import (
	"context"
	"errors"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/mongo"
)

func TestNamespaceRuntimeSameInstanceBindingCannotBorrowMarkerOrPreparedCatalog(t *testing.T) {
	h, _ := readinessSelectionFixture(t)
	db := new(mongo.Database)
	gate := &ownedcontrol.NamespaceRuntimeStartupGate{}
	b := &NamespaceRuntimeBinding{gate: gate, db: db, writer: &NamespaceImportWriter{header: h}, stores: map[*Store]namespaceRuntimeBindingEntry{}, names: map[string]*Store{}, components: map[string]namespaceRuntimeComponent{}}
	s := &Store{db: db, originalDatabase: db, epoch: h.Epoch, activationSHA: h.ActivationSHA256}
	t.Run("same-instance-still-needs-known-admission", func(t *testing.T) {
		if err := b.BindOriginalStore(s, "original"); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(b.validateNamespaceRuntimeStore(context.Background(), s), ErrNamespaceWritersUnbound) {
			t.Fatal("prepared Store became a writer without actual publication")
		}
	})
	t.Run("copied-store-or-foreign-DAO-cannot-borrow-binding", func(t *testing.T) {
		copy := *s
		if b.validateNamespaceRuntimeStore(context.Background(), &copy) == nil {
			t.Fatal("copy borrowed original instance")
		}
		foreign := &Store{db: new(mongo.Database), epoch: h.Epoch, activationSHA: h.ActivationSHA256}
		if b.BindOriginalStore(foreign, "foreign") == nil {
			t.Fatal("another DAO borrowed namespace capability")
		}
	})
	t.Run("sealed-source-cannot-renew-components", func(t *testing.T) {
		b.sealed = true
		extra := &Store{db: db, originalDatabase: db, epoch: h.Epoch, activationSHA: h.ActivationSHA256}
		if b.BindOriginalStore(extra, "late") == nil {
			t.Fatal("late constructor renewed a sealed graph")
		}
	})
	t.Run("missing-original-context-never-falls-back-in-owned-required", func(t *testing.T) {
		t.Setenv(HistoryWindowModeEnv, "required")
		t.Setenv("LUOBO_MANAGED_OWNER_ANNOUNCEMENT_PATH", "synthetic-private-owner")
		if store, err := NewRuntimeMembershipStore(context.Background(), nil, h.Epoch, h.ActivationSHA256, "missing"); err == nil || store != nil {
			t.Fatal("raw constructor substituted for private original context")
		}
	})
}
