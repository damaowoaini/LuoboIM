// Package groupdelivery contains the explicit admission for the minimal new
// durable group lane. This isolated guard is not installed in any running RPC.
package groupdelivery

import (
	"errors"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

var (
	ErrDisabled                = errors.New("durable group mode not explicitly activated")
	ErrPushCallbacksUnprepared = errors.New("durable group push callback obligations unprepared")
	ErrOfflineUnprepared       = errors.New("durable group offline vendor obligations unprepared")
)

// Minimal admission refuses the ENTIRE new group lane if any group policy
// needs unsupported offline/vendor or push callback effects. It never disables
// those policies, downgrades them to dummy delivery or commits an obligation
// that this minimal worker cannot finish. Business after-callbacks have the
// separate stage1 durable unknown/inspect/confirmation mechanism.
func CheckMinimalCapabilities(allowNewSilentHistory bool) error {
	if !allowNewSilentHistory {
		return ErrDisabled
	}
	if config.Config.Callback.CallbackOnlinePush.Enable || config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable || config.Config.Callback.CallbackOfflinePush.Enable {
		return ErrPushCallbacksUnprepared
	}
	n := config.Config.Notification
	for _, policy := range []config.NotificationConf{n.GroupCreated, n.GroupInfoSet, n.JoinGroupApplication, n.MemberQuit, n.GroupApplicationAccepted, n.GroupApplicationRejected, n.GroupOwnerTransferred, n.MemberKicked, n.MemberInvited, n.MemberEnter, n.GroupDismissed, n.GroupMuted, n.GroupCancelMuted, n.GroupMemberMuted, n.GroupMemberCancelMuted, n.GroupMemberInfoSet, n.GroupMemberSetToAdmin, n.GroupMemberSetToOrdinary, n.GroupInfoSetAnnouncement, n.GroupInfoSetName} {
		if policy.OfflinePush.Enable {
			return ErrOfflineUnprepared
		}
	}
	return nil
}

// The full RPC wiring must call admission before its first external callback,
// and again immediately before Queue inside its captured configuration epoch.
// Unsupported mode is rejected BEFORE business TX rather than silently stuck
// after commit. This helper alone does not prove that those callers are wired.
func FreezeMinimalNotification(allowNewSilentHistory bool, operation string, ordinal int32, gid, actor string, message *sdkws.MsgData, at time.Time) (groupeffects.Frozen, error) {
	if err := CheckMinimalCapabilities(allowNewSilentHistory); err != nil {
		return groupeffects.Frozen{}, err
	}
	if message == nil || message.Options == nil || msgprocessor.Options(message.Options).IsNotNotification() {
		return groupeffects.Frozen{}, groupeffects.ErrInvalid
	}
	if msgprocessor.Options(message.Options).IsOfflinePush() {
		return groupeffects.Frozen{}, ErrOfflineUnprepared
	}
	prepared := proto.Clone(message).(*sdkws.MsgData)
	prepared.Options[constant.IsHistory] = true
	prepared.Options[constant.IsPersistent] = true
	return groupeffects.NewNotification(operation, ordinal, gid, actor, prepared, at)
}
