package controller

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/tools/tx"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

type namespaceConversationModelFixture struct {
	relation.ConversationModelInterface
	row   *relation.ConversationModel
	reads int
}

func (f *namespaceConversationModelFixture) Find(context.Context, string, []string) ([]*relation.ConversationModel, error) {
	f.reads++
	return []*relation.ConversationModel{f.row}, nil
}
func (f *namespaceConversationModelFixture) Take(context.Context, string, string) (*relation.ConversationModel, error) {
	f.reads++
	return f.row, nil
}
func (f *namespaceConversationModelFixture) FindUserIDAllConversations(context.Context, string) ([]*relation.ConversationModel, error) {
	f.reads++
	return []*relation.ConversationModel{f.row}, nil
}
func (f *namespaceConversationModelFixture) FindUserIDAllConversationID(context.Context, string) ([]string, error) {
	f.reads++
	return []string{f.row.ConversationID}, nil
}

type namespaceConversationCacheFixture struct{ cache.ConversationCache }
type namespaceConversationTXFixture struct{ tx.CtxTx }
type namespaceConversationBoundsFixture struct{ err error }

func (f *namespaceConversationBoundsFixture) ManagedNamespaceHistory() bool { return true }
func (f *namespaceConversationBoundsFixture) ReadDirectNamespaceHistoryBounds(context.Context, string, string) (bool, int64, int64, int64, error) {
	return true, 12, 14, 0, f.err
}
func TestManagedConversationSourceProjectionBypassesStaleCacheAndPreservesModel(t *testing.T) {
	ctx := context.Background()
	original := &relation.ConversationModel{OwnerUserID: "owner", ConversationID: "si_original_pair", UserID: "peer", MinSeq: 2, MaxSeq: 5}
	model := &namespaceConversationModelFixture{row: original}
	db, e := NewConversationDatabaseWithNamespaceHistory(model, &namespaceConversationCacheFixture{}, &namespaceConversationTXFixture{}, &namespaceConversationBoundsFixture{})
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"one", "selected", "all", "ids", "id-hash"} {
		t.Run(name, func(t *testing.T) {
			before := model.reads
			switch name {
			case "one":
				row, e := db.(*conversationDatabase).GetConversation(ctx, "owner", original.ConversationID)
				if e != nil || row.MinSeq != 12 || row.MaxSeq != 0 {
					t.Fatal("managed actual source projection", e)
				}
			case "selected":
				rows, e := db.FindConversations(ctx, "owner", []string{original.ConversationID})
				if e != nil || len(rows) != 1 || rows[0].MinSeq != 12 || rows[0].MaxSeq != 0 {
					t.Fatal("managed selected projection", e)
				}
			case "all":
				rows, e := db.GetUserAllConversation(ctx, "owner")
				if e != nil || len(rows) != 1 || rows[0].MinSeq != 12 || rows[0].MaxSeq != 0 {
					t.Fatal("managed all projection", e)
				}
			case "ids":
				ids, e := db.GetConversationIDs(ctx, "owner")
				if e != nil || len(ids) != 1 || ids[0] != original.ConversationID {
					t.Fatal("stale cached ID set", e)
				}
			case "id-hash":
				hash, e := db.GetUserConversationIDsHash(ctx, "owner")
				if e != nil || hash != namespaceConversationIDsHash([]string{original.ConversationID}) {
					t.Fatal("ID hash contract changed", e)
				}
			}
			if model.reads != before+1 || original.MinSeq != 2 || original.MaxSeq != 5 {
				t.Fatal("cache fallback or mutation of shared source row")
			}
		})
	}
}
func TestManagedConversationUnknownProofNeverFallsBackToLegacyCache(t *testing.T) {
	errProof := errors.New("fixture proof unavailable")
	model := &namespaceConversationModelFixture{row: &relation.ConversationModel{OwnerUserID: "owner", ConversationID: "si_pair"}}
	db, e := NewConversationDatabaseWithNamespaceHistory(model, &namespaceConversationCacheFixture{}, &namespaceConversationTXFixture{}, &namespaceConversationBoundsFixture{err: errProof})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.FindConversations(context.Background(), "owner", []string{"si_pair"}); !errors.Is(e, errProof) {
		t.Fatal("unknown proof silently fell back", e)
	}
	if _, e = db.FindConversations(context.Background(), "foreign", []string{"si_pair"}); e == nil {
		t.Fatal("foreign source owner accepted")
	}
	if _, e = NewConversationDatabaseWithNamespaceHistory(model, &namespaceConversationCacheFixture{}, &namespaceConversationTXFixture{}, nil); e == nil {
		t.Fatal("missing managed binding selected legacy")
	}
}
