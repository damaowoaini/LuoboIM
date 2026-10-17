package controller

import (
	"context"
	"errors"
	"math/big"
	"strings"

	"github.com/OpenIMSDK/tools/tx"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

// This interface avoids a controller -> receiver import cycle. The real
// namespace receiver supplies it; it is not a public request-supplied grant.
type DirectNamespaceHistoryBoundsReader interface {
	ManagedNamespaceHistory() bool
	ReadDirectNamespaceHistoryBounds(context.Context, string, string) (bool, int64, int64, int64, error)
}

func NewConversationDatabaseWithNamespaceHistory(model relation.ConversationModelInterface, cached cache.ConversationCache, transaction tx.CtxTx, history DirectNamespaceHistoryBoundsReader) (ConversationDatabase, error) {
	if model == nil || cached == nil || transaction == nil || history == nil || !history.ManagedNamespaceHistory() {
		return nil, errors.New("managed namespace conversation reader unavailable")
	}
	return &conversationDatabase{conversationDB: model, cache: cached, tx: transaction, history: history}, nil
}
func (c *conversationDatabase) projectNamespaceConversations(ctx context.Context, owner string, rows []*relation.ConversationModel) ([]*relation.ConversationModel, error) {
	out := make([]*relation.ConversationModel, 0, len(rows))
	seen := map[string]bool{}
	for _, row := range rows {
		if row == nil || row.OwnerUserID != owner || row.ConversationID == "" || row.MinSeq < 0 || row.MaxSeq < 0 || seen[row.ConversationID] {
			return nil, errors.New("managed namespace conversation identity differs")
		}
		seen[row.ConversationID] = true
		direct, lower, _, maximum, err := c.history.ReadDirectNamespaceHistoryBounds(ctx, row.ConversationID, owner)
		if err != nil {
			return nil, err
		}
		copied := *row
		if direct {
			if lower < 0 || maximum < 0 {
				return nil, errors.New("managed namespace conversation bounds invalid")
			}
			copied.MinSeq = max(copied.MinSeq, lower)
			copied.MaxSeq = maximum
		}
		out = append(out, &copied)
	}
	return out, nil
}

// Preserve the original Redis ID-hash wire contract while reading the actual
// source-projected model. A stale cached ID set cannot hide a new conversation.
func namespaceConversationIDsHash(ids []string) uint64 {
	ids = append([]string(nil), ids...)
	utils.Sort(ids, true)
	value, _ := big.NewInt(0).SetString(utils.Md5(strings.Join(ids, ";"))[0:8], 16)
	return value.Uint64()
}
