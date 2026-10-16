package controller

import (
	"context"

	"github.com/OpenIMSDK/tools/errs"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/redis/go-redis/v9"
)

func (db *commonMsgDatabase) mergedFloor(ctx context.Context, conversation, user string, cached int64, cacheErr error) (int64, error) {
	if cacheErr != nil && errs.Unwrap(cacheErr) != redis.Nil {
		return 0, cacheErr
	}
	floors, err := db.msgDocDatabase.ReadMessageVisibility(ctx, []ut.MessageVisibilityFloor{{ConversationID: conversation, UserID: user}})
	if err != nil {
		return 0, err
	}
	for _, floor := range floors {
		cached = max(cached, floor.MinSeq)
	}
	return cached, nil
}

func (db *commonMsgDatabase) GetMinSeq(ctx context.Context, conversation string) (int64, error) {
	cached, err := db.cache.GetMinSeq(ctx, conversation)
	return db.mergedFloor(ctx, conversation, "", cached, err)
}
func (db *commonMsgDatabase) GetConversationUserMinSeq(ctx context.Context, conversation, user string) (int64, error) {
	cached, err := db.cache.GetConversationUserMinSeq(ctx, conversation, user)
	return db.mergedFloor(ctx, conversation, user, cached, err)
}

func (db *commonMsgDatabase) mergeFloors(ctx context.Context, keys []ut.MessageVisibilityFloor, cached map[string]int64, byUser bool, cacheErr error) (map[string]int64, error) {
	if cacheErr != nil && errs.Unwrap(cacheErr) != redis.Nil {
		return nil, cacheErr
	}
	floors, err := db.msgDocDatabase.ReadMessageVisibility(ctx, keys)
	if err != nil {
		return nil, err
	}
	if cached == nil {
		cached = map[string]int64{}
	}
	for _, floor := range floors {
		key := floor.ConversationID
		if byUser {
			key = floor.UserID
		}
		cached[key] = max(cached[key], floor.MinSeq)
	}
	return cached, nil
}
func (db *commonMsgDatabase) GetMinSeqs(ctx context.Context, conversations []string) (map[string]int64, error) {
	keys := make([]ut.MessageVisibilityFloor, 0, len(conversations))
	for _, conversation := range conversations {
		keys = append(keys, ut.MessageVisibilityFloor{ConversationID: conversation})
	}
	cached, err := db.cache.GetMinSeqs(ctx, conversations)
	return db.mergeFloors(ctx, keys, cached, false, err)
}
func (db *commonMsgDatabase) GetConversationUserMinSeqs(ctx context.Context, conversation string, users []string) (map[string]int64, error) {
	keys := make([]ut.MessageVisibilityFloor, 0, len(users))
	for _, user := range users {
		keys = append(keys, ut.MessageVisibilityFloor{ConversationID: conversation, UserID: user})
	}
	cached, err := db.cache.GetConversationUserMinSeqs(ctx, conversation, users)
	return db.mergeFloors(ctx, keys, cached, true, err)
}

// Mongo is authoritative. Cache failure is reported after the durable advance;
// retrying cannot lower the restriction or falsely acknowledge missing storage.
func (db *commonMsgDatabase) SetMinSeq(ctx context.Context, conversation string, seq int64) error {
	if err := db.msgDocDatabase.AdvanceMessageVisibility(ctx, []ut.MessageVisibilityFloor{{ConversationID: conversation, MinSeq: seq}}); err != nil {
		return err
	}
	return db.cache.SetMinSeq(ctx, conversation, seq)
}
func (db *commonMsgDatabase) SetMinSeqs(ctx context.Context, seqs map[string]int64) error {
	keys := make([]ut.MessageVisibilityFloor, 0, len(seqs))
	for conversation, seq := range seqs {
		keys = append(keys, ut.MessageVisibilityFloor{ConversationID: conversation, MinSeq: seq})
	}
	if err := db.msgDocDatabase.AdvanceMessageVisibility(ctx, keys); err != nil {
		return err
	}
	return db.cache.SetMinSeqs(ctx, seqs)
}
func (db *commonMsgDatabase) SetConversationUserMinSeq(ctx context.Context, conversation, user string, seq int64) error {
	if user == "" {
		return errs.ErrArgs.Wrap("visibility owner is required")
	}
	if err := db.msgDocDatabase.AdvanceMessageVisibility(ctx, []ut.MessageVisibilityFloor{{ConversationID: conversation, UserID: user, MinSeq: seq}}); err != nil {
		return err
	}
	return db.cache.SetConversationUserMinSeq(ctx, conversation, user, seq)
}
func (db *commonMsgDatabase) SetConversationUserMinSeqs(ctx context.Context, conversation string, seqs map[string]int64) error {
	keys := make([]ut.MessageVisibilityFloor, 0, len(seqs))
	for user, seq := range seqs {
		if user == "" {
			return errs.ErrArgs.Wrap("visibility owner is required")
		}
		keys = append(keys, ut.MessageVisibilityFloor{ConversationID: conversation, UserID: user, MinSeq: seq})
	}
	if err := db.msgDocDatabase.AdvanceMessageVisibility(ctx, keys); err != nil {
		return err
	}
	return db.cache.SetConversationUserMinSeqs(ctx, conversation, seqs)
}
func (db *commonMsgDatabase) SetUserConversationsMinSeqs(ctx context.Context, user string, seqs map[string]int64) error {
	if user == "" {
		return errs.ErrArgs.Wrap("visibility owner is required")
	}
	keys := make([]ut.MessageVisibilityFloor, 0, len(seqs))
	for conversation, seq := range seqs {
		keys = append(keys, ut.MessageVisibilityFloor{ConversationID: conversation, UserID: user, MinSeq: seq})
	}
	if err := db.msgDocDatabase.AdvanceMessageVisibility(ctx, keys); err != nil {
		return err
	}
	return db.cache.SetUserConversationsMinSeqs(ctx, user, seqs)
}
