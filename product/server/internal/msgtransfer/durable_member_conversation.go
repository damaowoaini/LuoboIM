package msgtransfer

import (
	"context"
	"errors"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
)

func (w *ManagedGroupActionWorker) bindMemberConversationRecovery(authority *messagedelivery.Store, effects *groupeffects.Store, rdb redis.UniversalClient) error {
	if w == nil || authority == nil || effects == nil || rdb == nil || w.memberConversations != nil {
		return messagedelivery.ErrInvalid
	}
	// Only the original invalidation methods are used. No new DAO constructor,
	// collection/index creation or standalone conversation RPC is performed.
	projection := cache.NewConversationRedis(rdb, cache.GetDefaultOpt(), nil)
	cursor := ""
	w.memberConversations = func(ctx context.Context) error {
		page, err := effects.ListMemberConversationProjections(ctx, cursor, 8)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			cursor = ""
			return nil
		}
		var failures []error
		for _, row := range page {
			if ctx.Err() != nil {
				return errors.Join(append(failures, ctx.Err())...)
			}
			cursor = row.Cursor
			work, cancel := context.WithTimeout(ctx, 30*time.Second)
			err := effects.RecoverMemberConversationProjection(work, row.ID, authority, func(ctx context.Context, owner, cid string) error {
				return projection.NewCache().DelConversations(owner, cid).DelConversationIDs(owner).DelUserConversationIDsHash(owner).DelConversationNotReceiveMessageUserIDs(cid).DelConversationByConversationID(cid).ExecDel(ctx)
			})
			cancel()
			if err != nil {
				failures = append(failures, err)
			}
		}
		return errors.Join(failures...)
	}
	return nil
}
