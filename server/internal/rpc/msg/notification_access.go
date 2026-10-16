package msg

import (
	"context"
	"strings"

	"github.com/OpenIMSDK/tools/utils"
)

// allowedNotificationConversationIDs 用户可以拉取的通知会话：
// 自己的会话 + 这些会话对应的通知会话 + 自己的系统通知会话。
// 与 GetMaxSeq 下发给 SDK 的集合保持一致，正常客户端不受影响。
func allowedNotificationConversationIDs(userID string, conversationIDs []string) map[string]struct{} {
	set := make(map[string]struct{}, len(conversationIDs)*2+1)
	for _, id := range conversationIDs {
		set[id] = struct{}{}
		if n := utils.GetNotificationConversationIDByConversationID(id); n != "" {
			set[n] = struct{}{}
		}
	}
	set[utils.GetSelfNotificationConversationID(userID)] = struct{}{}
	return set
}

// canPullNotificationConversation 能否拉取该通知会话：
//   - 在自己的会话集合里（含群通知、自己的系统通知）；或
//   - 是两方通知会话 n_<a>_<b>，且自己是其中一方。通知消息不会创建会话记录，
//     陌生人发来的好友申请等只能这样判断。对方 ID 含下划线时无法无歧义解析，按拒绝处理。
func canPullNotificationConversation(userID, conversationID string, allowed map[string]struct{}) bool {
	if _, ok := allowed[conversationID]; ok {
		return true
	}
	if userID == "" || !strings.HasPrefix(conversationID, "n_") {
		return false
	}
	rest := strings.TrimPrefix(conversationID, "n_")
	validOther := func(other string) bool { return other != "" && !strings.Contains(other, "_") }
	if strings.HasPrefix(rest, userID+"_") && validOther(strings.TrimPrefix(rest, userID+"_")) {
		return true
	}
	if strings.HasSuffix(rest, "_"+userID) && validOther(strings.TrimSuffix(rest, "_"+userID)) {
		return true
	}
	return false
}

func (m *msgServer) notificationAccessSet(ctx context.Context, userID string) (map[string]struct{}, error) {
	conversationIDs, err := m.ConversationLocalCache.GetConversationIDs(ctx, userID)
	if err != nil {
		return nil, err
	}
	return allowedNotificationConversationIDs(userID, conversationIDs), nil
}
