package msgtransfer

import (
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

func TestDurableGroupNotificationHistoryUpgradeIsExplicitAndKeepsOtherWireSemantics(t *testing.T) {
	for _, visible := range []bool{false, true} {
		original := effectPlanFixture(t, false, visible, true, true)
		var message sdkws.MsgData
		if proto.Unmarshal(original.Payload, &message) != nil {
			t.Fatal("fixture")
		}
		if _, err := FreezeDurableGroupNotification(false, original.OperationID, original.Ordinal, original.GroupID, original.Actor, &message, original.CreatedAt); err == nil {
			t.Fatal("unapproved silent-history upgrade happened implicitly")
		}
		durable, err := FreezeDurableGroupNotification(true, original.OperationID, original.Ordinal, original.GroupID, original.Actor, &message, original.CreatedAt)
		if err != nil {
			t.Fatal(err)
		}
		var upgraded sdkws.MsgData
		if proto.Unmarshal(durable.Payload, &upgraded) != nil {
			t.Fatal("durable fixture")
		}
		if msgprocessor.Options(message.Options).IsHistory() || !msgprocessor.Options(upgraded.Options).IsHistory() || !msgprocessor.Options(upgraded.Options).IsPersistent() {
			t.Fatal("history upgrade mutated ordinary input or was not explicit")
		}
		upgraded.Options[constant.IsHistory] = message.Options[constant.IsHistory]
		upgraded.Options[constant.IsPersistent] = message.Options[constant.IsPersistent]
		if !proto.Equal(&message, &upgraded) {
			t.Fatal("reliable upgrade changed silent CID, unread, grey-bar, recipients, content or identity")
		}
		branches, err := planGroupEffect(durable)
		if err != nil {
			t.Fatal(err)
		}
		for _, branch := range branches {
			if !branch.Persist {
				t.Fatal("durable recovery admitted an unsequenced branch")
			}
		}
	}
}
