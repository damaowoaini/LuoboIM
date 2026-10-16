package group

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
)

// Profile hydration supplies display fields only. Roles and membership are
// read again from the active transaction; neither profile grants authority.
func (s *groupServer) noticeTargetProfile(ctx context.Context, gid, uid string) (*sdkws.PublicUserInfo, error) {
	member, err := s.freshMember(ctx, gid, uid)
	if err != nil { return nil, err }
	if err := s.PopulateGroupMember(ctx, member); err != nil { return nil, err }
	if member.GroupID != gid || member.UserID != uid { return nil, groupauthority.ErrInvalidScope }
	return &sdkws.PublicUserInfo{UserID: uid, Nickname: member.Nickname, FaceURL: member.FaceURL}, nil
}

func (s *groupServer) buildGroupMemberStatusNotice(snapshot *groupauthority.NoticeSnapshot, actorProfile, targetProfile *sdkws.PublicUserInfo, policy noticePolicy) (*sdkws.MsgData, error) {
	if snapshot == nil { return nil, groupauthority.ErrInvalidScope }
	command := snapshot.Command()
	mute, ok := snapshot.MemberMute()
	if !ok || actorProfile == nil || actorProfile.UserID != command.Actor || targetProfile == nil || targetProfile.UserID != mute.UserID { return nil, groupauthority.ErrInvalidScope }
	group := snapshot.Group()
	info := s.groupDB2PB(&group, snapshot.OwnerID(), snapshot.MemberCount())
	member, exists := snapshot.Member(command.Actor, true)
	var actor *sdkws.GroupMemberFullInfo
	if exists {
		actor = convert.Db2PbGroupMember(&member)
	} else if authverify.IsManagerUserID(command.Actor) {
		actor = &sdkws.GroupMemberFullInfo{GroupID: command.GroupID, UserID: command.Actor, RoleLevel: constant.GroupAdmin, AppMangerLevel: constant.AppAdmin}
	} else { return nil, groupauthority.ErrInvalidScope }
	if actor.Nickname == "" { actor.Nickname = actorProfile.Nickname }
	if actor.FaceURL == "" { actor.FaceURL = actorProfile.FaceURL }
	target, exists := snapshot.Member(mute.UserID, false)
	if !exists { return nil, groupauthority.ErrInvalidScope }
	muted := convert.Db2PbGroupMember(&target)
	if muted.Nickname == "" { muted.Nickname = targetProfile.Nickname }
	if muted.FaceURL == "" { muted.FaceURL = targetProfile.FaceURL }
	var tips any
	var contentType int32
	switch command.Action {
	case groupauthority.NoticeMuteMember:
		tips, contentType = &sdkws.GroupMemberMutedTips{Group: info, MutedSeconds: mute.Seconds, OpUser: actor, MutedUser: muted}, constant.GroupMemberMutedNotification
	case groupauthority.NoticeUnmuteMember:
		tips, contentType = &sdkws.GroupMemberCancelMutedTips{Group: info, OpUser: actor, MutedUser: muted}, constant.GroupMemberCancelMutedNotification
	default: return nil, groupauthority.ErrInvalidScope
	}
	content, err := json.Marshal(&sdkws.NotificationElem{Detail: utils.StructToJsonString(tips)})
	if err != nil { return nil, err }
	return &sdkws.MsgData{SendID: command.Actor, RecvID: command.GroupID, GroupID: command.GroupID, MsgFrom: constant.SysMsgType, ContentType: contentType, SessionType: constant.SuperGroupChatType, Content: content, CreateTime: command.CreatedAt.UnixMilli(), Options: config.GetOptionsByNotification(policy.notification), OfflinePushInfo: &sdkws.OfflinePushInfo{}}, nil
}

// The original member, duration and intent are captured before display I/O.
// Only that member's mute field and the mandatory notification are committed.
// Cache failure after a known commit never invites an SDK to replay the write.
func (s *groupServer) runGroupMemberStatusNoticeMutation(ctx context.Context, gid, uid string, seconds uint32, action groupauthority.NoticeAction, authorize func(context.Context) error) error {
	if s == nil || ctx == nil || authorize == nil || s.authority == nil || s.notices == nil || s.notices.db == nil || s.notices.effects == nil { return errs.ErrInternalServer.Wrap("group notification storage is not ready") }
	ctx, finish := context.WithTimeout(ctx, 30*time.Second)
	defer finish()
	policy, ok := s.notices.policies[action]
	if !ok { return errs.ErrInternalServer.Wrap("group notification policy is not ready") }
	commandCtx, commandInfo, err := groupauthority.NewMemberMuteCommand(ctx, s.notices.epoch, action, gid, uid, seconds)
	if err != nil { return err }
	actorProfile, err := s.noticeActorProfile(commandCtx, gid)
	if err != nil { return err }
	targetProfile, err := s.noticeTargetProfile(commandCtx, gid, uid)
	if err != nil { return err }
	ready, cancel := context.WithTimeout(commandCtx, 5*time.Second)
	err = s.notices.effects.CheckReady(ready)
	cancel()
	if err != nil { return errs.ErrInternalServer.Wrap("group notification storage is not ready") }
	var snapshot *groupauthority.NoticeSnapshot
	commit, err := s.authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: gid, Members: []string{uid}}}, func(tx context.Context) error {
		snapshot = nil
		if err := authorize(tx); err != nil { return err }
		var err error
		snapshot, err = groupauthority.BeginNotice(tx, s.notices.db, gid)
		return err
	}, func(tx context.Context) error {
		mute, ok := snapshot.MemberMute()
		if !ok || mute.UserID != uid || mute.Seconds != seconds { return groupauthority.ErrInvalidScope }
		if err := s.db.UpdateGroupMember(tx, gid, uid, UpdateGroupMemberMutedTimeMap(mute.Until)); err != nil { return err }
		if err := snapshot.Finish(tx, s.notices.db); err != nil { return err }
		message, err := s.buildGroupMemberStatusNotice(snapshot, actorProfile, targetProfile, policy)
		if err != nil { return err }
		_, err = s.notices.effects.QueueActionNotification(tx, snapshot, message, policy.delivery)
		return err
	})
	if err != nil {
		if errors.Is(err, groupauthority.ErrCommitUnconfirmed) { return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay") }
		return err
	}
	if commit == nil { return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay") }
	if s.invalidate == nil {
		log.ZWarn(commandCtx, "group projection recovery pending", nil, "commandID", commandInfo.ID, "action", action)
		return nil
	}
	flush, stop := context.WithTimeout(context.WithoutCancel(commandCtx), 5*time.Second)
	defer stop()
	if err := commit.Flush(flush, s.invalidate); err != nil {
		log.ZWarn(commandCtx, "group projection recovery pending", nil, "commandID", commandInfo.ID, "action", action)
	}
	return nil
}
