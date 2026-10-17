package msg

import (
	"testing"

	"github.com/OpenIMSDK/tools/utils"
)

func TestAllowedNotificationConversationIDs(t *testing.T) {
	convs := []string{"si_u1_u2", "sg_g100"}
	set := allowedNotificationConversationIDs("u1", convs)

	mustHave := []string{
		utils.GetNotificationConversationIDByConversationID("si_u1_u2"),
		utils.GetNotificationConversationIDByConversationID("sg_g100"),
		utils.GetSelfNotificationConversationID("u1"),
	}
	for _, id := range mustHave {
		if _, ok := set[id]; !ok {
			t.Fatalf("own notification conversation %q must be allowed, set=%v", id, set)
		}
	}

	mustNot := []string{
		utils.GetNotificationConversationIDByConversationID("si_u3_u4"),
		utils.GetNotificationConversationIDByConversationID("sg_g999"),
		utils.GetSelfNotificationConversationID("u2"),
	}
	for _, id := range mustNot {
		if _, ok := set[id]; ok {
			t.Fatalf("foreign notification conversation %q must be denied", id)
		}
	}
}

func TestCanPullNotificationConversation(t *testing.T) {
	set := allowedNotificationConversationIDs("u1", []string{"sg_g100"})
	allow := []string{
		"n_g100",  // 自己所在群
		"n_u1_u1", // 自己的系统通知
		"n_u1_u9", // 陌生人发来的好友申请：还没有单聊会话记录，但自己是参与方
		"n_u0_u1",
	}
	for _, id := range allow {
		if !canPullNotificationConversation("u1", id, set) {
			t.Fatalf("%q must be allowed", id)
		}
	}
	deny := []string{
		"n_g999",  // 别人的群
		"n_u2_u3", // 别人之间的通知
		"n_u2_u2", // 别人的系统通知
		"n_u1",    // 不是两方格式，也不在会话集合里
		"n_u1_",   // 对方为空
		"n__u1",
		"n_u1_a_b", // 对方 ID 含下划线：无法无歧义解析，拒绝
		"si_u1_u2", // 不是通知会话
	}
	for _, id := range deny {
		if canPullNotificationConversation("u1", id, set) {
			t.Fatalf("%q must be denied", id)
		}
	}
}
