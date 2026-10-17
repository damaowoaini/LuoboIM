package msg

import (
	"context"
	"unicode/utf8"

	"github.com/OpenIMSDK/protocol/constant"
	pg "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
)

// The wire list can update unread/@ state, so it needs authoritative group
// membership checks even when message bodies are encrypted or opaque.
func (m *msgServer) verifyGroupMentionRecipients(ctx context.Context, message *sdkws.MsgData, role int32) error {
	if len(message.AtUserIDList) == 0 {
		return nil
	}
	if message.ContentType != constant.AtText {
		return errs.ErrArgs.Wrap("mention list on non-mention message")
	}
	wanted := map[string]bool{}
	ids := []string{}
	for _, id := range message.AtUserIDList {
		if id == constant.AtAllString {
			if role != constant.GroupOwner && role != constant.GroupAdmin {
				return errs.ErrNoPermission.Wrap("only group owner or administrator may mention everyone")
			}
			continue
		}
		if id == "" || len(id) > 512 || !utf8.ValidString(id) {
			return errs.ErrArgs.Wrap("invalid mention recipient")
		}
		if !wanted[id] {
			wanted[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	response, err := m.Group.Client.GetGroupMembersInfo(ctx, &pg.GetGroupMembersInfoReq{GroupID: message.GroupID, UserIDs: ids})
	if err != nil {
		return err
	}
	if response == nil || len(response.Members) != len(ids) {
		return errs.ErrNotInGroupYet.Wrap("mention recipient is not in this group")
	}
	found := map[string]bool{}
	for _, row := range response.Members {
		if row == nil || row.GroupID != message.GroupID || !wanted[row.UserID] || found[row.UserID] ||
			(row.RoleLevel != constant.GroupOwner && row.RoleLevel != constant.GroupAdmin && row.RoleLevel != constant.GroupOrdinaryUsers) {
			return errs.ErrNotInGroupYet.Wrap("invalid mention membership response")
		}
		found[row.UserID] = true
	}
	return nil
}
