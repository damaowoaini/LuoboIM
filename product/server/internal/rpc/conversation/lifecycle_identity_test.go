package conversation

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	pb "github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

type lifecycleConversationFixture struct {
	controller.ConversationDatabase
	row   *relation.ConversationModel
	calls int
}

func (d *lifecycleConversationFixture) FindConversations(context.Context, string, []string) ([]*relation.ConversationModel, error) {
	d.calls++
	if d.row == nil {
		return nil, nil
	}
	return []*relation.ConversationModel{d.row}, nil
}

func TestLifecyclePatchKeepsCanonicalOwnerCIDPeerAndDuration(t *testing.T) {
	for _, name := range []string{"valid_enable", "explicit_off_zero", "enabled_zero", "negative", "missing_owner", "forged_cid", "forged_peer", "stored_foreign_owner", "stored_replaced_peer", "private_only_with_legacy_invalid_periodic"} {
		t.Run(name, func(t *testing.T) {
			row := &relation.ConversationModel{OwnerUserID: "owner", ConversationID: "si_owner_peer", UserID: "peer", ConversationType: constant.SingleChatType, IsMsgDestruct: false, MsgDestructTime: 30}
			req := &pb.SetConversationsReq{UserIDs: []string{"owner"}, Conversation: &pb.ConversationReq{ConversationID: row.ConversationID, ConversationType: constant.SingleChatType, UserID: "peer", IsMsgDestruct: &wrapperspb.BoolValue{Value: true}}}
			allowed := false
			switch name {
			case "valid_enable":
				allowed = true
			case "explicit_off_zero":
				req.Conversation.IsMsgDestruct.Value = false
				req.Conversation.MsgDestructTime = &wrapperspb.Int64Value{Value: 0}
				allowed = true
			case "enabled_zero":
				req.Conversation.MsgDestructTime = &wrapperspb.Int64Value{Value: 0}
			case "negative":
				req.Conversation.MsgDestructTime = &wrapperspb.Int64Value{Value: -1}
			case "missing_owner":
				req.UserIDs = nil
			case "forged_cid":
				req.Conversation.ConversationID = "si_other_peer"
			case "forged_peer":
				req.Conversation.UserID = "replacement"
			case "stored_foreign_owner":
				row.OwnerUserID = "foreign"
			case "stored_replaced_peer":
				row.UserID = "replacement"
			case "private_only_with_legacy_invalid_periodic":
				row.IsMsgDestruct = true
				row.MsgDestructTime = 0
				req.Conversation.IsMsgDestruct = nil
				req.Conversation.IsPrivateChat = &wrapperspb.BoolValue{Value: false}
				allowed = true
			}
			db := &lifecycleConversationFixture{row: row}
			server := &conversationServer{conversationDatabase: db}
			ctx := context.WithValue(context.Background(), constant.OpUserID, "owner")
			if err := server.validateLifecycleRequest(ctx, req); (err == nil) != allowed {
				t.Fatalf("allowed=%v expected=%v error=%v", err == nil, allowed, err)
			}
		})
	}
}

func TestLifecycleFormerGroupMemberMayEditOwnPeriodicPreferenceButNotPrivateMode(t *testing.T) {
	row := &relation.ConversationModel{OwnerUserID: "owner", ConversationID: "sg_group", GroupID: "group", ConversationType: constant.SuperGroupChatType, MsgDestructTime: 604800}
	db := &lifecycleConversationFixture{row: row}
	server := &conversationServer{conversationDatabase: db}
	req := &pb.SetConversationsReq{UserIDs: []string{"owner"}, Conversation: &pb.ConversationReq{ConversationID: "sg_group", GroupID: "group", ConversationType: constant.SuperGroupChatType, IsMsgDestruct: &wrapperspb.BoolValue{Value: true}}}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "owner")
	if err := server.validateLifecycleRequest(ctx, req); err != nil {
		t.Fatal("existing owner history preference unexpectedly needs current membership", err)
	}
	req.Conversation.IsPrivateChat = &wrapperspb.BoolValue{Value: true}
	if err := server.validateLifecycleRequest(ctx, req); err == nil {
		t.Fatal("group private mode accepted")
	}
}

type deniedLifecycleTransaction struct {
	lifecycleConversationFixture
	scopes int
}

func (d *deniedLifecycleTransaction) RetentionTransaction(context.Context, func(context.Context) error) error {
	d.scopes++
	return retention.ErrUnavailable
}

func TestLifecyclePluralRequestRejectsMissingOrWeakOuterBeforeAnyPartialWork(t *testing.T) {
	for _, field := range []string{"private", "burn", "enabled", "duration", "missing_provider"} {
		t.Run(field, func(t *testing.T) {
			request := &pb.ConversationReq{ConversationID: "si_owner_peer", ConversationType: constant.SingleChatType, UserID: "peer"}
			switch field {
			case "private", "missing_provider":
				request.IsPrivateChat = &wrapperspb.BoolValue{Value: true}
			case "burn":
				request.BurnDuration = &wrapperspb.Int32Value{Value: 30}
			case "enabled":
				request.IsMsgDestruct = &wrapperspb.BoolValue{Value: true}
			case "duration":
				request.MsgDestructTime = &wrapperspb.Int64Value{Value: 3600}
			}
			db := &deniedLifecycleTransaction{}
			var provider controller.ConversationDatabase = db
			if field == "missing_provider" {
				provider = &db.lifecycleConversationFixture
			}
			server := &conversationServer{conversationDatabase: provider}
			response, err := server.SetConversations(context.Background(), &pb.SetConversationsReq{UserIDs: []string{"owner"}, Conversation: request})
			if response != nil || !errors.Is(err, retention.ErrUnavailable) || db.calls != 0 {
				t.Fatal("request performed work or ACKed before strong outer admission")
			}
			if field != "missing_provider" && db.scopes != 1 {
				t.Fatal("lifecycle request skipped outer transaction")
			}
		})
	}
}
