package conversation

import (
	"context"
	"github.com/OpenIMSDK/protocol/constant"
	pc "github.com/OpenIMSDK/protocol/conversation"
	pm "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	pu "github.com/OpenIMSDK/protocol/user"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"testing"
)

type summaryConversationDB struct {
	controller.ConversationDatabase
	rows []*relation.ConversationModel
}

func (d summaryConversationDB) FindConversations(context.Context, string, []string) ([]*relation.ConversationModel, error) {
	return d.rows, nil
}

type summaryMessageRPC struct{ pm.MsgClient }

func (summaryMessageRPC) GetMaxSeqs(context.Context, *pm.GetMaxSeqsReq, ...grpc.CallOption) (*pm.SeqsInfoResp, error) {
	return &pm.SeqsInfoResp{MaxSeqs: map[string]int64{"si_a_u1": 1, "si_b_u1": 1}}, nil
}
func (summaryMessageRPC) GetHasReadSeqs(context.Context, *pm.GetHasReadSeqsReq, ...grpc.CallOption) (*pm.SeqsInfoResp, error) {
	return &pm.SeqsInfoResp{MaxSeqs: map[string]int64{}}, nil
}
func (summaryMessageRPC) GetMsgByConversationIDs(context.Context, *pm.GetMsgByConversationIDsReq, ...grpc.CallOption) (*pm.GetMsgByConversationIDsResp, error) {
	return &pm.GetMsgByConversationIDsResp{MsgDatas: map[string]*sdkws.MsgData{"si_a_u1": {SendID: "a", RecvID: "u1", SessionType: constant.SingleChatType, SendTime: 123, KeyVersion: 1, Content: []byte{255, 0, 128}}, "si_b_u1": {SendID: "b", RecvID: "u1", SessionType: constant.SingleChatType, SendTime: 123, Content: []byte(`{"content":"plain"}`)}}}, nil
}

type summaryUserRPC struct{ pu.UserClient }

func (summaryUserRPC) GetDesignateUsers(context.Context, *pu.GetDesignateUsersReq, ...grpc.CallOption) (*pu.GetDesignateUsersResp, error) {
	return &pu.GetDesignateUsersResp{UsersInfo: []*sdkws.UserInfo{{UserID: "a", Nickname: "A"}}}, nil
}
func TestConversationSummaryRetainsTimeTiesEmptyRowsAndOpaqueEncryption(t *testing.T) {
	rows := []*relation.ConversationModel{{OwnerUserID: "u1", ConversationID: "si_a_u1", UserID: "a"}, {OwnerUserID: "u1", ConversationID: "si_b_u1", UserID: "b"}, {OwnerUserID: "u1", ConversationID: "si_empty_u1", UserID: "empty"}}
	server := &conversationServer{conversationDatabase: summaryConversationDB{rows: rows}, msgRpcClient: &rpcclient.MessageRpcClient{Client: summaryMessageRPC{}}, user: &rpcclient.UserRpcClient{Client: summaryUserRPC{}}}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "u1")
	result, err := server.GetConversationList(ctx, &pc.GetConversationListReq{UserID: "u1", ConversationIDs: []string{"si_a_u1", "si_b_u1", "si_empty_u1"}})
	if err != nil || len(result.ConversationElems) != 3 {
		t.Fatal("same-time or empty conversation was dropped", err)
	}
	if result.ConversationElems[0].ConversationID != "si_a_u1" || result.ConversationElems[1].ConversationID != "si_b_u1" || result.ConversationElems[1].MsgInfo.SenderName != "b" {
		t.Fatal("summary stable sort/deleted-user fallback invalid")
	}
	if result.ConversationElems[0].MsgInfo.Content != "" {
		t.Fatal("opaque ciphertext put into versionless protobuf string")
	}
	if _, err := proto.Marshal(result); err != nil {
		t.Fatal("encrypted conversation summary could not cross protobuf wire")
	}
}
