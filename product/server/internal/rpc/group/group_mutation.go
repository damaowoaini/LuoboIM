package group

import (
	"context"
	"errors"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

// An internal dependency boundary, not a wire capability. Runtime installs only
// groupauthority.Store; real model writers independently require its private
// strong scope. RPC unit fixtures may exercise write admission without Mongo.
type groupMutationRunner interface {
	Mutation(context.Context, []groupauthority.Target, func(context.Context) error, func(context.Context) error) (*groupauthority.Commit, error)
}

type groupIntent struct {
	actor string
	keys  []string
}

func captureGroupIntent(ctx context.Context, keys ...string) (*groupIntent, error) {
	if ctx == nil || mcontext.GetOpUserID(ctx) == "" {
		return nil, errs.ErrNoPermission.Wrap("group actor required")
	}
	return &groupIntent{actor: mcontext.GetOpUserID(ctx), keys: append([]string(nil), keys...)}, nil
}
func (i *groupIntent) check(ctx context.Context, keys ...string) error {
	if i == nil || ctx == nil || i.actor == "" || mcontext.GetOpUserID(ctx) != i.actor || len(keys) != len(i.keys) {
		return errs.ErrNoPermission.Wrap("group intent identity changed")
	}
	for n, key := range keys {
		if key != i.keys[n] {
			return errs.ErrNoPermission.Wrap("group intent target changed")
		}
	}
	return nil
}

func (s *groupServer) runGroupMutation(ctx context.Context, targets []groupauthority.Target, authorize, write func(context.Context) error) error {
	if s == nil || s.authority == nil {
		return errs.ErrInternalServer.Wrap("group authority storage is not ready")
	}
	if mcontext.GetOpUserID(ctx) == "" {
		return errs.ErrNoPermission.Wrap("group actor required")
	}
	commit, err := s.authority.Mutation(ctx, targets, authorize, write)
	if err != nil {
		switch {
		case errors.Is(err, groupauthority.ErrCommitUnconfirmed):
			return errs.ErrInternalServer.Wrap("group operation unconfirmed; do not replay")
		case errors.Is(err, groupauthority.ErrUnavailable):
			return errs.ErrInternalServer.Wrap("group authority storage is not ready")
		case errors.Is(err, groupauthority.ErrMissing):
			return errs.ErrRecordNotFound.Wrap("group not found")
		default:
			return err
		}
	}
	if commit == nil || s.invalidate == nil {
		return errs.ErrInternalServer.Wrap("group write committed; cache synchronization pending")
	}
	// A known commit is never called rolled back when the caller disconnects.
	// Invalidation has its own short finite budget; notification/after callbacks
	// below the caller's write boundary run only after this confirmed commit.
	flush, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := commit.Flush(flush, s.invalidate); err != nil {
		return errs.ErrInternalServer.Wrap("group write committed; cache synchronization pending")
	}
	return nil
}

func (s *groupServer) freshGroup(ctx context.Context, gid string, active bool) (*relation.GroupModel, error) {
	if gid == "" {
		return nil, errs.ErrArgs.Wrap("group ID required")
	}
	g, err := s.db.TakeGroupForPermission(ctx, gid)
	if err != nil {
		return nil, err
	}
	if g == nil || g.GroupID != gid {
		return nil, errs.ErrNoPermission.Wrap("invalid group permission result")
	}
	if active && g.Status == constant.GroupStatusDismissed {
		return nil, errs.ErrDismissedAlready.Wrap()
	}
	return g, nil
}
func (s *groupServer) freshMember(ctx context.Context, gid, uid string) (*relation.GroupMemberModel, error) {
	if gid == "" || uid == "" {
		return nil, errs.ErrNoPermission.Wrap("group and member required")
	}
	m, err := s.db.TakeGroupMemberForPermission(ctx, gid, uid)
	if err != nil {
		return nil, err
	}
	if m == nil || m.GroupID != gid || m.UserID != uid {
		return nil, errs.ErrNoPermission.Wrap("invalid group member permission result")
	}
	return m, nil
}
func (s *groupServer) freshAdmin(ctx context.Context, gid string, active bool) error {
	if _, err := s.freshGroup(ctx, gid, active); err != nil {
		return err
	}
	if mcontext.GetOpUserID(ctx) == "" {
		return errs.ErrNoPermission.Wrap("group actor required")
	}
	if authverify.IsAppManagerUid(ctx) {
		return nil
	}
	actor, err := s.freshMember(ctx, gid, mcontext.GetOpUserID(ctx))
	if err != nil {
		return err
	}
	if actor.RoleLevel != constant.GroupOwner && actor.RoleLevel != constant.GroupAdmin {
		return errs.ErrNoPermission.Wrap("no group owner or admin")
	}
	return nil
}
func (s *groupServer) freshOwner(ctx context.Context, gid string, active bool) error {
	if _, err := s.freshGroup(ctx, gid, active); err != nil {
		return err
	}
	if mcontext.GetOpUserID(ctx) == "" {
		return errs.ErrNoPermission.Wrap("group actor required")
	}
	owner, err := s.db.TakeGroupOwnerForPermission(ctx, gid)
	if err != nil {
		return err
	}
	if owner == nil || owner.GroupID != gid || owner.RoleLevel != constant.GroupOwner || owner.UserID == "" {
		return errs.ErrNoPermission.Wrap("invalid group owner")
	}
	if !authverify.IsAppManagerUid(ctx) && owner.UserID != mcontext.GetOpUserID(ctx) {
		return errs.ErrNoPermission.Wrap("not group owner")
	}
	return nil
}
func (s *groupServer) authorizeMemberMute(ctx context.Context, gid, uid string) error {
	if _, err := s.freshGroup(ctx, gid, true); err != nil {
		return err
	}
	member, err := s.freshMember(ctx, gid, uid)
	if err != nil {
		return err
	}
	if mcontext.GetOpUserID(ctx) == "" {
		return errs.ErrNoPermission.Wrap("group actor required")
	}
	if authverify.IsAppManagerUid(ctx) {
		return nil
	}
	actor, err := s.freshMember(ctx, gid, mcontext.GetOpUserID(ctx))
	if err != nil {
		return err
	}
	switch member.RoleLevel {
	case constant.GroupOwner:
		return errs.ErrNoPermission.Wrap("set group owner mute")
	case constant.GroupAdmin:
		if actor.RoleLevel != constant.GroupOwner {
			return errs.ErrNoPermission.Wrap("set group admin mute")
		}
	case constant.GroupOrdinaryUsers:
		if actor.RoleLevel != constant.GroupAdmin && actor.RoleLevel != constant.GroupOwner {
			return errs.ErrNoPermission.Wrap("set group ordinary users mute")
		}
	default:
		return errs.ErrNoPermission.Wrap("unknown group target role")
	}
	return nil
}
func (s *groupServer) freshMembers(ctx context.Context, gid string, ids []string) (map[string]*relation.GroupMemberModel, error) {
	allowed := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, errs.ErrNoPermission.Wrap("member identity required")
		}
		allowed[id] = true
	}
	if gid == "" || len(allowed) == 0 {
		return nil, errs.ErrNoPermission.Wrap("group and members required")
	}
	rows, err := s.db.FindGroupMembersForPermission(ctx, gid, ids)
	if err != nil {
		return nil, err
	}
	result := make(map[string]*relation.GroupMemberModel, len(rows))
	for _, row := range rows {
		if row == nil || row.GroupID != gid || !allowed[row.UserID] || result[row.UserID] != nil {
			return nil, errs.ErrNoPermission.Wrap("invalid group permission result")
		}
		result[row.UserID] = row
	}
	return result, nil
}
func (s *groupServer) authorizeKick(ctx context.Context, gid string, ids []string) error {
	if _, err := s.freshGroup(ctx, gid, true); err != nil {
		return err
	}
	actorID := mcontext.GetOpUserID(ctx)
	if actorID == "" || len(ids) == 0 {
		return errs.ErrNoPermission.Wrap("group actor and targets required")
	}
	requested := append([]string(nil), ids...)
	if !authverify.IsAppManagerUid(ctx) {
		requested = append(requested, actorID)
	}
	rows, err := s.freshMembers(ctx, gid, requested)
	if err != nil {
		return err
	}
	actor := rows[actorID]
	if !authverify.IsAppManagerUid(ctx) && actor == nil {
		return errs.ErrNoPermission.Wrap("operator not in group")
	}
	for _, uid := range ids {
		if uid == actorID {
			return errs.ErrArgs.Wrap("opUserID in KickedUserIDs")
		}
		member := rows[uid]
		if member == nil {
			return errs.ErrNoPermission.Wrap("target not in group")
		}
		if authverify.IsAppManagerUid(ctx) {
			continue
		}
		switch actor.RoleLevel {
		case constant.GroupOwner:
		case constant.GroupAdmin:
			if member.RoleLevel == constant.GroupOwner || member.RoleLevel == constant.GroupAdmin {
				return errs.ErrNoPermission.Wrap("group admins cannot remove the group owner and other admins")
			}
		default:
			return errs.ErrNoPermission.Wrap("opUserID no permission")
		}
	}
	return nil
}

// Callback fields remain supported, including nickname/Ex/avatar/mute policy.
// A newly joining ordinary user cannot acquire a privileged role through that
// callback. Explicit create-owner is the sole new-member role100 contract.
func authorizeNewMemberRoles(members []*relation.GroupMemberModel, gid string, canAssign bool, createOwner string) error {
	seen := map[string]bool{}
	for _, m := range members {
		if m == nil || m.GroupID != gid || m.UserID == "" || seen[m.UserID] {
			return errs.ErrNoPermission.Wrap("new member identity changed")
		}
		seen[m.UserID] = true
		if m.UserID == createOwner && createOwner != "" {
			if m.RoleLevel != constant.GroupOwner {
				return errs.ErrNoPermission.Wrap("create owner role changed")
			}
			continue
		}
		switch m.RoleLevel {
		case constant.GroupOrdinaryUsers:
		case constant.GroupAdmin:
			if !canAssign {
				return errs.ErrNoPermission.Wrap("ordinary member cannot assign a group role")
			}
		case constant.GroupOwner:
			return errs.ErrNoPermission.Wrap("cannot set group owner")
		default:
			return errs.ErrArgs.Wrap("invalid role level")
		}
	}
	return nil
}

func (s *groupServer) freshInviteMode(ctx context.Context, gid string) (request, mayAssign bool, err error) {
	g, err := s.freshGroup(ctx, gid, true)
	if err != nil {
		return false, false, err
	}
	if mcontext.GetOpUserID(ctx) == "" {
		return false, false, errs.ErrNoPermission.Wrap("group actor required")
	}
	if authverify.IsAppManagerUid(ctx) {
		return false, true, nil
	}
	actor, err := s.freshMember(ctx, gid, mcontext.GetOpUserID(ctx))
	if err != nil {
		return false, false, err
	}
	switch actor.RoleLevel {
	case constant.GroupOwner, constant.GroupAdmin:
		return false, true, nil
	case constant.GroupOrdinaryUsers:
		return g.NeedVerification == constant.AllNeedVerification, false, nil
	default:
		return false, false, errs.ErrNoPermission.Wrap("unknown group actor role")
	}
}
func (s *groupServer) freshJoinMode(ctx context.Context, gid, applicant string) (direct bool, err error) {
	if err := authverify.CheckAccessV3(ctx, applicant); err != nil {
		return false, err
	}
	g, err := s.freshGroup(ctx, gid, true)
	if err != nil {
		return false, err
	}
	_, err = s.freshMember(ctx, gid, applicant)
	if err == nil {
		return false, errs.ErrArgs.Wrap("already in group")
	}
	if !s.IsNotFound(err) && !errs.ErrRecordNotFound.Is(errs.Unwrap(err)) {
		return false, err
	}
	return g.NeedVerification == constant.Directly, nil
}
func newMemberIDs(members []*relation.GroupMemberModel) []string {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		if member != nil {
			ids = append(ids, member.UserID)
		}
	}
	return ids
}
