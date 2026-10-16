package controller

import (
	"context"
	relationtb "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

// These methods retain the caller's Mongo session and never consult cached
// membership, owner, or group status. A preflight read remains advisory; final
// authorization must run after the group's revision CAS in the same session.
func (g *groupDatabase) TakeGroupForPermission(ctx context.Context, groupID string) (*relationtb.GroupModel, error) {
	return g.groupDB.Take(ctx, groupID)
}
func (g *groupDatabase) TakeGroupMemberForPermission(ctx context.Context, groupID, userID string) (*relationtb.GroupMemberModel, error) {
	return g.groupMemberDB.Take(ctx, groupID, userID)
}
func (g *groupDatabase) TakeGroupOwnerForPermission(ctx context.Context, groupID string) (*relationtb.GroupMemberModel, error) {
	return g.groupMemberDB.TakeOwner(ctx, groupID)
}
