package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
)

type retentionReadFixture struct {
	calls      int
	owner, cid string
	replace    bool
}

func (f *retentionReadFixture) Readback(_ context.Context, owner, cid string) (retention.Readback, error) {
	f.calls++
	f.owner, f.cid = owner, cid
	flag := false
	zero := int64(0)
	if f.replace {
		owner = "foreign"
	}
	return retention.Readback{Owner: owner, Conversation: cid, Enabled: &flag, Duration: &zero}, nil
}

func retentionRequest(t *testing.T, owner, body string, source *retentionReadFixture) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/test", func(c *gin.Context) {
		if owner != "" {
			c.Set(constant.OpUserID, owner)
		}
		c.Next()
	}, retentionPolicyReadback(source))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	var result map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestRetentionReadbackOwnerComesOnlyFromAuthenticatedContext(t *testing.T) {
	f := &retentionReadFixture{}
	result := retentionRequest(t, "owner", `{"conversationID":"cid"}`, f)
	if result["errCode"] != float64(0) || f.calls != 1 || f.owner != "owner" || f.cid != "cid" {
		t.Fatal("owner read failed")
	}
	data := result["data"].(map[string]any)
	if data["isMsgDestruct"] != false || data["msgDestructTime"] != float64(0) {
		t.Fatal("false/zero response lost")
	}
	if data["isPrivateChat"] != nil {
		t.Fatal("missing private state invented")
	}
}

func TestRetentionReadbackRejectsForeignOwnerAndUnboundedRequestsBeforeSource(t *testing.T) {
	for _, body := range []string{`{"conversationID":"cid","ownerUserID":"victim"}`, `{"conversationID":"cid"}{}`, `{"conversationID":""}`, `{"conversationID":"` + strings.Repeat("x", 2049) + `"}`} {
		f := &retentionReadFixture{}
		result := retentionRequest(t, "owner", body, f)
		if result["errCode"] == float64(0) || f.calls != 0 {
			t.Fatal("invalid request reached source")
		}
	}
	f := &retentionReadFixture{}
	retentionRequest(t, "", `{"conversationID":"cid"}`, f)
	if f.calls != 0 {
		t.Fatal("unauthenticated source read")
	}
}

func TestRetentionReadbackRejectsMismatchedSourceIdentity(t *testing.T) {
	f := &retentionReadFixture{replace: true}
	result := retentionRequest(t, "owner", `{"conversationID":"cid"}`, f)
	if result["errCode"] == float64(0) || result["data"] != nil {
		t.Fatal("foreign source escaped")
	}
}
