package controller

import (
	"context"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
)

type MsgGroupReadDatabase interface {
	EnsureGroupMessage(ctx context.Context, conversationID, clientMsgID string, getUserIDs cache.GetUserIDsFunc) error
	GetMsgNum(ctx context.Context, conversationID string, clientMsgID string) (readNum int64, unreadNum int64, err error)
	MarkGroupMessageRead(ctx context.Context, conversationID string, clientMsgID string, userID string, readTime *time.Time, getUserIDs cache.GetUserIDsFunc) (int32, error)
	PageReadUserList(ctx context.Context, conversationID string, clientMsgID string, read bool, pageNumber int32, showNumber int32) (int64, []*cache.UserReadTime, error)
}
