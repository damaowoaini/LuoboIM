package group

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	pbgroup "github.com/OpenIMSDK/protocol/group"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func removalPolicy(t *testing.T, action string) *config.CallBackConfig {
	t.Helper()
	switch action {
	case "kick":
		return &config.Config.Callback.CallbackKillGroupMember
	case "dismiss":
		return &config.Config.Callback.CallbackDismissGroup
	default:
		t.Fatal("invalid synthetic removal action")
		return nil
	}
}

func invokeRemovalCallback(t *testing.T, action string) error {
	t.Helper()
	ctx := memberEditContext("owner")
	switch action {
	case "kick":
		return CallbackKillGroupMember(ctx, &pbgroup.KickGroupMemberReq{GroupID: "g", KickedUserIDs: []string{"member"}})
	case "dismiss":
		return CallbackDismissGroup(ctx, &callbackstruct.CallbackDisMissGroupReq{GroupID: "g", OwnerID: "owner", MembersID: []string{"owner", "member"}})
	default:
		t.Fatal("invalid synthetic removal action")
		return nil
	}
}

// This is a real, signed loopback HTTP contract, with no business mutation or
// after-effect worker claim. Every response deliberately exercises one policy.
func removalPolicyHTTP(t *testing.T, action string, responseStatus int) (*atomic.Int32, *atomic.Int32) {
	t.Helper()
	wanted := callbackstruct.CallbackKillGroupCommand
	if action == "dismiss" { wanted = callbackstruct.CallbackDisMissGroupCommand }
	calls, invalid := &atomic.Int32{}, &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, readErr := io.ReadAll(io.LimitReader(r.Body, 8193))
		command := strings.TrimPrefix(r.URL.Path, "/")
		mac := hmac.New(sha256.New, []byte(config.Config.Secret))
		mac.Write([]byte(r.Header.Get("X-OpenIM-Callback-Timestamp") + "\n" + r.Header.Get("X-OpenIM-Callback-Nonce") + "\n" + command + "\n"))
		mac.Write(body)
		signature, decodeErr := hex.DecodeString(r.Header.Get("X-OpenIM-Callback-Signature"))
		var payload struct {
			Command string `json:"callbackCommand"`
			GroupID string `json:"groupID"`
		}
		if readErr != nil || decodeErr != nil || len(body) > 8192 || r.Method != http.MethodPost || command != wanted || len(r.Header.Get("X-OpenIM-Callback-Nonce")) != 32 || !hmac.Equal(mac.Sum(nil), signature) || json.Unmarshal(body, &payload) != nil || payload.Command != command || payload.GroupID != "g" {
			invalid.Add(1)
			http.Error(w, "synthetic callback rejected", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(responseStatus)
		_, _ = w.Write([]byte("synthetic callback status"))
	}))
	t.Cleanup(server.Close)
	config.Config.Callback.CallbackUrl = server.URL
	return calls, invalid
}

func TestRemovalCallbacksUseOwnFailurePolicyInsteadOfQuit(t *testing.T) {
	for _, action := range []string{"kick", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			for _, tc := range []struct { name string; ownContinue, quitEnabled bool }{
				{"own-stop-quit-off", false, false},
				{"own-stop-quit-on", false, true},
				{"own-continue-quit-off", true, false},
				{"own-continue-quit-on", true, true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					memberEditSetup(t)
					ownContinue, quitContinue := tc.ownContinue, !tc.ownContinue
					*removalPolicy(t, action) = config.CallBackConfig{Enable: true, CallbackTimeOut: 2, CallbackFailedContinue: &ownContinue}
					config.Config.Callback.CallbackQuitGroup = config.CallBackConfig{Enable: tc.quitEnabled, CallbackTimeOut: 4, CallbackFailedContinue: &quitContinue}
					calls, invalid := removalPolicyHTTP(t, action, http.StatusServiceUnavailable)
					err := invokeRemovalCallback(t, action)
					if ownContinue {
						if err != nil { t.Fatal("own continue policy was replaced by quit policy", err) }
					} else if !errs.ErrNetwork.Is(err) { t.Fatal("own stop policy was replaced by quit policy", err) }
					if calls.Load() != 1 || invalid.Load() != 0 { t.Fatal("signed action must be dispatched exactly once", calls.Load(), invalid.Load()) }
				})
			}
		})
	}
}

func TestRemovalCallbacksDisabledRemainNoIOWhenQuitEnabled(t *testing.T) {
	for _, action := range []string{"kick", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			memberEditSetup(t)
			*removalPolicy(t, action) = config.CallBackConfig{Enable: false, CallbackTimeOut: 2}
			config.Config.Callback.CallbackQuitGroup = config.CallBackConfig{Enable: true, CallbackTimeOut: 4}
			calls, invalid := removalPolicyHTTP(t, action, http.StatusServiceUnavailable)
			if err := invokeRemovalCallback(t, action); err != nil || calls.Load() != 0 || invalid.Load() != 0 { t.Fatal("disabled callback performed I/O or inherited quit enable", err, calls.Load()) }
		})
	}
}

func TestRemovalCallbacksAuthenticationDenialNeverContinues(t *testing.T) {
	for _, action := range []string{"kick", "dismiss"} {
		t.Run(action, func(t *testing.T) {
			for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
				t.Run(strconv.Itoa(status), func(t *testing.T) {
					memberEditSetup(t)
					allow := true
					*removalPolicy(t, action) = config.CallBackConfig{Enable: true, CallbackTimeOut: 2, CallbackFailedContinue: &allow}
					config.Config.Callback.CallbackQuitGroup = config.CallBackConfig{Enable: true, CallbackTimeOut: 4, CallbackFailedContinue: &allow}
					calls, invalid := removalPolicyHTTP(t, action, status)
					if err := invokeRemovalCallback(t, action); !errs.ErrNoPermission.Is(err) || calls.Load() != 1 || invalid.Load() != 0 { t.Fatal("authentication denial was ignored or action retried", err, calls.Load(), invalid.Load()) }
				})
			}
		})
	}
}
