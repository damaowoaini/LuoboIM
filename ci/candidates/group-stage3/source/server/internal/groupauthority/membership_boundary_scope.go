package groupauthority

import (
	"context"
	"sort"

	"github.com/OpenIMSDK/tools/mcontext"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// MemberRemovalScope is a minimum WRITE capability, not a notification roster
// capability. Its members come only from the original mutation's exact writer
// target, checked against the completed same-transaction membership change.
// It cannot be constructed from recipient IDs or survive this TX attempt.
type MemberRemovalScope struct {
	scope *mutationScope
	command *noticeCommand
	revision int64
	members []string
}

type MemberRemovalInfo struct {
	Command NoticeCommandInfo
	GroupRevision int64
	Members []string
}

func CompletedMemberRemoval(ctx context.Context, db *mongo.Database, gid string) (*MemberRemovalScope, error) {
	scope, err := noticeScope(ctx, db, gid)
	if err != nil { return nil, err }
	if scope.command.action != NoticeKick && scope.command.action != NoticeQuit { return nil, ErrInvalidScope }
	target := scope.targets[gid]
	notice := scope.notice
	if target == nil || target.create || target.all || len(target.members) == 0 || notice == nil || !notice.finished || notice.current(ctx, db) != nil { return nil, ErrInvalidScope }
	members := make([]string, 0, len(target.members))
	for uid := range target.members {
		if _, exists := notice.pre[uid]; !exists { return nil, ErrInvalidScope }
		if _, exists := notice.post[uid]; exists { return nil, ErrInvalidScope }
		members = append(members, uid)
	}
	if err := RequireMembers(ctx, db, gid, members); err != nil { return nil, err }
	var group bson.Raw
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Decode(&group); err != nil { return nil, err }
	revision, ok := group.Lookup(RevisionField).Int64OK()
	if !ok || revision < 1 { return nil, ErrInvalidRevision }
	sort.Strings(members)
	return &MemberRemovalScope{scope: scope, command: scope.command, revision: revision, members: members}, nil
}

func (cap *MemberRemovalScope) Inspect(ctx context.Context, db *mongo.Database) (MemberRemovalInfo, error) {
	if cap == nil || cap.scope == nil || cap.command == nil || ctx == nil || cap.command.actor != mcontext.GetOpUserID(ctx) { return MemberRemovalInfo{}, ErrInvalidScope }
	scope, err := noticeScope(ctx, db, cap.command.group)
	if err != nil { return MemberRemovalInfo{}, err }
	if scope != cap.scope || scope.command != cap.command || scope.notice == nil || !scope.notice.finished || cap.revision < 1 || len(cap.members) != len(scope.targets[cap.command.group].members) { return MemberRemovalInfo{}, ErrInvalidScope }
	if err := RequireMembers(ctx, db, cap.command.group, cap.members); err != nil { return MemberRemovalInfo{}, err }
	group, err := readNoticeGroup(ctx, db, cap.command.group)
	if err != nil { return MemberRemovalInfo{}, err }
	if group != scope.notice.postGroup { return MemberRemovalInfo{}, ErrConflict }
	var raw bson.Raw
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": cap.command.group}).Decode(&raw); err != nil { return MemberRemovalInfo{}, err }
	revision, ok := raw.Lookup(RevisionField).Int64OK()
	if !ok || revision != cap.revision { return MemberRemovalInfo{}, ErrConflict }
	remaining, err := db.Collection("group_member").CountDocuments(ctx, bson.M{"group_id": cap.command.group, "user_id": bson.M{"$in": cap.members}})
	if err != nil { return MemberRemovalInfo{}, err }
	if remaining != 0 { return MemberRemovalInfo{}, ErrConflict }
	return MemberRemovalInfo{Command: cap.command.info(), GroupRevision: cap.revision, Members: append([]string(nil), cap.members...)}, nil
}
