package group

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func TestGroupMemberBeforeAndAfterSwitchesUseTheirOwnSignedCommand(t *testing.T) {
	for _, fixture:=range []struct{name string; before,after bool}{{"both-off",false,false},{"before-only",true,false},{"after-only",false,true},{"both-on",true,true}} {
		t.Run(fixture.name,func(t *testing.T) {
			memberEditSetup(t)
			var before,after,invalid atomic.Int32
			server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
				body,err:=io.ReadAll(io.LimitReader(r.Body,8193))
				command:=strings.TrimPrefix(r.URL.Path,"/")
				mac:=hmac.New(sha256.New,[]byte(config.Config.Secret))
				mac.Write([]byte(r.Header.Get("X-OpenIM-Callback-Timestamp")+"\n"+r.Header.Get("X-OpenIM-Callback-Nonce")+"\n"+command+"\n"))
				mac.Write(body)
				signature,decodeErr:=hex.DecodeString(r.Header.Get("X-OpenIM-Callback-Signature"))
				var payload struct{Command string `json:"callbackCommand"`}
				if err!=nil || decodeErr!=nil || len(body)>8192 || len(r.Header.Get("X-OpenIM-Callback-Nonce"))!=32 || !hmac.Equal(mac.Sum(nil),signature) || json.Unmarshal(body,&payload)!=nil || payload.Command!=command { invalid.Add(1);http.Error(w,"synthetic signed command rejected",http.StatusUnauthorized);return }
				switch command {
				case callbackstruct.CallbackBeforeSetGroupMemberInfoCommand:before.Add(1)
				case callbackstruct.CallbackAfterSetGroupMemberInfoCommand:after.Add(1)
				default:invalid.Add(1);http.Error(w,"synthetic unexpected command",http.StatusBadRequest);return
				}
				w.Header().Set("Content-Type","application/json")
				_,_=w.Write([]byte("{}"))
			}))
			t.Cleanup(server.Close)
			config.Config.Callback.CallbackUrl=server.URL
			config.Config.Callback.CallbackBeforeSetGroupMemberInfo=config.CallBackConfig{Enable:fixture.before,CallbackTimeOut:2}
			config.Config.Callback.CallbackAfterSetGroupMemberInfo=config.CallBackConfig{Enable:fixture.after,CallbackTimeOut:2}
			request:=memberEdit("g","member",nil,nil,nil)
			if err:=CallbackBeforeSetGroupMemberInfo(memberEditContext("owner"),request);err!=nil {t.Fatal(err)}
			if err:=CallbackAfterSetGroupMemberInfo(memberEditContext("owner"),request);err!=nil {t.Fatal(err)}
			wantBefore,wantAfter:=int32(0),int32(0)
			if fixture.before {wantBefore=1};if fixture.after {wantAfter=1}
			if before.Load()!=wantBefore || after.Load()!=wantAfter || invalid.Load()!=0 {t.Fatalf("callback switch/command mismatch: before=%d after=%d invalid=%d",before.Load(),after.Load(),invalid.Load())}
		})
	}
}
