package groupauthority

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
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

// NoticeAction is an internal action policy, not an incoming recipient list.
// The first slice connects the two group status RPCs. Kick/quit policies are
// implemented here for their next real-RPC slice, not claimed wired by this file.
type NoticeAction string

const (
	NoticeMuteGroup   NoticeAction = "mute-group"
	NoticeUnmuteGroup NoticeAction = "unmute-group"
	NoticeKick        NoticeAction = "kick"
	NoticeQuit        NoticeAction = "quit"
)

type noticeCommandKey struct{}
type noticeCommand struct {
	id, epoch, actor, group string
	action                  NoticeAction
	at                      time.Time
}

type NoticeCommandInfo struct {
	ID, Epoch, Actor, GroupID string
	Action                    NoticeAction
	CreatedAt                 time.Time
}

// NewNoticeCommand creates one server intent before any external callback or
// transaction retry. An untrusted wire operationID cannot choose this identity.
func NewNoticeCommand(ctx context.Context, epoch string, action NoticeAction, group string) (context.Context, NoticeCommandInfo, error) {
	if ctx == nil || !canonicalUUID(epoch) || !noticeIdentity(group) || !noticeIdentity(mcontext.GetOpUserID(ctx)) || ctx.Value(noticeCommandKey{}) != nil {
		return nil, NoticeCommandInfo{}, ErrInvalidScope
	}
	switch action {
	case NoticeMuteGroup, NoticeUnmuteGroup, NoticeKick, NoticeQuit:
	default:
		return nil, NoticeCommandInfo{}, ErrInvalidScope
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, NoticeCommandInfo{}, ErrUnavailable
	}
	c := &noticeCommand{id: id.String(), epoch: epoch, actor: mcontext.GetOpUserID(ctx), group: group, action: action, at: time.Now().UTC().Truncate(time.Millisecond)}
	return context.WithValue(ctx, noticeCommandKey{}, c), c.info(), nil
}

func canonicalUUID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u != uuid.Nil && u.String() == s
}
func noticeIdentity(s string) bool {
	return s != "" && len(s) <= 256 && strings.TrimSpace(s) == s && !strings.ContainsAny(s, "\x00\r\n")
}
func (c *noticeCommand) info() NoticeCommandInfo {
	return NoticeCommandInfo{ID: c.id, Epoch: c.epoch, Actor: c.actor, GroupID: c.group, Action: c.action, CreatedAt: c.at}
}
func commandForMutation(ctx context.Context, ordered []Target) (*noticeCommand, error) {
	value := ctx.Value(noticeCommandKey{})
	if value == nil {
		return nil, nil // Original callers remain outside the typed-notice lane.
	}
	c, ok := value.(*noticeCommand)
	if !ok || c == nil || c.actor != mcontext.GetOpUserID(ctx) || !canonicalUUID(c.id) || !canonicalUUID(c.epoch) || len(ordered) != 1 || ordered[0].GroupID != c.group || ordered[0].Create || ordered[0].AllMembers {
		return nil, ErrInvalidScope
	}
	if (c.action == NoticeMuteGroup || c.action == NoticeUnmuteGroup) && len(ordered[0].Members) != 0 {
		return nil, ErrInvalidScope
	}
	if (c.action == NoticeKick || c.action == NoticeQuit) && len(ordered[0].Members) == 0 {
		return nil, ErrInvalidScope
	}
	return c, nil
}

// NoticeSnapshot is a read-only capability tied to the same active Mongo
// attempt as the minimum writer scope. No constructor accepts recipient IDs.
// It never satisfies RequireMembers or RequireAllMembers.
type NoticeSnapshot struct {
	scope      *mutationScope
	command    *noticeCommand
	preGroup   relation.GroupModel
	postGroup  relation.GroupModel
	pre, post  map[string]relation.GroupMemberModel
	finished   bool
	rosterSHA  string
}

func noticeScope(ctx context.Context, db *mongo.Database, gid string) (*mutationScope, error) {
	if err := Require(ctx, db, gid); err != nil {
		return nil, err
	}
	s := ctx.Value(scopeKey{}).(*mutationScope)
	if s.command == nil || s.command.group != gid || s.command.actor != mcontext.GetOpUserID(ctx) || ctx.Value(noticeCommandKey{}) != s.command {
		return nil, ErrInvalidScope
	}
	return s, nil
}

func readNoticeGroup(ctx context.Context, db *mongo.Database, gid string) (relation.GroupModel, error) {
	var group relation.GroupModel
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Decode(&group); err != nil {
		return group, err
	}
	if group.GroupID != gid {
		return group, ErrInvalidScope
	}
	return group, nil
}
func readNoticeRoster(ctx context.Context, db *mongo.Database, gid string) (map[string]relation.GroupMemberModel, error) {
	cur, err := db.Collection("group_member").Find(ctx, bson.M{"group_id": gid}, options.Find().SetSort(bson.D{{Key: "user_id", Value: 1}}).SetLimit(50001))
	if err != nil {
		return nil, err
	}
	rows := make(map[string]relation.GroupMemberModel)
	for cur.Next(ctx) {
		var member relation.GroupMemberModel
		if err = cur.Decode(&member); err != nil {
			break
		}
		if member.GroupID != gid || !noticeIdentity(member.UserID) || len(rows) == 50000 {
			err = ErrInvalidScope
			break
		}
		if _, exists := rows[member.UserID]; exists {
			err = ErrInvalidScope
			break
		}
		rows[member.UserID] = member
	}
	if err == nil {
		err = cur.Err()
	}
	closeErr := cur.Close(ctx)
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	owners := 0
	for _, member := range rows {
		if member.RoleLevel == constant.GroupOwner { owners++ }
	}
	if len(rows) == 0 || owners > 1 {
		return nil, ErrInvalidScope
	}
	return rows, nil
}

func BeginNotice(ctx context.Context, db *mongo.Database, gid string) (*NoticeSnapshot, error) {
	scope, err := noticeScope(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	if scope.notice != nil {
		return nil, ErrInvalidScope
	}
	group, err := readNoticeGroup(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	rows, err := readNoticeRoster(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	if scope.command.action != NoticeQuit && group.Status == constant.GroupStatusDismissed {
		return nil, ErrConflict
	}
	actor := rows[scope.command.actor]
	manager := authverify.IsAppManagerUid(ctx)
	target := scope.targets[gid]
	switch scope.command.action {
	case NoticeMuteGroup, NoticeUnmuteGroup:
		if !manager && actor.RoleLevel != constant.GroupOwner && actor.RoleLevel != constant.GroupAdmin {
			return nil, ErrInvalidScope
		}
	case NoticeKick:
		for uid := range target.members {
			member, exists := rows[uid]
			if !exists || uid == scope.command.actor || !manager && actor.RoleLevel != constant.GroupOwner && (actor.RoleLevel != constant.GroupAdmin || member.RoleLevel >= constant.GroupAdmin) {
				return nil, ErrInvalidScope
			}
		}
	case NoticeQuit:
		if len(target.members) != 1 {
			return nil, ErrInvalidScope
		}
		for uid := range target.members {
			member, exists := rows[uid]
			if !exists || member.RoleLevel == constant.GroupOwner || !manager && uid != scope.command.actor {
				return nil, ErrInvalidScope
			}
		}
	}
	n := &NoticeSnapshot{scope: scope, command: scope.command, preGroup: group, pre: rows}
	scope.notice = n
	return n, nil
}

func (n *NoticeSnapshot) current(ctx context.Context, db *mongo.Database) error {
	if n == nil || n.scope == nil || n.command == nil {
		return ErrInvalidScope
	}
	scope, err := noticeScope(ctx, db, n.command.group)
	if err != nil {
		return err
	}
	if scope != n.scope || scope.notice != n || scope.command != n.command {
		return ErrInvalidScope
	}
	return nil
}

func (n *NoticeSnapshot) Finish(ctx context.Context, db *mongo.Database) error {
	if err := n.current(ctx, db); err != nil {
		return err
	}
	if n.finished {
		return ErrInvalidScope
	}
	group, err := readNoticeGroup(ctx, db, n.command.group)
	if err != nil {
		return err
	}
	rows, err := readNoticeRoster(ctx, db, n.command.group)
	if err != nil {
		return err
	}
	expectedStatus := n.preGroup.Status
	if n.command.action == NoticeMuteGroup {
		expectedStatus = constant.GroupStatusMuted
	} else if n.command.action == NoticeUnmuteGroup {
		expectedStatus = constant.GroupOk
	}
	expectedGroup := n.preGroup
	expectedGroup.Status = expectedStatus
	if group != expectedGroup {
		return ErrConflict
	}
	removed := n.scope.targets[n.command.group].members
	for uid, before := range n.pre {
		after, exists := rows[uid]
		shouldRemove := (n.command.action == NoticeKick || n.command.action == NoticeQuit) && removed[uid]
		if shouldRemove {
			if exists {
				return ErrConflict
			}
			continue
		}
		if !exists || before != after {
			return ErrConflict
		}
	}
	for uid := range rows {
		if _, exists := n.pre[uid]; !exists {
			return ErrConflict
		}
	}
	n.postGroup, n.post = group, rows
	encoded, err := json.Marshal(struct {
		Command NoticeCommandInfo
		Pre, Post map[string]relation.GroupMemberModel
	}{n.command.info(), n.pre, rows})
	if err != nil {
		return ErrInvalidScope
	}
	sum := sha256.Sum256(encoded)
	n.rosterSHA, n.finished = hex.EncodeToString(sum[:]), true
	return nil
}

func (n *NoticeSnapshot) Command() NoticeCommandInfo {
	if n == nil || n.command == nil {
		return NoticeCommandInfo{}
	}
	return n.command.info()
}
func (n *NoticeSnapshot) Group() relation.GroupModel { return n.postGroup }
func (n *NoticeSnapshot) Member(uid string, before bool) (relation.GroupMemberModel, bool) {
	rows := n.post
	if before { rows = n.pre }
	m, ok := rows[uid]
	return m, ok
}
func (n *NoticeSnapshot) MemberCount() uint32 { return uint32(len(n.post)) }
func (n *NoticeSnapshot) OwnerID() string {
	for uid, member := range n.post {
		if member.RoleLevel == constant.GroupOwner { return uid }
	}
	return ""
}

// NotificationRecipients derives the complete branch recipients. There is no
// userID input, and a binding cannot authorize any model/member/request write.
func (n *NoticeSnapshot) NotificationRecipients(ctx context.Context, db *mongo.Database, branchID, payloadSHA string, contentType int32, silent bool) ([]string, string, error) {
	if err := n.current(ctx, db); err != nil { return nil, "", err }
	rawSHA, shaErr := hex.DecodeString(payloadSHA)
	if !n.finished || !canonicalUUID(branchID) || shaErr != nil || len(rawSHA) != 32 || hex.EncodeToString(rawSHA) != payloadSHA { return nil, "", ErrInvalidScope }
	expected := map[NoticeAction]int32{NoticeMuteGroup: constant.GroupMutedNotification, NoticeUnmuteGroup: constant.GroupCancelMutedNotification, NoticeKick: constant.MemberKickedNotification, NoticeQuit: constant.MemberQuitNotification}
	if expected[n.command.action] != contentType { return nil, "", ErrInvalidScope }
	ids := make(map[string]bool)
	for uid, member := range n.post {
		if silent || n.command.action == NoticeMuteGroup || n.command.action == NoticeUnmuteGroup || member.RoleLevel == constant.GroupOwner || member.RoleLevel == constant.GroupAdmin { ids[uid] = true }
	}
	if silent && (n.command.action == NoticeKick || n.command.action == NoticeQuit) {
		for uid := range n.scope.targets[n.command.group].members { ids[uid] = true }
	}
	result := make([]string, 0, len(ids))
	for uid := range ids { result = append(result, uid) }
	sort.Strings(result)
	if len(result) == 0 { return nil, "", ErrInvalidScope }
	body, err := json.Marshal([]any{n.command.info(), branchID, payloadSHA, contentType, silent, n.rosterSHA, result})
	if err != nil { return nil, "", ErrInvalidScope }
	sum := sha256.Sum256(body)
	return result, hex.EncodeToString(sum[:]), nil
}
