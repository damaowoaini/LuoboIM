package msggateway

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	callbackhttp "github.com/openimsdk/open-im-server/v3/pkg/common/http"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"google.golang.org/grpc"
)

var errOwnedGateway = errors.New("owned gateway lifecycle is unconfirmed")

// Values are the original authenticated request identity. Only cancellation,
// deadline and owned callback transport come from the original process owner.
type ownedRequestContext struct {
	context.Context
	values context.Context
}

func (c ownedRequestContext) Value(key any) any {
	if v := c.values.Value(key); v != nil {
		if text, ok := v.(string); !ok || text != "" {
			return v
		}
	}
	return c.Context.Value(key)
}

type ownedSocketKey struct{}
type ownedSocket struct {
	conn     net.Conn
	client   *Client
	hijacked bool
}
type ownedAcceptListener struct {
	net.Listener
	once      sync.Once
	accepting chan struct{}
}

func (l *ownedAcceptListener) Accept() (net.Conn, error) {
	l.once.Do(func() { close(l.accepting) })
	return l.Listener.Accept()
}

// One actual listener, HTTP handler admission, upgraded sockets, readers and
// event/callback loop. No Mongo/Kafka pool is invented for this role. The old
// unowned Run remains unchanged; this owner never starts that signal loop.
type ownedWebsocketRuntime struct {
	ws                       *WsServer
	messages                 context.Context
	cancelMessages           context.CancelFunc
	callbacks                *callbackhttp.OwnedCallbackHTTP
	cancelProcess            context.CancelFunc
	listener                 *ownedAcceptListener
	server                   *http.Server
	serveDone                chan struct{}
	serveErr                 error
	unexpected               error
	loopDone, loopStop, wake chan struct{}
	mu                       sync.Mutex
	enabled, stopping        bool
	sockets                  map[net.Conn]*ownedSocket
	handlers, readers        sync.WaitGroup
	unregistered             []*Client
	stopOnce                 sync.Once
	stopDone                 chan struct{}
	stopErr                  error
}

func runOwnedGateway(rpcPort, wsPort, metricsPort int) error {
	lifetime, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	defer cancel()
	return runOwnedGatewayContext(lifetime, cancel, rpcPort, wsPort, metricsPort)
}
func runOwnedGatewayContext(lifetime context.Context, cancel context.CancelFunc, rpcPort, wsPort, metricsPort int) error {
	ws, err := NewWsServer(WithPort(wsPort), WithMaxConnNum(int64(config.Config.LongConnSvr.WebsocketMaxConnNum)), WithHandshakeTimeout(time.Duration(config.Config.LongConnSvr.WebsocketTimeout)*time.Second), WithMessageMaxMsgLength(config.Config.LongConnSvr.WebsocketMaxMsgLen), WithWriteBufferSize(config.Config.LongConnSvr.WebsocketWriteBufferSize))
	if err != nil {
		return err
	}
	hub := NewServer(rpcPort, metricsPort, ws)
	return startrpc.StartOwnedManagedContext(lifetime, rpcPort, config.Config.RpcRegisterName.OpenImMessageGatewayName, metricsPort, "msggateway", func(ctx context.Context, registry discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) (startrpc.ManagedStop, error) {
		// Hub Init preserves exact original cache, handler, RPC registration and auth.
		if err := hub.InitServer(registry, server); err != nil {
			return nil, err
		}
		callbacks, err := callbackhttp.NewOwnedCallbackHTTP()
		if err != nil {
			return nil, err
		}
		if err := startrpc.OwnRPCResource(server, "callback-http", func(context.Context) error { return callbacks.Close() }); err != nil {
			return nil, errors.Join(err, callbacks.Close())
		}
		owned, err := startOwnedWebsocket(ctx, ws, callbacks, cancel)
		if owned != nil {
			if trackErr := startrpc.OwnRPCResource(server, "websocket-runtime", owned.Stop); trackErr != nil {
				end, stop := context.WithTimeout(context.Background(), 30*time.Second)
				defer stop()
				return nil, errors.Join(err, trackErr, owned.Stop(end))
			}
		}
		if err != nil {
			return nil, err
		}
		if err := startrpc.OwnRPCGatewayAdmission(server, owned.enable, owned.stopAdmission, owned.Failure); err != nil {
			return nil, err
		}
		return nil, nil
	})
}

func startOwnedWebsocket(parent context.Context, ws *WsServer, callbacks *callbackhttp.OwnedCallbackHTTP, cancelProcess context.CancelFunc) (*ownedWebsocketRuntime, error) {
	if parent == nil || parent.Done() == nil || ws == nil || ws.owned != nil || callbacks == nil || cancelProcess == nil {
		return nil, errOwnedGateway
	}
	messages, cancel := context.WithCancel(callbacks.Bind(parent))
	o := &ownedWebsocketRuntime{ws: ws, messages: messages, cancelMessages: cancel, callbacks: callbacks, cancelProcess: cancelProcess, serveDone: make(chan struct{}), loopDone: make(chan struct{}), loopStop: make(chan struct{}), wake: make(chan struct{}, 1), stopDone: make(chan struct{}), sockets: make(map[net.Conn]*ownedSocket)}
	listener, err := net.Listen("tcp", webSocketListenAddress(config.Config.Rpc.ListenIP, ws.port))
	if err != nil {
		cancel()
		close(o.serveDone)
		close(o.loopDone)
		return o, err
	}
	o.listener = &ownedAcceptListener{Listener: listener, accepting: make(chan struct{})}
	ws.owned = o
	mux := http.NewServeMux()
	mux.HandleFunc("/", o.handle)
	o.server = &http.Server{Handler: mux, ReadHeaderTimeout: ws.handshakeTimeout, BaseContext: func(net.Listener) context.Context { return o.messages }, ConnContext: func(ctx context.Context, c net.Conn) context.Context {
		record := &ownedSocket{conn: c}
		o.mu.Lock()
		o.sockets[c] = record
		stopping := o.stopping
		o.mu.Unlock()
		if stopping {
			_ = c.Close()
		}
		return context.WithValue(ctx, ownedSocketKey{}, record)
	}, ConnState: func(c net.Conn, state http.ConnState) {
		o.mu.Lock()
		defer o.mu.Unlock()
		record := o.sockets[c]
		if record == nil {
			return
		}
		if state == http.StateHijacked {
			record.hijacked = true
		}
		if state == http.StateClosed {
			delete(o.sockets, c)
		}
	}}
	go o.loop()
	go func() {
		err := o.server.Serve(o.listener)
		o.mu.Lock()
		o.serveErr = err
		unexpected := !o.stopping
		if unexpected {
			o.unexpected = errors.Join(errOwnedGateway, err)
		}
		o.mu.Unlock()
		close(o.serveDone)
		if unexpected {
			o.cancelProcess()
		}
	}()
	select {
	case <-o.listener.accepting:
		return o, nil
	case <-o.serveDone:
		return o, errOwnedGateway
	case <-parent.Done():
		return o, parent.Err()
	}
}
func (o *ownedWebsocketRuntime) enable() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.stopping || o.enabled || o.server == nil || o.messages.Err() != nil {
		return errOwnedGateway
	}
	select {
	case <-o.serveDone:
		return errOwnedGateway
	default:
	}
	o.enabled = true
	return nil
}
func (o *ownedWebsocketRuntime) messageContext() context.Context { return o.messages }
func (o *ownedWebsocketRuntime) handle(w http.ResponseWriter, r *http.Request) {
	o.mu.Lock()
	if !o.enabled || o.stopping {
		o.mu.Unlock()
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
		return
	}
	o.handlers.Add(1)
	o.mu.Unlock()
	defer o.handlers.Done()
	defer func() {
		if record, ok := r.Context().Value(ownedSocketKey{}).(*ownedSocket); ok {
			o.mu.Lock()
			if record.hijacked && record.client == nil {
				delete(o.sockets, record.conn)
			}
			o.mu.Unlock()
		}
	}()
	o.ws.wsHandler(w, r)
}
func (o *ownedWebsocketRuntime) startClient(c *Client) {
	o.mu.Lock()
	if o.stopping || !o.enabled {
		o.mu.Unlock()
		_ = c.conn.Close()
		return
	}
	record, ok := c.ctx.Req.Context().Value(ownedSocketKey{}).(*ownedSocket)
	if !ok || o.sockets[record.conn] != record {
		o.mu.Unlock()
		_ = c.conn.Close()
		return
	}
	record.client = c
	done := make(chan struct{})
	c.ownedReadDone = done
	o.readers.Add(1)
	o.mu.Unlock()
	go func() {
		defer o.readers.Done()
		defer func() { close(done); o.mu.Lock(); delete(o.sockets, record.conn); o.mu.Unlock() }()
		c.readMessage()
	}()
	select {
	case o.ws.registerChan <- c:
	case <-o.messages.Done():
		c.close()
	}
}
func (o *ownedWebsocketRuntime) unregister(c *Client) {
	o.mu.Lock()
	o.unregistered = append(o.unregistered, c)
	o.mu.Unlock()
	select {
	case o.wake <- struct{}{}:
	default:
	}
}
func (o *ownedWebsocketRuntime) kick(k *kickHandler) {
	select {
	case <-o.messages.Done():
		return
	default:
	}
	select {
	case o.ws.kickHandlerChan <- k:
	case <-o.messages.Done():
	}
}
func (o *ownedWebsocketRuntime) processUnregistered() {
	o.mu.Lock()
	pending := o.unregistered
	o.unregistered = nil
	o.mu.Unlock()
	for _, c := range pending {
		// read owns closedErr; a concurrent RPC kick may close the socket first.
		// Wait for the original reader before unregister logs/callbacks inspect it.
		if c.ownedReadDone != nil {
			<-c.ownedReadDone
		}
		o.ws.unregisterClient(c)
	}
}
func (o *ownedWebsocketRuntime) processKick(k *kickHandler) {
	unlock := o.ws.lockSession(k.newClient.UserID)
	defer unlock()
	_ = o.ws.multiTerminalLoginChecker(k.clientOK, k.oldClients, k.newClient)
}
func (o *ownedWebsocketRuntime) loop() {
	defer close(o.loopDone)
	for {
		select {
		case c := <-o.ws.registerChan:
			o.ws.registerClient(c)
		case k := <-o.ws.kickHandlerChan:
			o.processKick(k)
		case <-o.wake:
			o.processUnregistered()
		case <-o.loopStop:
			// Producers are already joined: drain only actual work admitted by this owner.
			for {
				select {
				case c := <-o.ws.registerChan:
					o.ws.registerClient(c)
				case k := <-o.ws.kickHandlerChan:
					o.processKick(k)
				default:
					o.processUnregistered()
					return
				}
			}
		}
	}
}
func (o *ownedWebsocketRuntime) presenceContext(values context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(o.callbacks.Bind(context.Background()), 8*time.Second)
	return ownedRequestContext{Context: ctx, values: values}, cancel
}
func (o *ownedWebsocketRuntime) stopAdmission(context.Context) error {
	o.mu.Lock()
	if !o.stopping {
		o.stopping = true
		o.enabled = false
		o.cancelMessages()
	}
	o.mu.Unlock()
	if o.listener != nil {
		_ = o.listener.Close()
	}
	return nil
}
func (o *ownedWebsocketRuntime) Stop(ctx context.Context) error {
	if ctx == nil {
		return errOwnedGateway
	}
	o.stopOnce.Do(func() { go func() { o.stopErr = o.stop(ctx); close(o.stopDone) }() })
	select {
	case <-o.stopDone:
		return o.stopErr
	case <-ctx.Done():
		return errors.Join(errOwnedGateway, ctx.Err())
	}
}
func (o *ownedWebsocketRuntime) stop(ctx context.Context) error {
	if err := o.stopAdmission(ctx); err != nil {
		return err
	}
	if o.server == nil {
		return nil
	} // actual listener constructor failed; no goroutines made
	// Raw socket close is concurrency-safe and unblocks both a pending write and
	// a reader. Do not wait for Client.w before closing the original socket.
	o.mu.Lock()
	sockets := make([]net.Conn, 0, len(o.sockets))
	for conn := range o.sockets {
		sockets = append(sockets, conn)
	}
	o.mu.Unlock()
	for _, conn := range sockets {
		_ = conn.Close()
	}
	if err := o.server.Shutdown(ctx); err != nil {
		return err
	}
	join := make(chan struct{})
	go func() { o.handlers.Wait(); o.readers.Wait(); close(join) }()
	select {
	case <-join:
	case <-ctx.Done():
		return errors.Join(errOwnedGateway, ctx.Err())
	}
	close(o.loopStop)
	select {
	case <-o.loopDone:
	case <-ctx.Done():
		return errors.Join(errOwnedGateway, ctx.Err())
	}
	select {
	case <-o.serveDone:
	case <-ctx.Done():
		return errors.Join(errOwnedGateway, ctx.Err())
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.serveErr != nil && !errors.Is(o.serveErr, http.ErrServerClosed) && !errors.Is(o.serveErr, net.ErrClosed) {
		return o.serveErr
	}
	if len(o.sockets) != 0 || o.ws.onlineUserConnNum.Load() != 0 || len(o.unregistered) != 0 {
		return errOwnedGateway
	}
	return nil
}

// A real unexpected Serve failure is preserved independently of signal/cancel.
// Local close may finish, but the original owner must not emit an exclusion drain.
func (o *ownedWebsocketRuntime) Failure() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.unexpected
}
