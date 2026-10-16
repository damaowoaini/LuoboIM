package group

import (
	"context"
	"fmt"

	"github.com/OpenIMSDK/protocol/constant"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/redis/go-redis/v9"

	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

// forbidNewMemberHistoryRedisKey 由 chart 管理后台在 client_config 更新后同步写入。
// chart 与 server 共用 Redis；字符串必须与 chart/pkg/common/db/database/admin.go 保持一致。
const forbidNewMemberHistoryRedisKey = "GLOBAL_CONFIG:FORBID_NEW_MEMBER_HISTORY"

// restrictNewMembersHistory 设置新成员的消息读取下界。入群流程会在写入成员关系前后各调用一次：
// 前一次保证边界设置失败时不产生新成员，后一次用于覆盖入群瞬间的并发消息窗口。
// ClearConversationsMsg 会把该用户在群会话中的 userMinSeq 设置为当前 maxSeq+1，
// 因而入群通知和之后的新消息仍可见，入群前的消息无法再从服务端拉取。
func (s *groupServer) restrictNewMembersHistory(ctx context.Context, groupID string, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	enabled, err := s.forbidNewMemberHistoryEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return nil
	}

	conversationID := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, groupID)
	for _, userID := range userIDs {
		if userID == "" {
			return errs.ErrArgs.Wrap("new group member userID is empty")
		}
		// 这是群服务发起的内部 RPC。将操作用户设为目标成员，使消息服务的
		// CheckAccessV3 校验与 ClearConversationsMsg 的 userID 保持一致。
		userCtx := mcontext.WithOpUserIDContext(ctx, userID)
		if _, err := s.msgRpcClient.Client.ClearConversationsMsg(userCtx, &pbmsg.ClearConversationsMsgReq{
			ConversationIDs: []string{conversationID},
			UserID:          userID,
		}); err != nil {
			return err
		}
	}
	log.ZInfo(ctx, "restricted new group members from history messages", "groupID", groupID, "userIDs", userIDs)
	return nil
}

func (s *groupServer) forbidNewMemberHistoryEnabled(ctx context.Context) (bool, error) {
	if s.rdb == nil {
		return false, errs.ErrInternalServer.Wrap("redis client is nil")
	}
	value, err := s.rdb.Get(ctx, forbidNewMemberHistoryRedisKey).Result()
	if err == redis.Nil {
		// 老版本没有该 key，默认关闭，保持升级前新成员可查看历史的行为。
		return false, nil
	}
	if err != nil {
		return false, errs.Wrap(err)
	}
	switch value {
	case "true":
		return true, nil
	case "false", "":
		return false, nil
	default:
		return false, errs.ErrData.Wrap(fmt.Sprintf("invalid %s value %q", forbidNewMemberHistoryRedisKey, value))
	}
}
