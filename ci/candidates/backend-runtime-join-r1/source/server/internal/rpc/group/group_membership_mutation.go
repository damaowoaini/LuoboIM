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
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
)

type membershipEffectQueue interface {
	roleEffectQueue
	QueueOrderedActionNotification(context.Context, *groupauthority.NoticeSnapshot, *sdkws.MsgData, groupeffects.ActionDeliveryPolicy) (groupeffects.Frozen, error)
	QueueMemberAdmissionNotification(context.Context, *groupauthority.MemberAdmissionScope, *sdkws.MsgData, groupeffects.ActionDeliveryPolicy) (groupeffects.Frozen, error)
	QueueMemberConversationProjections(context.Context, *groupauthority.MemberAdmissionScope, string) error
}

func (s *groupServer) membershipQueue() (membershipEffectQueue, error) {
	if s == nil || s.authority == nil || s.notices == nil || s.notices.db == nil || s.newGroups == nil {
		return nil, errs.ErrInternalServer.Wrap("group membership runtime unavailable")
	}
	q, ok := s.notices.effects.(membershipEffectQueue)
	if !ok {
		return nil, errs.ErrInternalServer.Wrap("group membership outbox unavailable")
	}
	return q, nil
}

func (s *groupServer) membershipCommitted(ctx context.Context, command groupauthority.NoticeCommandInfo, commit *groupauthority.Commit, err error) error {
	if errors.Is(err, groupauthority.ErrCommitUnconfirmed) {
		return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay")
	}
	if err != nil {
		return err
	}
	if commit == nil {
		return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay")
	}
	if s.invalidate != nil {
		flush, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		if commit.Flush(flush, s.invalidate) == nil {
			return nil
		}
	}
	// Business + membership boundaries + required effects are already durable.
	// A projection failure never turns that ACK into a replay invitation.
	log.ZWarn(ctx, "group membership projection recovery pending", nil, "commandID", command.ID, "action", command.Action)
	return nil
}

func (s *groupServer) queueMembershipAfter(ctx context.Context, q membershipEffectQueue, command groupauthority.NoticeCommandInfo, predecessor groupeffects.Frozen, name groupeffects.CallbackName, body any) error {
	turn := &roleQueueTurn{command: groupauthority.RoleCommandInfo{ID: command.ID, Epoch: command.Epoch, Actor: command.Actor, Action: string(command.Action), CreatedAt: command.CreatedAt}, previous: predecessor}
	return s.queueRoleCallback(ctx, q, turn, command.GroupID, name, 0, body)
}

func memberDisplay(member *sdkws.GroupMemberFullInfo, profile *sdkws.PublicUserInfo) *sdkws.GroupMemberFullInfo {
	if profile != nil && member != nil && member.UserID == profile.UserID {
		if member.Nickname == "" {
			member.Nickname = profile.Nickname
		}
		if member.FaceURL == "" {
			member.FaceURL = profile.FaceURL
		}
	}
	return member
}

func (s *groupServer) removalMessage(snapshot *groupauthority.NoticeSnapshot, removed []string, actor *sdkws.PublicUserInfo, targets map[string]*sdkws.PublicUserInfo, policy noticePolicy) (*sdkws.MsgData, error) {
	command := snapshot.Command()
	group := snapshot.Group()
	info := s.groupDB2PB(&group, snapshot.OwnerID(), snapshot.MemberCount())
	var op *sdkws.GroupMemberFullInfo
	if member, ok := snapshot.Member(command.Actor, true); ok {
		op = memberDisplay(convert.Db2PbGroupMember(&member), actor)
	} else if authverify.IsManagerUserID(command.Actor) {
		op = &sdkws.GroupMemberFullInfo{GroupID: command.GroupID, UserID: command.Actor, RoleLevel: constant.GroupAdmin, AppMangerLevel: constant.AppAdmin}
		op = memberDisplay(op, actor)
	} else {
		return nil, groupauthority.ErrInvalidScope
	}
	var tips any
	var kind int32
	switch command.Action {
	case groupauthority.NoticeKick:
		users := make([]*sdkws.GroupMemberFullInfo, 0, len(removed))
		for _, uid := range removed {
			member, ok := snapshot.Member(uid, true)
			if !ok {
				return nil, groupauthority.ErrInvalidScope
			}
			users = append(users, memberDisplay(convert.Db2PbGroupMember(&member), targets[uid]))
		}
		tips = &sdkws.MemberKickedTips{Group: info, OpUser: op, KickedUserList: users}
		kind = constant.MemberKickedNotification
	case groupauthority.NoticeQuit:
		if len(removed) != 1 {
			return nil, groupauthority.ErrInvalidScope
		}
		member, ok := snapshot.Member(removed[0], true)
		if !ok {
			return nil, groupauthority.ErrInvalidScope
		}
		tips = &sdkws.MemberQuitTips{Group: info, QuitUser: memberDisplay(convert.Db2PbGroupMember(&member), targets[removed[0]])}
		kind = constant.MemberQuitNotification
	default:
		return nil, groupauthority.ErrInvalidScope
	}
	body, err := json.Marshal(&sdkws.NotificationElem{Detail: utils.StructToJsonString(tips)})
	if err != nil {
		return nil, err
	}
	return &sdkws.MsgData{SendID: command.Actor, RecvID: command.GroupID, GroupID: command.GroupID, MsgFrom: constant.SysMsgType, ContentType: kind, SessionType: constant.SuperGroupChatType, Content: body, CreateTime: command.CreatedAt.UnixMilli(), Options: config.GetOptionsByNotification(policy.notification), OfflinePushInfo: &sdkws.OfflinePushInfo{}}, nil
}

func (s *groupServer) runMemberRemovalMutation(ctx context.Context, gid string, removed []string, action groupauthority.NoticeAction, authorize, write func(context.Context) error, afterName groupeffects.CallbackName, afterBody any) error {
	if ctx == nil || authorize == nil || write == nil {
		return groupauthority.ErrInvalidScope
	}
	q, err := s.membershipQueue()
	if err != nil {
		return err
	}
	policy, ok := s.notices.policies[action]
	if !ok {
		return errs.ErrInternalServer.Wrap("membership notification policy unavailable")
	}
	commandCtx, command, err := groupauthority.NewNoticeCommand(ctx, s.notices.epoch, action, gid)
	if err != nil {
		return err
	}
	actor, err := s.noticeActorProfile(commandCtx, gid)
	if err != nil {
		return err
	}
	profiles := map[string]*sdkws.PublicUserInfo{}
	for _, uid := range removed {
		p, err := s.noticeTargetProfile(commandCtx, gid, uid)
		if err != nil {
			return err
		}
		profiles[uid] = p
	}
	ready, stop := context.WithTimeout(commandCtx, 5*time.Second)
	err = s.notices.effects.CheckReady(ready)
	stop()
	if err != nil {
		return err
	}
	var snapshot *groupauthority.NoticeSnapshot
	commit, err := s.authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: gid, Members: append([]string(nil), removed...)}}, func(tx context.Context) error {
		snapshot = nil
		if err := authorize(tx); err != nil {
			return err
		}
		var err error
		snapshot, err = groupauthority.BeginNotice(tx, s.notices.db, gid)
		return err
	}, func(tx context.Context) error {
		if err := write(tx); err != nil {
			return err
		}
		if err := snapshot.Finish(tx, s.notices.db); err != nil {
			return err
		}
		cap, err := groupauthority.CompletedMemberRemoval(tx, s.notices.db, gid)
		if err != nil {
			return err
		}
		if _, err := s.newGroups.RecordMemberRemoval(tx, cap); err != nil {
			return err
		}
		message, err := s.removalMessage(snapshot, removed, actor, profiles, policy)
		if err != nil {
			return err
		}
		frozen, err := q.QueueOrderedActionNotification(tx, snapshot, message, policy.delivery)
		if err != nil {
			return err
		}
		// Root's storage successor reads this exact frozen row in THIS TX. It
		// binds the separate sg_/n_ heads and exact notification entitlement to
		// command/actor/group/removed set/policy. It never raises an upper bound
		// to a later asynchronously allocated notice sequence.
		if err := s.newGroups.RecordMemberNotificationPending(tx, cap, frozen.ID); err != nil {
			return err
		}
		return s.queueMembershipAfter(tx, q, command, frozen, afterName, afterBody)
	})
	return s.membershipCommitted(commandCtx, command, commit, err)
}

func (s *groupServer) admissionMessage(cap *groupauthority.MemberAdmissionScope, ids []string, kind groupauthority.MemberAdmissionKind, actor *sdkws.PublicUserInfo, profiles map[string]*sdkws.PublicUserInfo, policy noticePolicy) (*sdkws.MsgData, error) {
	command := cap.Command()
	group := cap.Group()
	info := s.groupDB2PB(&group, cap.OwnerID(), cap.MemberCount())
	var tips any
	var contentType int32
	if kind == groupauthority.MemberAdmissionInvite {
		var op *sdkws.GroupMemberFullInfo
		if member, ok := cap.Member(command.Actor); ok {
			op = memberDisplay(convert.Db2PbGroupMember(&member), actor)
		} else if authverify.IsManagerUserID(command.Actor) {
			op = memberDisplay(&sdkws.GroupMemberFullInfo{GroupID: command.GroupID, UserID: command.Actor, RoleLevel: constant.GroupAdmin, AppMangerLevel: constant.AppAdmin}, actor)
		} else {
			return nil, groupauthority.ErrInvalidScope
		}
		var members []*sdkws.GroupMemberFullInfo
		for _, uid := range ids {
			m, ok := cap.Member(uid)
			if !ok {
				return nil, groupauthority.ErrInvalidScope
			}
			members = append(members, memberDisplay(convert.Db2PbGroupMember(&m), profiles[uid]))
		}
		tips = &sdkws.MemberInvitedTips{Group: info, OpUser: op, InvitedUserList: members}
		contentType = constant.MemberInvitedNotification
	} else {
		if len(ids) != 1 {
			return nil, groupauthority.ErrInvalidScope
		}
		m, ok := cap.Member(ids[0])
		if !ok {
			return nil, groupauthority.ErrInvalidScope
		}
		tips = &sdkws.MemberEnterTips{Group: info, EntrantUser: memberDisplay(convert.Db2PbGroupMember(&m), profiles[ids[0]])}
		contentType = constant.MemberEnterNotification
	}
	body, err := json.Marshal(&sdkws.NotificationElem{Detail: utils.StructToJsonString(tips)})
	if err != nil {
		return nil, err
	}
	return &sdkws.MsgData{SendID: command.Actor, RecvID: command.GroupID, GroupID: command.GroupID, MsgFrom: constant.SysMsgType, ContentType: contentType, SessionType: constant.SuperGroupChatType, Content: body, CreateTime: command.CreatedAt.UnixMilli(), Options: config.GetOptionsByNotification(policy.notification), OfflinePushInfo: &sdkws.OfflinePushInfo{}}, nil
}

func (s *groupServer) runMemberAdmissionMutation(ctx context.Context, gid string, ids []string, kind groupauthority.MemberAdmissionKind, authorize, write func(context.Context) error, afterName groupeffects.CallbackName, afterBody any) error {
	if ctx == nil || authorize == nil || write == nil {
		return groupauthority.ErrInvalidScope
	}
	q, err := s.membershipQueue()
	if err != nil {
		return err
	}
	policy, ok := s.notices.policies[groupauthority.NoticeAction(kind)]
	if !ok {
		return errs.ErrInternalServer.Wrap("membership notification policy unavailable")
	}
	// This value comes from the existing server-owned Redis history policy,
	// never a client field. The storage successor preserves/maxes old floors.
	forbidHistory, err := s.forbidNewMemberHistoryEnabled(ctx)
	if err != nil {
		return err
	}
	commandCtx, command, err := groupauthority.NewMemberAdmissionCommand(ctx, s.notices.epoch, gid, ids, kind)
	if err != nil {
		return err
	}
	var actor *sdkws.PublicUserInfo
	if kind == groupauthority.MemberAdmissionInvite {
		actor, err = s.noticeActorProfile(commandCtx, gid)
		if err != nil {
			return err
		}
	}
	users, err := s.User.GetUsersInfoMap(commandCtx, ids)
	if err != nil {
		return err
	}
	if len(users) != len(ids) {
		return groupauthority.ErrInvalidScope
	}
	profiles := map[string]*sdkws.PublicUserInfo{}
	for _, uid := range ids {
		user := users[uid]
		if user == nil || user.UserID != uid {
			return groupauthority.ErrInvalidScope
		}
		profiles[uid] = &sdkws.PublicUserInfo{UserID: uid, Nickname: user.Nickname, FaceURL: user.FaceURL}
	}
	var cap *groupauthority.MemberAdmissionScope
	commit, err := s.authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: gid, Members: append([]string(nil), ids...)}}, func(tx context.Context) error {
		cap = nil
		if err := authorize(tx); err != nil {
			return err
		}
		var err error
		cap, err = groupauthority.BeginMemberAdmission(tx, s.notices.db, gid)
		return err
	}, func(tx context.Context) error {
		if err := write(tx); err != nil {
			return err
		}
		if err := cap.Complete(tx, s.notices.db); err != nil {
			return err
		}
		if err := s.newGroups.RecordMemberAdmission(tx, cap, forbidHistory); err != nil {
			return err
		}
		info, err := cap.Inspect(tx, s.notices.db)
		if err != nil {
			return err
		}
		if len(info.NewMembers) == 0 {
			return nil
		} // Existing membership never renews a window or emits a false join.
		if policy.delivery.Owned == nil {
			return groupeffects.ErrUnavailable
		}
		if err := q.QueueMemberConversationProjections(tx, cap, policy.delivery.Owned.Transport.ActivationSHA256); err != nil {
			return err
		}
		message, err := s.admissionMessage(cap, ids, kind, actor, profiles, policy)
		if err != nil {
			return err
		}
		frozen, err := q.QueueMemberAdmissionNotification(tx, cap, message, policy.delivery)
		if err != nil {
			return err
		}
		if afterName != "" {
			return s.queueMembershipAfter(tx, q, command, frozen, afterName, afterBody)
		}
		return nil
	})
	return s.membershipCommitted(commandCtx, command, commit, err)
}

func (s *groupServer) runMemberApplicationMutation(ctx context.Context, gid, uid string, kind groupauthority.MemberApplicationKind, admitting bool, authorize, write func(context.Context) error) error {
	return s.runMemberRequestMutation(ctx, gid, []string{uid}, kind, admitting, authorize, write)
}
