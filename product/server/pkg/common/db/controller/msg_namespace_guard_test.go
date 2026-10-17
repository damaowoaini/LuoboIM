package controller

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

func TestMessageNamespaceRequiredModeRejectsBothRawLegacyWritersBeforeStorage(t *testing.T) {
	t.Setenv(messagedelivery.HistoryWindowModeEnv, "required")
	t.Setenv(messagedelivery.HistoryWindowEpochEnv, "c2fb972f-c1ae-4d34-b937-2ec837d7529d")
	t.Setenv(messagedelivery.HistoryWindowActivationEnv, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	// Nil storage interfaces make any accidental Redis/Mongo access a real
	// fixture failure. A permission refusal must happen before either access.
	db := &commonMsgDatabase{}
	t.Run("redis-allocator", func(t *testing.T) {
		if seq, fresh, err := db.BatchInsertChat2Cache(context.Background(), "n_a_b", []*sdkws.MsgData{{SendID: "a", RecvID: "b"}}); err == nil || seq != 0 || fresh {
			t.Fatal("required mode admitted raw Redis sequence/cache writer")
		}
	})
	t.Run("mongo-storage", func(t *testing.T) {
		if err := db.BatchInsertChat2DB(context.Background(), "n_a_b", []*sdkws.MsgData{{SendID: "a", RecvID: "b", Seq: 1}}, 0); err == nil {
			t.Fatal("required mode admitted raw Mongo legacy writer")
		}
	})
}
