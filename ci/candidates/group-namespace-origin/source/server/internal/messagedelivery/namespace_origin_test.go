package messagedelivery

import (
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

func namespaceMessage(sender, receiver string, session int32, normal, history bool) *sdkws.MsgData {
	message := &sdkws.MsgData{SendID: sender, RecvID: receiver, SessionType: session, MsgFrom: constant.UserMsgType, ContentType: constant.Text, ServerMsgID: "synthetic-namespace-message", ClientMsgID: "synthetic-namespace-client", CreateTime: 2000, SendTime: 2000, Content: []byte("synthetic namespace payload"), Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(normal), msgprocessor.WithHistory(history))}
	if session == constant.SuperGroupChatType || session == constant.GroupChatType {
		message.GroupID = receiver
	}
	if session == constant.NotificationChatType {
		message.MsgFrom = constant.SysMsgType
	}
	return message
}

func TestMessageNamespaceProtocolOriginNeverParsesUnderscoreAliases(t *testing.T) {
	for _, gid := range []string{"a_b", "sg_a_b"} {
		t.Run(gid, func(t *testing.T) {
			normal, err := messageNamespace(namespaceMessage("owner", gid, constant.SuperGroupChatType, true, true))
			if err != nil {
				t.Fatal(err)
			}
			notice, err := messageNamespace(namespaceMessage("owner", gid, constant.SuperGroupChatType, false, false))
			if err != nil || normal.CID != "sg_"+gid || notice.CID != "n_"+gid || normal.Kind != "group" || normal.SHA256 != notice.SHA256 {
				t.Fatal("group protocol pair or common origin changed", err)
			}
		})
	}
	t.Run("single-reverse", func(t *testing.T) {
		a, err := messageNamespace(namespaceMessage("a_b", "c", constant.SingleChatType, true, true))
		if err != nil {
			t.Fatal(err)
		}
		b, err := messageNamespace(namespaceMessage("c", "a_b", constant.SingleChatType, true, true))
		if err != nil || a != b {
			t.Fatal("ordinary single tuple stopped being symmetric", err)
		}
		collision, err := messageNamespace(namespaceMessage("a", "b_c", constant.SingleChatType, true, true))
		if err != nil || collision.CID != a.CID || collision.SHA256 == a.SHA256 {
			t.Fatal("same-kind underscore alias lost its distinct typed origin", err)
		}
	})
	t.Run("notification-direction", func(t *testing.T) {
		a, err := messageNamespace(namespaceMessage("a", "b", constant.NotificationChatType, false, false))
		if err != nil {
			t.Fatal(err)
		}
		b, err := messageNamespace(namespaceMessage("b", "a", constant.NotificationChatType, false, false))
		if err != nil || a.CID != "n_a_b" || b.CID != "n_b_a" || a.SHA256 == b.SHA256 {
			t.Fatal("server notification tuple was sorted like a single chat", err)
		}
		group, err := messageNamespace(namespaceMessage("owner", "a_b", constant.SuperGroupChatType, false, false))
		if err != nil || group.CID != a.CID || group.Kind == a.Kind || group.SHA256 == a.SHA256 {
			t.Fatal("group/direct alias accepted as the same origin", err)
		}
	})
	t.Run("foreign-group-field", func(t *testing.T) {
		message := namespaceMessage("a", "b", constant.SingleChatType, true, true)
		message.GroupID = "foreign"
		if _, err := messageNamespace(message); err == nil {
			t.Fatal("direct DTO carried group authority")
		}
	})
}

func TestMessageNamespaceFactoryKeepsCompleteLegacyModeAndRejectsHalfAdmission(t *testing.T) {
	for _, item := range []struct {
		name, mode, epoch, activation string
		legacy                        bool
	}{
		{"legacy", "", "", "", true}, {"half", "", "synthetic-epoch", "", false}, {"required-without-db", "required", "c2fb972f-c1ae-4d34-b937-2ec837d7529d", hash([]byte("synthetic activation")), false},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Setenv(HistoryWindowModeEnv, item.mode)
			t.Setenv(HistoryWindowEpochEnv, item.epoch)
			t.Setenv(HistoryWindowActivationEnv, item.activation)
			store, err := NewMessageNamespaceAdmissionFromEnvironment(nil)
			if item.legacy {
				if store != nil || err != nil {
					t.Fatal("complete legacy mode changed", err)
				}
			} else if store != nil || err == nil {
				t.Fatal("half/required settings silently selected legacy enqueue")
			}
		})
	}
}
