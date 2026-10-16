package controller

import (
	"context"
	"math"
	"github.com/OpenIMSDK/protocol/sdkws"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
)

// The durable metadata query is the read's visibility linearization point.
// A stale/rehydrated cache body cannot override an already committed tombstone.
func (db *commonMsgDatabase) filterDurableDeletions(ctx context.Context, owner, conversationID string, messages []*sdkws.MsgData) ([]*sdkws.MsgData, error) {
	filtered, _, err := db.filterDurableDeletionsWithMaximum(ctx, owner, conversationID, messages, math.MaxInt64)
	return filtered, err
}

// The upper bound is read AFTER cache, source-slot and lower/tombstone awaits.
// This is a final authority read, not an atomic promise about later sockets.
func (db *commonMsgDatabase) filterDurableDeletionsWithMaximum(ctx context.Context, owner, conversationID string, messages []*sdkws.MsgData, maximum int64) ([]*sdkws.MsgData, int64, error) {
	seqs := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message != nil && message.Seq > 0 {
			seqs = append(seqs, message.Seq)
		}
	}
	if len(seqs) == 0 {
		maximum, _, err := db.groupHistoryMaximum(ctx, owner, conversationID, maximum)
		return messages, maximum, err
	}
	hidden, err := db.msgDocDatabase.HiddenMessageSequences(ctx, owner, conversationID, seqs)
	if err != nil {
		return nil, 0, err
	}
	keys := []ut.MessageVisibilityFloor{{ConversationID: conversationID}}
	if owner != "" {
		keys = append(keys, ut.MessageVisibilityFloor{ConversationID: conversationID, UserID: owner})
	}
	floors, err := db.msgDocDatabase.ReadMessageVisibility(ctx, keys)
	if err != nil {
		return nil, 0, err
	}
	var minSeq int64
	for _, floor := range floors {
		minSeq = max(minSeq, floor.MinSeq)
	}
	hide := make(map[int64]bool, len(hidden))
	for _, seq := range hidden {
		hide[seq] = true
	}
	maximum, _, err = db.groupHistoryMaximum(ctx, owner, conversationID, maximum)
	if err != nil { return nil, 0, err }
	result := make([]*sdkws.MsgData, 0, len(messages))
	for _, message := range messages {
		if message != nil && message.Seq >= minSeq && message.Seq <= maximum && !hide[message.Seq] {
			result = append(result, message)
		}
	}
	return result, maximum, nil
}
