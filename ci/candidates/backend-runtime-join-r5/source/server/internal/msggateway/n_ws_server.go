// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package msggateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/OpenIMSDK/tools/apiresp"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"golang.org/x/sync/errgroup"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msggateway"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/utils"

	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/prommetrics"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

type LongConnServer interface {
	Run() error
	wsHandler(w http.ResponseWriter, r *http.Request)
	GetUserAllCons(userID string) ([]*Client, bool)
	GetUserPlatformCons(userID string, platform int) ([]*Client, bool, bool)
	Validate(s any) error
	SetCacheHandler(cache cache.MsgModel)
	SetDiscoveryRegistry(client discoveryregistry.SvcDiscoveryRegistry)
	KickUserConn(client *Client) error
	UnRegister(c *Client)
	SetKickHandlerInfo(i *kickHandler)
	Compressor
	Encoder
	MessageHandler
}

var bufferPool = sync.Pool{
	New: func() any {
		return make([]byte, 1024)
	},
}

type WsServer struct {
	sessionLocks      [256]sync.Mutex
	owned             *ownedWebsocketRuntime
	port              int
	wsMaxConnNum      int64
	registerChan      chan *Client
	unregisterChan    chan *Client
	kickHandlerChan   chan *kickHandler
	clients           *UserMap
	onlineUserNum     atomic.Int64
	onlineUserConnNum atomic.Int64
	handshakeTimeout  time.Duration
	writeBufferSize   int
	validate          *validator.Validate
	cache             cache.MsgModel
	userClient        *rpcclient.UserRpcClient
	disCov            discoveryregistry.SvcDiscoveryRegistry
	Compressor
	Encoder
	MessageHandler
}
type kickHandler struct {
	clientOK   bool
	oldClients []*Client
	newClient  *Client
}

func (ws *WsServer) SetDiscoveryRegistry(disCov discoveryregistry.SvcDiscoveryRegistry) {
	ws.MessageHandler = NewGrpcHandler(ws.validate, disCov)
	u := rpcclient.NewUserRpcClient(disCov)
	ws.userClient = &u
	ws.disCov = disCov
}

func (ws *WsServer) SetUserOnlineStatus(ctx context.Context, client *Client, status int32) {
	err := ws.userClient.SetUserStatus(ctx, client.UserID, status, client.PlatformID)
	if err != nil {
		log.ZWarn(ctx, "SetUserStatus err", err)
	}
	switch status {
	case constant.Online:
		err := CallbackUserOnline(ctx, client.UserID, client.PlatformID, client.IsBackground, client.ctx.GetConnID())
		if err != nil {
			log.ZWarn(ctx, "CallbackUserOnline err", err)
		}
	case constant.Offline:
		err := CallbackUserOffline(ctx, client.UserID, client.PlatformID, client.ctx.GetConnID())
		if err != nil {
			log.ZWarn(ctx, "CallbackUserOffline err", err)
		}
	}
}

func (ws *WsServer) SetCacheHandler(cache cache.MsgModel) {
	ws.cache = cache
}

func (ws *WsServer) UnRegister(c *Client) {
	if ws.owned != nil {
		ws.owned.unregister(c)
		return
	}
	ws.unregisterChan <- c
}

func (ws *WsServer) Validate(s any) error {
	//?question?
	return nil
}

func (ws *WsServer) GetUserAllCons(userID string) ([]*Client, bool) {
	return ws.clients.GetAll(userID)
}

func (ws *WsServer) GetUserPlatformCons(userID string, platform int) ([]*Client, bool, bool) {
	return ws.clients.Get(userID, platform)
}

func NewWsServer(opts ...Option) (*WsServer, error) {
	var config configs
	for _, o := range opts {
		o(&config)
	}
	v := validator.New()
	return &WsServer{
		port:             config.port,
		wsMaxConnNum:     config.maxConnNum,
		writeBufferSize:  config.writeBufferSize,
		handshakeTimeout: config.handshakeTimeout,
		registerChan:     make(chan *Client, 1000),
		unregisterChan:   make(chan *Client, 1000),
		kickHandlerChan:  make(chan *kickHandler, 1000),
		validate:         v,
		clients:          newUserMap(),
		Compressor:       NewGzipCompressor(),
		Encoder:          NewGobEncoder(),
	}, nil
}

func (ws *WsServer) Run() error {
	var (
		client *Client
		wg     errgroup.Group

		sigs = make(chan os.Signal, 1)
		done = make(chan struct{}, 1)
	)

	server := http.Server{Addr: webSocketListenAddress(config.Config.Rpc.ListenIP, ws.port), Handler: nil}

	wg.Go(func() error {
		for {
			select {
			case <-done:
				return nil

			case client = <-ws.registerChan:
				ws.registerClient(client)
			case client = <-ws.unregisterChan:
				ws.unregisterClient(client)
			case onlineInfo := <-ws.kickHandlerChan:
				func() {
					unlock := ws.lockSession(onlineInfo.newClient.UserID)
					defer unlock()
					_ = ws.multiTerminalLoginChecker(onlineInfo.clientOK, onlineInfo.oldClients, onlineInfo.newClient)
				}()
			}
		}
	})

	wg.Go(func() error {
		http.HandleFunc("/", ws.wsHandler)
		return server.ListenAndServe()
	})

	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	<-sigs

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// graceful exit operation for server
		_ = server.Shutdown(ctx)
		_ = wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil

	case <-time.After(15 * time.Second):
		return utils.Wrap1(errors.New("timeout exit"))
	}

}

var concurrentRequest = 3

func (ws *WsServer) sendUserOnlineInfoToOtherNode(ctx context.Context, client *Client) error {
	conns, err := ws.disCov.GetConns(ctx, config.Config.RpcRegisterName.OpenImMessageGatewayName)
	if err != nil {
		return err
	}

	wg := errgroup.Group{}
	wg.SetLimit(concurrentRequest)

	// Online push user online message to other node
	for _, v := range conns {
		v := v // safe closure var
		if v.Target() == ws.disCov.GetSelfConnTarget() {
			log.ZDebug(ctx, "Filter out this node", "node", v.Target())
			continue
		}

		wg.Go(func() error {
			msgClient := msggateway.NewMsgGatewayClient(v)
			_, err := msgClient.MultiTerminalLoginCheck(ctx, &msggateway.MultiTerminalLoginCheckReq{
				UserID:     client.UserID,
				PlatformID: int32(client.PlatformID), Token: client.token,
			})
			if err != nil {
				log.ZWarn(ctx, "MultiTerminalLoginCheck err", err, "node", v.Target())
			}
			return nil
		})
	}

	_ = wg.Wait()
	return nil
}

func (ws *WsServer) SetKickHandlerInfo(i *kickHandler) {
	if ws.owned != nil {
		ws.owned.kick(i)
		return
	}
	ws.kickHandlerChan <- i
}

func (ws *WsServer) registerClient(client *Client) {
	unlock := ws.lockSession(client.UserID)
	defer unlock()
	if client.closed.Load() {
		return
	}
	// Authentication at HTTP handshake may precede a queued logout. Recheck
	// inside the same local critical section as exact-session kick snapshots.
	if err := ws.verifyCurrentSession(client); err != nil {
		client.close()
		return
	}

	var (
		userOK     bool
		clientOK   bool
		oldClients []*Client
	)
	oldClients, userOK, clientOK = ws.clients.Get(client.UserID, client.PlatformID)
	if !userOK {
		ws.clients.Set(client.UserID, client)
		log.ZDebug(client.ctx, "user not exist", "userID", client.UserID, "platformID", client.PlatformID)
		prommetrics.OnlineUserGauge.Add(1)
		ws.onlineUserNum.Add(1)
		ws.onlineUserConnNum.Add(1)
	} else {
		if err := ws.multiTerminalLoginChecker(clientOK, oldClients, client); err != nil {
			client.close()
			return
		}
		log.ZDebug(client.ctx, "user exist", "userID", client.UserID, "platformID", client.PlatformID)
		if clientOK {
			ws.clients.Set(client.UserID, client)
			// 已经有同平台的连接存在
			log.ZInfo(client.ctx, "repeat login", "userID", client.UserID, "platformID", client.PlatformID, "old remote addr", getRemoteAdders(oldClients))
			ws.onlineUserConnNum.Add(1)
		} else {
			ws.clients.Set(client.UserID, client)
			ws.onlineUserConnNum.Add(1)
		}
	}

	client.registered.Store(true)
	registrationCtx, cancelRegistration := context.WithTimeout(client.ctx, 8*time.Second)
	defer cancelRegistration()
	wg := sync.WaitGroup{}
	if config.Config.Envs.Discovery == "zookeeper" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = ws.sendUserOnlineInfoToOtherNode(registrationCtx, client)
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		ws.SetUserOnlineStatus(registrationCtx, client, constant.Online)
	}()

	wg.Wait()

	log.ZInfo(
		client.ctx,
		"user online",
		"online user Num",
		ws.onlineUserNum.Load(),
		"online user conn Num",
		ws.onlineUserConnNum.Load(),
	)
}

func getRemoteAdders(client []*Client) string {
	var ret string
	for i, c := range client {
		if i == 0 {
			ret = c.ctx.GetRemoteAddr()
		} else {
			ret += "@" + c.ctx.GetRemoteAddr()
		}
	}
	return ret
}

func (ws *WsServer) KickUserConn(client *Client) error {
	ws.clients.deleteClients(client.UserID, []*Client{client})
	return client.KickOnlineMessage()
}

func (ws *WsServer) multiTerminalLoginChecker(clientOK bool, oldClients []*Client, newClient *Client) error {
	switch config.Config.MultiLoginPolicy {
	case constant.DefalutNotKick:
	case constant.PCAndOther:
		if constant.PlatformIDToClass(newClient.PlatformID) == constant.TerminalPC {
			return nil
		}
		fallthrough
	case constant.AllLoginButSameTermKick:
		if !clientOK {
			return nil
		}
		if err := ws.cache.KickOtherTokensByUidPid(newClient.ctx, newClient.UserID, newClient.PlatformID, newClient.ctx.GetToken()); err != nil {
			return err
		}
		ws.clients.deleteClients(newClient.UserID, oldClients)
		for _, client := range oldClients {
			if err := client.KickOnlineMessage(); err != nil {
				log.ZWarn(client.ctx, "KickOnlineMessage", err)
			}
		}

	}

	return nil
}
func (ws *WsServer) unregisterClient(client *Client) {
	if !client.registered.CompareAndSwap(true, false) {
		return
	}
	isDeleteUser := ws.clients.deleteClients(client.UserID, []*Client{client})
	if isDeleteUser {
		ws.onlineUserNum.Add(-1)
		prommetrics.OnlineUserGauge.Dec()
	}
	ws.onlineUserConnNum.Add(-1)
	if ws.owned != nil {
		ctx, cancel := ws.owned.presenceContext(client.ctx)
		ws.SetUserOnlineStatus(ctx, client, constant.Offline)
		cancel()
	} else {
		ws.SetUserOnlineStatus(client.ctx, client, constant.Offline)
	}
	log.ZInfo(client.ctx, "user offline", "close reason", client.closedErr, "online user Num", ws.onlineUserNum.Load(), "online user conn Num",
		ws.onlineUserConnNum.Load(),
	)
}

func (ws *WsServer) ParseWSArgs(r *http.Request) (args *WSArgs, err error) {
	var v WSArgs
	defer func() {
		args = &v
	}()
	query := r.URL.Query()
	v.MsgResp, _ = strconv.ParseBool(query.Get(MsgResp))
	if ws.onlineUserConnNum.Load() >= ws.wsMaxConnNum {
		return nil, errs.ErrConnOverMaxNumLimit.Wrap("over max conn num limit")
	}
	if v.Token = query.Get(Token); v.Token == "" {
		return nil, errs.ErrConnArgsErr.Wrap("token is empty")
	}
	if v.UserID = query.Get(WsUserID); v.UserID == "" {
		return nil, errs.ErrConnArgsErr.Wrap("sendID is empty")
	}
	platformIDStr := query.Get(PlatformID)
	if platformIDStr == "" {
		return nil, errs.ErrConnArgsErr.Wrap("platformID is empty")
	}
	platformID, err := strconv.Atoi(platformIDStr)
	if err != nil {
		return nil, errs.ErrConnArgsErr.Wrap("platformID is not int")
	}
	v.PlatformID = platformID
	if err = authverify.WsVerifyToken(v.Token, v.UserID, platformID); err != nil {
		return nil, err
	}
	if query.Get(Compression) == GzipCompressionProtocol {
		v.Compression = true
	}
	if r.Header.Get(Compression) == GzipCompressionProtocol {
		v.Compression = true
	}
	tokenContext := context.Background()
	if ws.owned != nil {
		tokenContext = r.Context()
	}
	m, err := ws.cache.GetTokensWithoutError(tokenContext, v.UserID, platformID)
	if err != nil {
		return nil, err
	}
	if v, ok := m[v.Token]; ok {
		switch v {
		case constant.NormalToken:
		case constant.KickedToken:
			return nil, errs.ErrTokenKicked.Wrap()
		default:
			return nil, errs.ErrTokenUnknown.Wrap(fmt.Sprintf("token status is %d", v))
		}
	} else {
		return nil, errs.ErrTokenNotExist.Wrap()
	}
	return &v, nil
}

type WSArgs struct {
	Token       string
	UserID      string
	PlatformID  int
	Compression bool
	MsgResp     bool
}

func (ws *WsServer) wsHandler(w http.ResponseWriter, r *http.Request) {
	connContext := newContext(w, r)
	if ws.owned != nil {
		connContext.owned = ws.owned.messageContext()
	}
	args, pErr := ws.ParseWSArgs(r)
	var wsLongConn *GWebSocket
	if args.MsgResp {
		wsLongConn = newGWebSocket(WebSocket, ws.handshakeTimeout, ws.writeBufferSize)
		if err := wsLongConn.GenerateLongConn(w, r); err != nil {
			httpError(connContext, err)
			return
		}
		data, err := json.Marshal(apiresp.ParseError(pErr))
		if err != nil {
			_ = wsLongConn.Close()
			return
		}
		if err := wsLongConn.WriteMessage(MessageText, data); err != nil {
			_ = wsLongConn.Close()
			return
		}
		if pErr != nil {
			_ = wsLongConn.Close()
			return
		}
	} else {
		if pErr != nil {
			httpError(connContext, pErr)
			return
		}
		wsLongConn = newGWebSocket(WebSocket, ws.handshakeTimeout, ws.writeBufferSize)
		if err := wsLongConn.GenerateLongConn(w, r); err != nil {
			httpError(connContext, err)
			return
		}
	}
	// RPC push/kick handlers may retain snapshots of a disconnected client.
	// Never recycle its identity/token into another connection.
	client := new(Client)
	client.ResetClient(connContext, wsLongConn, connContext.GetBackground(), args.Compression, ws, args.Token)
	err := client.ServerConfigMessage(connContext)
	if err != nil {
		log.ZWarn(connContext, "ServerConfigMessage", err)
	}
	if ws.owned != nil {
		ws.owned.startClient(client)
		return
	}
	ws.registerChan <- client
	go client.readMessage()
}

func webSocketListenAddress(host string, port int) string {
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
}
