package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OpenIMSDK/protocol/third"
	"github.com/gin-gonic/gin"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"google.golang.org/grpc"
)

type objectRedirectURLFixture struct {
	third.ThirdClient
	name string
}

func (f *objectRedirectURLFixture) AccessURL(_ context.Context, req *third.AccessURLReq, _ ...grpc.CallOption) (*third.AccessURLResp, error) {
	f.name = req.Name
	return &third.AccessURLResp{Url: "https://objects.fixture.invalid/bucket/server-generated-key?signature=synthetic"}, nil
}

// Exercise the actual Gin wildcard and ObjectRedirect, not a second URL parser
// invented by the test. Gin's default URL.Path is already decoded exactly once.
func TestCanonicalObjectRedirectPreservesExactUnicodeSpaceAndPercentNames(t *testing.T) {
	gin.SetMode(gin.TestMode)
	client := &objectRedirectURLFixture{}
	api := NewThirdApi(rpcclient.Third{Client: client})
	router := gin.New()
	router.GET("/object/*name", api.ObjectRedirect)
	for _, name := range []string{"owner/中文 证据.png", "owner/a+b.png", "owner/a#b?.png", "owner/50%.png", "owner/literal%2Fname.png"} {
		raw := objectverify.CanonicalObjectURL("https://media.fixture.invalid/object/", name)
		request := httptest.NewRequest(http.MethodGet, raw, nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusFound || client.name != name {
			t.Fatal("Gin redirect changed or decoded the issued logical name more than once")
		}
	}
}
