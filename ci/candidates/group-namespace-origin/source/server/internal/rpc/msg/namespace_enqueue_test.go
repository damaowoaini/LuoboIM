package msg

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
)

type namespaceQueueFixture struct {
	controller.CommonMsgDatabase
	calls   int
	key     string
	message *sdkws.MsgData
}

func (f *namespaceQueueFixture) MsgToMQ(_ context.Context, key string, message *sdkws.MsgData) error {
	f.calls++
	f.key, f.message = key, message
	return nil
}

// This checks the actual final producer helper used by all three SendMsg
// branches, not the whole RPC callback/risk/transport chain. The real Mongo
// reservation/unknown-commit tests are in internal/messagedelivery.
func TestMessageNamespaceEnqueueCannotFallBackInRequiredOrHalfMode(t *testing.T) {
	for _, item := range []struct {
		name, mode, epoch, activation string
		allowed                       bool
	}{
		{"legacy", "", "", "", true}, {"required", "required", "c2fb972f-c1ae-4d34-b937-2ec837d7529d", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false}, {"half", "", "c2fb972f-c1ae-4d34-b937-2ec837d7529d", "", false},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Setenv(messagedelivery.HistoryWindowModeEnv, item.mode)
			t.Setenv(messagedelivery.HistoryWindowEpochEnv, item.epoch)
			t.Setenv(messagedelivery.HistoryWindowActivationEnv, item.activation)
			queue := &namespaceQueueFixture{}
			server := &msgServer{MsgDatabase: queue}
			message := &sdkws.MsgData{SendID: "a", RecvID: "b", SessionType: constant.SingleChatType}
			err := server.enqueueNamespaced(context.Background(), "original-partition-key", message)
			if item.allowed {
				if err != nil || queue.calls != 1 || queue.message != message || queue.key != "original-partition-key" {
					t.Fatal("legacy queue argument/dispatch contract changed", err)
				}
			} else if err == nil || queue.calls != 0 {
				t.Fatal("missing namespace admission reached MQ", err)
			}
		})
	}
}
