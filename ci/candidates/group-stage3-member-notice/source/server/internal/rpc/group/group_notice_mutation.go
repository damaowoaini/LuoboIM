package group

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"go.mongodb.org/mongo-driver/mongo"
)

type groupNoticeQueue interface {
	CheckReady(context.Context) error
	QueueActionNotification(context.Context, *groupauthority.NoticeSnapshot, *sdkws.MsgData, groupeffects.ActionDeliveryPolicy) (groupeffects.Frozen, error)
}

// Captured startup policy; no caller recipient list or wire operationID can
// replace the epoch/configuration. Owned delivery workers are a REQUIRED next
// slice. This producer source alone is NOT ready for runtime activation.
type groupNoticeRuntime struct {
	db *mongo.Database
	epoch string
	effects groupNoticeQueue
	policies map[groupauthority.NoticeAction]noticePolicy
}
type noticePolicy struct {
	notification config.NotificationConf
	delivery groupeffects.ActionDeliveryPolicy
}
func noticeHash(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil { return "", err }
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// No DDL/connections or random activation epoch. Enabled callback/offline
// policies are frozen required obligations, never disabled to admit this slice.
func newGroupNoticeRuntime(db *mongo.Database, epoch string) (*groupNoticeRuntime, error) {
	u, err := uuid.Parse(epoch)
	if err != nil || u == uuid.Nil || u.String() != epoch || db == nil { return nil, groupauthority.ErrUnavailable }
	store, err := groupeffects.NewStore(db)
	if err != nil { return nil, err }
	r := &groupNoticeRuntime{db: db, epoch: epoch, effects: store, policies: make(map[groupauthority.NoticeAction]noticePolicy)}
	for action, conf := range map[groupauthority.NoticeAction]config.NotificationConf{
		groupauthority.NoticeMuteGroup: config.Config.Notification.GroupMuted,
		groupauthority.NoticeUnmuteGroup: config.Config.Notification.GroupCancelMuted,
		groupauthority.NoticeMuteMember: config.Config.Notification.GroupMemberMuted,
		groupauthority.NoticeUnmuteMember: config.Config.Notification.GroupMemberCancelMuted,
	} {
		p := groupeffects.ActionDeliveryPolicy{Epoch: epoch, OfflineEnabled: conf.OfflinePush.Enable}
		bindings := []struct { name string; policy config.CallBackConfig; target *string }{
			{"before-group-message", config.Config.Callback.CallbackBeforeSendGroupMsg, &p.BeforeMessageBinding},
			{"after-group-message", config.Config.Callback.CallbackAfterSendGroupMsg, &p.AfterMessageBinding},
			{"before-supergroup-push", config.Config.Callback.CallbackBeforeSuperGroupOnlinePush, &p.BeforeGroupPushBinding},
			{"online-push", config.Config.Callback.CallbackOnlinePush, &p.OnlinePushBinding},
			{"offline-push", config.Config.Callback.CallbackOfflinePush, &p.OfflinePushBinding},
		}
		for _, binding := range bindings {
			if binding.policy.Enable {
				v, err := noticeHash([]any{db.Name(), config.Config.Callback.CallbackUrl, binding.name, binding.policy})
				if err != nil { return nil, err }
				*binding.target = v
			}
		}
		p.ConfigurationSHA256, err = noticeHash([]any{epoch, action, conf, p.BeforeMessageBinding, p.AfterMessageBinding, p.BeforeGroupPushBinding, p.OnlinePushBinding, p.OfflinePushBinding, config.Config.Push.Enable})
		if err != nil { return nil, err }
		r.policies[action] = noticePolicy{notification: conf, delivery: p}
	}
	return r, nil
}

// Display hydration occurs once outside retry. It carries no role authority.
func (s *groupServer) noticeActorProfile(ctx context.Context, gid string) (*sdkws.PublicUserInfo, error) {
	actor := mcontext.GetOpUserID(ctx)
	member, err := s.freshMember(ctx, gid, actor)
	if err == nil {
		if member.Nickname == "" || member.FaceURL == "" {
			if err := s.PopulateGroupMember(ctx, member); err != nil { return nil, err }
		}
		return &sdkws.PublicUserInfo{UserID: actor, Nickname: member.Nickname, FaceURL: member.FaceURL}, nil
	}
	// Only a configured, authenticated manager may hydrate outside the roster.
	// The authoritative Mongo DAO returns raw/wrapped ErrNoDocuments; foreign
	// rows and all other storage errors must never become a profile fallback.
	missing := errs.ErrRecordNotFound.Is(err) || errors.Is(err, mongo.ErrNoDocuments)
	if !authverify.IsAppManagerUid(ctx) || !missing { return nil, err }
	user, err := s.User.GetUserInfo(ctx, actor)
	if err != nil { return nil, err }
	if user == nil || user.UserID != actor { return nil, errs.ErrNoPermission.Wrap("invalid notification actor profile") }
	return &sdkws.PublicUserInfo{UserID: actor, Nickname: user.Nickname, FaceURL: user.FaceURL}, nil
}

func (s *groupServer) buildGroupStatusNotice(snapshot *groupauthority.NoticeSnapshot, profile *sdkws.PublicUserInfo, policy noticePolicy) (*sdkws.MsgData, error) {
	command := snapshot.Command()
	if profile == nil || profile.UserID != command.Actor { return nil, groupauthority.ErrInvalidScope }
	group := snapshot.Group()
	info := s.groupDB2PB(&group, snapshot.OwnerID(), snapshot.MemberCount())
	member, exists := snapshot.Member(command.Actor, true)
	var actor *sdkws.GroupMemberFullInfo
	if exists {
		actor = convert.Db2PbGroupMember(&member)
	} else if authverify.IsManagerUserID(command.Actor) {
		actor = &sdkws.GroupMemberFullInfo{GroupID: command.GroupID, UserID: command.Actor, RoleLevel: constant.GroupAdmin, AppMangerLevel: constant.AppAdmin}
	} else {
		actor = &sdkws.GroupMemberFullInfo{GroupID: command.GroupID, UserID: command.Actor, OperatorUserID: command.Actor}
	}
	if actor.Nickname == "" { actor.Nickname = profile.Nickname }
	if actor.FaceURL == "" { actor.FaceURL = profile.FaceURL }
	var tips any
	var contentType int32
	switch command.Action {
	case groupauthority.NoticeMuteGroup:
		tips, contentType = &sdkws.GroupMutedTips{Group: info, OpUser: actor}, constant.GroupMutedNotification
	case groupauthority.NoticeUnmuteGroup:
		tips, contentType = &sdkws.GroupCancelMutedTips{Group: info, OpUser: actor}, constant.GroupCancelMutedNotification
	default:
		return nil, groupauthority.ErrInvalidScope
	}
	content, err := json.Marshal(&sdkws.NotificationElem{Detail: utils.StructToJsonString(tips)})
	if err != nil { return nil, err }
	// Original ReliabilityLevel/IsSendMsg/unread/offline options are kept. No
	// silent Seq0 notification is quietly converted to stored history here.
	return &sdkws.MsgData{SendID: command.Actor, RecvID: command.GroupID, GroupID: command.GroupID, MsgFrom: constant.SysMsgType, ContentType: contentType, SessionType: constant.SuperGroupChatType, Content: content, CreateTime: command.CreatedAt.UnixMilli(), Options: config.GetOptionsByNotification(policy.notification), OfflinePushInfo: &sdkws.OfflinePushInfo{}}, nil
}

// Real mute/unmute entry points. Neither has a business after callback. Enabled
// push callbacks are retained in policy; no external call may run inside retry.
func (s *groupServer) runGroupStatusNoticeMutation(ctx context.Context, gid string, action groupauthority.NoticeAction, authorize, write func(context.Context) error) error {
	if s == nil || ctx == nil || authorize == nil || write == nil || s.authority == nil || s.notices == nil || s.notices.db == nil || s.notices.effects == nil { return errs.ErrInternalServer.Wrap("group notification storage is not ready") }
	ctx, finish := context.WithTimeout(ctx, 30*time.Second)
	defer finish()
	policy, ok := s.notices.policies[action]
	if !ok { return errs.ErrInternalServer.Wrap("group notification policy is not ready") }
	commandCtx, commandInfo, err := groupauthority.NewNoticeCommand(ctx, s.notices.epoch, action, gid)
	if err != nil { return err }
	profile, err := s.noticeActorProfile(commandCtx, gid)
	if err != nil { return err }
	ready, cancel := context.WithTimeout(commandCtx, 5*time.Second)
	err = s.notices.effects.CheckReady(ready)
	cancel()
	if err != nil { return errs.ErrInternalServer.Wrap("group notification storage is not ready") }
	var snapshot *groupauthority.NoticeSnapshot
	commit, err := s.authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: gid}}, func(tx context.Context) error {
		snapshot = nil // Never retain a capability from an abandoned TX attempt.
		if err := authorize(tx); err != nil { return err }
		var err error
		snapshot, err = groupauthority.BeginNotice(tx, s.notices.db, gid)
		return err
	}, func(tx context.Context) error {
		if err := write(tx); err != nil { return err }
		if err := snapshot.Finish(tx, s.notices.db); err != nil { return err }
		message, err := s.buildGroupStatusNotice(snapshot, profile, policy)
		if err != nil { return err }
		_, err = s.notices.effects.QueueActionNotification(tx, snapshot, message, policy.delivery)
		return err
	})
	if err != nil {
		if errors.Is(err, groupauthority.ErrCommitUnconfirmed) { return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay") }
		return err
	}
	if commit == nil { return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay") }
	// Normal legacy write ACK means the business mutation committed, not that
	// every cache, gateway or vendor completed. Projection has its own durable
	// job. Do not turn a known successful business write into a retry invitation.
	if s.invalidate == nil {
		log.ZWarn(commandCtx, "group projection recovery pending", nil, "commandID", commandInfo.ID, "action", action)
		return nil
	}
	flush, stop := context.WithTimeout(context.WithoutCancel(commandCtx), 5*time.Second)
	defer stop()
	if err := commit.Flush(flush, s.invalidate); err != nil {
		// Both writes are committed and the cache job is durable. Existing SDKs
		// understand their normal success ACK; they do not parse an English
		// error into a committed receipt. No driver detail/payload is logged.
		log.ZWarn(commandCtx, "group projection recovery pending", nil, "commandID", commandInfo.ID, "action", action)
		return nil
	}
	return nil
}
