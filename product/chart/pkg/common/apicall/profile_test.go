package apicall

import (
	"context"
	"encoding/json"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProfileProjectionWirePreservesExplicitEmptyAvatar(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/update_user_info_ex" || r.Header.Get("token") != "synthetic-service-token" {
			t.Error("wrong projection path/credential")
		}
		var req map[string]map[string]json.RawMessage
		if json.NewDecoder(r.Body).Decode(&req) != nil {
			t.Error("invalid body")
		}
		info := req["userInfo"]
		var avatar string
		if raw, ok := info["faceURL"]; !ok || json.Unmarshal(raw, &avatar) != nil || avatar != "" {
			t.Error("explicit clear lost primitive JSON presence")
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"errCode":0,"data":{}}`))
	}))
	defer server.Close()
	old := config.Config.OpenIMUrl
	config.Config.OpenIMUrl = server.URL
	defer func() { config.Config.OpenIMUrl = old }()
	if err := (&Caller{}).UpdateUserProfile(mctx.WithApiToken(context.Background(), "synthetic-service-token"), "owner", "name", ""); err != nil {
		t.Fatal(err)
	}
}
