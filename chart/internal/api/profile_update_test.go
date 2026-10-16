package api

import (
	"context"
	"encoding/json"
	pb "github.com/OpenIMSDK/chat/pkg/proto/chat"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
	"net/http/httptest"
	"strings"
	"testing"
)

type profileClientFixture struct {
	pb.ChatClient
	calls int
}

func (f *profileClientFixture) UpdateUserInfo(ctx context.Context, req *pb.UpdateUserInfoReq, opts ...grpc.CallOption) (*pb.UpdateUserInfoResp, error) {
	f.calls++
	return &pb.UpdateUserInfoResp{NickName: "canonical", FaceUrl: "https://fixture.invalid/current", SyncPending: true}, nil
}
func TestProfileAPIUsesDurableRPCStatusWithoutSecondIMWrite(t *testing.T) {
	client := &profileClientFixture{}
	api := &ChatApi{chatClient: client}
	router := gin.New()
	router.POST("/user/update", api.UpdateUserInfo)
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/user/update", strings.NewReader(`{"userID":"owner","nickname":"new-name"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	var got struct {
		Code int `json:"errCode"`
		Data struct {
			Pending bool `json:"imSyncPending"`
		} `json:"data"`
	}
	if json.Unmarshal(response.Body.Bytes(), &got) != nil || got.Code != 0 || !got.Data.Pending || client.calls != 1 {
		t.Fatal("profile API lost canonical pending status")
	}
}
