package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/proto/common"
	pb "github.com/OpenIMSDK/chat/pkg/proto/office"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

type tagOfficeClientStub struct{ pb.OfficeClient }

func (tagOfficeClientStub) SendMsg2Tag(context.Context, *pb.SendMsg2TagReq, ...grpc.CallOption) (*pb.SendMsg2TagResp, error) {
	return &pb.SendMsg2TagResp{SendUser: &common.UserPublicInfo{UserID: "u1"}, RecvGroupIDs: []string{"private-group"}}, nil
}

type tagCallerStub struct {
	apicall.CallerInterface
	members []string
	sent    int
}

func (*tagCallerStub) ImAdminTokenWithDefaultAdmin(context.Context) (string, error) {
	return "admin-token", nil
}
func (s *tagCallerStub) FindGroupMemberUserIDs(context.Context, string) ([]string, error) {
	return s.members, nil
}
func (s *tagCallerStub) SendMsg(context.Context, *apicall.SendMsgReq) (*apicall.SendMsgResp, error) {
	s.sent++
	return &apicall.SendMsgResp{}, nil
}

func TestTagAPISecurityGroupRequiresMembershipOrAdmin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		role    int32
		members []string
		allowed bool
	}{
		{"nonmember", constant.NormalUser, []string{"u2"}, false},
		{"member", constant.NormalUser, []string{"u1", "u2"}, true},
		{"authorized admin", constant.AdminUser, []string{"u2"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &tagCallerStub{members: tc.members}
			office := &Office{officeClient: tagOfficeClientStub{}, imApiCaller: caller}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/office/tag/send", strings.NewReader(`{"content":"{}","senderPlatformID":2}`))
			SetToken(c, "u1", tc.role)
			office.SendMsg2Tag(c)
			var result struct {
				ErrCode int `json:"errCode"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if (result.ErrCode == 0) != tc.allowed || (caller.sent > 0) != tc.allowed {
				t.Fatalf("unexpected result: %s", w.Body.String())
			}
		})
	}
}
