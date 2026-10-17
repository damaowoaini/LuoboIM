package group

import (
	"context"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

type groupMemberEditIdentity struct {
	actor string
	rows  [][2]string
}

func captureGroupMemberEdit(ctx context.Context, req *pbgroup.SetGroupMemberInfoReq) (*groupMemberEditIdentity, error) {
	if ctx == nil || mcontext.GetOpUserID(ctx) == "" {
		return nil, errs.ErrNoPermission.Wrap("no op user id")
	}
	if req == nil || len(req.Members) == 0 {
		return nil, errs.ErrArgs.Wrap("members empty")
	}
	identity := &groupMemberEditIdentity{actor: mcontext.GetOpUserID(ctx)}
	for _, member := range req.Members {
		if member == nil || member.GroupID == "" || member.UserID == "" {
			return nil, errs.ErrArgs.Wrap("group and member identity required")
		}
		identity.rows = append(identity.rows, [2]string{member.GroupID, member.UserID})
	}
	return identity, nil
}

func (identity *groupMemberEditIdentity) check(ctx context.Context, req *pbgroup.SetGroupMemberInfoReq) error {
	if ctx == nil || identity == nil || identity.actor == "" || mcontext.GetOpUserID(ctx) != identity.actor || req == nil || len(req.Members) != len(identity.rows) {
		return errs.ErrNoPermission.Wrap("group member edit identity changed")
	}
	for i, member := range req.Members {
		if member == nil || identity.rows[i] != [2]string{member.GroupID, member.UserID} {
			return errs.ErrNoPermission.Wrap("group member edit target changed")
		}
	}
	return nil
}

// This same field/role rule runs before callbacks, on the callback result, and
// after the group revision CAS in the final strong transaction. Calls outside
// that transaction are preflight only; they confer no reusable write authority.
func (s *groupServer) authorizeGroupMemberEdits(ctx context.Context, req *pbgroup.SetGroupMemberInfoReq) error {
	if _, err := captureGroupMemberEdit(ctx, req); err != nil {
		return err
	}
	if s == nil || s.db == nil {
		return errs.ErrNoPermission.Wrap("group permission storage unavailable")
	}
	actor := mcontext.GetOpUserID(ctx)
	manager := authverify.IsAppManagerUid(ctx)
	groups := make(map[string][]*pbgroup.SetGroupMemberInfo)
	for _, member := range req.Members {
		if member.RoleLevel != nil {
			switch member.RoleLevel.Value {
			case constant.GroupAdmin, constant.GroupOrdinaryUsers:
			case constant.GroupOwner:
				return errs.ErrNoPermission.Wrap("cannot set group owner")
			default:
				return errs.ErrArgs.Wrap("invalid role level")
			}
		}
		groups[member.GroupID] = append(groups[member.GroupID], member)
	}
	for groupID, edits := range groups {
		requested := make(map[string]bool)
		ids := make([]string, 0, len(edits)+1)
		for _, edit := range edits {
			if requested[edit.UserID] {
				return errs.ErrArgs.Wrap("duplicate group member edit")
			}
			requested[edit.UserID] = true
			ids = append(ids, edit.UserID)
		}
		if !requested[actor] {
			ids = append(ids, actor)
		}
		rows, err := s.db.FindGroupMembersForPermission(ctx, groupID, ids)
		if err != nil {
			return err
		}
		allowedIDs := make(map[string]bool, len(ids))
		for _, id := range ids {
			allowedIDs[id] = true
		}
		members := make(map[string]*relation.GroupMemberModel, len(rows))
		for _, row := range rows {
			if row == nil || row.GroupID != groupID || !allowedIDs[row.UserID] || members[row.UserID] != nil {
				return errs.ErrNoPermission.Wrap("invalid group permission result")
			}
			members[row.UserID] = row
		}
		for _, edit := range edits {
			if members[edit.UserID] == nil {
				return errs.ErrNoPermission.Wrap("target not in group")
			}
		}
		if manager {
			continue
		}
		member := members[actor]
		if member == nil {
			return errs.ErrNoPermission.Wrap("operator not in group")
		}
		switch member.RoleLevel {
		case constant.GroupOwner:
			// Preserve the existing owner's valid 20/60 assignment contract.
		case constant.GroupAdmin:
			// Preserve the existing administrator-to-member contract. A formal
			// redesign of which roles an administrator may assign is separate.
			for _, target := range members {
				if target.RoleLevel == constant.GroupOwner || target.RoleLevel == constant.GroupAdmin && target.UserID != actor {
					return errs.ErrNoPermission.Wrap("admin cannot change owner or other admin")
				}
			}
		case constant.GroupOrdinaryUsers:
			for _, edit := range edits {
				if edit.UserID != actor || members[edit.UserID].RoleLevel != constant.GroupOrdinaryUsers || edit.RoleLevel != nil && edit.RoleLevel.Value != constant.GroupOrdinaryUsers {
					return errs.ErrNoPermission.Wrap("ordinary member cannot assign a group role")
				}
			}
		default:
			return errs.ErrNoPermission.Wrap("unknown operator group role")
		}
	}
	return nil
}
