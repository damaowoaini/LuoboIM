package msg

import (
	"context"
	"strings"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

func TestUnsupportedModifierActualSendMsgRejectsBeforeEncapsulationOrCallbacks(t *testing.T) {
	for _, kind := range []int32{constant.ReactionMessageModifier, constant.ReactionMessageDeleter} {
		name := "modifier"
		if kind == constant.ReactionMessageDeleter {
			name = "deleter"
		}
		t.Run(name, func(t *testing.T) {
			q := &namespaceQueueFixture{}
			server := &msgServer{MsgDatabase: q}
			message := &sdkws.MsgData{SendID: "sender", RecvID: "peer", ClientMsgID: "synthetic-original-intent", MsgFrom: constant.UserMsgType, SessionType: constant.SingleChatType, ContentType: kind}
			ctx := context.WithValue(context.Background(), constant.OpUserID, "sender")
			result, err := server.SendMsg(ctx, &pbmsg.SendMsgReq{MsgData: message})
			if err == nil || !strings.Contains(err.Error(), "unsupported message modifier content type") || result != nil || q.calls != 0 || message.ServerMsgID != "" || message.SendTime != 0 {
				t.Fatal("unsupported request reached side effects or fake ACK", err)
			}
		})
	}
}

func TestUnsupportedModifierActualCallbackFinalEnqueueCannotBypassAdmission(t *testing.T) {
	t.Setenv(messagedelivery.HistoryWindowModeEnv, "")
	t.Setenv(messagedelivery.HistoryWindowEpochEnv, "")
	t.Setenv(messagedelivery.HistoryWindowActivationEnv, "")
	for _, kind := range []int32{constant.ReactionMessageModifier, constant.ReactionMessageDeleter, constant.Text} {
		name := "modifier"
		if kind == constant.ReactionMessageDeleter {
			name = "deleter"
		}
		if kind == constant.Text {
			name = "ordinary-text"
		}
		t.Run(name, func(t *testing.T) {
			server, _, req := callbackAuthorizationFixture(t, constant.GroupAdmin)
			q := &namespaceQueueFixture{}
			server.MsgDatabase = q
			callbacks := 0
			err := server.withVerifiedGroupMessage(context.Background(), req, func(_ context.Context, r *pbmsg.SendMsgReq) error {
				callbacks++
				r.MsgData.ContentType = kind
				return nil
			}, func(ctx context.Context, r *pbmsg.SendMsgReq) error {
				return server.enqueueNamespaced(ctx, "original-partition-key", r.MsgData)
			})
			if callbacks != 1 {
				t.Fatal("real callback boundary was not exercised")
			}
			if kind == constant.Text {
				if err != nil || q.calls != 1 || q.message != req.MsgData {
					t.Fatal("ordinary text was globally blocked", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "unsupported message modifier content type") || q.calls != 0 {
				t.Fatal("post-callback modifier reached MQ", err)
			}
		})
	}
}
