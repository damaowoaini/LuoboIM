package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/internal/favorite"
	"google.golang.org/grpc"
)

type favoriteConversationProbe struct {
	conversation.ConversationClient
	row       *conversation.Conversation
	err       error
	owner, id string
}

func (p *favoriteConversationProbe) GetConversation(_ context.Context, req *conversation.GetConversationReq, _ ...grpc.CallOption) (*conversation.GetConversationResp, error) {
	p.owner, p.id = req.OwnerUserID, req.ConversationID
	return &conversation.GetConversationResp{Conversation: p.row}, p.err
}

type favoriteIdentityProbe struct {
	message                 *sdkws.MsgData
	err                     error
	calls                   int
	owner, conversation, id string
}

func (p *favoriteIdentityProbe) GetMsgByClientMsgIDs(_ context.Context, owner, conversation string, ids []string) (map[string]*sdkws.MsgData, error) {
	p.calls++
	p.owner, p.conversation, p.id = owner, conversation, ids[0]
	return map[string]*sdkws.MsgData{ids[0]: p.message}, p.err
}

func TestFavoriteSourceChecksExactOwnedHistoryAndAuthoritativePrivateMetadata(t *testing.T) {
	for _, tc := range []struct {
		name                string
		mutate              func(*favoriteConversationProbe, *favoriteIdentityProbe)
		allowed, dependency bool
	}{
		{"ordinary visible single", func(*favoriteConversationProbe, *favoriteIdentityProbe) {}, true, false},
		{"private source", func(_ *favoriteConversationProbe, m *favoriteIdentityProbe) {
			m.message.AttachedInfo = `{"isPrivateChat":true}`
		}, false, false},
		{"foreign conversation owner", func(c *favoriteConversationProbe, _ *favoriteIdentityProbe) { c.row.OwnerUserID = "other" }, false, false},
		{"wrong conversation record", func(c *favoriteConversationProbe, _ *favoriteIdentityProbe) { c.row.ConversationID = "different" }, false, false},
		{"deleted source", func(_ *favoriteConversationProbe, m *favoriteIdentityProbe) { m.message.Status = constant.MsgDeleted }, false, false},
		{"cleared source", func(c *favoriteConversationProbe, _ *favoriteIdentityProbe) { c.row.MinSeq = 13 }, false, false},
		{"outside user max", func(c *favoriteConversationProbe, _ *favoriteIdentityProbe) { c.row.MaxSeq = 11 }, false, false},
		{"substituted message", func(_ *favoriteConversationProbe, m *favoriteIdentityProbe) { m.message.ClientMsgID = "different" }, false, false},
		{"missing source", func(_ *favoriteConversationProbe, m *favoriteIdentityProbe) { m.message = nil }, false, false},
		{"foreign participants despite forged owned row", func(c *favoriteConversationProbe, m *favoriteIdentityProbe) {
			c.row.ConversationID = "si_other_peer"
			m.message.SendID = "other"
		}, false, false},
		{"private metadata malformed", func(_ *favoriteConversationProbe, m *favoriteIdentityProbe) {
			m.message.AttachedInfo = `{"isPrivateChat":"false"}`
		}, false, false},
		{"missing conversation", func(c *favoriteConversationProbe, _ *favoriteIdentityProbe) { c.err = errs.ErrRecordNotFound.Wrap() }, false, false},
		{"source hidden by reader", func(_ *favoriteConversationProbe, m *favoriteIdentityProbe) { m.err = errs.ErrRecordNotFound.Wrap() }, false, false},
		{"conversation dependency", func(c *favoriteConversationProbe, _ *favoriteIdentityProbe) {
			c.err = errors.New("private DB unavailable")
		}, false, true},
		{"message dependency", func(_ *favoriteConversationProbe, m *favoriteIdentityProbe) {
			m.err = errors.New("private Redis unavailable")
		}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conversations := &favoriteConversationProbe{row: &conversation.Conversation{OwnerUserID: "actor", ConversationID: "si_actor_peer", ConversationType: constant.SingleChatType, MinSeq: 10, MaxSeq: 20}}
			messages := &favoriteIdentityProbe{message: &sdkws.MsgData{SendID: "actor", RecvID: "peer", ClientMsgID: "message", SessionType: constant.SingleChatType, ContentType: constant.Text, Status: constant.MsgSendSuccessed, Seq: 12, Options: map[string]bool{constant.IsNotNotification: true}, AttachedInfo: `{"isPrivateChat":false}`}}
			tc.mutate(conversations, messages)
			id := "si_actor_peer"
			if tc.name == "foreign participants despite forged owned row" {
				id = "si_other_peer"
			}
			source := &favoriteMessageSource{conversations: conversations, messages: messages}
			allowed, err := source.Retainable(context.Background(), "actor", id, "message")
			if allowed != tc.allowed || (err != nil) != tc.dependency || conversations.owner != "actor" || conversations.id != id {
				t.Fatal("wrong authority outcome", allowed, err)
			}
			if messages.calls > 0 && (messages.owner != "actor" || messages.conversation != id || messages.id != "message") {
				t.Fatal("reader did not receive trusted exact owner/source")
			}
		})
	}
}

func TestFavoriteSourceChecksGroupIdentityAndPreservesOrdinaryEncryptedMessages(t *testing.T) {
	c := &favoriteConversationProbe{row: &conversation.Conversation{OwnerUserID: "actor", ConversationID: "sg_group", GroupID: "group", ConversationType: constant.SuperGroupChatType}}
	m := &favoriteIdentityProbe{message: &sdkws.MsgData{SendID: "peer", GroupID: "group", ClientMsgID: "message", SessionType: constant.SuperGroupChatType, ContentType: constant.Custom, Seq: 4, Options: map[string]bool{constant.IsNotNotification: true}, AttachedInfo: `{"isPrivateChat":false}`, Content: []byte("opaque encrypted content")}}
	s := &favoriteMessageSource{conversations: c, messages: m}
	if ok, err := s.Retainable(context.Background(), "actor", "sg_group", "message"); !ok || err != nil {
		t.Fatal("ordinary encrypted group message was confused with burn", err)
	}
	c.row.GroupID = "other"
	if ok, err := s.Retainable(context.Background(), "actor", "sg_group", "message"); ok || err != nil {
		t.Fatal("foreign group metadata accepted")
	}
}

type favoriteStorageProbe struct {
	calls int
	item  favorite.Favorite
}

func (p *favoriteStorageProbe) Execute(context.Context, string, string, favorite.Request, time.Time) (any, error) {
	p.calls++
	return map[string]any{"favoriteInfo": p.item}, nil
}

type favoriteRetentionDecision bool

func (p favoriteRetentionDecision) Retainable(context.Context, string, string, string) (bool, error) {
	return bool(p), nil
}

func TestFavoriteHTTPPrivateSourceCannotBeOverriddenAndLegacyReadIsRedacted(t *testing.T) {
	store := &favoriteStorageProbe{item: favorite.Favorite{FavoriteID: "retained", UserID: "actor", Title: "private title", Content: `{"attachedInfoElem":{"isPrivateChat":true},"textElem":{"content":"private body"}}`}}
	a := &favoriteAPI{service: &favorite.Service{Store: store}, retention: &favorite.RetentionGate{Source: favoriteRetentionDecision(false)}}
	call := func(operation string, body any) (int, string) {
		encoded, _ := json.Marshal(body)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/favorite/"+operation, strings.NewReader(string(encoded)))
		c.Set(constant.OpUserID, "actor")
		a.handle(operation)(c)
		var response struct {
			ErrCode int `json:"errCode"`
		}
		if json.Unmarshal(w.Body.Bytes(), &response) != nil {
			t.Fatal("invalid envelope")
		}
		return response.ErrCode, w.Body.String()
	}
	body := map[string]any{"userID": "actor", "favoriteType": 1, "sourceID": "message", "conversationID": "si_actor_peer", "content": `{"clientMsgID":"message","attachedInfoElem":{"isPrivateChat":false}}`}
	if code, _ := call("add", body); code != 1002 || store.calls != 0 {
		t.Fatal("forged false flag reached persistent favorites")
	}
	code, response := call("info", map[string]any{"userID": "actor", "favoriteID": "retained"})
	if code != 0 || !strings.Contains(response, `"retentionBlocked":true`) || strings.Contains(response, "private body") || strings.Contains(response, "private title") || store.item.Title != "private title" {
		t.Fatal("old private response leaked or silently deleted original")
	}
}
