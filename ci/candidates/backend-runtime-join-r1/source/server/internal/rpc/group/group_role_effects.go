package group

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"google.golang.org/protobuf/proto"
)

type roleEffectQueue interface {
	QueueRoleCallback(context.Context, groupeffects.Frozen, groupeffects.RoleEffectOrder) error
	QueueRoleNotification(context.Context, *groupauthority.RoleNoticeSnapshot, string, groupeffects.Frozen, groupeffects.ActionDeliveryPolicy, groupeffects.RoleEffectOrder) error
}
type roleAfterPolicy struct {
	owned                     *groupeffects.OwnedRoleAfterProfile
	binding, configurationSHA string
	policy                    config.CallBackConfig
}

func initializeRoleAfterPolicies(r *groupNoticeRuntime) error {
	r.roleAfter = map[groupeffects.CallbackName]roleAfterPolicy{}
	for name, conf := range map[groupeffects.CallbackName]config.CallBackConfig{groupeffects.AfterSetMember: config.Config.Callback.CallbackAfterSetGroupMemberInfo, groupeffects.AfterTransfer: config.Config.Callback.CallbackTransferGroupOwnerAfter, groupeffects.AfterKick: config.Config.Callback.CallbackKillGroupMember, groupeffects.AfterQuit: config.Config.Callback.CallbackQuitGroup, groupeffects.AfterJoin: config.Config.Callback.CallbackAfterJoinGroup} {
		if !conf.Enable {
			continue
		}
		// Binding computation only; no credential is captured in Mongo. Runtime
		// dispatch separately requires the retained source-owned signing channel.
		endpoint, err := groupeffects.NewEndpoint(config.Config.Callback.CallbackUrl, r.db.Name(), name, []byte("binding-only-not-a-dispatch-key"), time.Second)
		if err != nil {
			return err
		}
		if conf.CallbackFailedContinue != nil {
			value := *conf.CallbackFailedContinue
			conf.CallbackFailedContinue = &value
		}
		hash, err := noticeHash([]any{r.epoch, name, endpoint.Binding(), conf})
		if err != nil {
			return err
		}
		r.roleAfter[name] = roleAfterPolicy{binding: endpoint.Binding(), configurationSHA: hash, policy: conf}
	}
	return nil
}
func roleNoticePolicyKey(kind int32) groupauthority.NoticeAction {
	switch kind {
	case constant.GroupOwnerTransferredNotification:
		return "transfer-owner"
	case constant.GroupMemberSetToAdminNotification:
		return "set-member-admin"
	case constant.GroupMemberSetToOrdinaryUserNotification:
		return "set-member-ordinary"
	case constant.GroupMemberInfoSetNotification:
		return "set-member-info"
	}
	return ""
}

// Frozen IDs are stable across transaction retries without imposing a new
// 4096-member batch limit: each step derives its own operation from the one
// server command. Untrusted operationID remains trace-only.
func roleEffectOperation(command groupauthority.RoleCommandInfo, gid, label string, index int) string {
	root, _ := uuid.Parse(command.ID)
	body, _ := json.Marshal([]any{gid, label, index})
	return uuid.NewSHA1(root, body).String()
}
func (s *groupServer) runRoleEffects(ctx context.Context, targets []groupauthority.Target, newCommand func(context.Context) (context.Context, groupauthority.RoleCommandInfo, error), authorize, write func(context.Context) error, queue func(context.Context, map[string]*groupauthority.RoleNoticeSnapshot, groupauthority.RoleCommandInfo, roleEffectQueue) error) error {
	if s == nil || ctx == nil || s.authority == nil {
		return errs.ErrInternalServer.Wrap("group authority is not ready")
	}
	// Preserve the first business/authorization error on an unready runtime.
	// Even if the writer succeeds, this closure returns an error and the strong
	// authority transaction MUST abort: no legacy direct-effect commit fallback.
	if s.notices == nil || s.notices.effects == nil || s.notices.db == nil {
		_, err := s.authority.Mutation(ctx, targets, authorize, func(tx context.Context) error {
			if err := write(tx); err != nil {
				return err
			}
			return errs.ErrInternalServer.Wrap("group notification storage is not ready")
		})
		return err
	}
	effects, ok := s.notices.effects.(roleEffectQueue)
	if !ok {
		return errs.ErrInternalServer.Wrap("group notification storage is not ready")
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	commandCtx, command, err := newCommand(bounded)
	if err != nil {
		return err
	}
	ready, stop := context.WithTimeout(commandCtx, 5*time.Second)
	err = s.notices.effects.CheckReady(ready)
	stop()
	if err != nil {
		return errs.ErrInternalServer.Wrap("group notification storage is not ready")
	}
	var snapshots map[string]*groupauthority.RoleNoticeSnapshot
	commit, err := s.authority.Mutation(commandCtx, targets, func(tx context.Context) error {
		snapshots = map[string]*groupauthority.RoleNoticeSnapshot{}
		if err := authorize(tx); err != nil {
			return err
		}
		for _, target := range targets {
			if snapshots[target.GroupID] != nil {
				continue
			}
			snapshot, err := groupauthority.BeginRoleNotice(tx, s.notices.db, target.GroupID)
			if err != nil {
				return err
			}
			snapshots[target.GroupID] = snapshot
		}
		return nil
	}, func(tx context.Context) error {
		if err := write(tx); err != nil {
			return err
		}
		for _, snapshot := range snapshots {
			if err := snapshot.Finish(tx, s.notices.db); err != nil {
				return err
			}
		}
		return queue(tx, snapshots, command, effects)
	})
	if err != nil {
		if errors.Is(err, groupauthority.ErrCommitUnconfirmed) {
			return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay")
		}
		return err
	}
	if commit == nil {
		return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay")
	}
	// Business, notification and after-callback obligations are already durable.
	// A projection failure must retain normal write ACK, not invite RPC replay.
	if s.invalidate != nil {
		flush, stop := context.WithTimeout(context.WithoutCancel(commandCtx), 5*time.Second)
		err = commit.Flush(flush, s.invalidate)
		stop()
	}
	if s.invalidate == nil || err != nil {
		log.ZWarn(commandCtx, "group projection recovery pending", nil, "commandID", command.ID, "action", command.Action)
	}
	return nil
}

type roleQueueTurn struct {
	command  groupauthority.RoleCommandInfo
	previous groupeffects.Frozen
}

func (t *roleQueueTurn) order() groupeffects.RoleEffectOrder {
	return groupeffects.RoleEffectOrder{CommandID: t.command.ID, Epoch: t.command.Epoch, PreviousID: t.previous.ID, PreviousPayloadSHA256: t.previous.PayloadSHA256}
}
func (s *groupServer) queueRoleCallback(ctx context.Context, q roleEffectQueue, turn *roleQueueTurn, gid string, name groupeffects.CallbackName, index int, body any) error {
	p, enabled := s.notices.roleAfter[name]
	if !enabled {
		return nil
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	f, err := groupeffects.NewCallback(roleEffectOperation(turn.command, gid, "after", index), 0, gid, turn.command.Actor, groupeffects.CallbackPayload{Name: name, EndpointBinding: p.binding, TraceOperationID: mcontext.GetOperationID(ctx), Body: raw}, turn.command.CreatedAt)
	if err != nil {
		return err
	}
	order := turn.order()
	order.After = &groupeffects.RoleAfterPolicy{ConfigurationSHA256: p.configurationSHA, TimeoutSeconds: p.policy.CallbackTimeOut, FailedContinue: p.policy.CallbackFailedContinue != nil && *p.policy.CallbackFailedContinue}
	if p.owned != nil {
		ownedQueue, ok := q.(interface {
			QueueOwnedRoleCallback(context.Context, groupeffects.Frozen, groupeffects.RoleEffectOrder, groupeffects.OwnedRoleAfterProfile) error
		})
		if !ok {
			return groupeffects.ErrUnavailable
		}
		if err = ownedQueue.QueueOwnedRoleCallback(ctx, f, order, *p.owned); err != nil {
			return err
		}
	} else if err = q.QueueRoleCallback(ctx, f, order); err != nil {
		return err
	}
	turn.previous = f
	return nil
}
func (s *groupServer) roleNoticeMember(snapshot *groupauthority.RoleNoticeSnapshot, uid string, profile *sdkws.PublicUserInfo) (*sdkws.GroupMemberFullInfo, error) {
	m, ok := snapshot.Member(uid, false)
	if !ok {
		if !authverify.IsManagerUserID(uid) {
			return nil, groupauthority.ErrInvalidScope
		}
		m.GroupID = snapshot.Group().GroupID
		m.UserID = uid
		m.RoleLevel = constant.GroupAdmin
	}
	result := convert.Db2PbGroupMember(&m)
	if !ok {
		result.AppMangerLevel = constant.AppAdmin
	}
	if profile != nil && profile.UserID == uid {
		if result.Nickname == "" {
			result.Nickname = profile.Nickname
		}
		if result.FaceURL == "" {
			result.FaceURL = profile.FaceURL
		}
	}
	return result, nil
}
func (s *groupServer) queueRoleNotice(ctx context.Context, q roleEffectQueue, turn *roleQueueTurn, snapshot *groupauthority.RoleNoticeSnapshot, uid string, kind int32, index int, actorProfile, targetProfile *sdkws.PublicUserInfo) error {
	policy, ok := s.notices.policies[roleNoticePolicyKey(kind)]
	if !ok {
		return errs.ErrInternalServer.Wrap("group notification policy is not ready")
	}
	g := snapshot.Group()
	info := s.groupDB2PB(&g, snapshot.OwnerID(), snapshot.MemberCount())
	actor, err := s.roleNoticeMember(snapshot, turn.command.Actor, actorProfile)
	if err != nil {
		return err
	}
	target, err := s.roleNoticeMember(snapshot, uid, targetProfile)
	if err != nil {
		return err
	}
	var tips any
	if kind == constant.GroupOwnerTransferredNotification {
		tips = &sdkws.GroupOwnerTransferredTips{Group: info, OpUser: actor, NewGroupOwner: target}
	} else {
		tips = &sdkws.GroupMemberInfoSetTips{Group: info, OpUser: actor, ChangedUser: target}
	}
	content, err := json.Marshal(&sdkws.NotificationElem{Detail: utils.StructToJsonString(tips)})
	if err != nil {
		return err
	}
	message := &sdkws.MsgData{SendID: turn.command.Actor, RecvID: g.GroupID, GroupID: g.GroupID, MsgFrom: constant.SysMsgType, SessionType: constant.SuperGroupChatType, ContentType: kind, Content: content, Options: config.GetOptionsByNotification(policy.notification), OfflinePushInfo: &sdkws.OfflinePushInfo{}}
	f, err := groupeffects.NewNotification(roleEffectOperation(turn.command, g.GroupID, "notice", index), 0, g.GroupID, turn.command.Actor, message, turn.command.CreatedAt)
	if err != nil {
		return err
	}
	if err = q.QueueRoleNotification(ctx, snapshot, uid, f, policy.delivery, turn.order()); err != nil {
		return err
	}
	turn.previous = f
	return nil
}
func (s *groupServer) runMemberEditEffects(ctx context.Context, req *pbgroup.SetGroupMemberInfoReq, targets []groupauthority.Target, authorize, write func(context.Context) error) error {
	frozen := proto.Clone(req).(*pbgroup.SetGroupMemberInfoReq)
	edits := make([]groupauthority.RoleEdit, 0, len(frozen.Members))
	actors := map[string]*sdkws.PublicUserInfo{}
	profiles := map[[2]string]*sdkws.PublicUserInfo{}
	for _, m := range frozen.Members {
		e := groupauthority.RoleEdit{GroupID: m.GroupID, UserID: m.UserID}
		if m.RoleLevel != nil {
			e.Role = &m.RoleLevel.Value
		}
		if m.Nickname != nil {
			e.Nickname = &m.Nickname.Value
		}
		if m.FaceURL != nil {
			e.FaceURL = &m.FaceURL.Value
		}
		if m.Ex != nil {
			e.Ex = &m.Ex.Value
		}
		edits = append(edits, e)
		if s.notices != nil {
			var err error
			if actors[m.GroupID] == nil {
				actors[m.GroupID], err = s.noticeActorProfile(ctx, m.GroupID)
				if err != nil {
					return err
				}
			}
			profiles[[2]string{m.GroupID, m.UserID}], err = s.noticeTargetProfile(ctx, m.GroupID, m.UserID)
			if err != nil {
				return err
			}
		}
	}
	return s.runRoleEffects(ctx, targets, func(c context.Context) (context.Context, groupauthority.RoleCommandInfo, error) {
		return groupauthority.NewRoleEditCommand(c, s.notices.epoch, edits)
	}, authorize, write, func(tx context.Context, snapshots map[string]*groupauthority.RoleNoticeSnapshot, command groupauthority.RoleCommandInfo, q roleEffectQueue) error {
		turn := &roleQueueTurn{command: command}
		ordinal := 0
		// Original batch order: all member notifications, then all after callbacks.
		for _, m := range frozen.Members {
			kinds := []int32{}
			if m.RoleLevel != nil {
				if m.RoleLevel.Value == constant.GroupAdmin {
					kinds = append(kinds, constant.GroupMemberSetToAdminNotification)
				} else {
					kinds = append(kinds, constant.GroupMemberSetToOrdinaryUserNotification)
				}
			}
			if m.Nickname != nil || m.FaceURL != nil || m.Ex != nil {
				kinds = append(kinds, constant.GroupMemberInfoSetNotification)
			}
			for _, kind := range kinds {
				if err := s.queueRoleNotice(tx, q, turn, snapshots[m.GroupID], m.UserID, kind, ordinal, actors[m.GroupID], profiles[[2]string{m.GroupID, m.UserID}]); err != nil {
					return err
				}
				ordinal++
			}
		}
		for i, m := range frozen.Members {
			body := callbackstruct.CallbackAfterSetGroupMemberInfoReq{CallbackCommand: callbackstruct.CallbackAfterSetGroupMemberInfoCommand, GroupID: m.GroupID, UserID: m.UserID}
			if m.Nickname != nil {
				body.Nickname = &m.Nickname.Value
			}
			if m.FaceURL != nil {
				body.FaceURL = &m.FaceURL.Value
			}
			if m.RoleLevel != nil {
				body.RoleLevel = &m.RoleLevel.Value
			}
			if m.Ex != nil {
				body.Ex = &m.Ex.Value
			}
			if err := s.queueRoleCallback(tx, q, turn, m.GroupID, groupeffects.AfterSetMember, i, body); err != nil {
				return err
			}
		}
		return nil
	})
}
func (s *groupServer) runOwnerTransferEffects(ctx context.Context, req *pbgroup.TransferGroupOwnerReq, targets []groupauthority.Target, authorize, write func(context.Context) error) error {
	frozen := proto.Clone(req).(*pbgroup.TransferGroupOwnerReq)
	var actor, target *sdkws.PublicUserInfo
	if s.notices != nil {
		var err error
		actor, err = s.noticeActorProfile(ctx, frozen.GroupID)
		if err != nil {
			return err
		}
		target, err = s.noticeTargetProfile(ctx, frozen.GroupID, frozen.NewOwnerUserID)
		if err != nil {
			return err
		}
	}
	return s.runRoleEffects(ctx, targets, func(c context.Context) (context.Context, groupauthority.RoleCommandInfo, error) {
		return groupauthority.NewOwnerTransferCommand(c, s.notices.epoch, frozen.GroupID, frozen.OldOwnerUserID, frozen.NewOwnerUserID)
	}, authorize, write, func(tx context.Context, snapshots map[string]*groupauthority.RoleNoticeSnapshot, command groupauthority.RoleCommandInfo, q roleEffectQueue) error {
		turn := &roleQueueTurn{command: command}
		body := callbackstruct.CallbackTransferGroupOwnerReq{CallbackCommand: callbackstruct.CallbackTransferGroupOwnerAfter, GroupID: frozen.GroupID, OldOwnerUserID: frozen.OldOwnerUserID, NewOwnerUserID: frozen.NewOwnerUserID}
		if err := s.queueRoleCallback(tx, q, turn, frozen.GroupID, groupeffects.AfterTransfer, 0, body); err != nil {
			return err
		}
		return s.queueRoleNotice(tx, q, turn, snapshots[frozen.GroupID], frozen.NewOwnerUserID, constant.GroupOwnerTransferredNotification, 0, actor, target)
	})
}
