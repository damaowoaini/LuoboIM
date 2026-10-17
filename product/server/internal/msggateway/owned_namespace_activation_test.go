package msggateway

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	callbackhttp "github.com/openimsdk/open-im-server/v3/pkg/common/http"
)

// An empty gate is only a test selector for the constructor phase. It cannot
// accept a release or mint the production *grpc.Server/Store/graph capability.
func TestOwnedNamespaceWebsocketPreservesPreparedListenerAndActualServePhase(t *testing.T) {
	for _, mode := range []string{"activate-original-serve", "cancel-before-serve"} {
		t.Run(mode, func(t *testing.T) {
			old := config.Config.Rpc.ListenIP
			config.Config.Rpc.ListenIP = "127.0.0.1"
			defer func() { config.Config.Rpc.ListenIP = old }()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = ownedcontrol.WithNamespaceRuntimeStartup(ctx, &ownedcontrol.NamespaceRuntimeStartupGate{})
			ws, err := NewWsServer(WithPort(0), WithMaxConnNum(8), WithHandshakeTimeout(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			callbacks, err := callbackhttp.NewOwnedCallbackHTTP()
			if err != nil {
				t.Fatal(err)
			}
			defer callbacks.Close()
			owner, err := startOwnedWebsocket(ctx, ws, callbacks, cancel)
			if err != nil {
				t.Fatal(err)
			}
			if owner.server == nil || owner.listener == nil || owner.started || owner.enabled {
				t.Fatal("actual websocket was served while only prepared")
			}
			select {
			case <-owner.listener.accepting:
				t.Fatal("Accept loop started before admission")
			default:
			}
			if mode == "activate-original-serve" {
				if err = owner.startPrepared(); err != nil {
					t.Fatal(err)
				}
				client := &http.Client{Timeout: time.Second}
				response, err := client.Get("http://" + owner.listener.Addr().String() + "/")
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != http.StatusServiceUnavailable {
					t.Fatal("original websocket admission was bypassed")
				}
			} else {
				cancel()
				if owner.startPrepared() == nil {
					t.Fatal("canceled prepared owner revived")
				}
			}
			end, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			if err = owner.Stop(end); err != nil {
				t.Fatal(err)
			}
			if owner.startPrepared() == nil {
				t.Fatal("stopped owner reactivated")
			}
		})
	}
}
