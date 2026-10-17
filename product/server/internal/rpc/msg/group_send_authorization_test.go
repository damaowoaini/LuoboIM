package msg

import (
	"context"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pg "github.com/OpenIMSDK/protocol/group"
	pm "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
)

// Invoke the actual verifier and actual RPC client adapters, using in-process
// GroupClient fixtures. No Redis/MQ/network, real members, or message writes.
type sendAuthorizationGroup struct {
	pg.GroupClient
	info                     *sdkws.GroupInfo
	member                   *sdkws.GroupMemberFullInfo
	memberError              error
	targets                  map[string]bool
	memberReads, targetReads int
	targetLookup             func(*pg.GetGroupMembersInfoReq) (*pg.GetGroupMembersInfoResp, error)
}

func (f *sendAuthorizationGroup) GetGroupInfoCache(context.Context, *pg.GetGroupInfoCacheReq, ...grpc.CallOption) (*pg.GetGroupInfoCacheResp, error) {
	return &pg.GetGroupInfoCacheResp{GroupInfo: f.info}, nil
}
func (f *sendAuthorizationGroup) GetGroupMemberCache(context.Context, *pg.GetGroupMemberCacheReq, ...grpc.CallOption) (*pg.GetGroupMemberCacheResp, error) {
	f.memberReads++
	return &pg.GetGroupMemberCacheResp{Member: f.member}, f.memberError
}
func (f *sendAuthorizationGroup) GetGroupMembersInfo(_ context.Context, r *pg.GetGroupMembersInfoReq, _ ...grpc.CallOption) (*pg.GetGroupMembersInfoResp, error) {
	f.targetReads++
	if f.targetLookup != nil {
		return f.targetLookup(r)
	}
	rows := []*sdkws.GroupMemberFullInfo{}
	for _, id := range r.UserIDs {
		if f.targets[id] {
			rows = append(rows, &sdkws.GroupMemberFullInfo{GroupID: r.GroupID, UserID: id, RoleLevel: constant.GroupOrdinaryUsers})
		}
	}
	return &pg.GetGroupMembersInfoResp{Members: rows}, nil
}

func TestGroupSendAuthorization(t *testing.T) {
	oldManagers := config.Config.Manager.UserID
	config.Config.Manager.UserID = []string{"configured-service"}
	t.Cleanup(func() { config.Config.Manager.UserID = oldManagers })
	cases := []struct {
		name                    string
		groupType, role, status int32
		mute                    bool
		outside                 bool
		foreignRow              bool
		at                      []string
		allow                   bool
	}{
		{name: "super group outsider is rejected", groupType: constant.SuperGroup, outside: true},
		{name: "super group individually muted member is rejected", groupType: constant.SuperGroup, role: constant.GroupOrdinaryUsers, mute: true},
		{name: "super group muted ordinary member is rejected", groupType: constant.SuperGroup, role: constant.GroupOrdinaryUsers, status: constant.GroupStatusMuted},
		{name: "ordinary member cannot mention everyone", groupType: constant.WorkingGroup, role: constant.GroupOrdinaryUsers, at: []string{constant.AtAllString}},
		{name: "administrator may mention everyone", groupType: constant.WorkingGroup, role: constant.GroupAdmin, at: []string{constant.AtAllString}, allow: true},
		{name: "owner may mention everyone", groupType: constant.WorkingGroup, role: constant.GroupOwner, at: []string{constant.AtAllString}, allow: true},
		{name: "ordinary plain message remains allowed", groupType: constant.WorkingGroup, role: constant.GroupOrdinaryUsers, allow: true},
		{name: "ordinary current member mention remains allowed", groupType: constant.WorkingGroup, role: constant.GroupOrdinaryUsers, at: []string{"member"}, allow: true},
		{name: "mention target outside this group is rejected", groupType: constant.WorkingGroup, role: constant.GroupOrdinaryUsers, at: []string{"outsider"}},
		{name: "foreign cache identity is rejected", groupType: constant.WorkingGroup, role: constant.GroupOrdinaryUsers, foreignRow: true},
		{name: "super group ordinary message remains allowed", groupType: constant.SuperGroup, role: constant.GroupOrdinaryUsers, allow: true},
		{name: "group mute still permits administrator", groupType: constant.SuperGroup, role: constant.GroupAdmin, status: constant.GroupStatusMuted, allow: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &sendAuthorizationGroup{
				info:    &sdkws.GroupInfo{GroupID: "group", GroupType: tc.groupType, Status: tc.status},
				member:  &sdkws.GroupMemberFullInfo{GroupID: "group", UserID: "sender", RoleLevel: tc.role},
				targets: map[string]bool{"member": true},
			}
			if tc.outside {
				f.memberError = errs.ErrRecordNotFound
			}
			if tc.foreignRow {
				f.member.UserID = "other-account"
			}
			if tc.mute {
				f.member.MuteEndTime = time.Now().Add(time.Hour).UnixMilli()
			}
			message := &sdkws.MsgData{SendID: "sender", GroupID: "group", SessionType: constant.SuperGroupChatType, ContentType: constant.Text, MsgFrom: constant.UserMsgType, AtUserIDList: tc.at}
			if len(tc.at) > 0 {
				message.ContentType = constant.AtText
			}
			s := &msgServer{Group: &rpcclient.GroupRpcClient{Client: f}}
			err := s.messageVerification(context.Background(), &pm.SendMsgReq{MsgData: message})
			if (err == nil) != tc.allow {
				t.Fatalf("allowed=%t want=%t memberReads=%d targetReads=%d", err == nil, tc.allow, f.memberReads, f.targetReads)
			}
		})
	}
}
