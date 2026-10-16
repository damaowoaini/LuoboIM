package msgtransfer

import (
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

// FreezeDurableGroupNotification is an explicit product/configuration change,
// not a reinterpretation of an already queued unreliable notification. It must
// run before the business TX freezes its new effect. Ordinary NotificationSender
// and previously frozen v1 effects are unchanged. The activation manifest must
// explicitly enable the additional silent history, retention and query scope.
func FreezeDurableGroupNotification(allowSilentHistoryUpgrade bool, operation string, ordinal int32, gid, actor string, message *sdkws.MsgData, at time.Time) (groupeffects.Frozen, error) {
	if !allowSilentHistoryUpgrade || message == nil || message.Options == nil || msgprocessor.Options(message.Options).IsNotNotification() {
		return groupeffects.Frozen{}, errGroupEffectPlan
	}
	prepared := proto.Clone(message).(*sdkws.MsgData)
	prepared.Options[constant.IsHistory] = true
	prepared.Options[constant.IsPersistent] = true
	return groupeffects.NewNotification(operation, ordinal, gid, actor, prepared, at)
}
