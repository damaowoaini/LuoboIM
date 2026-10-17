package msg

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pc "github.com/OpenIMSDK/protocol/conversation"
	pg "github.com/OpenIMSDK/protocol/group"
	pm "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
)

type receiptGroupFixture struct {
	pg.GroupClient
	members []string
}

func (f *receiptGroupFixture) GetGroupMemberUserIDs(context.Context, *pg.GetGroupMemberUserIDsReq, ...grpc.CallOption) (*pg.GetGroupMemberUserIDsResp, error) {
	return &pg.GetGroupMemberUserIDsResp{UserIDs: f.members}, nil
}

type receiptConversationFixture struct {
	pc.ConversationClient
	minimum, maximum int64
}

func (f *receiptConversationFixture) GetConversation(_ context.Context, req *pc.GetConversationReq, _ ...grpc.CallOption) (*pc.GetConversationResp, error) {
	return &pc.GetConversationResp{Conversation: &pc.Conversation{OwnerUserID: req.OwnerUserID, ConversationID: req.ConversationID, ConversationType: constant.SuperGroupChatType, GroupID: "group", MinSeq: f.minimum, MaxSeq: f.maximum}}, nil
}

type receiptMessageFixture struct {
	controller.CommonMsgDatabase
	messages map[string]*sdkws.MsgData
	reads    int
}

func (f *receiptMessageFixture) GetMsgByClientMsgIDs(context.Context, string, string, []string) (map[string]*sdkws.MsgData, error) {
	f.reads++
	return f.messages, nil
}

type receiptStoreFixture struct {
	controller.MsgGroupReadDatabase
	cohorts map[string][]string
	marks   int
	read    bool
}

func (f *receiptStoreFixture) EnsureGroupMessage(ctx context.Context, _ string, id string, users cache.GetUserIDsFunc) error {
	ids, err := users(ctx)
	f.cohorts[id] = append([]string(nil), ids...)
	return err
}
func (f *receiptStoreFixture) MarkGroupMessageRead(context.Context, string, string, string, *time.Time, cache.GetUserIDsFunc) (int32, error) {
	f.marks++
	return constant.GroupMessageReadMarked, nil
}
func (*receiptStoreFixture) GetMsgNum(context.Context, string, string) (int64, int64, error) {
	return 0, 2, nil
}
func (f *receiptStoreFixture) PageReadUserList(_ context.Context, _, _ string, read bool, _, _ int32) (int64, []*cache.UserReadTime, error) {
	f.read = read
	return 0, nil, nil
}

func newReceiptFixture() (*msgServer, *receiptGroupFixture, *receiptConversationFixture, *receiptMessageFixture, *receiptStoreFixture) {
	group := &receiptGroupFixture{members: []string{"a", "b", "reader"}}
	conversation := &receiptConversationFixture{}
	messages := &receiptMessageFixture{messages: map[string]*sdkws.MsgData{
		"m1": {ClientMsgID: "m1", SendID: "a", GroupID: "group", SessionType: constant.SuperGroupChatType, Seq: 1, ContentType: constant.Text},
		"m2": {ClientMsgID: "m2", SendID: "b", GroupID: "group", SessionType: constant.SuperGroupChatType, Seq: 2, ContentType: constant.Text},
	}}
	store := &receiptStoreFixture{cohorts: map[string][]string{}}
	return &msgServer{Group: &rpcclient.GroupRpcClient{Client: group}, Conversation: &rpcclient.ConversationRpcClient{Client: conversation}, MsgDatabase: messages, MsgGroupReadDatabase: store}, group, conversation, messages, store
}

func receiptActor(id string) context.Context {
	return context.WithValue(context.Background(), constant.OpUserID, id)
}

func TestGroupReceiptRejectsForeignActorGroupMessageAndVisibilityBeforeWrites(t *testing.T) {
	for _, name := range []string{"missing actor", "foreign owner", "outside group", "missing message", "foreign group message", "hidden old message", "hidden future message", "forged sender"} {
		t.Run(name, func(t *testing.T) {
			s, _, conversations, messages, store := newReceiptFixture()
			ctx := receiptActor("reader")
			req := &pm.MarkGroupMessageReadReq{UserID: "reader", ConversationID: "sg_group", ClientMsgs: map[string]string{"m1": "a", "m2": "b"}}
			switch name {
			case "missing actor":
				ctx = context.Background()
			case "foreign owner":
				req.UserID = "a"
			case "outside group":
				ctx = receiptActor("outsider")
				req.UserID = "outsider"
			case "missing message":
				delete(messages.messages, "m1")
			case "foreign group message":
				messages.messages["m1"].GroupID = "foreign"
			case "hidden old message":
				conversations.minimum = 2
			case "hidden future message":
				conversations.maximum = 1
			case "forged sender":
				req.ClientMsgs["m2"] = "reader"
			}
			if _, err := s.MarkGroupMessageRead(ctx, req); err == nil {
				t.Fatal("unscoped receipt write accepted")
			}
			if len(store.cohorts) != 0 || store.marks != 0 {
				t.Fatal("invalid batch caused partial receipt writes")
			}
		})
	}
	s, _, _, _, _ := newReceiptFixture()
	if _, err := s.GetGroupMessageReadNum(receiptActor("outsider"), &pm.GetGroupMessageReadNumReq{ConversationID: "sg_group", ClientMsgIDs: []string{"m1"}}); err == nil {
		t.Fatal("outsider read counts accepted")
	}
	if _, err := s.GetGroupMessageHasRead(receiptActor("outsider"), &pm.GetGroupMessageHasReadReq{ConversationID: "sg_group", ClientMsgID: "m1", Pagination: &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 10}}); err == nil {
		t.Fatal("outsider recipient list accepted")
	}
}

func TestGroupReceiptUsesStoredSenderAndIndependentAudienceAndPreservesFilterValues(t *testing.T) {
	s, group, _, _, store := newReceiptFixture()
	ctx := receiptActor("reader")
	if _, err := s.MarkGroupMessageRead(ctx, &pm.MarkGroupMessageReadReq{UserID: "reader", ConversationID: "sg_group", ClientMsgs: map[string]string{"m1": "", "m2": "b"}}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(store.cohorts["m1"], []string{"b", "reader"}) || !reflect.DeepEqual(store.cohorts["m2"], []string{"a", "reader"}) || !reflect.DeepEqual(group.members, []string{"a", "b", "reader"}) {
		t.Fatal("sender exclusion corrupted another message's recipient cohort", store.cohorts)
	}
	for _, filter := range []int32{0, 1} {
		_, err := s.GetGroupMessageHasRead(ctx, &pm.GetGroupMessageHasReadReq{ConversationID: "sg_group", ClientMsgID: "m1", Type: filter, Pagination: &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 20}})
		if err != nil || store.read != (filter == 0) {
			t.Fatal("legacy type0=read/type1=unread changed", filter, err)
		}
	}
	if _, err := s.GetGroupMessageReadNum(ctx, &pm.GetGroupMessageReadNumReq{ConversationID: "sg_group", ClientMsgIDs: []string{"m1"}}); err != nil {
		t.Fatal("omitted legacy owner must derive from authenticated actor", err)
	}
}
