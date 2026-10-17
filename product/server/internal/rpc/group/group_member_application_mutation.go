package group

import (
	"context"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

type memberApplicationQueue interface {
	membershipEffectQueue
	QueueMemberApplicationDirect(context.Context, *groupauthority.MemberApplicationScope, *sdkws.MsgData, string, string, string) error
}

func memberApplicationPolicy(kind groupauthority.MemberApplicationKind) (config.NotificationConf, error) {
	switch kind {
	case groupauthority.MemberApplicationCreated:
		return config.Config.Notification.JoinGroupApplication, nil
	case groupauthority.MemberApplicationAccepted:
		return config.Config.Notification.GroupApplicationAccepted, nil
	case groupauthority.MemberApplicationRejected:
		return config.Config.Notification.GroupApplicationRejected, nil
	default:
		return config.NotificationConf{}, groupauthority.ErrInvalidScope
	}
}

// Only the original same-TX request mutation may create these direct notices.
// The real MessageRPC consumer keeps callback/risk/MQ behavior, and ordinary
// intake inherits this producer's cut instead of refreshing it after a Clear.
func (s *groupServer) runMemberRequestMutation(ctx context.Context, gid string, ids []string, kind groupauthority.MemberApplicationKind, admitting bool, authorize, write func(context.Context) error) error {
	if ctx == nil || authorize == nil || write == nil || admitting && kind != groupauthority.MemberApplicationAccepted {
		return groupauthority.ErrInvalidScope
	}
	queue, err := s.membershipQueue()
	if err != nil {
		return err
	}
	q, ok := queue.(memberApplicationQueue)
	if !ok {
		return groupeffects.ErrUnavailable
	}
	commandCtx, command, err := groupauthority.NewMemberApplicationCommand(ctx, s.notices.epoch, gid, ids, kind)
	if err != nil {
		return err
	}
	policy, err := memberApplicationPolicy(kind)
	if err != nil {
		return err
	}
	options := config.GetOptionsByNotification(policy)
	owned := s.notices.policies[groupauthority.NoticeMuteGroup].delivery.Owned
	if owned == nil {
		return groupeffects.ErrUnavailable
	}
	serviceUser := ""
	for _, uid := range append(append([]string(nil), config.Config.IMAdmin.UserID...), config.Config.Manager.UserID...) {
		if uid != "" {
			serviceUser = uid
			break
		}
	}
	if !authverify.IsManagerUserID(serviceUser) {
		return groupeffects.ErrUnavailable
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
		profiles[uid] = &sdkws.PublicUserInfo{UserID: uid, Nickname: user.Nickname, FaceURL: user.FaceURL, Ex: user.Ex}
	}
	if profiles[command.Actor] == nil {
		actor, err := s.noticeActorProfile(commandCtx, gid)
		if err != nil {
			return err
		}
		profiles[command.Actor] = actor
	}
	var cap *groupauthority.MemberApplicationScope
	check := func(tx context.Context) error {
		cap = nil
		if err := authorize(tx); err != nil {
			return err
		}
		var err error
		cap, err = groupauthority.BeginMemberApplication(tx, s.notices.db, gid, profiles)
		return err
	}
	work := func(tx context.Context) error {
		if err := write(tx); err != nil {
			return err
		}
		if err := cap.Complete(tx, s.notices.db); err != nil {
			return err
		}
		info, err := cap.Inspect(tx, s.notices.db)
		if err != nil {
			return err
		}
		for _, applicant := range info.Applicants {
			recipients, err := cap.RecipientsFor(tx, s.notices.db, applicant)
			if err != nil {
				return err
			}
			for _, receiver := range recipients {
				message, err := cap.Message(tx, s.notices.db, applicant, receiver, options)
				if err != nil {
					return err
				}
				// Actual original principals -> normal+n pair, no guessed heads.
				if err = s.newGroups.RecordMemberApplicationSource(tx, cap, message); err != nil {
					return err
				}
				if err = q.QueueMemberApplicationDirect(tx, cap, message, serviceUser, owned.Transport.ActivationSHA256, mcontext.GetOperationID(commandCtx)); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if admitting {
		return s.runMemberAdmissionMutation(commandCtx, gid, ids, groupauthority.MemberAdmissionApprove, check, work, "", nil)
	}
	commit, err := s.authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: gid, Members: append([]string(nil), ids...)}}, check, work)
	return s.membershipCommitted(commandCtx, command, commit, err)
}
