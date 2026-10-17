package groupauthority

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// RoleEdit freezes exact post-before-callback fields. It grants no write scope.
// Cross-group batches remain ONE strong transaction; there is no partial split.
type RoleEdit struct {
	GroupID, UserID       string
	Role                  *int32
	Nickname, FaceURL, Ex *string
}
type RoleCommandInfo struct {
	ID, Epoch, Actor, Action string
	CreatedAt                time.Time
}
type roleCommandKey struct{}
type roleCommand struct {
	info     RoleCommandInfo
	edits    []RoleEdit
	transfer [3]string
}
type RoleNoticeSnapshot struct {
	scope               *mutationScope
	command             *roleCommand
	gid                 string
	preGroup, postGroup relation.GroupModel
	pre, post           map[string]relation.GroupMemberModel
	finished            bool
	binding             string
}

func copyRoleEdit(in RoleEdit) RoleEdit {
	out := in
	if in.Role != nil {
		v := *in.Role
		out.Role = &v
	}
	if in.Nickname != nil {
		v := *in.Nickname
		out.Nickname = &v
	}
	if in.FaceURL != nil {
		v := *in.FaceURL
		out.FaceURL = &v
	}
	if in.Ex != nil {
		v := *in.Ex
		out.Ex = &v
	}
	return out
}
func newRoleCommand(ctx context.Context, epoch, action string) (*roleCommand, error) {
	if ctx == nil || !canonicalUUID(epoch) || !noticeIdentity(mcontext.GetOpUserID(ctx)) || ctx.Value(roleCommandKey{}) != nil || ctx.Value(noticeCommandKey{}) != nil {
		return nil, ErrInvalidScope
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrUnavailable
	}
	return &roleCommand{info: RoleCommandInfo{ID: id.String(), Epoch: epoch, Actor: mcontext.GetOpUserID(ctx), Action: action, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}}, nil
}
func NewRoleEditCommand(ctx context.Context, epoch string, edits []RoleEdit) (context.Context, RoleCommandInfo, error) {
	c, err := newRoleCommand(ctx, epoch, "edit-members")
	if err != nil {
		return nil, RoleCommandInfo{}, err
	}
	if len(edits) == 0 {
		return nil, RoleCommandInfo{}, ErrInvalidScope
	}
	seen := map[[2]string]bool{}
	for _, e := range edits {
		key := [2]string{e.GroupID, e.UserID}
		if !noticeIdentity(e.GroupID) || !noticeIdentity(e.UserID) || seen[key] || e.Role != nil && *e.Role != constant.GroupAdmin && *e.Role != constant.GroupOrdinaryUsers {
			return nil, RoleCommandInfo{}, ErrInvalidScope
		}
		seen[key] = true
		c.edits = append(c.edits, copyRoleEdit(e))
	}
	return context.WithValue(ctx, roleCommandKey{}, c), c.info, nil
}
func NewOwnerTransferCommand(ctx context.Context, epoch, gid, oldOwner, newOwner string) (context.Context, RoleCommandInfo, error) {
	c, err := newRoleCommand(ctx, epoch, "transfer-owner")
	if err != nil {
		return nil, RoleCommandInfo{}, err
	}
	if !noticeIdentity(gid) || !noticeIdentity(oldOwner) || !noticeIdentity(newOwner) || oldOwner == newOwner {
		return nil, RoleCommandInfo{}, ErrInvalidScope
	}
	c.transfer = [3]string{gid, oldOwner, newOwner}
	return context.WithValue(ctx, roleCommandKey{}, c), c.info, nil
}

// The complete command equals the minimum write set; full-roster reads never
// widen RequireMembers or grant a cross-group write capability.
func roleCommandScope(ctx context.Context, db *mongo.Database, gid string) (*mutationScope, *roleCommand, error) {
	if err := Require(ctx, db, gid); err != nil {
		return nil, nil, err
	}
	s := ctx.Value(scopeKey{}).(*mutationScope)
	c, ok := ctx.Value(roleCommandKey{}).(*roleCommand)
	if !ok || c == nil || c.info.Actor != mcontext.GetOpUserID(ctx) {
		return nil, nil, ErrInvalidScope
	}
	want := map[string]map[string]bool{}
	add := func(g, u string) {
		if want[g] == nil {
			want[g] = map[string]bool{}
		}
		want[g][u] = true
	}
	if c.info.Action == "transfer-owner" {
		add(c.transfer[0], c.transfer[1])
		add(c.transfer[0], c.transfer[2])
	} else {
		for _, e := range c.edits {
			add(e.GroupID, e.UserID)
		}
	}
	if len(want) != len(s.targets) {
		return nil, nil, ErrInvalidScope
	}
	for g, ids := range want {
		target := s.targets[g]
		if target == nil || target.create || target.all || len(ids) != len(target.members) {
			return nil, nil, ErrInvalidScope
		}
		for uid := range ids {
			if !target.members[uid] {
				return nil, nil, ErrInvalidScope
			}
		}
	}
	return s, c, nil
}

// No arbitrary member-count cap. Cursor completeness, TX deadline and Mongo's
// actual document-size limits apply, including the effect write before commit.
func readRoleRoster(ctx context.Context, db *mongo.Database, gid string) (map[string]relation.GroupMemberModel, error) {
	cur, err := db.Collection("group_member").Find(ctx, bson.M{"group_id": gid}, options.Find().SetSort(bson.D{{Key: "user_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	rows := map[string]relation.GroupMemberModel{}
	owners := 0
	for cur.Next(ctx) {
		var m relation.GroupMemberModel
		if err = cur.Decode(&m); err != nil {
			break
		}
		if m.GroupID != gid || !noticeIdentity(m.UserID) {
			err = ErrInvalidScope
			break
		}
		if _, ok := rows[m.UserID]; ok {
			err = ErrInvalidScope
			break
		}
		if m.RoleLevel != constant.GroupOwner && m.RoleLevel != constant.GroupAdmin && m.RoleLevel != constant.GroupOrdinaryUsers {
			err = ErrInvalidScope
			break
		}
		if m.RoleLevel == constant.GroupOwner {
			owners++
		}
		rows[m.UserID] = m
	}
	if err == nil {
		err = cur.Err()
	}
	closed := cur.Close(ctx)
	if err != nil {
		return nil, err
	}
	if closed != nil {
		return nil, closed
	}
	if len(rows) == 0 || owners != 1 {
		return nil, ErrInvalidScope
	}
	return rows, nil
}
func BeginRoleNotice(ctx context.Context, db *mongo.Database, gid string) (*RoleNoticeSnapshot, error) {
	s, c, err := roleCommandScope(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	g, err := readNoticeGroup(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	if g.Status == constant.GroupStatusDismissed {
		return nil, ErrConflict
	}
	rows, err := readRoleRoster(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	actor, exists := rows[c.info.Actor]
	manager := authverify.IsAppManagerUid(ctx)
	if !manager && !exists {
		return nil, ErrInvalidScope
	}
	if c.info.Action == "transfer-owner" {
		old, ok := rows[c.transfer[1]]
		next, found := rows[c.transfer[2]]
		if !ok || !found || old.RoleLevel != constant.GroupOwner || next.RoleLevel != constant.GroupAdmin && next.RoleLevel != constant.GroupOrdinaryUsers || !manager && (c.info.Actor != old.UserID || actor.RoleLevel != constant.GroupOwner) {
			return nil, ErrInvalidScope
		}
	} else {
		// Preserve ordinary self-profile and existing admin-to-member 20/60 rules.
		// Group mute/canSend is not a management permission.
		for _, e := range c.edits {
			if e.GroupID != gid {
				continue
			}
			target, ok := rows[e.UserID]
			if !ok {
				return nil, ErrInvalidScope
			}
			if manager {
				continue
			}
			switch actor.RoleLevel {
			case constant.GroupOwner:
			case constant.GroupAdmin:
				if target.RoleLevel == constant.GroupOwner || target.RoleLevel == constant.GroupAdmin && target.UserID != actor.UserID {
					return nil, ErrInvalidScope
				}
			case constant.GroupOrdinaryUsers:
				if e.UserID != actor.UserID || e.Role != nil && *e.Role != constant.GroupOrdinaryUsers {
					return nil, ErrInvalidScope
				}
			default:
				return nil, ErrInvalidScope
			}
		}
	}
	return &RoleNoticeSnapshot{scope: s, command: c, gid: gid, preGroup: g, pre: rows}, nil
}
func (n *RoleNoticeSnapshot) current(ctx context.Context, db *mongo.Database) error {
	if n == nil || n.scope == nil || n.command == nil {
		return ErrInvalidScope
	}
	s, c, err := roleCommandScope(ctx, db, n.gid)
	if err != nil {
		return err
	}
	if s != n.scope || c != n.command {
		return ErrInvalidScope
	}
	return nil
}
func (n *RoleNoticeSnapshot) Finish(ctx context.Context, db *mongo.Database) error {
	if err := n.current(ctx, db); err != nil {
		return err
	}
	if n.finished {
		return ErrInvalidScope
	}
	g, err := readNoticeGroup(ctx, db, n.gid)
	if err != nil {
		return err
	}
	if g != n.preGroup {
		return ErrConflict
	}
	rows, err := readRoleRoster(ctx, db, n.gid)
	if err != nil {
		return err
	}
	if len(rows) != len(n.pre) {
		return ErrConflict
	}
	for uid, before := range n.pre {
		want := before
		if n.command.info.Action == "transfer-owner" {
			if uid == n.command.transfer[1] {
				want.RoleLevel = n.pre[n.command.transfer[2]].RoleLevel
			}
			if uid == n.command.transfer[2] {
				want.RoleLevel = constant.GroupOwner
			}
		} else {
			for _, e := range n.command.edits {
				if e.GroupID != n.gid || e.UserID != uid {
					continue
				}
				if e.Role != nil {
					want.RoleLevel = *e.Role
				}
				if e.Nickname != nil {
					want.Nickname = *e.Nickname
				}
				if e.FaceURL != nil {
					want.FaceURL = *e.FaceURL
				}
				if e.Ex != nil {
					want.Ex = *e.Ex
				}
			}
		}
		after, ok := rows[uid]
		if !ok || after != want {
			return ErrConflict
		}
	}
	n.postGroup, n.post = g, rows
	body, err := json.Marshal([]any{n.command.info, n.command.edits, n.command.transfer, n.gid, n.pre, rows})
	if err != nil {
		return ErrInvalidScope
	}
	hash := sha256.Sum256(body)
	n.binding = hex.EncodeToString(hash[:])
	n.finished = true
	return nil
}
func (n *RoleNoticeSnapshot) Command() RoleCommandInfo {
	if n == nil || n.command == nil {
		return RoleCommandInfo{}
	}
	return n.command.info
}
func (n *RoleNoticeSnapshot) Group() relation.GroupModel { return n.postGroup }
func (n *RoleNoticeSnapshot) Member(uid string, before bool) (relation.GroupMemberModel, bool) {
	rows := n.post
	if before {
		rows = n.pre
	}
	m, ok := rows[uid]
	return m, ok
}
func (n *RoleNoticeSnapshot) OwnerID() string {
	for uid, m := range n.post {
		if m.RoleLevel == constant.GroupOwner {
			return uid
		}
	}
	return ""
}
func (n *RoleNoticeSnapshot) MemberCount() uint32 { return uint32(len(n.post)) }

// Action-derived read permission, never an arbitrary recipient-list grant.
func (n *RoleNoticeSnapshot) NotificationRecipients(ctx context.Context, db *mongo.Database, uid, branch, payload string, kind int32) ([]string, string, error) {
	if err := n.current(ctx, db); err != nil {
		return nil, "", err
	}
	raw, err := hex.DecodeString(payload)
	if !n.finished || !canonicalUUID(branch) || err != nil || len(raw) != 32 || hex.EncodeToString(raw) != payload {
		return nil, "", ErrInvalidScope
	}
	valid := false
	if n.command.info.Action == "transfer-owner" {
		valid = uid == n.command.transfer[2] && kind == constant.GroupOwnerTransferredNotification
	} else {
		for _, e := range n.command.edits {
			if e.GroupID != n.gid || e.UserID != uid {
				continue
			}
			valid = kind == constant.GroupMemberInfoSetNotification && (e.Nickname != nil || e.FaceURL != nil || e.Ex != nil) || e.Role != nil && (*e.Role == constant.GroupAdmin && kind == constant.GroupMemberSetToAdminNotification || *e.Role == constant.GroupOrdinaryUsers && kind == constant.GroupMemberSetToOrdinaryUserNotification)
		}
	}
	if !valid {
		return nil, "", ErrInvalidScope
	}
	ids := make([]string, 0, len(n.post))
	for id := range n.post {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	body, _ := json.Marshal([]any{n.command.info, n.gid, uid, branch, payload, kind, n.binding, ids})
	hash := sha256.Sum256(body)
	return ids, hex.EncodeToString(hash[:]), nil
}
