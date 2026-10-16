package msgtransfer

import (
	"bytes"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

func effectPlanFixture(t *testing.T, history, visible, offline, unread bool) groupeffects.Frozen {
	t.Helper()
	message := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", MsgFrom: constant.SysMsgType, SessionType: constant.SuperGroupChatType, ContentType: constant.MemberKickedNotification, Content: []byte(`{"detail":"synthetic"}`), AtUserIDList: []string{"owner"}, Options: msgprocessor.NewOptions(msgprocessor.WithHistory(history), msgprocessor.WithSendMsg(visible), msgprocessor.WithOfflinePush(offline), msgprocessor.WithUnreadCount(unread))}
	f, err := groupeffects.NewNotification("b93fd30a-82d8-470f-a352-7b312a722bf7", 0, "g", "owner", message, time.UnixMilli(2000))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// This calls the actual existing split helper, rather than mirroring its
// expected defaults in a fake. Transport, sequence and push are not executed.
func TestGroupEffectPlanPreservesExistingNotificationSplit(t *testing.T) {
	for _, visible := range []bool{false, true} {
		for _, history := range []bool{false, true} {
			for _, offline := range []bool{false, true} {
				for _, unread := range []bool{false, true} {
					f := effectPlanFixture(t, history, visible, offline, unread)
					branches, err := planGroupEffect(f)
					if err != nil {
						t.Fatal(err)
					}
					var legacy sdkws.MsgData
					if err := proto.Unmarshal(f.Payload, &legacy); err != nil {
						t.Fatal(err)
					}
					handler := &OnlineHistoryRedisConsumerHandler{}
					storedChat, ephemeralChat, storedNotification, ephemeralNotification, modified := handler.getPushStorageMsgList([]*ContextMsg{{message: &legacy}})
					if len(modified) != 0 {
						t.Fatal("fixture unexpectedly entered modifier path")
					}
					expected := append(append(append(append([]*sdkws.MsgData{}, storedChat...), ephemeralChat...), storedNotification...), ephemeralNotification...)
					if len(branches) != len(expected) {
						t.Fatal("notification branch count changed")
					}
					for i, branch := range branches {
						var actual sdkws.MsgData
						if err := proto.Unmarshal(branch.Payload, &actual); err != nil {
							t.Fatal(err)
						}
						if _, err := uuid.Parse(actual.ServerMsgID); err != nil {
							t.Fatal("server identity is not frozen")
						}
						actual.ServerMsgID = expected[i].ServerMsgID
						if !proto.Equal(&actual, expected[i]) || branch.Persist != msgprocessor.Options(expected[i].Options).IsHistory() {
							t.Fatal("existing silent/visible options or message changed")
						}
					}
				}
			}
		}
	}
}

func TestGroupEffectPlanHasStableSeparateBranchIdentityAndNoBorrowedData(t *testing.T) {
	f := effectPlanFixture(t, true, true, true, true)
	first, err := planGroupEffect(f)
	if err != nil {
		t.Fatal(err)
	}
	second, err := planGroupEffect(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || first[0].ConversationID != "sg_g" || first[1].ConversationID != "n_g" || first[0].ID == first[1].ID {
		t.Fatal("chat and silent branches overlap")
	}
	for i := range first {
		if first[i].ID != second[i].ID || first[i].PayloadSHA256 != second[i].PayloadSHA256 || !bytes.Equal(first[i].Payload, second[i].Payload) {
			t.Fatal("retry refroze notification")
		}
	}
	first[0].Payload[0] ^= 1
	third, err := planGroupEffect(f)
	if err != nil || !bytes.Equal(second[0].Payload, third[0].Payload) {
		t.Fatal("branch borrowed prior projection bytes")
	}
}

func TestGroupEffectPlanRejectsForeignOrCorruptDurableEffect(t *testing.T) {
	for name, change := range map[string]func(*groupeffects.Frozen){"group": func(f *groupeffects.Frozen) { f.GroupID = "foreign" }, "actor": func(f *groupeffects.Frozen) { f.Actor = "foreign" }, "id": func(f *groupeffects.Frozen) { f.ID = uuid.NewString() }, "body": func(f *groupeffects.Frozen) { f.Payload = append([]byte(nil), f.Payload...); f.Payload[0] ^= 1 }, "kind": func(f *groupeffects.Frozen) { f.Kind = groupeffects.AfterCallback }} {
		t.Run(name, func(t *testing.T) {
			f := effectPlanFixture(t, true, true, true, true)
			change(&f)
			if _, err := planGroupEffect(f); err == nil {
				t.Fatal("corrupt/foreign effect planned")
			}
		})
	}
}
