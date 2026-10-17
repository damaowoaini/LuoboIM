package groupeffects

import (
	"bytes"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
)

// Canonical typed callback bodies prevent a logical destination from carrying
// another command/group through duplicate keys, unknown fields or generic maps.
// Only business targets (not informational full-group member snapshots) require
// the private member writer scope at Queue.
func canonicalCallback(gid string, p CallbackPayload) ([]byte, []string, error) {
	var value any
	var command callbackstruct.CallbackCommand
	var group string
	var targets []string
	switch p.Name {
	case AfterCreate:
		x := &callbackstruct.CallbackAfterCreateGroupReq{}
		if json.Unmarshal(p.Body, x) != nil || x.GroupInfo == nil {
			return nil, nil, ErrInvalid
		}
		command, group = x.CallbackCommand, x.GroupInfo.GroupID
		value = x
		for _, m := range x.InitMemberList {
			if m == nil {
				return nil, nil, ErrInvalid
			}
			targets = append(targets, m.UserID)
		}
		if command != callbackstruct.CallbackAfterCreateGroupCommand {
			return nil, nil, ErrInvalid
		}
	case AfterSetInfo:
		x := &callbackstruct.CallbackAfterSetGroupInfoReq{}
		if json.Unmarshal(p.Body, x) != nil {
			return nil, nil, ErrInvalid
		}
		command, group, value = x.CallbackCommand, x.GroupID, x
		if command != callbackstruct.CallbackAfterSetGroupInfoCommand {
			return nil, nil, ErrInvalid
		}
	case AfterSetMember:
		x := &callbackstruct.CallbackAfterSetGroupMemberInfoReq{}
		if json.Unmarshal(p.Body, x) != nil {
			return nil, nil, ErrInvalid
		}
		command, group, value = x.CallbackCommand, x.GroupID, x
		targets = []string{x.UserID}
		if command != callbackstruct.CallbackAfterSetGroupMemberInfoCommand {
			return nil, nil, ErrInvalid
		}
	case AfterJoin:
		x := &callbackstruct.CallbackAfterJoinGroupReq{}
		if json.Unmarshal(p.Body, x) != nil {
			return nil, nil, ErrInvalid
		}
		command, group, value = x.CallbackCommand, x.GroupID, x
		targets = []string{x.InviterUserID}
		if command != callbackstruct.CallbackAfterJoinGroupCommand {
			return nil, nil, ErrInvalid
		}
	case AfterQuit:
		x := &callbackstruct.CallbackQuitGroupReq{}
		if json.Unmarshal(p.Body, x) != nil {
			return nil, nil, ErrInvalid
		}
		command, group, value = x.CallbackCommand, x.GroupID, x
		targets = []string{x.UserID}
		if command != callbackstruct.CallbackQuitGroupCommand {
			return nil, nil, ErrInvalid
		}
	case AfterKick:
		x := &callbackstruct.CallbackKillGroupMemberReq{}
		if json.Unmarshal(p.Body, x) != nil {
			return nil, nil, ErrInvalid
		}
		command, group, value = x.CallbackCommand, x.GroupID, x
		targets = append([]string(nil), x.KickedUserIDs...)
		if command != callbackstruct.CallbackKillGroupCommand {
			return nil, nil, ErrInvalid
		}
	case AfterDismiss:
		x := &callbackstruct.CallbackDisMissGroupReq{}
		if json.Unmarshal(p.Body, x) != nil {
			return nil, nil, ErrInvalid
		}
		command, group, value = x.CallbackCommand, x.GroupID, x
		if command != callbackstruct.CallbackDisMissGroupCommand {
			return nil, nil, ErrInvalid
		}
	case AfterTransfer:
		x := &callbackstruct.CallbackTransferGroupOwnerReq{}
		if json.Unmarshal(p.Body, x) != nil {
			return nil, nil, ErrInvalid
		}
		command, group, value = x.CallbackCommand, x.GroupID, x
		targets = []string{x.OldOwnerUserID, x.NewOwnerUserID}
		if command != callbackstruct.CallbackTransferGroupOwnerAfter {
			return nil, nil, ErrInvalid
		}
	default:
		return nil, nil, ErrInvalid
	}
	if group != gid {
		return nil, nil, ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range targets {
		if !validIdentity(id) || seen[id] {
			return nil, nil, ErrInvalid
		}
		seen[id] = true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	// Caller payload is normally json.Marshal of the concrete legacy structure.
	// Require exact canonical bytes: ignored/duplicate keys cannot be hidden in
	// a hash that later decodes to another effective request.
	if !bytes.Equal(encoded, p.Body) {
		return nil, nil, ErrInvalid
	}
	return encoded, targets, nil
}
