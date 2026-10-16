package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	pb "github.com/OpenIMSDK/chat/pkg/proto/office"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc"
)

type audienceCallerStub struct {
	tagCallerStub
	lookups int
}

func (s *audienceCallerStub) FindGroupMemberUserIDs(context.Context, string) ([]string, error) {
	s.lookups++
	return s.members, nil
}
func (s *audienceCallerStub) SendBusinessNotification(context.Context, string, any, string, string) error {
	return nil
}

type audienceOfficeStub struct {
	pb.OfficeClient
	creates int
	got     *pb.CreateOneWorkMomentReq
}

func (s *audienceOfficeStub) CreateOneWorkMoment(_ context.Context, req *pb.CreateOneWorkMomentReq, _ ...grpc.CallOption) (*pb.CreateOneWorkMomentResp, error) {
	s.creates++
	s.got = req
	return &pb.CreateOneWorkMomentResp{WorkMoment: &pb.WorkMoment{UserID: req.UserID, Content: req.Content}}, nil
}

func TestMomentAudienceCannotExpandAnUnjoinedGroupOrSpoofItsOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, owner string
		role        int32
		members     []string
		allow       bool
		lookups     int
	}{
		{"foreign group", "u1", constant.NormalUser, []string{"u2"}, false, 1},
		{"joined group", "u1", constant.NormalUser, []string{"u1", "u2"}, true, 1},
		{"admin explicit group", "u2", constant.AdminUser, []string{"u2"}, true, 1},
		{"spoof before privileged lookup", "u2", constant.NormalUser, []string{"u1", "u2"}, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			caller := &audienceCallerStub{tagCallerStub: tagCallerStub{members: tc.members}}
			rpc := &audienceOfficeStub{}
			handler := &Office{officeClient: rpc, imApiCaller: caller}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/office/work_moment/add", strings.NewReader(`{"userID":"`+tc.owner+`","content":{"text":"synthetic"},"permission":2,"permissionGroupIDs":["private-group","private-group"]}`))
			SetToken(c, "u1", tc.role)
			handler.CreateOneWorkMoment(c)
			var response struct {
				ErrCode int `json:"errCode"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if (response.ErrCode == 0) != tc.allow || (rpc.creates == 1) != tc.allow || caller.lookups != tc.lookups {
				t.Fatalf("wrong authorization: code=%d creates=%d lookups=%d", response.ErrCode, rpc.creates, caller.lookups)
			}
			if tc.allow && len(rpc.got.PermissionUserIDs) != len(tc.members) {
				t.Fatal("group expansion duplicated audience")
			}
		})
	}
}
