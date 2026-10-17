package groupdelivery

import (
	"errors"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

func minimalConfig(t *testing.T) []*config.NotificationConf {
	t.Helper()
	original := config.Config
	t.Cleanup(func() { config.Config = original })
	config.Config.Callback.CallbackOnlinePush.Enable = false
	config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable = false
	config.Config.Callback.CallbackOfflinePush.Enable = false
	n := &config.Config.Notification
	policies := []*config.NotificationConf{&n.GroupCreated, &n.GroupInfoSet, &n.JoinGroupApplication, &n.MemberQuit, &n.GroupApplicationAccepted, &n.GroupApplicationRejected, &n.GroupOwnerTransferred, &n.MemberKicked, &n.MemberInvited, &n.MemberEnter, &n.GroupDismissed, &n.GroupMuted, &n.GroupCancelMuted, &n.GroupMemberMuted, &n.GroupMemberCancelMuted, &n.GroupMemberInfoSet, &n.GroupMemberSetToAdmin, &n.GroupMemberSetToOrdinary, &n.GroupInfoSetAnnouncement, &n.GroupInfoSetName}
	for _, p := range policies {
		p.OfflinePush.Enable = false
	}
	return policies
}
func TestMinimalDeliveryAdmissionRefusesEveryUnsupportedGroupOfflinePolicy(t *testing.T) {
	policies := minimalConfig(t)
	if err := CheckMinimalCapabilities(false); !errors.Is(err, ErrDisabled) {
		t.Fatal("implicit activation accepted")
	}
	if err := CheckMinimalCapabilities(true); err != nil {
		t.Fatal("explicit online-only minimal mode rejected", err)
	}
	for i, p := range policies {
		p.OfflinePush.Enable = true
		if err := CheckMinimalCapabilities(true); !errors.Is(err, ErrOfflineUnprepared) {
			t.Fatalf("unsupported group offline policy %d accepted", i)
		}
		p.OfflinePush.Enable = false
	}
}
func TestMinimalDeliveryAdmissionRefusesEachUnsupportedPushCallbackBeforeFreeze(t *testing.T) {
	minimalConfig(t)
	for _, enabled := range []*bool{&config.Config.Callback.CallbackOnlinePush.Enable, &config.Config.Callback.CallbackBeforeSuperGroupOnlinePush.Enable, &config.Config.Callback.CallbackOfflinePush.Enable} {
		*enabled = true
		frozen, err := FreezeMinimalNotification(true, uuid.NewString(), 0, "g", "owner", nil, time.Now())
		if !errors.Is(err, ErrPushCallbacksUnprepared) || frozen.ID != "" {
			t.Fatal("unsupported callback reached notification freeze", err)
		}
		*enabled = false
	}
}
func TestMinimalDeliveryFreezePreservesOrdinaryInputAndRejectsPerMessageOffline(t *testing.T) {
	minimalConfig(t)
	message := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", SessionType: constant.SuperGroupChatType, MsgFrom: constant.SysMsgType, ContentType: constant.GroupDismissedNotification, Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(false))}
	original := proto.Clone(message)
	frozen, err := FreezeMinimalNotification(true, uuid.NewString(), 0, "g", "owner", message, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(message, original) {
		t.Fatal("new reliable mode rewrote ordinary input/options")
	}
	var result sdkws.MsgData
	if err := proto.Unmarshal(frozen.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if !msgprocessor.Options(result.Options).IsHistory() || !msgprocessor.Options(result.Options).IsPersistent() || msgprocessor.Options(result.Options).IsOfflinePush() {
		t.Fatal("new silent branch capability mismatch")
	}
	message.Options[constant.IsOfflinePush] = true
	if f, err := FreezeMinimalNotification(true, uuid.NewString(), 0, "g", "owner", message, time.Now()); !errors.Is(err, ErrOfflineUnprepared) || f.ID != "" {
		t.Fatal("unprepared per-message vendor duty accepted", err)
	}
}
