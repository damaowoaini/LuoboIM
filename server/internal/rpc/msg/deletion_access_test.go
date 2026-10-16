package msg

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pc "github.com/OpenIMSDK/protocol/conversation"
	pg "github.com/OpenIMSDK/protocol/group"
	pm "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
)

type deletionConversationFixture struct {
	pc.ConversationClient
	row *pc.Conversation
}

func (f *deletionConversationFixture) GetConversation(context.Context, *pc.GetConversationReq, ...grpc.CallOption) (*pc.GetConversationResp, error) {
	return &pc.GetConversationResp{Conversation: f.row}, nil
}

type deletionGroupFixture struct {
	pg.GroupClient
	role int32
}

func (f deletionGroupFixture) GetGroupMembersInfo(_ context.Context, req *pg.GetGroupMembersInfoReq, _ ...grpc.CallOption) (*pg.GetGroupMembersInfoResp, error) {
	return &pg.GetGroupMembersInfoResp{Members: []*sdkws.GroupMemberFullInfo{{UserID: req.UserIDs[0], GroupID: req.GroupID, RoleLevel: f.role}}}, nil
}

type deletionStoreFixture struct {
	controller.CommonMsgDatabase
	messages                     []*sdkws.MsgData
	physical, personal, globally int
}

func (f *deletionStoreFixture) GetMsgBySeqs(_ context.Context, _, _ string, seqs []int64) (int64, int64, []*sdkws.MsgData, error) {
	result := []*sdkws.MsgData{}
	for _, m := range f.messages {
		for _, seq := range seqs {
			if m.Seq == seq {
				result = append(result, m)
			}
		}
	}
	return 1, 10, result, nil
}
func (f *deletionStoreFixture) DeleteMsgsPhysicalBySeqs(context.Context, string, []int64) error {
	f.physical++
	return nil
}
func (f *deletionStoreFixture) DeleteUserMsgsBySeqs(context.Context, string, string, []int64) error {
	f.personal++
	return nil
}
func (*deletionStoreFixture) GetMaxSeqs(_ context.Context, ids []string) (map[string]int64, error) {
	result := map[string]int64{}
	for _, id := range ids {
		result[id] = 10
	}
	return result, nil
}
func (f *deletionStoreFixture) SetMinSeqs(context.Context, map[string]int64) error {
	f.globally++
	return nil
}
func (f *deletionStoreFixture) SetUserConversationsMinSeqs(context.Context, string, map[string]int64) error {
	f.personal++
	return nil
}
func (*deletionStoreFixture) UserSetHasReadSeqs(context.Context, string, map[string]int64) error {
	return nil
}

func deletionFixture(t *testing.T) (*msgServer, *deletionConversationFixture, *deletionStoreFixture) {
	t.Helper()
	oldManager, oldAdmin := config.Config.Manager.UserID, config.Config.IMAdmin.UserID
	config.Config.Manager.UserID, config.Config.IMAdmin.UserID = nil, []string{"fixture-service"}
	t.Cleanup(func() { config.Config.Manager.UserID, config.Config.IMAdmin.UserID = oldManager, oldAdmin })
	conversation := &deletionConversationFixture{row: &pc.Conversation{OwnerUserID: "owner", ConversationID: "sg_group", ConversationType: constant.SuperGroupChatType, GroupID: "group"}}
	store := &deletionStoreFixture{messages: []*sdkws.MsgData{{Seq: 1, SendID: "owner", GroupID: "group"}}}
	notifications := rpcclient.NewNotificationSender(rpcclient.WithLocalSendMsg(func(context.Context, *pm.SendMsgReq) (*pm.SendMsgResp, error) { return &pm.SendMsgResp{}, nil }))
	return &msgServer{Conversation: &rpcclient.ConversationRpcClient{Client: conversation}, MsgDatabase: store, Group: &rpcclient.GroupRpcClient{Client: deletionGroupFixture{role: constant.GroupOrdinaryUsers}}, notificationSender: notifications}, conversation, store
}

func TestMessageDeletionCannotUseOwnActorToDeleteForeignConversation(t *testing.T) {
	for _, shared := range []bool{false, true} {
		s, conversation, store := deletionFixture(t)
		conversation.row.OwnerUserID = "victim"
		req := &pm.DeleteMsgsReq{UserID: "owner", ConversationID: "sg_group", Seqs: []int64{1}, DeleteSyncOpt: &pm.DeleteSyncOpt{IsSyncOther: shared}}
		if _, err := s.DeleteMsgs(receiptActor("owner"), req); err == nil || store.personal != 0 || store.physical != 0 {
			t.Fatal("foreign conversation deletion accepted", shared, err)
		}
		if _, err := s.ClearConversationsMsg(receiptActor("owner"), &pm.ClearConversationsMsgReq{UserID: "owner", ConversationIDs: []string{"sg_group"}, DeleteSyncOpt: req.DeleteSyncOpt}); err == nil || store.globally != 0 || store.personal != 0 {
			t.Fatal("foreign conversation clearing accepted", shared, err)
		}
	}
}

func TestSharedMessageDeletionRequiresSenderOrAuthorizedGroupManager(t *testing.T) {
	for _, scenario := range []struct {
		name, sender, actor string
		role                int32
		allowed             bool
	}{
		{"original sender", "owner", "owner", constant.GroupOrdinaryUsers, true},
		{"ordinary member other sender", "peer", "owner", constant.GroupOrdinaryUsers, false},
		{"group manager", "peer", "owner", constant.GroupAdmin, true},
		{"service manager", "peer", "fixture-service", constant.GroupOrdinaryUsers, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, _, store := deletionFixture(t)
			store.messages[0].SendID = scenario.sender
			s.Group.Client = deletionGroupFixture{role: scenario.role}
			_, err := s.DeleteMsgs(receiptActor(scenario.actor), &pm.DeleteMsgsReq{UserID: "owner", ConversationID: "sg_group", Seqs: []int64{1}, DeleteSyncOpt: &pm.DeleteSyncOpt{IsSyncOther: true}})
			if (err == nil) != scenario.allowed || (store.physical == 1) != scenario.allowed {
				t.Fatal("shared deletion permission mismatch", err, store.physical)
			}
		})
	}
}

func TestGroupClearRequiresManagementAndPersonalClearRemainsAvailable(t *testing.T) {
	s, conversation, store := deletionFixture(t)
	request := &pm.ClearConversationsMsgReq{UserID: "owner", ConversationIDs: []string{"sg_group"}, DeleteSyncOpt: &pm.DeleteSyncOpt{IsSyncOther: true}}
	if _, err := s.ClearConversationsMsg(receiptActor("owner"), request); err == nil || store.globally != 0 {
		t.Fatal("ordinary member cleared entire group's shared history")
	}
	request.DeleteSyncOpt.IsSyncOther = false
	if _, err := s.ClearConversationsMsg(receiptActor("owner"), request); err != nil || store.personal != 1 {
		t.Fatal("personal group history clearing regressed", err)
	}
	s.Group.Client = deletionGroupFixture{role: constant.GroupAdmin}
	request.DeleteSyncOpt.IsSyncOther = true
	if _, err := s.ClearConversationsMsg(receiptActor("owner"), request); err != nil || store.globally != 1 {
		t.Fatal("authorized group management clear blocked", err)
	}
	conversation.row.ConversationID = "sg_someone-else"
	if _, err := s.ClearConversationsMsg(receiptActor("owner"), request); err == nil {
		t.Fatal("forged conversation participant tuple accepted")
	}
}
