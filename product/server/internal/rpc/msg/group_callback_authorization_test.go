package msg

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pg "github.com/OpenIMSDK/protocol/group"
	pm "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

func callbackAuthorizationFixture(t *testing.T, role int32) (*msgServer, *sendAuthorizationGroup, *pm.SendMsgReq) {
	t.Helper()
	old := config.Config.Manager.UserID
	config.Config.Manager.UserID = []string{"configured-service"}
	t.Cleanup(func() { config.Config.Manager.UserID = old })
	f := &sendAuthorizationGroup{
		info:    &sdkws.GroupInfo{GroupID: "group", GroupType: constant.SuperGroup},
		member:  &sdkws.GroupMemberFullInfo{GroupID: "group", UserID: "sender", RoleLevel: role},
		targets: map[string]bool{"member": true},
	}
	r := &pm.SendMsgReq{MsgData: &sdkws.MsgData{
		SendID: "sender", RecvID: "group", GroupID: "group", SenderPlatformID: 2,
		ClientMsgID: "client-identity", ServerMsgID: "server-identity", Content: []byte("before"),
		SessionType: constant.SuperGroupChatType, MsgFrom: constant.UserMsgType, ContentType: constant.Text,
	}}
	return &msgServer{Group: &rpcclient.GroupRpcClient{Client: f}}, f, r
}

// Fake callbacks run inside the exact production helper used immediately before
// MsgToMQ. A rejected callback must never reach the supplied MQ commit action.
func TestGroupCallbackAuthorizationBoundary(t *testing.T) {
	cases := []struct {
		name   string
		role   int32
		mutate func(*pm.SendMsgReq)
		allow  bool
	}{
		{"ordinary body edit", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.Content = []byte("edited") }, true},
		{"ordinary legal mention edit", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) {
			r.MsgData.ContentType = constant.AtText
			r.MsgData.AtUserIDList = []string{"member"}
		}, true},
		{"administrator legal everyone edit", constant.GroupAdmin, func(r *pm.SendMsgReq) {
			r.MsgData.ContentType = constant.AtText
			r.MsgData.AtUserIDList = []string{constant.AtAllString}
		}, true},
		{"ordinary text to everyone", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) {
			r.MsgData.ContentType = constant.AtText
			r.MsgData.AtUserIDList = []string{constant.AtAllString}
		}, false},
		{"ordinary text to outside member", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) {
			r.MsgData.ContentType = constant.AtText
			r.MsgData.AtUserIDList = []string{"outsider"}
		}, false},
		{"ordinary text to notification", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.ContentType = constant.GroupDismissedNotification }, false},
		{"group replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.GroupID = "outside-group" }, false},
		{"sender replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.SendID = "configured-service" }, false},
		{"receiver replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.RecvID = "outside-group" }, false},
		{"session replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.SessionType = constant.NotificationChatType }, false},
		{"source replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.MsgFrom = constant.UserMsgType + 1 }, false},
		{"device replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.SenderPlatformID = 5 }, false},
		{"client identity replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.ClientMsgID = "different-client" }, false},
		{"server identity replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.ServerMsgID = "different-server" }, false},
		{"nil message replacement", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData = nil }, false},
		{"wire mention on text", constant.GroupOrdinaryUsers, func(r *pm.SendMsgReq) { r.MsgData.AtUserIDList = []string{"member"} }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, r := callbackAuthorizationFixture(t, tc.role)
			callbacks, commits := 0, 0
			err := s.withVerifiedGroupMessage(context.Background(), r,
				func(_ context.Context, r *pm.SendMsgReq) error { callbacks++; tc.mutate(r); return nil },
				func(_ context.Context, submitted *pm.SendMsgReq) error {
					commits++
					if submitted != r {
						t.Fatal("commit did not receive the final request")
					}
					return nil
				},
			)
			if (err == nil) != tc.allow || callbacks != 1 || commits != boolCount(tc.allow) {
				t.Fatalf("allowed=%t want=%t callbacks=%d commits=%d", err == nil, tc.allow, callbacks, commits)
			}
		})
	}
}

func boolCount(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestGroupCallbackAuthorizationFailureBoundaries(t *testing.T) {
	t.Run("initial membership failure precedes callbacks", func(t *testing.T) {
		s, f, r := callbackAuthorizationFixture(t, constant.GroupOrdinaryUsers)
		f.member.UserID = "foreign"
		calls := 0
		err := s.withVerifiedGroupMessage(context.Background(), r, func(context.Context, *pm.SendMsgReq) error { calls++; return nil }, func(context.Context, *pm.SendMsgReq) error { calls++; return nil })
		if err == nil || calls != 0 {
			t.Fatalf("error=%t calls=%d", err != nil, calls)
		}
	})
	t.Run("callback failure never commits", func(t *testing.T) {
		s, _, r := callbackAuthorizationFixture(t, constant.GroupOrdinaryUsers)
		want := errors.New("synthetic callback failure")
		err := s.withVerifiedGroupMessage(context.Background(), r, func(context.Context, *pm.SendMsgReq) error { return want }, func(context.Context, *pm.SendMsgReq) error { t.Fatal("committed after callback failure"); return nil })
		if !errors.Is(err, want) {
			t.Fatal("callback error changed")
		}
	})
	t.Run("final role is rechecked", func(t *testing.T) {
		s, f, r := callbackAuthorizationFixture(t, constant.GroupAdmin)
		err := s.withVerifiedGroupMessage(context.Background(), r, func(_ context.Context, r *pm.SendMsgReq) error {
			f.member.RoleLevel = constant.GroupOrdinaryUsers
			r.MsgData.ContentType = constant.AtText
			r.MsgData.AtUserIDList = []string{constant.AtAllString}
			return nil
		}, func(context.Context, *pm.SendMsgReq) error { t.Fatal("committed with obsolete role"); return nil })
		if err == nil || f.memberReads != 2 {
			t.Fatalf("error=%t reads=%d", err != nil, f.memberReads)
		}
	})
	t.Run("commit error is not replaced", func(t *testing.T) {
		s, _, r := callbackAuthorizationFixture(t, constant.GroupOrdinaryUsers)
		want := errors.New("synthetic MQ failure")
		err := s.withVerifiedGroupMessage(context.Background(), r, func(context.Context, *pm.SendMsgReq) error { return nil }, func(context.Context, *pm.SendMsgReq) error { return want })
		if !errors.Is(err, want) {
			t.Fatal("commit error changed")
		}
	})
}

func TestGroupCallbackTrustedVerifierCompatibility(t *testing.T) {
	for _, manager := range []bool{false, true} {
		name := "previously authenticated notification"
		if manager {
			name = "configured manager"
		}
		t.Run(name, func(t *testing.T) {
			s, f, r := callbackAuthorizationFixture(t, 0)
			f.member = nil
			if manager {
				r.MsgData.SendID = "configured-service"
			} else {
				r.MsgData.ContentType = constant.GroupDismissedNotification
			}
			commits := 0
			err := s.withVerifiedGroupMessage(context.Background(), r, func(context.Context, *pm.SendMsgReq) error { return nil }, func(context.Context, *pm.SendMsgReq) error { commits++; return nil })
			if err != nil || commits != 1 || f.memberReads != 0 {
				t.Fatalf("error=%t commits=%d reads=%d", err != nil, commits, f.memberReads)
			}
		})
	}
}

func TestGroupMentionRecipientIntegrity(t *testing.T) {
	cases := []struct {
		name        string
		rows        []*sdkws.GroupMemberFullInfo
		nilResponse bool
		lookupError bool
		allow       bool
	}{
		{name: "exact response", rows: []*sdkws.GroupMemberFullInfo{{GroupID: "group", UserID: "member", RoleLevel: constant.GroupOrdinaryUsers}}, allow: true},
		{name: "wrong group", rows: []*sdkws.GroupMemberFullInfo{{GroupID: "different", UserID: "member", RoleLevel: constant.GroupOrdinaryUsers}}},
		{name: "wrong user", rows: []*sdkws.GroupMemberFullInfo{{GroupID: "group", UserID: "different", RoleLevel: constant.GroupOrdinaryUsers}}},
		{name: "unknown role", rows: []*sdkws.GroupMemberFullInfo{{GroupID: "group", UserID: "member", RoleLevel: 999}}},
		{name: "nil row", rows: []*sdkws.GroupMemberFullInfo{nil}},
		{name: "missing response", nilResponse: true},
		{name: "missing rows"},
		{name: "lookup failure", lookupError: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, f, r := callbackAuthorizationFixture(t, constant.GroupOrdinaryUsers)
			r.MsgData.ContentType = constant.AtText
			r.MsgData.AtUserIDList = []string{"member", "member"}
			f.targetLookup = func(req *pg.GetGroupMembersInfoReq) (*pg.GetGroupMembersInfoResp, error) {
				if req.GroupID != "group" || len(req.UserIDs) != 1 || req.UserIDs[0] != "member" {
					t.Fatal("membership lookup was not scoped and deduplicated")
				}
				if tc.lookupError {
					return nil, errors.New("synthetic lookup failure")
				}
				if tc.nilResponse {
					return nil, nil
				}
				return &pg.GetGroupMembersInfoResp{Members: tc.rows}, nil
			}
			err := s.messageVerification(context.Background(), r)
			if (err == nil) != tc.allow {
				t.Fatalf("allowed=%t want=%t", err == nil, tc.allow)
			}
		})
	}
	t.Run("duplicate returned identities cannot replace another target", func(t *testing.T) {
		s, f, r := callbackAuthorizationFixture(t, constant.GroupOrdinaryUsers)
		r.MsgData.ContentType = constant.AtText
		r.MsgData.AtUserIDList = []string{"member", "other"}
		f.targetLookup = func(*pg.GetGroupMembersInfoReq) (*pg.GetGroupMembersInfoResp, error) {
			return &pg.GetGroupMembersInfoResp{Members: []*sdkws.GroupMemberFullInfo{{GroupID: "group", UserID: "member", RoleLevel: constant.GroupOrdinaryUsers}, {GroupID: "group", UserID: "member", RoleLevel: constant.GroupOrdinaryUsers}}}, nil
		}
		if s.messageVerification(context.Background(), r) == nil {
			t.Fatal("duplicate response accepted")
		}
	})
}
