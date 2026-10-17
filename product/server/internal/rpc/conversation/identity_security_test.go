package conversation

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
)

type historyConversationDB struct {
	controller.ConversationDatabase
	exists bool
}

func (d historyConversationDB) FindConversations(context.Context, string, []string) ([]*relation.ConversationModel, error) {
	if d.exists {
		return []*relation.ConversationModel{{ConversationID: "sg_group", OwnerUserID: "u1"}}, nil
	}
	return nil, nil
}

type groupMemberClient struct {
	group.GroupClient
	member bool
	calls  int
}

func (g *groupMemberClient) GetGroupMembersInfo(_ context.Context, req *group.GetGroupMembersInfoReq, _ ...grpc.CallOption) (*group.GetGroupMembersInfoResp, error) {
	g.calls++
	members := []*sdkws.GroupMemberFullInfo{}
	if g.member {
		members = append(members, &sdkws.GroupMemberFullInfo{UserID: req.UserIDs[0]})
	}
	return &group.GetGroupMembersInfoResp{Members: members}, nil
}
func TestConversationIdentityCannotBeForgedToClaimEncryptionKeys(t *testing.T) {
	ctx := context.WithValue(context.Background(), constant.OpUserID, "u1")
	for _, tc := range []struct {
		name                      string
		existing, member, allowed bool
	}{{"current member can create", false, true, true}, {"outsider cannot create", false, false, false}, {"former member keeps existing history preferences", true, false, true}} {
		t.Run(tc.name, func(t *testing.T) {
			client := &groupMemberClient{member: tc.member}
			server := &conversationServer{conversationDatabase: historyConversationDB{exists: tc.existing}, groupRpcClient: &rpcclient.GroupRpcClient{Client: client}}
			err := server.validateConversationIdentity(ctx, &pb.Conversation{OwnerUserID: "u1", ConversationID: "sg_group", GroupID: "group", ConversationType: constant.SuperGroupChatType})
			if (err == nil) != tc.allowed {
				t.Fatal("conversation membership result incorrect", err)
			}
			if tc.existing && client.calls != 0 {
				t.Fatal("former member preference update unnecessarily requires current membership")
			}
		})
	}
	server := &conversationServer{}
	if err := server.validateConversationIdentity(ctx, &pb.Conversation{OwnerUserID: "attacker", UserID: "u2", ConversationID: "si_u1_u2", ConversationType: constant.SingleChatType}); err == nil {
		t.Fatal("forged participant tuple accepted")
	}
}

func TestNotificationConversationTupleRemainsCompatible(t *testing.T) {
	server := &conversationServer{}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "u1")
	for _, id := range []string{"sn_service_u1", "sn_u1"} {
		if err := server.validateConversationIdentity(ctx, &pb.Conversation{OwnerUserID: "u1", UserID: "service", ConversationID: id, ConversationType: constant.NotificationChatType}); err != nil {
			t.Fatal("valid existing notification conversation rejected", err)
		}
	}
	if err := server.validateConversationIdentity(ctx, &pb.Conversation{OwnerUserID: "u1", UserID: "service", ConversationID: "sn_service_victim", ConversationType: constant.NotificationChatType}); err == nil {
		t.Fatal("foreign notification tuple accepted")
	}
}
