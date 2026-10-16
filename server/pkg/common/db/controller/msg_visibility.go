package controller

import (
	"context"
	"github.com/OpenIMSDK/protocol/sdkws"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
)

// The durable metadata query is the read's visibility linearization point.
// A stale/rehydrated cache body cannot override an already committed tombstone.
func (db *commonMsgDatabase) filterDurableDeletions(ctx context.Context, owner, conversationID string, messages []*sdkws.MsgData) ([]*sdkws.MsgData, error) {
	seqs := make([]int64, 0, len(messages))
	for _, message := range messages {
		if message != nil && message.Seq > 0 {
			seqs = append(seqs, message.Seq)
		}
	}
	if len(seqs) == 0 {
		return messages, nil
	}
	hidden, err := db.msgDocDatabase.HiddenMessageSequences(ctx, owner, conversationID, seqs)
	if err != nil {
		return nil, err
	}
	keys := []ut.MessageVisibilityFloor{{ConversationID: conversationID}}
	if owner != "" {
		keys = append(keys, ut.MessageVisibilityFloor{ConversationID: conversationID, UserID: owner})
	}
	floors, err := db.msgDocDatabase.ReadMessageVisibility(ctx, keys)
	if err != nil {
		return nil, err
	}
	var minSeq int64
	for _, floor := range floors {
		minSeq = max(minSeq, floor.MinSeq)
	}
	hide := make(map[int64]bool, len(hidden))
	for _, seq := range hidden {
		hide[seq] = true
	}
	result := make([]*sdkws.MsgData, 0, len(messages))
	for _, message := range messages {
		if message != nil && message.Seq >= minSeq && !hide[message.Seq] {
			result = append(result, message)
		}
	}
	return result, nil
}
