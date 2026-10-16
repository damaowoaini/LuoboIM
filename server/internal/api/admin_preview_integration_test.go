package api

import (
	"context"
	"encoding/json"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/OpenIMSDK/tools/mw"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/adminauth"
	"github.com/openimsdk/open-im-server/v3/internal/admincapability"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/grpc"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

type previewConversationRPC struct {
	conversation.ConversationClient
	owner, id string
}

func (f previewConversationRPC) GetAllConversations(_ context.Context, req *conversation.GetAllConversationsReq, _ ...grpc.CallOption) (*conversation.GetAllConversationsResp, error) {
	return &conversation.GetAllConversationsResp{Conversations: []*conversation.Conversation{{OwnerUserID: f.owner, ConversationID: f.id, ConversationType: constant.SingleChatType, UserID: "zz-peer"}}}, nil
}
func (f previewConversationRPC) GetConversationList(context.Context, *conversation.GetConversationListReq, ...grpc.CallOption) (*conversation.GetConversationListResp, error) {
	return &conversation.GetConversationListResp{ConversationElems: []*conversation.ConversationElem{{ConversationID: f.id, MsgInfo: &conversation.MsgInfo{SenderName: "Peer", LatestMsgRecvTime: 1700000000001}}}}, nil
}
func TestAdminPreviewHTTPRequiresTwoRevocableCredentialsAndExplicitScope(t *testing.T) {
	uri, socket := os.Getenv("LUOBO_INTEGRATION_MONGO_URI"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if uri == "" || socket == "" {
		t.Skip("requires isolated Mongo and Redis")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") || !strings.Contains(socket, "/references/integration/redis8.2/") {
		t.Fatal("only private fixtures permitted")
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect(ctx)
	db := client.Database("luobo_preview_http_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	defer db.Drop(ctx)
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() {
		if err := rdb.Close(); err != nil {
			t.Error("preview fixture Redis close failed")
		}
	})
	service, user, administrator := "service-"+uuid.NewString(), "owner-"+uuid.NewString(), "admin-"+uuid.NewString()
	conv := "si_" + user + "_zz-peer"
	secretOld, adminsOld := config.Config.Secret, config.Config.IMAdmin.UserID
	issuerOld := config.Config.AdminDelegation.Issuer
	config.Config.Secret = strings.Repeat("test-preview-", 4)
	config.Config.IMAdmin.UserID = []string{service}
	config.Config.AdminDelegation.Issuer = "synthetic-preview-integration"
	defer func() { config.Config.Secret = secretOld; config.Config.IMAdmin.UserID = adminsOld }()
	defer func() { config.Config.AdminDelegation.Issuer = issuerOld }()
	t.Setenv("LUOBO_CHART_TOKEN_SECRET", "")
	t.Setenv("LUOBO_MESSAGE_PREVIEW_ADMIN_IDS", "[]")
	imToken := integrationServiceToken(t, service, 5)
	legacyToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, tokenverify.BuildClaims(service, 5, 1)).SignedString([]byte(config.Config.Secret))
	chartToken, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, &adminauth.Claims{UserID: administrator, UserType: 2, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}).SignedString([]byte(config.Config.Secret))
	tokens := cache.NewMsgCacheModel(rdb)
	tokens.SetTokenMapByUidPid(ctx, service, 5, map[string]int{imToken: 0, legacyToken: 0})
	chartKey := "CHAT_UID_TOKEN_STATUS:" + administrator
	rdb.HSet(ctx, chartKey, chartToken, 0)
	defer tokens.DeleteAllTokensByUidPid(ctx, service, 5)
	defer rdb.Del(ctx, chartKey, "ACCOUNT_DISABLED:"+administrator, "ACCOUNT_DISABLED:"+service)
	defer func() {
		keys, _, _ := rdb.Scan(ctx, 0, "*"+conv+"*", 100).Result()
		if len(keys) > 0 {
			rdb.Del(ctx, keys...)
		}
	}()
	if _, err := db.Collection("conversation").InsertOne(ctx, &relation.ConversationModel{OwnerUserID: user, ConversationID: conv, ConversationType: constant.SingleChatType, UserID: "zz-peer"}); err != nil {
		t.Fatal(err)
	}
	tokens.SetMaxSeq(ctx, conv, 1)
	tokens.SetMinSeq(ctx, conv, 1)
	tokens.SetMessageToCache(ctx, conv, []*sdkws.MsgData{{Seq: 1, SendID: "zz-peer", RecvID: user, ContentType: constant.Text, Content: []byte(`{"content":"read-only visible"}`)}})
	router := gin.New()
	router.Use(mw.GinParseOperationID(), admincapability.Guard(rdb, nil))
	registerAdminPreviewRoutes(router, GinParseToken(rdb), db, rdb, previewConversationRPC{owner: user, id: conv})
	call := func(operation, body, im, chart string) (int, map[string]json.RawMessage) {
		request := httptest.NewRequest("POST", "/admin/message_preview/"+operation, strings.NewReader(body))
		request.Header.Set("token", im)
		request.Header.Set(previewChartTokenHeader, chart)
		if im != "" {
			signIntegrationServiceRequest(t, rdb, request, im, []byte(body))
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		var result map[string]json.RawMessage
		if json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatal("invalid preview HTTP envelope")
		}
		var code int
		json.Unmarshal(result["errCode"], &code)
		return code, result
	}
	body, _ := json.Marshal(map[string]any{"userID": user, "conversationID": conv})
	if code, _ := call("search", string(body), legacyToken, chartToken); code == 0 {
		t.Fatal("legacy shared management token accepted even with normal Redis status")
	}
	if code, _ := call("search", string(body), imToken, ""); code == 0 {
		t.Fatal("missing chart actor credential accepted")
	}
	if code, _ := call("search", string(body), "", chartToken); code == 0 {
		t.Fatal("missing IM service token accepted")
	}
	if code, _ := call("search", string(body), imToken, chartToken); code != 1002 {
		t.Fatal("ungranted chart administrator accepted")
	}
	grant, _ := json.Marshal([]string{administrator})
	t.Setenv("LUOBO_MESSAGE_PREVIEW_ADMIN_IDS", string(grant))
	if code, result := call("search", string(body), imToken, chartToken); code != 0 || !strings.Contains(string(result["data"]), "read-only visible") {
		t.Fatal("granted audited preview failed")
	}
	if code, _ := call("conversations", `{"userID":"`+user+`","keyword":"peer"}`, imToken, chartToken); code != 0 {
		t.Fatal("read-only conversation list failed")
	}
	if code, _ := call("search", `{"userID":"victim","conversationID":"`+conv+`"}`, imToken, chartToken); code != 1002 {
		t.Fatal("target scope overridden")
	}
	if code, _ := call("search", `{"userID":"`+user+`","conversationID":"`+conv+`","adminUserID":"other"}`, imToken, chartToken); code != 1001 {
		t.Fatal("caller supplied admin identity accepted")
	}
	forged, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, &adminauth.Claims{UserID: administrator, UserType: 2, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))}}).SignedString([]byte("wrong secret"))
	if code, _ := call("search", string(body), imToken, forged); code == 0 {
		t.Fatal("forged chart token accepted")
	}
	rdb.HDel(ctx, chartKey, chartToken)
	if code, _ := call("search", string(body), imToken, chartToken); code == 0 {
		t.Fatal("revoked chart token accepted")
	}
	rdb.HSet(ctx, chartKey, chartToken, 0)
	rdb.Set(ctx, "ACCOUNT_DISABLED:"+administrator, "1", time.Minute)
	if code, _ := call("search", string(body), imToken, chartToken); code == 0 {
		t.Fatal("disabled chart administrator accepted")
	}
	if count, err := db.Collection("admin_message_preview_audit").CountDocuments(ctx, bson.M{"actor": administrator, "state": "complete"}); err != nil || count != 2 {
		t.Fatal("actual actor audit missing")
	}
	t.Log("LUOBO_ADMIN_PREVIEW_HTTP_REAL_AUTH_PASS")
}
