package conversation_msg

import (
	"context"
	"encoding/json"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/openim-sdk-core/v3/open_im_sdk_callback"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/ccontext"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/db"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/db/db_interface"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/db/model_struct"
	"github.com/openimsdk/openim-sdk-core/v3/sdk_struct"
	"net/http"
	"net/http/httptest"
	"testing"
)

type receiptCapture struct {
	open_im_sdk_callback.OnAdvancedMsgListener
	events []string
}

func (r *receiptCapture) OnRecvGroupReadReceipt(value string) { r.events = append(r.events, value) }
func groupTestDB(t *testing.T, base string) (context.Context, db_interface.DataBase) {
	t.Helper()
	ctx := ccontext.WithInfo(context.Background(), &ccontext.GlobalConfig{UserID: "alice", Token: "synthetic-token", IMConfig: sdk_struct.IMConfig{ApiAddr: base, AllowInsecureLoopbackForTesting: true}})
	store, err := db.NewDataBase(ctx, "alice", t.TempDir(), 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close(ctx) })
	if err := store.InsertConversation(ctx, &model_struct.LocalConversation{ConversationID: "sg_fixture", GroupID: "fixture", ConversationType: 3}); err != nil {
		t.Fatal(err)
	}
	store.GetMessage(ctx, "sg_fixture", "initialize-table")
	for _, message := range []*model_struct.LocalChatLog{{ClientMsgID: "first", SendID: "bob", AttachedInfo: `{"custom":{"keep":true},"groupHasReadInfo":{"groupMemberCount":5,"hasReadUserIDList":["bob"]}}`}, {ClientMsgID: "second", SendID: "carol", AttachedInfo: "null"}} {
		if err := store.InsertMessage(ctx, "sg_fixture", message); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, store
}
func TestCompatGroupReceiptPersistsOnlyTargetAndPreservesExtensions(t *testing.T) {
	ctx, store := groupTestDB(t, "")
	capture := &receiptCapture{}
	c := &Conversation{db: store, msgListener: func() open_im_sdk_callback.OnAdvancedMsgListener { return capture }}
	tips := &sdkws.GroupMsgReadTips{Reads: []*sdkws.GroupMsgReadTipsRead{{ConversationID: "sg_fixture", ClientMsgID: "first", ReadNum: 2, UnreadNum: 3, Users: []*sdkws.GroupMsgReadTipsUser{{UserID: "alice", ReadTime: 12}}}}}
	data, _ := json.Marshal(tips)
	envelope, _ := json.Marshal(map[string]string{"detail": string(data)})
	c.handleCompatGroupReceipt(ctx, &sdkws.MsgData{Content: envelope})
	first, _ := store.GetMessage(ctx, "sg_fixture", "first")
	second, _ := store.GetMessage(ctx, "sg_fixture", "second")
	if second.AttachedInfo != "null" {
		t.Fatal("receipt changed unrelated message")
	}
	var attached map[string]any
	json.Unmarshal([]byte(first.AttachedInfo), &attached)
	if attached["custom"].(map[string]any)["keep"] != true {
		t.Fatal("extension dropped")
	}
	group := attached["groupHasReadInfo"].(map[string]any)
	if group["hasReadCount"] != float64(2) || group["unreadCount"] != float64(3) || group["groupMemberCount"] != float64(5) || len(group["hasReadUserIDList"].([]any)) != 2 {
		t.Fatal("receipt fields lost")
	}
	var typed sdk_struct.AttachedInfoElem
	if json.Unmarshal([]byte(first.AttachedInfo), &typed) != nil || typed.GroupHasReadInfo.UnreadCount != 3 {
		t.Fatal("typed native history drops unread count")
	}
	// JSON null is a valid historical value and must not panic.
	tips.Reads[0].ClientMsgID = "second"
	data, _ = json.Marshal(tips)
	c.handleCompatGroupReceipt(ctx, &sdkws.MsgData{Content: data})
	if len(capture.events) != 2 {
		t.Fatal("receipt callback missing")
	}
}
func TestCompatGroupReadRequestUsesStoredSenderAndOffsetPaging(t *testing.T) {
	var marking map[string]any
	var pages []float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		json.NewDecoder(r.Body).Decode(&request)
		var data any
		switch r.URL.Path {
		case "/msg/mark_group_message_read":
			marking = request
			data = map[string]any{}
		case "/msg/get_group_message_has_read":
			if request["type"] != float64(0) {
				t.Error("runtime read filter reversed")
			}
			page := request["pagination"].(map[string]any)["pageNumber"].(float64)
			pages = append(pages, page)
			if page == 1 {
				data = map[string]any{"reads": []map[string]any{{"userID": "bob", "readTime": 1}, {"userID": "carol", "readTime": 2}}}
			} else {
				data = map[string]any{"reads": []map[string]any{{"userID": "dave", "readTime": 3}}}
			}
		case "/group/get_group_members_info":
			data = map[string]any{"members": []map[string]any{{"userID": "carol", "nickname": "Carol"}, {"userID": "dave", "nickname": "Dave"}}}
		default:
			t.Error("unexpected route")
		}
		json.NewEncoder(w).Encode(map[string]any{"errCode": 0, "data": data})
	}))
	defer server.Close()
	ctx, store := groupTestDB(t, server.URL)
	c := &Conversation{db: store}
	if _, err := c.CompatMarkGroupRead(ctx, "sg_fixture", []string{"first"}); err != nil {
		t.Fatal(err)
	}
	if marking["userID"] != "alice" || marking["clientMsgs"].(map[string]any)["first"] != "bob" {
		t.Fatal("actor/sender not bound")
	}
	readers, err := c.CompatGroupReaders(ctx, "sg_fixture", "first", 0, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 || len(readers) != 2 || readers[0]["userID"] != "carol" || readers[1]["userID"] != "dave" || readers[0]["readTime"] != int64(2) {
		t.Fatal("offset paging or profile metadata lost")
	}
	if _, err := c.CompatMarkGroupRead(ctx, "sg_fixture", []string{"nonexistent"}); err == nil {
		t.Fatal("unknown local message dispatched")
	}
}
