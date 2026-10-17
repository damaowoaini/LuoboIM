package group

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient/notification"
)

var stateWriteAdmission = errors.New("synthetic state writer admission")

type stateAuthorityDB struct {
	controller.GroupDatabase
	status  int32
	writes  int
	members map[string]*relation.GroupMemberModel
	request *relation.GroupRequestModel
}

func newStateAuthorityDB(status int32) *stateAuthorityDB {
	d := &stateAuthorityDB{status: status, members: map[string]*relation.GroupMemberModel{}}
	for id, role := range map[string]int32{"owner": 100, "admin": 60, "member": 20} {
		d.members[id] = &relation.GroupMemberModel{GroupID: "g", UserID: id, RoleLevel: role, Nickname: id, FaceURL: "synthetic-avatar", JoinTime: time.Unix(1, 0)}
	}
	d.request = &relation.GroupRequestModel{GroupID: "g", UserID: "applicant", ReqTime: time.Unix(2, 0)}
	return d
}
func (d *stateAuthorityDB) TakeGroupForPermission(_ context.Context, gid string) (*relation.GroupModel, error) {
	if gid != "g" {
		return nil, errs.ErrRecordNotFound
	}
	return &relation.GroupModel{GroupID: gid, Status: d.status, GroupType: constant.WorkingGroup, NeedVerification: constant.Directly}, nil
}
func (d *stateAuthorityDB) TakeGroup(ctx context.Context, gid string) (*relation.GroupModel, error) {
	return d.TakeGroupForPermission(ctx, gid)
}
func (d *stateAuthorityDB) TakeGroupMemberForPermission(_ context.Context, gid, uid string) (*relation.GroupMemberModel, error) {
	if gid != "g" || d.members[uid] == nil {
		return nil, errs.ErrRecordNotFound
	}
	r := *d.members[uid]
	return &r, nil
}
func (d *stateAuthorityDB) TakeGroupMember(ctx context.Context, gid, uid string) (*relation.GroupMemberModel, error) {
	return d.TakeGroupMemberForPermission(ctx, gid, uid)
}
func (d *stateAuthorityDB) TakeGroupOwnerForPermission(ctx context.Context, gid string) (*relation.GroupMemberModel, error) {
	return d.TakeGroupMemberForPermission(ctx, gid, "owner")
}
func (d *stateAuthorityDB) TakeGroupOwner(ctx context.Context, gid string) (*relation.GroupMemberModel, error) {
	return d.TakeGroupOwnerForPermission(ctx, gid)
}
func (d *stateAuthorityDB) FindGroupMembersForPermission(ctx context.Context, gid string, ids []string) ([]*relation.GroupMemberModel, error) {
	var rows []*relation.GroupMemberModel
	for _, id := range ids {
		if r, err := d.TakeGroupMemberForPermission(ctx, gid, id); err == nil {
			rows = append(rows, r)
		}
	}
	return rows, nil
}
func (d *stateAuthorityDB) FindGroupMemberNum(context.Context, string) (uint32, error) {
	return uint32(len(d.members)), nil
}
func (d *stateAuthorityDB) GetGroupRoleLevelMemberIDs(context.Context, string, int32) ([]string, error) {
	return []string{"owner"}, nil
}
func (d *stateAuthorityDB) TakeGroupRequest(context.Context, string, string) (*relation.GroupRequestModel, error) {
	r := *d.request
	return &r, nil
}
func (d *stateAuthorityDB) UpdateGroup(context.Context, string, map[string]any) error {
	d.writes++
	return stateWriteAdmission
}
func (d *stateAuthorityDB) UpdateGroupMember(context.Context, string, string, map[string]any) error {
	d.writes++
	return stateWriteAdmission
}
func (d *stateAuthorityDB) UpdateGroupMembers(context.Context, []*relation.BatchUpdateGroupMember) error {
	d.writes++
	return stateWriteAdmission
}
func (d *stateAuthorityDB) DismissGroup(context.Context, string, bool) error {
	d.writes++
	return stateWriteAdmission
}
func (d *stateAuthorityDB) DeleteGroupMember(context.Context, string, []string) error {
	d.writes++
	return stateWriteAdmission
}
func (d *stateAuthorityDB) TransferGroupOwner(context.Context, string, string, string, int32) error {
	d.writes++
	return stateWriteAdmission
}
func (d *stateAuthorityDB) HandlerGroupRequest(context.Context, string, string, string, int32, *relation.GroupMemberModel) error {
	d.writes++
	return stateWriteAdmission
}
func (d *stateAuthorityDB) CreateGroup(context.Context, []*relation.GroupModel, []*relation.GroupMemberModel) error {
	d.writes++
	return stateWriteAdmission
}
func (d *stateAuthorityDB) CreateGroupRequest(context.Context, []*relation.GroupRequestModel) error {
	d.writes++
	return stateWriteAdmission
}
func stateServer(t *testing.T, status int32) (*groupServer, *stateAuthorityDB) {
	t.Helper()
	before := config.Config
	t.Cleanup(func() { config.Config = before })
	reflect.ValueOf(&config.Config.Callback).Elem().SetZero()
	config.Config.Manager.UserID = []string{"service"}
	config.Config.IMAdmin.UserID = nil
	d := newStateAuthorityDB(status)
	return &groupServer{db: d, authority: admissionOnlyAuthority{}, Notification: &notification.GroupNotificationSender{}, User: rpcclient.UserRpcClient{Client: &joinApplicantUserClient{}}, rdb: &joinApplicantRedis{}}, d
}

// This matrix calls real RPCs and final authorizers with an explicit fake writer
// sentinel. It is admission/state-contract coverage, not Mongo commit coverage.
func TestGroupDismissedStateCannotBeRevivedByAnyManagementRPC(t *testing.T) {
	cases := map[string]func(*groupServer, context.Context) error{
		"mute": func(s *groupServer, c context.Context) error {
			_, e := s.MuteGroup(c, &pbgroup.MuteGroupReq{GroupID: "g"})
			return e
		},
		"cancel-mute": func(s *groupServer, c context.Context) error {
			_, e := s.CancelMuteGroup(c, &pbgroup.CancelMuteGroupReq{GroupID: "g"})
			return e
		},
		"mute-member": func(s *groupServer, c context.Context) error {
			_, e := s.MuteGroupMember(c, &pbgroup.MuteGroupMemberReq{GroupID: "g", UserID: "member", MutedSeconds: 1})
			return e
		},
		"cancel-mute-member": func(s *groupServer, c context.Context) error {
			_, e := s.CancelMuteGroupMember(c, &pbgroup.CancelMuteGroupMemberReq{GroupID: "g", UserID: "member"})
			return e
		},
		"set-info": func(s *groupServer, c context.Context) error {
			_, e := s.SetGroupInfo(c, &pbgroup.SetGroupInfoReq{GroupInfoForSet: &sdkws.GroupInfoForSet{GroupID: "g", GroupName: "edited"}})
			return e
		},
		"set-member": func(s *groupServer, c context.Context) error {
			_, e := s.SetGroupMemberInfo(c, &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{{GroupID: "g", UserID: "member", Nickname: wrapperspb.String("edited")}}})
			return e
		},
		"invite": func(s *groupServer, c context.Context) error {
			_, e := s.InviteUserToGroup(c, &pbgroup.InviteUserToGroupReq{GroupID: "g", InvitedUserIDs: []string{"applicant"}})
			return e
		},
		"join": func(s *groupServer, c context.Context) error {
			_, e := s.JoinGroup(c, &pbgroup.JoinGroupReq{GroupID: "g", InviterUserID: "applicant"})
			return e
		},
		"approve": func(s *groupServer, c context.Context) error {
			_, e := s.GroupApplicationResponse(c, &pbgroup.GroupApplicationResponseReq{GroupID: "g", FromUserID: "applicant", HandleResult: constant.GroupResponseAgree})
			return e
		},
		"refuse": func(s *groupServer, c context.Context) error {
			_, e := s.GroupApplicationResponse(c, &pbgroup.GroupApplicationResponseReq{GroupID: "g", FromUserID: "applicant", HandleResult: constant.GroupResponseRefuse})
			return e
		},
		"transfer": func(s *groupServer, c context.Context) error {
			_, e := s.TransferGroupOwner(c, &pbgroup.TransferGroupOwnerReq{GroupID: "g", OldOwnerUserID: "owner", NewOwnerUserID: "member"})
			return e
		},
		"kick": func(s *groupServer, c context.Context) error {
			_, e := s.KickGroupMember(c, &pbgroup.KickGroupMemberReq{GroupID: "g", KickedUserIDs: []string{"member"}})
			return e
		},
		"repeat-soft-dismiss": func(s *groupServer, c context.Context) error {
			_, e := s.DismissGroup(c, &pbgroup.DismissGroupReq{GroupID: "g"})
			return e
		},
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			s, d := stateServer(t, constant.GroupStatusDismissed)
			e := call(s, memberEditContext("service"))
			if !errs.ErrDismissedAlready.Is(e) || d.writes != 0 {
				t.Fatalf("dismissed state escaped: writes=%d error=%v", d.writes, e)
			}
		})
	}
}
func TestGroupStatePreservesLegalCleanupAndMutedAdmin(t *testing.T) {
	cases := []struct {
		name   string
		status int32
		actor  string
		call   func(*groupServer, context.Context) error
	}{
		{"muted-admin-can-manage", constant.GroupStatusMuted, "admin", func(s *groupServer, c context.Context) error {
			_, e := s.CancelMuteGroup(c, &pbgroup.CancelMuteGroupReq{GroupID: "g"})
			return e
		}},
		{"muted-member-can-edit-own-nickname", constant.GroupStatusMuted, "member", func(s *groupServer, c context.Context) error {
			_, e := s.SetGroupMemberInfo(c, &pbgroup.SetGroupMemberInfoReq{Members: []*pbgroup.SetGroupMemberInfo{{GroupID: "g", UserID: "member", Nickname: wrapperspb.String("edited")}}})
			return e
		}},
		{"soft-dismissed-member-can-leave", constant.GroupStatusDismissed, "member", func(s *groupServer, c context.Context) error {
			_, e := s.QuitGroup(c, &pbgroup.QuitGroupReq{GroupID: "g"})
			return e
		}},
		{"soft-dismissed-owner-can-hard-cleanup", constant.GroupStatusDismissed, "service", func(s *groupServer, c context.Context) error {
			_, e := s.DismissGroup(c, &pbgroup.DismissGroupReq{GroupID: "g", DeleteMember: true})
			return e
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, d := stateServer(t, tc.status)
			ctx := memberEditContext(tc.actor)
			if tc.name == "muted-admin-can-manage" {
				ctx, s = stateNoticeAdmissionFixture(t, d, tc.status, tc.actor)
			}
			e := tc.call(s, ctx)
			if !errors.Is(e, stateWriteAdmission) || d.writes != 1 {
				t.Fatalf("legal state contract rejected: writes=%d error=%v", d.writes, e)
			}
		})
	}
}
