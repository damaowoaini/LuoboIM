// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package group

import (
	"context"
	"fmt"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"

	pbconversation "github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/wrapperspb"

	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/riskcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"

	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"

	"github.com/OpenIMSDK/tools/mw/specialerror"

	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/redis/go-redis/v9"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	relationtb "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

type groupServer struct {
	db                    controller.GroupDatabase
	authority             groupMutationRunner
	invalidate            func(context.Context, string) error
	notices               *groupNoticeRuntime
	User                  rpcclient.UserRpcClient
	Notification          *notification.GroupNotificationSender
	conversationRpcClient rpcclient.ConversationRpcClient
	msgRpcClient          rpcclient.MessageRpcClient
	rdb                   redis.UniversalClient
}

func (s *groupServer) NotificationUserInfoUpdate(ctx context.Context, req *pbgroup.NotificationUserInfoUpdateReq) (*pbgroup.NotificationUserInfoUpdateResp, error) {
	defer log.ZDebug(ctx, "return")
	members, err := s.db.FindGroupMemberUser(ctx, nil, req.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	groupIDs := make([]string, 0, len(members))
	for _, member := range members {
		if member.Nickname != "" && member.FaceURL != "" {
			continue
		}
		groupIDs = append(groupIDs, member.GroupID)
	}
	log.ZInfo(ctx, "NotificationUserInfoUpdate", "joinGroupNum", len(members), "updateNum", len(groupIDs), "updateGroupIDs", groupIDs)
	for _, groupID := range groupIDs {
		if err := s.Notification.GroupMemberInfoSetNotification(ctx, groupID, req.UserID); err != nil {
			log.ZError(ctx, "NotificationUserInfoUpdate setGroupMemberInfo notification failed", err, "groupID", groupID)
		}
	}
	if err := s.db.DeleteGroupMemberHash(ctx, groupIDs); err != nil {
		log.ZError(ctx, "NotificationUserInfoUpdate DeleteGroupMemberHash", err, "groupID", groupIDs)
	}

	return &pbgroup.NotificationUserInfoUpdateResp{}, nil
}

func (s *groupServer) CheckGroupAdmin(ctx context.Context, groupID string) error {
	if !authverify.IsAppManagerUid(ctx) {
		groupMember, err := s.db.TakeGroupMember(ctx, groupID, mcontext.GetOpUserID(ctx))
		if err != nil {
			return err
		}
		if !(groupMember.RoleLevel == constant.GroupOwner || groupMember.RoleLevel == constant.GroupAdmin) {
			return errs.ErrNoPermission.Wrap("no group owner or admin")
		}
	}
	return nil
}

// checkGroupMemberOrAdmin 管理员可看任意群；普通用户必须是群成员。
// opUserID 为空视为内部 RPC（msg/cache），与官方 patch 16 一致，避免 @ 消息等内部调用失败。
func (s *groupServer) checkGroupMemberOrAdmin(ctx context.Context, groupID string) error {
	if authverify.IsAppManagerUid(ctx) {
		return nil
	}
	opUserID := mcontext.GetOpUserID(ctx)
	if opUserID == "" {
		return nil
	}
	if _, err := s.db.TakeGroupMember(ctx, groupID, opUserID); err != nil {
		if s.IsNotFound(err) {
			return errs.ErrNoPermission.Wrap("not group member")
		}
		return err
	}
	return nil
}

func (s *groupServer) GetPublicUserInfoMap(ctx context.Context, userIDs []string, complete bool) (map[string]*sdkws.PublicUserInfo, error) {
	if len(userIDs) == 0 {
		return map[string]*sdkws.PublicUserInfo{}, nil
	}
	users, err := s.User.GetPublicUserInfos(ctx, userIDs, complete)
	if err != nil {
		return nil, err
	}
	return utils.SliceToMapAny(users, func(e *sdkws.PublicUserInfo) (string, *sdkws.PublicUserInfo) {
		return e.UserID, e
	}), nil
}

func (s *groupServer) IsNotFound(err error) bool {
	return errs.ErrRecordNotFound.Is(specialerror.ErrCode(errs.Unwrap(err)))
}

func (s *groupServer) GenGroupID(ctx context.Context, groupID *string) error {
	if *groupID != "" {
		_, err := s.db.TakeGroup(ctx, *groupID)
		if err == nil {
			return errs.ErrGroupIDExisted.Wrap("group id existed " + *groupID)
		} else if s.IsNotFound(err) {
			return nil
		} else {
			return err
		}
	}
	for i := 0; i < 10; i++ {
		id := utils.Md5(strings.Join([]string{mcontext.GetOperationID(ctx), strconv.FormatInt(time.Now().UnixNano(), 10), strconv.Itoa(rand.Int())}, ",;,"))
		bi := big.NewInt(0)
		bi.SetString(id[0:8], 16)
		id = bi.String()
		_, err := s.db.TakeGroup(ctx, id)
		if err == nil {
			continue
		} else if s.IsNotFound(err) {
			*groupID = id
			return nil
		} else {
			return err
		}
	}
	return errs.ErrData.Wrap("group id gen error")
}

func (s *groupServer) CreateGroup(ctx context.Context, req *pbgroup.CreateGroupReq) (*pbgroup.CreateGroupResp, error) {
	if req == nil || req.GroupInfo == nil {
		return nil, errs.ErrArgs.Wrap("group info required")
	}
	if req.GroupInfo.GroupType != constant.WorkingGroup {
		return nil, errs.ErrArgs.Wrap(fmt.Sprintf("group type only supports %d", constant.WorkingGroup))
	}
	if req.OwnerUserID == "" {
		return nil, errs.ErrArgs.Wrap("no group owner")
	}
	if err := authverify.CheckAccessV3(ctx, req.OwnerUserID); err != nil {
		return nil, err
	}
	createKeys := func() []string {
		keys := []string{req.OwnerUserID, req.GroupInfo.CreatorUserID, strconv.Itoa(int(req.GroupInfo.GroupType)), strconv.Itoa(len(req.AdminUserIDs))}
		keys = append(keys, req.AdminUserIDs...)
		keys = append(keys, strconv.Itoa(len(req.MemberUserIDs)))
		return append(keys, req.MemberUserIDs...)
	}
	identity, err := captureGroupIntent(ctx, createKeys()...)
	if err != nil {
		return nil, err
	}
	originalGroupID := req.GroupInfo.GroupID
	userIDs := append(append(append([]string(nil), req.MemberUserIDs...), req.AdminUserIDs...), req.OwnerUserID)
	opUserID := mcontext.GetOpUserID(ctx)
	if !utils.Contain(opUserID, userIDs...) {
		userIDs = append(userIDs, opUserID)
	}
	if utils.Duplicate(userIDs) {
		return nil, errs.ErrArgs.Wrap("group member repeated")
	}
	userMap, err := s.User.GetUsersInfoMap(ctx, userIDs)
	if err != nil {
		return nil, err
	}
	if len(userMap) != len(userIDs) {
		return nil, errs.ErrUserIDNotFound.Wrap("user not found")
	}
	if blocked, err := riskcontrol.IsActionBlocked(ctx, riskcontrol.RiskDisableGroupActionKey); err != nil {
		return nil, err
	} else if blocked {
		return nil, errs.ErrNoPermission.Wrap("当前时段禁止群操作")
	}
	// Callback Before create Group
	if err := CallbackBeforeCreateGroup(ctx, req); err != nil {
		return nil, err
	}
	if err := identity.check(ctx, createKeys()...); err != nil {
		return nil, err
	}
	if originalGroupID != "" && req.GroupInfo.GroupID != originalGroupID {
		return nil, errs.ErrNoPermission.Wrap("create group identity changed")
	}
	if req.GroupInfo.GroupType != constant.WorkingGroup {
		return nil, errs.ErrArgs.Wrap("group type changed")
	}
	var groupMembers []*relationtb.GroupMemberModel
	group := convert.Pb2DBGroupInfo(req.GroupInfo)
	if err := s.GenGroupID(ctx, &group.GroupID); err != nil {
		return nil, err
	}
	joinGroup := func(userID string, roleLevel int32) error {
		groupMember := &relationtb.GroupMemberModel{
			GroupID:        group.GroupID,
			UserID:         userID,
			RoleLevel:      roleLevel,
			OperatorUserID: opUserID,
			JoinSource:     constant.JoinByInvitation,
			InviterUserID:  opUserID,
			JoinTime:       time.Now(),
			MuteEndTime:    time.UnixMilli(0),
		}
		if err := CallbackBeforeMemberJoinGroup(ctx, groupMember, group.Ex); err != nil {
			return err
		}
		groupMembers = append(groupMembers, groupMember)
		return nil
	}
	if err := joinGroup(req.OwnerUserID, constant.GroupOwner); err != nil {
		return nil, err
	}
	for _, userID := range req.AdminUserIDs {
		if err := joinGroup(userID, constant.GroupAdmin); err != nil {
			return nil, err
		}
	}
	for _, userID := range req.MemberUserIDs {
		if err := joinGroup(userID, constant.GroupOrdinaryUsers); err != nil {
			return nil, err
		}
	}
	fixedGroupID := group.GroupID
	memberIDs := newMemberIDs(groupMembers)
	if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: fixedGroupID, Create: true, Members: memberIDs}}, func(tx context.Context) error {
		if err := identity.check(tx, createKeys()...); err != nil {
			return err
		}
		if group.GroupID != fixedGroupID {
			return errs.ErrNoPermission.Wrap("group identity changed")
		}
		if err := authverify.CheckAccessV3(tx, req.OwnerUserID); err != nil {
			return err
		}
		return authorizeNewMemberRoles(groupMembers, fixedGroupID, true, req.OwnerUserID)
	}, func(tx context.Context) error {
		return s.db.CreateGroup(tx, []*relationtb.GroupModel{group}, groupMembers)
	}); err != nil {
		return nil, err
	}
	resp := &pbgroup.CreateGroupResp{GroupInfo: &sdkws.GroupInfo{}}
	resp.GroupInfo = convert.Db2PbGroupInfo(group, req.OwnerUserID, uint32(len(userIDs)))
	resp.GroupInfo.MemberCount = uint32(len(userIDs))
	tips := &sdkws.GroupCreatedTips{
		Group:          resp.GroupInfo,
		OperationTime:  group.CreateTime.UnixMilli(),
		GroupOwnerUser: s.groupMemberDB2PB(groupMembers[0], userMap[groupMembers[0].UserID].AppMangerLevel),
	}
	for _, member := range groupMembers {
		member.Nickname = userMap[member.UserID].Nickname
		tips.MemberList = append(tips.MemberList, s.groupMemberDB2PB(member, userMap[member.UserID].AppMangerLevel))
		if member.UserID == opUserID {
			tips.OpUser = s.groupMemberDB2PB(member, userMap[member.UserID].AppMangerLevel)
			break
		}
	}
	if req.GroupInfo.GroupType == constant.SuperGroup {
		go func() {
			for _, userID := range userIDs {
				s.Notification.SuperGroupNotification(ctx, userID, userID)
			}
		}()
	} else {
		// s.Notification.GroupCreatedNotification(ctx, group, groupMembers, userMap)
		tips := &sdkws.GroupCreatedTips{
			Group:          resp.GroupInfo,
			OperationTime:  group.CreateTime.UnixMilli(),
			GroupOwnerUser: s.groupMemberDB2PB(groupMembers[0], userMap[groupMembers[0].UserID].AppMangerLevel),
		}
		for _, member := range groupMembers {
			member.Nickname = userMap[member.UserID].Nickname
			tips.MemberList = append(tips.MemberList, s.groupMemberDB2PB(member, userMap[member.UserID].AppMangerLevel))
			if member.UserID == opUserID {
				tips.OpUser = s.groupMemberDB2PB(member, userMap[member.UserID].AppMangerLevel)
				break
			}
		}
		s.Notification.GroupCreatedNotification(ctx, tips)
	}
	reqCallBackAfter := &pbgroup.CreateGroupReq{
		MemberUserIDs: userIDs,
		GroupInfo:     resp.GroupInfo,
		OwnerUserID:   req.OwnerUserID,
		AdminUserIDs:  req.AdminUserIDs,
	}

	if err := CallbackAfterCreateGroup(ctx, reqCallBackAfter); err != nil {
		return nil, err
	}

	return resp, nil
}

func (s *groupServer) GetJoinedGroupList(ctx context.Context, req *pbgroup.GetJoinedGroupListReq) (*pbgroup.GetJoinedGroupListResp, error) {
	resp := &pbgroup.GetJoinedGroupListResp{}
	if err := authverify.CheckAccessV3(ctx, req.FromUserID); err != nil {
		return nil, err
	}
	total, members, err := s.db.PageGetJoinGroup(ctx, req.FromUserID, req.Pagination)
	if err != nil {
		return nil, err
	}
	resp.Total = uint32(total)
	if len(members) == 0 {
		return resp, nil
	}
	groupIDs := utils.Slice(members, func(e *relationtb.GroupMemberModel) string {
		return e.GroupID
	})
	groups, err := s.db.FindGroup(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	groupMemberNum, err := s.db.MapGroupMemberNum(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	owners, err := s.db.FindGroupsOwner(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	ownerMap := utils.SliceToMap(owners, func(e *relationtb.GroupMemberModel) string {
		return e.GroupID
	})
	resp.Groups = utils.Slice(utils.Order(groupIDs, groups, func(group *relationtb.GroupModel) string {
		return group.GroupID
	}), func(group *relationtb.GroupModel) *sdkws.GroupInfo {
		var userID string
		if user := ownerMap[group.GroupID]; user != nil {
			userID = user.UserID
		}
		return convert.Db2PbGroupInfo(group, userID, groupMemberNum[group.GroupID])
	})
	return resp, nil
}

func (s *groupServer) InviteUserToGroup(ctx context.Context, req *pbgroup.InviteUserToGroupReq) (*pbgroup.InviteUserToGroupResp, error) {
	resp := &pbgroup.InviteUserToGroupResp{}
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	identity, err := captureGroupIntent(ctx, append([]string{req.GroupID}, req.InvitedUserIDs...)...)
	if err != nil {
		return nil, err
	}

	if len(req.InvitedUserIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("user empty")
	}
	if utils.Duplicate(req.InvitedUserIDs) {
		return nil, errs.ErrArgs.Wrap("userID duplicate")
	}
	group, err := s.freshGroup(ctx, req.GroupID, true)
	if err != nil {
		return nil, err
	}

	if group.Status == constant.GroupStatusDismissed {
		return nil, errs.ErrDismissedAlready.Wrap()
	}
	userMap, err := s.User.GetUsersInfoMap(ctx, req.InvitedUserIDs)
	if err != nil {
		return nil, err
	}
	if len(userMap) != len(req.InvitedUserIDs) {
		return nil, errs.ErrRecordNotFound.Wrap("user not found")
	}
	var groupMember *relationtb.GroupMemberModel
	var opUserID string
	if !authverify.IsAppManagerUid(ctx) {
		opUserID = mcontext.GetOpUserID(ctx)
		var err error
		groupMember, err = s.freshMember(ctx, req.GroupID, opUserID)
		if err != nil {
			return nil, err
		}
		if err := s.PopulateGroupMember(ctx, groupMember); err != nil {
			return nil, err
		}
	}

	if err := CallbackBeforeInviteUserToGroup(ctx, req); err != nil {
		return nil, err
	}
	if err := identity.check(ctx, append([]string{req.GroupID}, req.InvitedUserIDs...)...); err != nil {
		return nil, err
	}
	if group.NeedVerification == constant.AllNeedVerification {
		if !authverify.IsAppManagerUid(ctx) {
			if !(groupMember.RoleLevel == constant.GroupOwner || groupMember.RoleLevel == constant.GroupAdmin) {
				var requests []*relationtb.GroupRequestModel
				for _, userID := range req.InvitedUserIDs {
					requests = append(requests, &relationtb.GroupRequestModel{
						UserID:        userID,
						GroupID:       req.GroupID,
						JoinSource:    constant.JoinByInvitation,
						InviterUserID: opUserID,
						ReqTime:       time.Now(),
						HandledTime:   time.Unix(0, 0),
					})
				}
				if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: req.GroupID, Members: req.InvitedUserIDs}}, func(tx context.Context) error {
					if err := identity.check(tx, append([]string{req.GroupID}, req.InvitedUserIDs...)...); err != nil {
						return err
					}
					request, _, err := s.freshInviteMode(tx, req.GroupID)
					if err != nil {
						return err
					}
					if !request {
						return groupauthority.ErrConflict
					}
					return nil
				}, func(tx context.Context) error { return s.db.CreateGroupRequest(tx, requests) }); err != nil {
					return nil, err
				}
				for _, request := range requests {
					s.Notification.JoinGroupApplicationNotification(ctx, &pbgroup.JoinGroupReq{
						GroupID:       request.GroupID,
						ReqMessage:    request.ReqMsg,
						JoinSource:    request.JoinSource,
						InviterUserID: request.InviterUserID,
					})
				}
				return resp, nil
			}
		}
	}
	if err := s.restrictNewMembersHistory(ctx, req.GroupID, req.InvitedUserIDs); err != nil {
		return nil, err
	}
	var groupMembers []*relationtb.GroupMemberModel
	for _, userID := range req.InvitedUserIDs {
		member := &relationtb.GroupMemberModel{
			GroupID:        req.GroupID,
			UserID:         userID,
			RoleLevel:      constant.GroupOrdinaryUsers,
			OperatorUserID: opUserID,
			InviterUserID:  opUserID,
			JoinSource:     constant.JoinByInvitation,
			JoinTime:       time.Now(),
			MuteEndTime:    time.UnixMilli(0),
		}
		if err := CallbackBeforeMemberJoinGroup(ctx, member, group.Ex); err != nil {
			return nil, err
		}
		groupMembers = append(groupMembers, member)
	}
	if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: req.GroupID, Members: req.InvitedUserIDs}}, func(tx context.Context) error {
		if err := identity.check(tx, append([]string{req.GroupID}, req.InvitedUserIDs...)...); err != nil {
			return err
		}
		request, mayAssign, err := s.freshInviteMode(tx, req.GroupID)
		if err != nil {
			return err
		}
		if request {
			return groupauthority.ErrConflict
		}
		return authorizeNewMemberRoles(groupMembers, req.GroupID, mayAssign, "")
	}, func(tx context.Context) error { return s.db.CreateGroup(tx, nil, groupMembers) }); err != nil {
		return nil, err
	}
	// 成员写入后再刷新一次边界，关闭“首次设置边界到成员正式入群”之间并发消息的时间窗。
	if err := s.restrictNewMembersHistory(ctx, req.GroupID, req.InvitedUserIDs); err != nil {
		return nil, err
	}
	if err := s.conversationRpcClient.GroupChatFirstCreateConversation(ctx, req.GroupID, req.InvitedUserIDs); err != nil {
		return nil, err
	}
	s.Notification.MemberInvitedNotification(ctx, req.GroupID, req.Reason, req.InvitedUserIDs)
	return resp, nil
}

func (s *groupServer) GetGroupAllMember(ctx context.Context, req *pbgroup.GetGroupAllMemberReq) (*pbgroup.GetGroupAllMemberResp, error) {
	if err := s.checkGroupMemberOrAdmin(ctx, req.GroupID); err != nil {
		return nil, err
	}
	members, err := s.db.FindGroupMemberAll(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	resp := &pbgroup.GetGroupAllMemberResp{}
	resp.Members = utils.Slice(members, func(e *relationtb.GroupMemberModel) *sdkws.GroupMemberFullInfo {
		return convert.Db2PbGroupMember(e)
	})
	return resp, nil
}

func (s *groupServer) GetGroupMemberList(ctx context.Context, req *pbgroup.GetGroupMemberListReq) (*pbgroup.GetGroupMemberListResp, error) {
	if err := s.checkGroupMemberOrAdmin(ctx, req.GroupID); err != nil {
		return nil, err
	}
	resp := &pbgroup.GetGroupMemberListResp{}
	var (
		total   int64
		members []*relationtb.GroupMemberModel
		err     error
	)
	if req.Keyword == "" {
		total, members, err = s.db.PageGetGroupMember(ctx, req.GroupID, req.Pagination)
	} else {
		// Match the ID or effective nickname in the database. Only this page
		// needs profile/avatar hydration, including the empty group-card fallback.
		total, members, err = s.db.SearchGroupMemberExact(ctx, req.GroupID, req.Keyword, req.Pagination)
	}
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	resp.Total = uint32(total)
	resp.Members = utils.Batch(convert.Db2PbGroupMember, members)
	return resp, nil
}

func (s *groupServer) KickGroupMember(ctx context.Context, req *pbgroup.KickGroupMemberReq) (*pbgroup.KickGroupMemberResp, error) {
	resp := &pbgroup.KickGroupMemberResp{}
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	identity, err := captureGroupIntent(ctx, append([]string{req.GroupID}, req.KickedUserIDs...)...)
	if err != nil {
		return nil, err
	}
	group, err := s.freshGroup(ctx, req.GroupID, true)
	if err != nil {
		return nil, err
	}
	if len(req.KickedUserIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("KickedUserIDs empty")
	}
	if utils.IsDuplicateStringSlice(req.KickedUserIDs) {
		return nil, errs.ErrArgs.Wrap("KickedUserIDs duplicate")
	}
	opUserID := mcontext.GetOpUserID(ctx)
	if utils.IsContain(opUserID, req.KickedUserIDs) {
		return nil, errs.ErrArgs.Wrap("opUserID in KickedUserIDs")
	}
	memberMap, err := s.freshMembers(ctx, req.GroupID, append(append([]string(nil), req.KickedUserIDs...), opUserID))
	if err != nil {
		return nil, err
	}
	members := make([]*relationtb.GroupMemberModel, 0, len(memberMap))
	for _, member := range memberMap {
		members = append(members, member)
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	isAppManagerUid := authverify.IsAppManagerUid(ctx)
	opMember := memberMap[opUserID]
	for _, userID := range req.KickedUserIDs {
		member, ok := memberMap[userID]
		if !ok {
			return nil, errs.ErrUserIDNotFound.Wrap(userID)
		}
		if !isAppManagerUid {
			if opMember == nil {
				return nil, errs.ErrNoPermission.Wrap("opUserID no in group")
			}
			switch opMember.RoleLevel {
			case constant.GroupOwner:
			case constant.GroupAdmin:
				if member.RoleLevel == constant.GroupOwner || member.RoleLevel == constant.GroupAdmin {
					return nil, errs.ErrNoPermission.Wrap("group admins cannot remove the group owner and other admins")
				}
			case constant.GroupOrdinaryUsers:
				return nil, errs.ErrNoPermission.Wrap("opUserID no permission")
			default:
				return nil, errs.ErrNoPermission.Wrap("opUserID roleLevel unknown")
			}
		}
	}
	num, err := s.db.FindGroupMemberNum(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	ownerUserIDs, err := s.db.GetGroupRoleLevelMemberIDs(ctx, req.GroupID, constant.GroupOwner)
	if err != nil {
		return nil, err
	}
	var ownerUserID string
	if len(ownerUserIDs) > 0 {
		ownerUserID = ownerUserIDs[0]
	}
	if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: group.GroupID, Members: req.KickedUserIDs}}, func(tx context.Context) error {
		if err := identity.check(tx, append([]string{req.GroupID}, req.KickedUserIDs...)...); err != nil {
			return err
		}
		return s.authorizeKick(tx, req.GroupID, req.KickedUserIDs)
	}, func(tx context.Context) error { return s.db.DeleteGroupMember(tx, group.GroupID, req.KickedUserIDs) }); err != nil {
		return nil, err
	}
	tips := &sdkws.MemberKickedTips{
		Group: &sdkws.GroupInfo{
			GroupID:                group.GroupID,
			GroupName:              group.GroupName,
			Notification:           group.Notification,
			Introduction:           group.Introduction,
			FaceURL:                group.FaceURL,
			OwnerUserID:            ownerUserID,
			CreateTime:             group.CreateTime.UnixMilli(),
			MemberCount:            num,
			Ex:                     group.Ex,
			Status:                 group.Status,
			CreatorUserID:          group.CreatorUserID,
			GroupType:              group.GroupType,
			NeedVerification:       group.NeedVerification,
			LookMemberInfo:         group.LookMemberInfo,
			ApplyMemberFriend:      group.ApplyMemberFriend,
			NotificationUpdateTime: group.NotificationUpdateTime.UnixMilli(),
			NotificationUserID:     group.NotificationUserID,
		},
		KickedUserList: []*sdkws.GroupMemberFullInfo{},
	}
	if opMember, ok := memberMap[opUserID]; ok {
		tips.OpUser = convert.Db2PbGroupMember(opMember)
	}
	for _, userID := range req.KickedUserIDs {
		tips.KickedUserList = append(tips.KickedUserList, convert.Db2PbGroupMember(memberMap[userID]))
	}
	s.Notification.MemberKickedNotification(ctx, tips)
	if err := s.deleteMemberAndSetConversationSeq(ctx, req.GroupID, req.KickedUserIDs); err != nil {
		return nil, err
	}

	if err := CallbackKillGroupMember(ctx, req); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *groupServer) GetGroupMembersInfo(ctx context.Context, req *pbgroup.GetGroupMembersInfoReq) (*pbgroup.GetGroupMembersInfoResp, error) {
	resp := &pbgroup.GetGroupMembersInfoResp{}
	if len(req.UserIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("userIDs empty")
	}
	if req.GroupID == "" {
		return nil, errs.ErrArgs.Wrap("groupID empty")
	}
	if err := s.checkGroupMemberOrAdmin(ctx, req.GroupID); err != nil {
		return nil, err
	}
	members, err := s.db.FindGroupMembers(ctx, req.GroupID, req.UserIDs)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	resp.Members = utils.Slice(members, func(e *relationtb.GroupMemberModel) *sdkws.GroupMemberFullInfo {
		return convert.Db2PbGroupMember(e)
	})
	return resp, nil
}

func (s *groupServer) GetGroupApplicationList(ctx context.Context, req *pbgroup.GetGroupApplicationListReq) (*pbgroup.GetGroupApplicationListResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.FromUserID); err != nil {
		return nil, err
	}
	groupIDs, err := s.db.FindUserManagedGroupID(ctx, req.FromUserID)
	if err != nil {
		return nil, err
	}
	resp := &pbgroup.GetGroupApplicationListResp{}
	if len(groupIDs) == 0 {
		return resp, nil
	}
	total, groupRequests, err := s.db.PageGroupRequest(ctx, groupIDs, req.Pagination)
	if err != nil {
		return nil, err
	}
	resp.Total = uint32(total)
	if len(groupRequests) == 0 {
		return resp, nil
	}
	var userIDs []string

	for _, gr := range groupRequests {
		userIDs = append(userIDs, gr.UserID)
	}
	userIDs = utils.Distinct(userIDs)
	userMap, err := s.User.GetPublicUserInfoMap(ctx, userIDs, true)
	if err != nil {
		return nil, err
	}
	groups, err := s.db.FindGroup(ctx, utils.Distinct(groupIDs))
	if err != nil {
		return nil, err
	}
	groupMap := utils.SliceToMap(groups, func(e *relationtb.GroupModel) string {
		return e.GroupID
	})
	if ids := utils.Single(utils.Keys(groupMap), groupIDs); len(ids) > 0 {
		return nil, errs.ErrGroupIDNotFound.Wrap(strings.Join(ids, ","))
	}
	groupMemberNumMap, err := s.db.MapGroupMemberNum(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	owners, err := s.db.FindGroupsOwner(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, owners...); err != nil {
		return nil, err
	}
	ownerMap := utils.SliceToMap(owners, func(e *relationtb.GroupMemberModel) string {
		return e.GroupID
	})
	resp.GroupRequests = utils.Slice(groupRequests, func(e *relationtb.GroupRequestModel) *sdkws.GroupRequest {
		var ownerUserID string
		if owner, ok := ownerMap[e.GroupID]; ok {
			ownerUserID = owner.UserID
		}
		return convert.Db2PbGroupRequest(e, userMap[e.UserID], convert.Db2PbGroupInfo(groupMap[e.GroupID], ownerUserID, groupMemberNumMap[e.GroupID]))
	})
	return resp, nil
}

func (s *groupServer) GetGroupsInfo(ctx context.Context, req *pbgroup.GetGroupsInfoReq) (*pbgroup.GetGroupsInfoResp, error) {
	resp := &pbgroup.GetGroupsInfoResp{}
	if len(req.GroupIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("groupID is empty")
	}
	groups, err := s.db.FindGroup(ctx, req.GroupIDs)
	if err != nil {
		return nil, err
	}
	groupMemberNumMap, err := s.db.MapGroupMemberNum(ctx, req.GroupIDs)
	if err != nil {
		return nil, err
	}
	owners, err := s.db.FindGroupsOwner(ctx, req.GroupIDs)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, owners...); err != nil {
		return nil, err
	}
	ownerMap := utils.SliceToMap(owners, func(e *relationtb.GroupMemberModel) string {
		return e.GroupID
	})
	resp.GroupInfos = utils.Slice(groups, func(e *relationtb.GroupModel) *sdkws.GroupInfo {
		var ownerUserID string
		if owner, ok := ownerMap[e.GroupID]; ok {
			ownerUserID = owner.UserID
		}
		return convert.Db2PbGroupInfo(e, ownerUserID, groupMemberNumMap[e.GroupID])
	})
	return resp, nil
}

func (s *groupServer) GroupApplicationResponse(ctx context.Context, req *pbgroup.GroupApplicationResponseReq) (*pbgroup.GroupApplicationResponseResp, error) {
	defer log.ZInfo(ctx, utils.GetFuncName()+" Return")
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	identity, err := captureGroupIntent(ctx, req.GroupID, req.FromUserID, strconv.Itoa(int(req.HandleResult)))
	if err != nil {
		return nil, err
	}
	if !utils.Contain(req.HandleResult, constant.GroupResponseAgree, constant.GroupResponseRefuse) {
		return nil, errs.ErrArgs.Wrap("HandleResult unknown")
	}
	if !authverify.IsAppManagerUid(ctx) {
		groupMember, err := s.freshMember(ctx, req.GroupID, mcontext.GetOpUserID(ctx))
		if err != nil {
			return nil, err
		}
		if !(groupMember.RoleLevel == constant.GroupOwner || groupMember.RoleLevel == constant.GroupAdmin) {
			return nil, errs.ErrNoPermission.Wrap("no group owner or admin")
		}
	}
	group, err := s.freshGroup(ctx, req.GroupID, true)
	if err != nil {
		return nil, err
	}
	groupRequest, err := s.db.TakeGroupRequest(ctx, req.GroupID, req.FromUserID)
	if err != nil {
		return nil, err
	}
	if groupRequest == nil || groupRequest.GroupID != req.GroupID || groupRequest.UserID != req.FromUserID {
		return nil, errs.ErrNoPermission.Wrap("invalid group request")
	}
	if groupRequest.HandleResult != 0 {
		return nil, errs.ErrGroupRequestHandled.Wrap("group request already processed")
	}
	var inGroup bool
	if _, err := s.freshMember(ctx, req.GroupID, req.FromUserID); err == nil {
		inGroup = true // 已经在群里了
	} else if !s.IsNotFound(err) {
		return nil, err
	}
	if _, err := s.User.GetPublicUserInfo(ctx, req.FromUserID); err != nil {
		return nil, err
	}
	var member *relationtb.GroupMemberModel
	if (!inGroup) && req.HandleResult == constant.GroupResponseAgree {
		if err := s.restrictNewMembersHistory(ctx, req.GroupID, []string{req.FromUserID}); err != nil {
			return nil, err
		}
		member = &relationtb.GroupMemberModel{
			GroupID:        req.GroupID,
			UserID:         req.FromUserID,
			Nickname:       "",
			FaceURL:        "",
			RoleLevel:      constant.GroupOrdinaryUsers,
			JoinTime:       time.Now(),
			JoinSource:     groupRequest.JoinSource,
			MuteEndTime:    time.Unix(0, 0),
			InviterUserID:  groupRequest.InviterUserID,
			OperatorUserID: mcontext.GetOpUserID(ctx),
			Ex:             groupRequest.Ex,
		}
		if err = CallbackBeforeMemberJoinGroup(ctx, member, group.Ex); err != nil {
			return nil, err
		}
	}
	log.ZDebug(ctx, "GroupApplicationResponse", "inGroup", inGroup, "HandleResult", req.HandleResult, "member", member)
	if err := identity.check(ctx, req.GroupID, req.FromUserID, strconv.Itoa(int(req.HandleResult))); err != nil {
		return nil, err
	}
	if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: req.GroupID, Members: []string{req.FromUserID}}}, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID, req.FromUserID, strconv.Itoa(int(req.HandleResult))); err != nil {
			return err
		}
		if err := s.freshAdmin(tx, req.GroupID, true); err != nil {
			return err
		}
		current, err := s.db.TakeGroupRequest(tx, req.GroupID, req.FromUserID)
		if err != nil {
			return err
		}
		if current == nil || current.GroupID != req.GroupID || current.UserID != req.FromUserID {
			return errs.ErrNoPermission.Wrap("invalid group request")
		}
		if current.HandleResult != 0 {
			return errs.ErrGroupRequestHandled.Wrap("group request already processed")
		}
		if !current.ReqTime.Equal(groupRequest.ReqTime) || current.JoinSource != groupRequest.JoinSource || current.InviterUserID != groupRequest.InviterUserID || current.Ex != groupRequest.Ex {
			return groupauthority.ErrConflict
		}
		present := false
		if _, err := s.freshMember(tx, req.GroupID, req.FromUserID); err == nil {
			present = true
		} else if !s.IsNotFound(err) {
			return err
		}
		if present != inGroup {
			return groupauthority.ErrConflict
		}
		if member != nil {
			return authorizeNewMemberRoles([]*relationtb.GroupMemberModel{member}, req.GroupID, true, "")
		}
		return nil
	}, func(tx context.Context) error {
		return s.db.HandlerGroupRequest(tx, req.GroupID, req.FromUserID, req.HandledMsg, req.HandleResult, member)
	}); err != nil {
		return nil, err
	}
	if member != nil {
		// 审批事务已写入成员后再刷新一次，避免审批瞬间并发消息落在旧边界之后。
		if err := s.restrictNewMembersHistory(ctx, req.GroupID, []string{req.FromUserID}); err != nil {
			return nil, err
		}
	}
	switch req.HandleResult {
	case constant.GroupResponseAgree:
		if err := s.conversationRpcClient.GroupChatFirstCreateConversation(ctx, req.GroupID, []string{req.FromUserID}); err != nil {
			return nil, err
		}
		s.Notification.GroupApplicationAcceptedNotification(ctx, req)
		if member == nil {
			log.ZDebug(ctx, "GroupApplicationResponse", "member is nil")
		} else {
			s.Notification.MemberEnterNotification(ctx, req.GroupID, req.FromUserID)
		}
	case constant.GroupResponseRefuse:
		s.Notification.GroupApplicationRejectedNotification(ctx, req)
	}

	return &pbgroup.GroupApplicationResponseResp{}, nil
}

func (s *groupServer) JoinGroup(ctx context.Context, req *pbgroup.JoinGroupReq) (resp *pbgroup.JoinGroupResp, err error) {
	defer log.ZInfo(ctx, "JoinGroup.Return")
	if err := authverify.CheckAccessV3(ctx, req.GetInviterUserID()); err != nil {
		return nil, err
	}
	identity, err := captureGroupIntent(ctx, req.GroupID, req.InviterUserID)
	if err != nil {
		return nil, err
	}
	user, err := s.User.GetUserInfo(ctx, req.InviterUserID)
	if err != nil {
		return nil, err
	}
	group, err := s.freshGroup(ctx, req.GroupID, true)
	if err != nil {
		return nil, err
	}
	if group.Status == constant.GroupStatusDismissed {
		return nil, errs.ErrDismissedAlready.Wrap()
	}

	reqCall := &callbackstruct.CallbackJoinGroupReq{
		GroupID:    req.GroupID,
		GroupType:  string(group.GroupType),
		ApplyID:    req.InviterUserID,
		ReqMessage: req.ReqMessage,
		Ex:         req.Ex,
	}

	if err = CallbackApplyJoinGroupBefore(ctx, reqCall); err != nil {
		return nil, err
	}
	_, err = s.freshMember(ctx, req.GroupID, req.InviterUserID)
	if err == nil {
		return nil, errs.ErrArgs.Wrap("already in group")
	} else if !s.IsNotFound(err) && utils.Unwrap(err) != errs.ErrRecordNotFound {
		return nil, err
	}
	log.ZInfo(ctx, "JoinGroup.groupInfo", "group", group, "eq", group.NeedVerification == constant.Directly)
	resp = &pbgroup.JoinGroupResp{}
	if user == nil || user.UserID != req.InviterUserID {
		return nil, errs.ErrNoPermission.Wrap("invalid applicant user result")
	}
	if err := identity.check(ctx, req.GroupID, req.InviterUserID); err != nil {
		return nil, err
	}
	if group.NeedVerification == constant.Directly {
		if err := s.restrictNewMembersHistory(ctx, req.GroupID, []string{req.InviterUserID}); err != nil {
			return nil, err
		}
		groupMember := &relationtb.GroupMemberModel{
			GroupID:        group.GroupID,
			UserID:         user.UserID,
			RoleLevel:      constant.GroupOrdinaryUsers,
			OperatorUserID: mcontext.GetOpUserID(ctx),
			InviterUserID:  req.InviterUserID,
			JoinTime:       time.Now(),
			MuteEndTime:    time.UnixMilli(0),
		}
		if err := CallbackBeforeMemberJoinGroup(ctx, groupMember, group.Ex); err != nil {
			return nil, err
		}
		if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: req.GroupID, Members: []string{req.InviterUserID}}}, func(tx context.Context) error {
			if err := identity.check(tx, req.GroupID, req.InviterUserID); err != nil {
				return err
			}
			direct, err := s.freshJoinMode(tx, req.GroupID, req.InviterUserID)
			if err != nil {
				return err
			}
			if !direct {
				return groupauthority.ErrConflict
			}
			return authorizeNewMemberRoles([]*relationtb.GroupMemberModel{groupMember}, req.GroupID, authverify.IsAppManagerUid(tx), "")
		}, func(tx context.Context) error {
			return s.db.CreateGroup(tx, nil, []*relationtb.GroupMemberModel{groupMember})
		}); err != nil {
			return nil, err
		}
		// 成员写入后再刷新一次边界，关闭入群过程中的并发消息窗口。
		if err := s.restrictNewMembersHistory(ctx, req.GroupID, []string{req.InviterUserID}); err != nil {
			return nil, err
		}

		if err := s.conversationRpcClient.GroupChatFirstCreateConversation(ctx, req.GroupID, []string{req.InviterUserID}); err != nil {
			return nil, err
		}
		s.Notification.MemberEnterNotification(ctx, req.GroupID, req.InviterUserID)
		if err = CallbackAfterJoinGroup(ctx, req); err != nil {
			return nil, err
		}
		return resp, nil
	}
	groupRequest := relationtb.GroupRequestModel{
		UserID:      req.InviterUserID,
		ReqMsg:      req.ReqMessage,
		GroupID:     req.GroupID,
		JoinSource:  req.JoinSource,
		ReqTime:     time.Now(),
		HandledTime: time.Unix(0, 0),
		Ex:          req.Ex,
	}
	if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: req.GroupID, Members: []string{req.InviterUserID}}}, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID, req.InviterUserID); err != nil {
			return err
		}
		direct, err := s.freshJoinMode(tx, req.GroupID, req.InviterUserID)
		if err != nil {
			return err
		}
		if direct {
			return groupauthority.ErrConflict
		}
		return nil
	}, func(tx context.Context) error {
		return s.db.CreateGroupRequest(tx, []*relationtb.GroupRequestModel{&groupRequest})
	}); err != nil {
		return nil, err
	}
	s.Notification.JoinGroupApplicationNotification(ctx, req)
	return resp, nil
}

func (s *groupServer) QuitGroup(ctx context.Context, req *pbgroup.QuitGroupReq) (*pbgroup.QuitGroupResp, error) {
	resp := &pbgroup.QuitGroupResp{}
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	if req.UserID == "" {
		req.UserID = mcontext.GetOpUserID(ctx)
	} else {
		if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
			return nil, err
		}
	}
	identity, err := captureGroupIntent(ctx, req.GroupID, req.UserID)
	if err != nil {
		return nil, err
	}
	member, err := s.freshMember(ctx, req.GroupID, req.UserID)
	if err != nil {
		return nil, err
	}
	if member.RoleLevel == constant.GroupOwner {
		return nil, errs.ErrNoPermission.Wrap("group owner can't quit")
	}
	if err := s.PopulateGroupMember(ctx, member); err != nil {
		return nil, err
	}
	err = s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: req.GroupID, Members: []string{req.UserID}}}, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID, req.UserID); err != nil {
			return err
		}
		if err := authverify.CheckAccessV3(tx, req.UserID); err != nil {
			return err
		}
		current, err := s.freshMember(tx, req.GroupID, req.UserID)
		if err != nil {
			return err
		}
		if current.RoleLevel == constant.GroupOwner {
			return errs.ErrNoPermission.Wrap("group owner can't quit")
		}
		return nil
	}, func(tx context.Context) error { return s.db.DeleteGroupMember(tx, req.GroupID, []string{req.UserID}) })
	if err != nil {
		return nil, err
	}
	_ = s.Notification.MemberQuitNotification(ctx, s.groupMemberDB2PB(member, 0))
	if err := s.deleteMemberAndSetConversationSeq(ctx, req.GroupID, []string{req.UserID}); err != nil {
		return nil, err
	}

	// callback
	if err := CallbackQuitGroup(ctx, req); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *groupServer) deleteMemberAndSetConversationSeq(ctx context.Context, groupID string, userIDs []string) error {
	conevrsationID := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, groupID)
	maxSeq, err := s.msgRpcClient.GetConversationMaxSeq(ctx, conevrsationID)
	if err != nil {
		return err
	}
	return s.conversationRpcClient.SetConversationMaxSeq(ctx, userIDs, conevrsationID, maxSeq)
}

func (s *groupServer) SetGroupInfo(ctx context.Context, req *pbgroup.SetGroupInfoReq) (*pbgroup.SetGroupInfoResp, error) {
	if req == nil || req.GroupInfoForSet == nil {
		return nil, errs.ErrArgs.Wrap("group info required")
	}
	identity, err := captureGroupIntent(ctx, req.GroupInfoForSet.GroupID)
	if err != nil {
		return nil, err
	}
	var opMember *relationtb.GroupMemberModel
	if !authverify.IsAppManagerUid(ctx) {
		var err error
		opMember, err = s.freshMember(ctx, req.GroupInfoForSet.GroupID, mcontext.GetOpUserID(ctx))
		if err != nil {
			return nil, err
		}
		if !(opMember.RoleLevel == constant.GroupOwner || opMember.RoleLevel == constant.GroupAdmin) {
			return nil, errs.ErrNoPermission.Wrap("no group owner or admin")
		}
		if err := s.PopulateGroupMember(ctx, opMember); err != nil {
			return nil, err
		}
		if blocked, err := riskcontrol.IsActionBlocked(ctx, riskcontrol.RiskDisableGroupActionKey); err != nil {
			return nil, err
		} else if blocked {
			return nil, errs.ErrNoPermission.Wrap("当前时段禁止群操作")
		}
	}
	if err := CallbackBeforeSetGroupInfo(ctx, req); err != nil {
		return nil, err
	}
	if err := identity.check(ctx, req.GroupInfoForSet.GroupID); err != nil {
		return nil, err
	}
	group, err := s.freshGroup(ctx, req.GroupInfoForSet.GroupID, true)
	if err != nil {
		return nil, err
	}
	if group.Status == constant.GroupStatusDismissed {
		return nil, utils.Wrap(errs.ErrDismissedAlready, "")
	}
	resp := &pbgroup.SetGroupInfoResp{}
	count, err := s.db.FindGroupMemberNum(ctx, group.GroupID)
	if err != nil {
		return nil, err
	}
	owner, err := s.db.TakeGroupOwner(ctx, group.GroupID)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, owner); err != nil {
		return nil, err
	}
	update := UpdateGroupInfoMap(ctx, req.GroupInfoForSet)
	if len(update) == 0 {
		return resp, nil
	}
	if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: group.GroupID}}, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupInfoForSet.GroupID); err != nil {
			return err
		}
		return s.freshAdmin(tx, group.GroupID, true)
	}, func(tx context.Context) error { return s.db.UpdateGroup(tx, group.GroupID, update) }); err != nil {
		return nil, err
	}
	group, err = s.db.TakeGroup(ctx, req.GroupInfoForSet.GroupID)
	if err != nil {
		return nil, err
	}
	tips := &sdkws.GroupInfoSetTips{
		Group:    s.groupDB2PB(group, owner.UserID, count),
		MuteTime: 0,
		OpUser:   &sdkws.GroupMemberFullInfo{},
	}
	if opMember != nil {
		tips.OpUser = s.groupMemberDB2PB(opMember, 0)
	}
	num := len(update)
	if req.GroupInfoForSet.Notification != "" {
		num--
		func() {
			conversation := &pbconversation.ConversationReq{
				ConversationID:   msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, req.GroupInfoForSet.GroupID),
				ConversationType: constant.SuperGroupChatType,
				GroupID:          req.GroupInfoForSet.GroupID,
			}
			resp, err := s.GetGroupMemberUserIDs(ctx, &pbgroup.GetGroupMemberUserIDsReq{GroupID: req.GroupInfoForSet.GroupID})
			if err != nil {
				log.ZWarn(ctx, "GetGroupMemberIDs", err)
				return
			}
			conversation.GroupAtType = &wrapperspb.Int32Value{Value: constant.GroupNotification}
			if err := s.conversationRpcClient.SetConversations(ctx, resp.UserIDs, conversation); err != nil {
				log.ZWarn(ctx, "SetConversations", err, resp.UserIDs, conversation)
			}
		}()
		_ = s.Notification.GroupInfoSetAnnouncementNotification(ctx, &sdkws.GroupInfoSetAnnouncementTips{Group: tips.Group, OpUser: tips.OpUser})
	}
	if req.GroupInfoForSet.GroupName != "" {
		num--
		_ = s.Notification.GroupInfoSetNameNotification(ctx, &sdkws.GroupInfoSetNameTips{Group: tips.Group, OpUser: tips.OpUser})
	}
	if num > 0 {
		_ = s.Notification.GroupInfoSetNotification(ctx, tips)
	}
	if err := CallbackAfterSetGroupInfo(ctx, req); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *groupServer) TransferGroupOwner(ctx context.Context, req *pbgroup.TransferGroupOwnerReq) (*pbgroup.TransferGroupOwnerResp, error) {
	resp := &pbgroup.TransferGroupOwnerResp{}
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	identity, err := captureGroupIntent(ctx, req.GroupID, req.OldOwnerUserID, req.NewOwnerUserID)
	if err != nil {
		return nil, err
	}
	group, err := s.freshGroup(ctx, req.GroupID, true)
	if err != nil {
		return nil, err
	}
	if group.Status == constant.GroupStatusDismissed {
		return nil, errs.ErrDismissedAlready.Wrap("")
	}
	if req.OldOwnerUserID == req.NewOwnerUserID {
		return nil, errs.ErrArgs.Wrap("OldOwnerUserID == NewOwnerUserID")
	}
	memberMap, err := s.freshMembers(ctx, req.GroupID, []string{req.OldOwnerUserID, req.NewOwnerUserID})
	if err != nil {
		return nil, err
	}
	members := make([]*relationtb.GroupMemberModel, 0, len(memberMap))
	for _, member := range memberMap {
		members = append(members, member)
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	if ids := utils.Single([]string{req.OldOwnerUserID, req.NewOwnerUserID}, utils.Keys(memberMap)); len(ids) > 0 {
		return nil, errs.ErrArgs.Wrap("user not in group " + strings.Join(ids, ","))
	}
	oldOwner := memberMap[req.OldOwnerUserID]
	if oldOwner == nil {
		return nil, errs.ErrArgs.Wrap("OldOwnerUserID not in group " + req.NewOwnerUserID)
	}
	newOwner := memberMap[req.NewOwnerUserID]
	if newOwner == nil {
		return nil, errs.ErrArgs.Wrap("NewOwnerUser not in group " + req.NewOwnerUserID)
	}
	if !authverify.IsAppManagerUid(ctx) {
		if !(mcontext.GetOpUserID(ctx) == oldOwner.UserID && oldOwner.RoleLevel == constant.GroupOwner) {
			return nil, errs.ErrNoPermission.Wrap("no permission transfer group owner")
		}
		if blocked, err := riskcontrol.IsActionBlocked(ctx, riskcontrol.RiskDisableGroupActionKey); err != nil {
			return nil, err
		} else if blocked {
			return nil, errs.ErrNoPermission.Wrap("当前时段禁止群操作")
		}
	}
	previousNewRole := newOwner.RoleLevel
	if err := s.runOwnerTransferEffects(ctx, req, []groupauthority.Target{{GroupID: req.GroupID, Members: []string{req.OldOwnerUserID, req.NewOwnerUserID}}}, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID, req.OldOwnerUserID, req.NewOwnerUserID); err != nil {
			return err
		}
		if err := s.freshOwner(tx, req.GroupID, true); err != nil {
			return err
		}
		old, err := s.freshMember(tx, req.GroupID, req.OldOwnerUserID)
		if err != nil {
			return err
		}
		if old.RoleLevel != constant.GroupOwner {
			return errs.ErrNoPermission.Wrap("old owner changed")
		}
		current, err := s.freshMember(tx, req.GroupID, req.NewOwnerUserID)
		if err != nil {
			return err
		}
		if current.RoleLevel != constant.GroupOrdinaryUsers && current.RoleLevel != constant.GroupAdmin {
			return errs.ErrNoPermission.Wrap("invalid successor role")
		}
		previousNewRole = current.RoleLevel
		return nil
	}, func(tx context.Context) error {
		return s.db.TransferGroupOwner(tx, req.GroupID, req.OldOwnerUserID, req.NewOwnerUserID, previousNewRole)
	}); err != nil {
		return nil, err
	}

	return resp, nil
}

func (s *groupServer) GetGroups(ctx context.Context, req *pbgroup.GetGroupsReq) (*pbgroup.GetGroupsResp, error) {
	if err := authverify.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	resp := &pbgroup.GetGroupsResp{}
	var (
		group []*relationtb.GroupModel
		err   error
	)
	if req.GroupID != "" {
		group, err = s.db.FindGroup(ctx, []string{req.GroupID})
		resp.Total = uint32(len(group))
	} else {
		var total int64
		total, group, err = s.db.SearchGroup(ctx, req.GroupName, req.Pagination)
		resp.Total = uint32(total)
	}
	if err != nil {
		return nil, err
	}

	var groups []*relationtb.GroupModel
	for _, v := range group {
		if v.Status == constant.GroupStatusDismissed {
			resp.Total--
			continue
		}
		groups = append(groups, v)
	}
	groupIDs := utils.Slice(groups, func(e *relationtb.GroupModel) string {
		return e.GroupID
	})
	ownerMembers, err := s.db.FindGroupsOwner(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	ownerMemberMap := utils.SliceToMap(ownerMembers, func(e *relationtb.GroupMemberModel) string {
		return e.GroupID
	})
	groupMemberNumMap, err := s.db.MapGroupMemberNum(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	resp.Groups = utils.Slice(groups, func(group *relationtb.GroupModel) *pbgroup.CMSGroup {
		var (
			userID   string
			username string
		)
		if member, ok := ownerMemberMap[group.GroupID]; ok {
			userID = member.UserID
			username = member.Nickname
		}
		return convert.Db2PbCMSGroup(group, userID, username, groupMemberNumMap[group.GroupID])
	})
	return resp, nil
}

func (s *groupServer) GetGroupMembersCMS(ctx context.Context, req *pbgroup.GetGroupMembersCMSReq) (*pbgroup.GetGroupMembersCMSResp, error) {
	if err := authverify.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	resp := &pbgroup.GetGroupMembersCMSResp{}
	total, members, err := s.db.SearchGroupMember(ctx, req.UserName, req.GroupID, req.Pagination)
	if err != nil {
		return nil, err
	}
	resp.Total = uint32(total)
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	resp.Members = utils.Slice(members, func(e *relationtb.GroupMemberModel) *sdkws.GroupMemberFullInfo {
		return convert.Db2PbGroupMember(e)
	})
	return resp, nil
}

func (s *groupServer) GetUserReqApplicationList(ctx context.Context, req *pbgroup.GetUserReqApplicationListReq) (*pbgroup.GetUserReqApplicationListResp, error) {
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	resp := &pbgroup.GetUserReqApplicationListResp{}
	user, err := s.User.GetPublicUserInfo(ctx, req.UserID)
	if err != nil {
		return nil, err
	}
	total, requests, err := s.db.PageGroupRequestUser(ctx, req.UserID, req.Pagination)
	if err != nil {
		return nil, err
	}
	resp.Total = uint32(total)
	if len(requests) == 0 {
		return resp, nil
	}
	groupIDs := utils.Distinct(utils.Slice(requests, func(e *relationtb.GroupRequestModel) string {
		return e.GroupID
	}))
	groups, err := s.db.FindGroup(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	groupMap := utils.SliceToMap(groups, func(e *relationtb.GroupModel) string {
		return e.GroupID
	})
	owners, err := s.db.FindGroupsOwner(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, owners...); err != nil {
		return nil, err
	}
	ownerMap := utils.SliceToMap(owners, func(e *relationtb.GroupMemberModel) string {
		return e.GroupID
	})
	groupMemberNum, err := s.db.MapGroupMemberNum(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	resp.GroupRequests = utils.Slice(requests, func(e *relationtb.GroupRequestModel) *sdkws.GroupRequest {
		var ownerUserID string
		if owner, ok := ownerMap[e.GroupID]; ok {
			ownerUserID = owner.UserID
		}
		return convert.Db2PbGroupRequest(e, user, convert.Db2PbGroupInfo(groupMap[e.GroupID], ownerUserID, groupMemberNum[e.GroupID]))
	})
	return resp, nil
}

func (s *groupServer) DismissGroup(ctx context.Context, req *pbgroup.DismissGroupReq) (*pbgroup.DismissGroupResp, error) {
	defer log.ZInfo(ctx, "DismissGroup.return")
	resp := &pbgroup.DismissGroupResp{}
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	identity, err := captureGroupIntent(ctx, req.GroupID, strconv.FormatBool(req.DeleteMember))
	if err != nil {
		return nil, err
	}
	owner, err := s.db.TakeGroupOwnerForPermission(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	if owner == nil || owner.GroupID != req.GroupID || owner.UserID == "" || owner.RoleLevel != constant.GroupOwner {
		return nil, errs.ErrNoPermission.Wrap("invalid group owner")
	}
	if !authverify.IsAppManagerUid(ctx) {
		if owner.UserID != mcontext.GetOpUserID(ctx) {
			return nil, errs.ErrNoPermission.Wrap("not group owner")
		}
		if blocked, err := riskcontrol.IsActionBlocked(ctx, riskcontrol.RiskDisableGroupActionKey); err != nil {
			return nil, err
		} else if blocked {
			return nil, errs.ErrNoPermission.Wrap("当前时段禁止群操作")
		}
	}
	if err := s.PopulateGroupMember(ctx, owner); err != nil {
		return nil, err
	}
	group, err := s.freshGroup(ctx, req.GroupID, false)
	if err != nil {
		return nil, err
	}
	if req.DeleteMember == false && group.Status == constant.GroupStatusDismissed {
		return nil, errs.ErrDismissedAlready.Wrap("group status is dismissed")
	}
	if err := s.runGroupMutation(ctx, []groupauthority.Target{{GroupID: req.GroupID, AllMembers: req.DeleteMember}}, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID, strconv.FormatBool(req.DeleteMember)); err != nil {
			return err
		}
		if err := s.freshOwner(tx, req.GroupID, false); err != nil {
			return err
		}
		current, err := s.freshGroup(tx, req.GroupID, false)
		if err != nil {
			return err
		}
		if !req.DeleteMember && current.Status == constant.GroupStatusDismissed {
			return errs.ErrDismissedAlready.Wrap("group status is dismissed")
		}
		return nil
	}, func(tx context.Context) error { return s.db.DismissGroup(tx, req.GroupID, req.DeleteMember) }); err != nil {
		return nil, err
	}
	if !req.DeleteMember {
		num, err := s.db.FindGroupMemberNum(ctx, req.GroupID)
		if err != nil {
			return nil, err
		}
		tips := &sdkws.GroupDismissedTips{
			Group:  s.groupDB2PB(group, owner.UserID, num),
			OpUser: &sdkws.GroupMemberFullInfo{},
		}
		if mcontext.GetOpUserID(ctx) == owner.UserID {
			tips.OpUser = s.groupMemberDB2PB(owner, 0)
		}
		s.Notification.GroupDismissedNotification(ctx, tips)
	}
	membersID, err := s.db.FindGroupMemberUserID(ctx, group.GroupID)
	if err != nil {
		return nil, err
	}
	reqCall := &callbackstruct.CallbackDisMissGroupReq{
		GroupID:   req.GroupID,
		OwnerID:   owner.UserID,
		MembersID: membersID,
		GroupType: string(group.GroupType),
	}
	if err := CallbackDismissGroup(ctx, reqCall); err != nil {
		return nil, err
	}

	return resp, nil
}

func (s *groupServer) MuteGroupMember(ctx context.Context, req *pbgroup.MuteGroupMemberReq) (*pbgroup.MuteGroupMemberResp, error) {
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	gid, uid, seconds := req.GroupID, req.UserID, req.MutedSeconds
	identity, err := captureGroupIntent(ctx, gid, uid)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeMemberMute(ctx, gid, uid); err != nil {
		return nil, err
	}
	if err := s.runGroupMemberStatusNoticeMutation(ctx, gid, uid, seconds, groupauthority.NoticeMuteMember, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID, req.UserID); err != nil {
			return err
		}
		if req.MutedSeconds != seconds {
			return errs.ErrNoPermission.Wrap("group intent duration changed")
		}
		return s.authorizeMemberMute(tx, gid, uid)
	}); err != nil {
		return nil, err
	}
	return &pbgroup.MuteGroupMemberResp{}, nil
}

func (s *groupServer) CancelMuteGroupMember(ctx context.Context, req *pbgroup.CancelMuteGroupMemberReq) (*pbgroup.CancelMuteGroupMemberResp, error) {
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	gid, uid := req.GroupID, req.UserID
	identity, err := captureGroupIntent(ctx, gid, uid)
	if err != nil {
		return nil, err
	}
	if err := s.authorizeMemberMute(ctx, gid, uid); err != nil {
		return nil, err
	}
	if err := s.runGroupMemberStatusNoticeMutation(ctx, gid, uid, 0, groupauthority.NoticeUnmuteMember, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID, req.UserID); err != nil {
			return err
		}
		return s.authorizeMemberMute(tx, gid, uid)
	}); err != nil {
		return nil, err
	}
	return &pbgroup.CancelMuteGroupMemberResp{}, nil
}

func (s *groupServer) MuteGroup(ctx context.Context, req *pbgroup.MuteGroupReq) (*pbgroup.MuteGroupResp, error) {
	resp := &pbgroup.MuteGroupResp{}
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	identity, err := captureGroupIntent(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	if err := s.freshAdmin(ctx, req.GroupID, true); err != nil {
		return nil, err
	}
	fields := map[string]any{"status": int32(constant.GroupStatusMuted)}
	if err := s.runGroupStatusNoticeMutation(ctx, req.GroupID, groupauthority.NoticeMuteGroup, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID); err != nil {
			return err
		}
		return s.freshAdmin(tx, req.GroupID, true)
	}, func(tx context.Context) error { return s.db.UpdateGroup(tx, req.GroupID, fields) }); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *groupServer) CancelMuteGroup(ctx context.Context, req *pbgroup.CancelMuteGroupReq) (*pbgroup.CancelMuteGroupResp, error) {
	resp := &pbgroup.CancelMuteGroupResp{}
	if req == nil {
		return nil, errs.ErrArgs.Wrap("request required")
	}
	identity, err := captureGroupIntent(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	if err := s.freshAdmin(ctx, req.GroupID, true); err != nil {
		return nil, err
	}
	fields := map[string]any{"status": int32(constant.GroupOk)}
	if err := s.runGroupStatusNoticeMutation(ctx, req.GroupID, groupauthority.NoticeUnmuteGroup, func(tx context.Context) error {
		if err := identity.check(tx, req.GroupID); err != nil {
			return err
		}
		return s.freshAdmin(tx, req.GroupID, true)
	}, func(tx context.Context) error { return s.db.UpdateGroup(tx, req.GroupID, fields) }); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *groupServer) SetGroupMemberInfo(ctx context.Context, req *pbgroup.SetGroupMemberInfoReq) (*pbgroup.SetGroupMemberInfoResp, error) {
	resp := &pbgroup.SetGroupMemberInfoResp{}
	identity, err := captureGroupMemberEdit(ctx, req)
	if err != nil {
		return nil, err
	}
	for _, member := range req.Members {
		member.FaceURL = nil
	}
	if err := s.authorizeGroupMemberEdits(ctx, req); err != nil {
		return nil, err
	}
	for _, member := range req.Members {
		if err := CallbackBeforeSetGroupMemberInfo(ctx, member); err != nil {
			return nil, err
		}
	}
	if err := identity.check(ctx, req); err != nil {
		return nil, err
	}
	// Callbacks may change editable fields. Re-read authority and validate the
	// final role values before admitting any write in this batch.
	if err := s.authorizeGroupMemberEdits(ctx, req); err != nil {
		return nil, err
	}
	if err := identity.check(ctx, req); err != nil {
		return nil, err
	}
	edits := utils.Slice(req.Members, func(e *pbgroup.SetGroupMemberInfo) *relationtb.BatchUpdateGroupMember {
		return &relationtb.BatchUpdateGroupMember{GroupID: e.GroupID, UserID: e.UserID, Map: UpdateGroupMemberMap(e)}
	})
	targets := make([]groupauthority.Target, 0, len(edits))
	for _, item := range edits {
		targets = append(targets, groupauthority.Target{GroupID: item.GroupID, Members: []string{item.UserID}})
	}
	if err := s.runMemberEditEffects(ctx, req, targets, func(tx context.Context) error {
		if err := identity.check(tx, req); err != nil {
			return err
		}
		for _, target := range targets {
			if _, err := s.freshGroup(tx, target.GroupID, true); err != nil {
				return err
			}
		}
		return s.authorizeGroupMemberEdits(tx, req)
	}, func(tx context.Context) error { return s.db.UpdateGroupMembers(tx, edits) }); err != nil {
		return nil, err
	}

	return resp, nil
}

func (s *groupServer) GetGroupAbstractInfo(ctx context.Context, req *pbgroup.GetGroupAbstractInfoReq) (*pbgroup.GetGroupAbstractInfoResp, error) {
	resp := &pbgroup.GetGroupAbstractInfoResp{}
	if len(req.GroupIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("groupIDs empty")
	}
	for _, groupID := range req.GroupIDs {
		if err := s.checkGroupMemberOrAdmin(ctx, groupID); err != nil {
			return nil, err
		}
	}
	if utils.Duplicate(req.GroupIDs) {
		return nil, errs.ErrArgs.Wrap("groupIDs duplicate")
	}
	groups, err := s.db.FindGroup(ctx, req.GroupIDs)
	if err != nil {
		return nil, err
	}
	if ids := utils.Single(req.GroupIDs, utils.Slice(groups, func(group *relationtb.GroupModel) string {
		return group.GroupID
	})); len(ids) > 0 {
		return nil, errs.ErrGroupIDNotFound.Wrap("not found group " + strings.Join(ids, ","))
	}
	groupUserMap, err := s.db.MapGroupMemberUserID(ctx, req.GroupIDs)
	if err != nil {
		return nil, err
	}
	if ids := utils.Single(req.GroupIDs, utils.Keys(groupUserMap)); len(ids) > 0 {
		return nil, errs.ErrGroupIDNotFound.Wrap(fmt.Sprintf("group %s not found member", strings.Join(ids, ",")))
	}
	resp.GroupAbstractInfos = utils.Slice(groups, func(group *relationtb.GroupModel) *pbgroup.GroupAbstractInfo {
		users := groupUserMap[group.GroupID]
		return convert.Db2PbGroupAbstractInfo(group.GroupID, users.MemberNum, users.Hash)
	})
	return resp, nil
}

func (s *groupServer) GetUserInGroupMembers(ctx context.Context, req *pbgroup.GetUserInGroupMembersReq) (*pbgroup.GetUserInGroupMembersResp, error) {
	resp := &pbgroup.GetUserInGroupMembersResp{}
	if err := authverify.CheckAccessV3(ctx, req.UserID); err != nil {
		return nil, err
	}
	if len(req.GroupIDs) == 0 {
		return nil, errs.ErrArgs.Wrap("groupIDs empty")
	}
	members, err := s.db.FindGroupMemberUser(ctx, req.GroupIDs, req.UserID)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	resp.Members = utils.Slice(members, func(e *relationtb.GroupMemberModel) *sdkws.GroupMemberFullInfo {
		return convert.Db2PbGroupMember(e)
	})
	return resp, nil
}

func (s *groupServer) GetGroupMemberUserIDs(ctx context.Context, req *pbgroup.GetGroupMemberUserIDsReq) (resp *pbgroup.GetGroupMemberUserIDsResp, err error) {
	if err = s.checkGroupMemberOrAdmin(ctx, req.GroupID); err != nil {
		return nil, err
	}
	resp = &pbgroup.GetGroupMemberUserIDsResp{}
	resp.UserIDs, err = s.db.FindGroupMemberUserID(ctx, req.GroupID)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *groupServer) GetGroupMemberRoleLevel(ctx context.Context, req *pbgroup.GetGroupMemberRoleLevelReq) (*pbgroup.GetGroupMemberRoleLevelResp, error) {
	resp := &pbgroup.GetGroupMemberRoleLevelResp{}
	if err := s.checkGroupMemberOrAdmin(ctx, req.GroupID); err != nil {
		return nil, err
	}
	if len(req.RoleLevels) == 0 {
		return nil, errs.ErrArgs.Wrap("RoleLevels empty")
	}
	members, err := s.db.FindGroupMemberRoleLevels(ctx, req.GroupID, req.RoleLevels)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, members...); err != nil {
		return nil, err
	}
	resp.Members = utils.Slice(members, func(e *relationtb.GroupMemberModel) *sdkws.GroupMemberFullInfo {
		return convert.Db2PbGroupMember(e)
	})
	return resp, nil
}

func (s *groupServer) GetGroupUsersReqApplicationList(ctx context.Context, req *pbgroup.GetGroupUsersReqApplicationListReq) (*pbgroup.GetGroupUsersReqApplicationListResp, error) {
	if err := s.checkGroupMemberOrAdmin(ctx, req.GroupID); err != nil {
		return nil, err
	}
	resp := &pbgroup.GetGroupUsersReqApplicationListResp{}
	requests, err := s.db.FindGroupRequests(ctx, req.GroupID, req.UserIDs)
	if err != nil {
		return nil, err
	}
	if len(requests) == 0 {
		return resp, nil
	}
	groupIDs := utils.Distinct(utils.Slice(requests, func(e *relationtb.GroupRequestModel) string {
		return e.GroupID
	}))
	groups, err := s.db.FindGroup(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	groupMap := utils.SliceToMap(groups, func(e *relationtb.GroupModel) string {
		return e.GroupID
	})
	if ids := utils.Single(groupIDs, utils.Keys(groupMap)); len(ids) > 0 {
		return nil, errs.ErrGroupIDNotFound.Wrap(strings.Join(ids, ","))
	}
	owners, err := s.db.FindGroupsOwner(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	if err := s.PopulateGroupMember(ctx, owners...); err != nil {
		return nil, err
	}
	ownerMap := utils.SliceToMap(owners, func(e *relationtb.GroupMemberModel) string {
		return e.GroupID
	})
	groupMemberNum, err := s.db.MapGroupMemberNum(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	resp.GroupRequests = utils.Slice(requests, func(e *relationtb.GroupRequestModel) *sdkws.GroupRequest {
		var ownerUserID string
		if owner, ok := ownerMap[e.GroupID]; ok {
			ownerUserID = owner.UserID
		}
		return convert.Db2PbGroupRequest(e, nil, convert.Db2PbGroupInfo(groupMap[e.GroupID], ownerUserID, groupMemberNum[e.GroupID]))
	})
	resp.Total = int64(len(resp.GroupRequests))
	return resp, nil
}
