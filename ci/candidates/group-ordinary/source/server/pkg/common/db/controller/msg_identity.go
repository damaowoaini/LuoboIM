package controller

import (
	"context"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

type MessageIdentityReader interface {
	GetMsgByClientMsgIDs(context.Context, string, string, []string) (map[string]*sdkws.MsgData, error)
}

// Like the historical sequence reader, this factory never starts MQ producers
// or performs a lazy quote rewrite while checking a retention source.
func NewReadOnlyMessageIdentityReader(rdb redis.UniversalClient, database *mongo.Database, readers ...GroupHistoryWindowReader) MessageIdentityReader {
	return &commonMsgDatabase{groupHistory: selectGroupHistoryReader(readers), cache: cache.NewMsgCacheModel(rdb), msgDocDatabase: unrelation.NewMsgMongoDriver(database), readOnly: true}
}

func (db *commonMsgDatabase) GetMsgByClientMsgIDs(ctx context.Context, userID, conversationID string, clientIDs []string) (map[string]*sdkws.MsgData, error) {
	if len(clientIDs) < 1 || len(clientIDs) > 100 {
		return nil, errs.ErrArgs.Wrap("message identity batch must contain 1..100 entries")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	locations, err := db.cache.GetClientMessageSeqs(ctx, conversationID, clientIDs)
	if err != nil {
		return nil, err
	}
	missing := make([]string, 0, len(clientIDs))
	for _, id := range clientIDs {
		if _, ok := locations[id]; !ok {
			missing = append(missing, id)
		}
	}
	if len(missing) > 0 {
		persisted, err := db.msgDocDatabase.FindSeqsByClientMsgIDs(ctx, conversationID, missing)
		if err != nil {
			return nil, err
		}
		for id, seq := range persisted {
			locations[id] = seq
		}
	}
	if len(locations) < len(clientIDs) {
		// Rolling upgrades can have messages cached by the old writer without a
		// locator, before MQ persistence. Search a bounded cache tail only; all
		// older persisted messages use the exact scoped Mongo lookup above.
		maximum, err := db.cache.GetMaxSeq(ctx, conversationID)
		if err != nil {
			return nil, err
		}
		seqs := make([]int64, 0, 200)
		for seq := maximum; seq > 0 && len(seqs) < 200; seq-- {
			seqs = append(seqs, seq)
		}
		messages, _, err := db.cache.GetMessagesBySeq(ctx, conversationID, seqs)
		if err != nil {
			return nil, err
		}
		wanted := make(map[string]bool, len(missing))
		for _, id := range missing {
			wanted[id] = true
		}
		for _, message := range messages {
			if message != nil && wanted[message.ClientMsgID] {
				if previous, ok := locations[message.ClientMsgID]; ok && previous != message.Seq {
					return nil, errs.ErrData.Wrap("ambiguous client message identity")
				}
				locations[message.ClientMsgID] = message.Seq
			}
		}
	}
	seqs := make([]int64, 0, len(locations))
	for _, seq := range locations {
		seqs = append(seqs, seq)
	}
	_, _, messages, err := db.GetMsgBySeqs(ctx, userID, conversationID, seqs)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*sdkws.MsgData, len(messages))
	for _, message := range messages {
		if message != nil && locations[message.ClientMsgID] == message.Seq {
			result[message.ClientMsgID] = message
		}
	}
	for _, id := range clientIDs {
		if result[id] == nil {
			return nil, errs.ErrRecordNotFound.Wrap("message is absent or outside the user's visible history")
		}
	}
	return result, nil
}
