package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func TestCallbackSecuritySignsThePostedPayload(t *testing.T) {
	old := config.Config.Secret
	defer func() { config.Config.Secret = old }()
	config.Config.Secret = "callback-test-secret"
	checked := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ts, nonce, signature := r.Header.Get("X-OpenIM-Callback-Timestamp"), r.Header.Get("X-OpenIM-Callback-Nonce"), r.Header.Get("X-OpenIM-Callback-Signature")
		mac := hmac.New(sha256.New, []byte(config.Config.Secret))
		mac.Write([]byte(ts + "\n" + nonce + "\n" + "beforeAddFriend" + "\n"))
		mac.Write(body)
		if ts == "" || len(nonce) != 32 || signature != hex.EncodeToString(mac.Sum(nil)) {
			t.Error("callback signature did not match posted bytes")
		}
		checked = true
		w.Write([]byte(`{"actionCode":0,"errCode":0}`))
	}))
	defer server.Close()
	err := callBackPostReturn(context.Background(), server.URL, "beforeAddFriend", map[string]string{"fromUserID": "u1", "toUserID": "u2"}, &callbackstruct.CallbackBeforeAddFriendResp{}, config.CallBackConfig{CallbackTimeOut: 1})
	if err != nil || !checked {
		t.Fatalf("signed callback failed: %v", err)
	}
}

func TestCallbackSecurityMissingSecretAndHTTPFailure(t *testing.T) {
	old := config.Config.Secret
	defer func() { config.Config.Secret = old }()
	config.Config.Secret = ""
	if _, err := callbackHeaders("command", map[string]string{}); err == nil {
		t.Fatal("unsigned callback allowed")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403); w.Write([]byte(`{"actionCode":0}`)) }))
	defer server.Close()
	if _, err := Post(context.Background(), server.URL, nil, nil, 1); err == nil {
		t.Fatal("HTTP authentication rejection interpreted as success")
	}
	config.Config.Secret = "configured-secret"
	allowFailure := true
	if err := callBackPostReturn(context.Background(), server.URL, "command", map[string]string{}, &callbackstruct.CallbackBeforeAddFriendResp{}, config.CallBackConfig{CallbackTimeOut: 1, CallbackFailedContinue: &allowFailure}); err == nil {
		t.Fatal("authorization failure bypassed via failedContinue")
	}
}
