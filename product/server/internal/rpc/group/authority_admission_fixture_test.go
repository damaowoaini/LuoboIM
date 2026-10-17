package group

import (
	"context"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

// Only the existing fake-DB RPC write-admission tests use this runner. It is NOT
// a transaction fixture: the fake writer always returns its existing sentinel,
// so no commit, notification, cache flush or transaction claim can be made.
type admissionOnlyAuthority struct{}

func (admissionOnlyAuthority) Mutation(ctx context.Context, _ []groupauthority.Target, authorize, write func(context.Context) error) (*groupauthority.Commit, error) {
	if err := authorize(ctx); err != nil {
		return nil, err
	}
	return nil, write(ctx)
}
func newMemberEditServer(db *memberEditDB) *groupServer {
	return &groupServer{db: db, authority: admissionOnlyAuthority{}}
}
func (d *memberEditDB) TakeGroupForPermission(_ context.Context, gid string) (*relation.GroupModel, error) {
	return &relation.GroupModel{GroupID: gid, Status: constant.GroupOk}, nil
}
func (d *joinApplicantDB) TakeGroupForPermission(ctx context.Context, gid string) (*relation.GroupModel, error) {
	return d.TakeGroup(ctx, gid)
}
func (d *joinApplicantDB) TakeGroupMemberForPermission(ctx context.Context, gid, uid string) (*relation.GroupMemberModel, error) {
	return d.TakeGroupMember(ctx, gid, uid)
}
