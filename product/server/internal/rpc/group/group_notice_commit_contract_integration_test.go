package group

import (
	"reflect"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"go.mongodb.org/mongo-driver/bson"
)

// Real RPC/Mongo producer coverage only. This test does not execute a message
// callback, gateway, vendor or managed worker; it proves the required policy is
// frozen with the committed duty instead of disappearing from the new lane.
func TestGroupStatusRPCFreezesBeforeAndAfterMessageCallbackPolicies(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, func() {
		config.Config.Callback.CallbackUrl = "https://synthetic.invalid/original-callback"
		config.Config.Callback.CallbackBeforeSendGroupMsg.Enable = true
		config.Config.Callback.CallbackBeforeSendGroupMsg.CallbackTimeOut = 3
		config.Config.Callback.CallbackAfterSendGroupMsg.Enable = true
		config.Config.Callback.CallbackAfterSendGroupMsg.CallbackTimeOut = 4
	})
	expected := service.notices.policies[groupauthority.NoticeMuteGroup].delivery
	if expected.BeforeMessageBinding == "" || expected.AfterMessageBinding == "" || expected.BeforeMessageBinding == expected.AfterMessageBinding {
		t.Fatal("distinct enabled message callbacks were not captured at construction")
	}
	config.Config.Callback.CallbackUrl = "https://synthetic.invalid/replacement-callback"
	config.Config.Callback.CallbackBeforeSendGroupMsg.Enable = false
	config.Config.Callback.CallbackAfterSendGroupMsg.Enable = false
	if _, err := service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"}); err != nil {
		t.Fatal(err)
	}
	row := readActionReceipt(t, ctx, db)
	if !reflect.DeepEqual(row.Policy, expected) || storedGroupStatus(t, ctx, db) != constant.GroupStatusMuted || effectCount(t, ctx, db) != 1 {
		t.Fatal("committed duty borrowed later global callback configuration")
	}
}

func TestGroupStatusRPCKnownCommitWithoutInlineCacheAdapterReturnsSuccess(t *testing.T) {
	ctx, service, _, db, _ := groupStatusFixture(t, nil, nil)
	service.invalidate = nil
	if _, err := service.MuteGroup(statusActor(ctx, "admin"), &pbgroup.MuteGroupReq{GroupID: "g"}); err != nil {
		t.Fatal("known committed mutation became a client retry invitation", err)
	}
	if storedGroupStatus(t, ctx, db) != constant.GroupStatusMuted || effectCount(t, ctx, db) != 1 {
		t.Fatal("normal write ACK was returned without committed business and notification duty")
	}
	n, err := db.Collection(groupauthority.InvalidationsCollection).CountDocuments(ctx, bson.M{})
	if err != nil || n != 1 {
		t.Fatal("normal write ACK discarded the durable cache recovery duty", err)
	}
}
