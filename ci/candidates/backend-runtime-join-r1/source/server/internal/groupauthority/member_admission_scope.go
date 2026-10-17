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
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type MemberAdmissionKind string

const (
	MemberAdmissionJoin    MemberAdmissionKind = "membership-join"
	MemberAdmissionInvite  MemberAdmissionKind = "membership-invite"
	MemberAdmissionApprove MemberAdmissionKind = "membership-approve"
)

type memberAdmissionKey struct{}
type memberAdmissionCommand struct {
	info    NoticeCommandInfo
	members []string
}

func NewMemberAdmissionCommand(ctx context.Context, epoch, gid string, ids []string, kind MemberAdmissionKind) (context.Context, NoticeCommandInfo, error) {
	if ctx == nil || !canonicalUUID(epoch) || !noticeIdentity(gid) || !noticeIdentity(mcontext.GetOpUserID(ctx)) || ctx.Value(memberAdmissionKey{}) != nil || len(ids) == 0 {
		return nil, NoticeCommandInfo{}, ErrInvalidScope
	}
	if kind != MemberAdmissionJoin && kind != MemberAdmissionInvite && kind != MemberAdmissionApprove {
		return nil, NoticeCommandInfo{}, ErrInvalidScope
	}
	copyIDs := append([]string(nil), ids...)
	sort.Strings(copyIDs)
	for i, id := range copyIDs {
		if !noticeIdentity(id) || i > 0 && id == copyIDs[i-1] {
			return nil, NoticeCommandInfo{}, ErrInvalidScope
		}
	}
	linked, err := admissionApplicationCommand(ctx, epoch, gid, copyIDs, kind)
	if err != nil {
		return nil, NoticeCommandInfo{}, err
	}
	if linked != nil {
		return context.WithValue(ctx, memberAdmissionKey{}, linked), linked.info, nil
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, NoticeCommandInfo{}, ErrUnavailable
	}
	command := &memberAdmissionCommand{info: NoticeCommandInfo{ID: id.String(), Epoch: epoch, Actor: mcontext.GetOpUserID(ctx), GroupID: gid, Action: NoticeAction(kind), CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}, members: copyIDs}
	return context.WithValue(ctx, memberAdmissionKey{}, command), command.info, nil
}

// No caller can construct a valid capability from a group/user tuple. Begin
// and Complete bind the same active private writer scope and actual Mongo TX.
type MemberAdmissionScope struct {
	scope               *mutationScope
	session             mongo.Session
	command             *memberAdmissionCommand
	preGroup, postGroup relation.GroupModel
	pre, post           map[string]relation.GroupMemberModel
	newMembers          []string
	groupRevision       int64
	finished            bool
}
type MemberAdmissionInfo struct {
	Command             NoticeCommandInfo
	GroupRevision       int64
	Members, NewMembers []string
}

func BeginMemberAdmission(ctx context.Context, db *mongo.Database, gid string) (*MemberAdmissionScope, error) {
	if ctx == nil || db == nil {
		return nil, ErrInvalidScope
	}
	command, ok := ctx.Value(memberAdmissionKey{}).(*memberAdmissionCommand)
	if !ok || command == nil || command.info.GroupID != gid || command.info.Actor != mcontext.GetOpUserID(ctx) || RequireMembers(ctx, db, gid, command.members) != nil {
		return nil, ErrInvalidScope
	}
	scope, ok := ctx.Value(scopeKey{}).(*mutationScope)
	if !ok || scope == nil {
		return nil, ErrInvalidScope
	}
	target := scope.targets[gid]
	if len(scope.targets) != 1 || target == nil || target.create || target.all || len(target.members) != len(command.members) {
		return nil, ErrInvalidScope
	}
	group, err := readNoticeGroup(ctx, db, gid)
	if err != nil || group.Status == constant.GroupStatusDismissed {
		return nil, ErrInvalidScope
	}
	pre, err := readRoleRoster(ctx, db, gid)
	if err != nil {
		return nil, err
	}
	return &MemberAdmissionScope{scope: scope, session: mongo.SessionFromContext(ctx), command: command, preGroup: group, pre: pre}, nil
}

func (cap *MemberAdmissionScope) current(ctx context.Context, db *mongo.Database) error {
	if cap == nil || cap.scope == nil || cap.session == nil || cap.command == nil || ctx == nil || mongo.SessionFromContext(ctx) != cap.session || cap.command.info.Actor != mcontext.GetOpUserID(ctx) || ctx.Value(memberAdmissionKey{}) != cap.command || ctx.Value(scopeKey{}) != cap.scope || RequireMembers(ctx, db, cap.command.info.GroupID, cap.command.members) != nil {
		return ErrInvalidScope
	}
	return nil
}

func (cap *MemberAdmissionScope) Complete(ctx context.Context, db *mongo.Database) error {
	if cap.current(ctx, db) != nil || cap.finished {
		return ErrInvalidScope
	}
	gid := cap.command.info.GroupID
	group, err := readNoticeGroup(ctx, db, gid)
	if err != nil {
		return err
	}
	if group != cap.preGroup {
		return ErrConflict
	}
	post, err := readRoleRoster(ctx, db, gid)
	if err != nil {
		return err
	}
	wanted := map[string]bool{}
	for _, uid := range cap.command.members {
		wanted[uid] = true
	}
	for uid, before := range cap.pre {
		after, ok := post[uid]
		if !ok || before != after {
			return ErrConflict
		}
	}
	var added []string
	for uid, after := range post {
		if _, exists := cap.pre[uid]; !exists {
			if !wanted[uid] || after.UserID != uid || after.GroupID != gid {
				return ErrConflict
			}
			added = append(added, uid)
		}
	}
	for _, uid := range cap.command.members {
		if _, ok := post[uid]; !ok {
			return ErrConflict
		}
	}
	var raw bson.Raw
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Decode(&raw); err != nil {
		return err
	}
	revision, ok := raw.Lookup(RevisionField).Int64OK()
	if !ok || revision < 1 {
		return ErrInvalidRevision
	}
	sort.Strings(added)
	cap.postGroup, cap.post, cap.newMembers, cap.groupRevision, cap.finished = group, post, added, revision, true
	return nil
}

func (cap *MemberAdmissionScope) Inspect(ctx context.Context, db *mongo.Database) (MemberAdmissionInfo, error) {
	if cap.current(ctx, db) != nil || !cap.finished {
		return MemberAdmissionInfo{}, ErrInvalidScope
	}
	group, err := readNoticeGroup(ctx, db, cap.command.info.GroupID)
	if err != nil || group != cap.postGroup {
		return MemberAdmissionInfo{}, ErrConflict
	}
	post, err := readRoleRoster(ctx, db, cap.command.info.GroupID)
	if err != nil || len(post) != len(cap.post) {
		return MemberAdmissionInfo{}, ErrConflict
	}
	for uid, member := range cap.post {
		if now, ok := post[uid]; !ok || now != member {
			return MemberAdmissionInfo{}, ErrConflict
		}
	}
	var raw bson.Raw
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": cap.command.info.GroupID}).Decode(&raw); err != nil {
		return MemberAdmissionInfo{}, err
	}
	if revision, ok := raw.Lookup(RevisionField).Int64OK(); !ok || revision != cap.groupRevision {
		return MemberAdmissionInfo{}, ErrConflict
	}
	return MemberAdmissionInfo{Command: cap.command.info, GroupRevision: cap.groupRevision, Members: append([]string(nil), cap.command.members...), NewMembers: append([]string(nil), cap.newMembers...)}, nil
}
func (cap *MemberAdmissionScope) Command() NoticeCommandInfo {
	if cap == nil || cap.command == nil {
		return NoticeCommandInfo{}
	}
	return cap.command.info
}
func (cap *MemberAdmissionScope) Group() relation.GroupModel { return cap.postGroup }
func (cap *MemberAdmissionScope) Member(uid string) (relation.GroupMemberModel, bool) {
	m, ok := cap.post[uid]
	return m, ok
}
func (cap *MemberAdmissionScope) MemberCount() uint32 { return uint32(len(cap.post)) }
func (cap *MemberAdmissionScope) OwnerID() string {
	for uid, m := range cap.post {
		if m.RoleLevel == constant.GroupOwner {
			return uid
		}
	}
	return ""
}

func (cap *MemberAdmissionScope) NotificationRecipients(ctx context.Context, db *mongo.Database, branchID, payloadSHA string, kind int32, silent bool) ([]string, string, error) {
	info, err := cap.Inspect(ctx, db)
	if err != nil {
		return nil, "", err
	}
	digest, err := hex.DecodeString(payloadSHA)
	if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != payloadSHA || !canonicalUUID(branchID) {
		return nil, "", ErrInvalidScope
	}
	expected := int32(constant.MemberEnterNotification)
	if info.Command.Action == NoticeAction(MemberAdmissionInvite) {
		expected = constant.MemberInvitedNotification
	}
	if kind != expected {
		return nil, "", ErrInvalidScope
	}
	var ids []string
	for uid, m := range cap.post {
		if silent || m.RoleLevel == constant.GroupOwner || m.RoleLevel == constant.GroupAdmin {
			ids = append(ids, uid)
		}
	}
	sort.Strings(ids)
	if len(ids) == 0 {
		return nil, "", ErrInvalidScope
	}
	body, err := json.Marshal([]any{info, branchID, payloadSHA, kind, silent, ids})
	if err != nil {
		return nil, "", ErrInvalidScope
	}
	sum := sha256.Sum256(body)
	return ids, hex.EncodeToString(sum[:]), nil
}
