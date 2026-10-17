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

package zookeeper

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/go-zookeeper/zk"
	"google.golang.org/grpc"
	"google.golang.org/grpc/resolver"
)

const (
	defaultFreq = time.Minute * 30
	timeout     = 5
)

type Logger interface {
	Printf(string, ...interface{})
}

type ZkClient struct {
	zkServers []string
	zkRoot    string
	userName  string
	password  string

	rpcRegisterName string
	rpcRegisterAddr string
	isRegistered    bool
	scheme          string

	timeout   int
	conn      ownedZKConnection
	eventChan <-chan zk.Event
	node      string
	ticker    *time.Ticker

	lock    sync.Locker
	options []grpc.DialOption

	resolvers           map[string]map[*Resolver]struct{}
	localConns          map[string][]*grpc.ClientConn
	cancel              context.CancelFunc
	ctx                 context.Context
	isStateDisconnected bool
	balancerName        string

	logger Logger
	owned  ownedLifecycle
}

type ZkOption func(*ZkClient)

func WithRoundRobin() ZkOption {
	return func(client *ZkClient) {
		client.balancerName = "round_robin"
	}
}

func WithUserNameAndPassword(userName, password string) ZkOption {
	return func(client *ZkClient) {
		client.userName = userName
		client.password = password
	}
}

func WithOptions(opts ...grpc.DialOption) ZkOption {
	return func(client *ZkClient) {
		client.options = opts
	}
}

func WithFreq(freq time.Duration) ZkOption {
	return func(client *ZkClient) {
		client.ticker.Stop()
		client.ticker = time.NewTicker(freq)
	}
}

func WithTimeout(timeout int) ZkOption {
	return func(client *ZkClient) {
		client.timeout = timeout
	}
}

func WithLogger(logger Logger) ZkOption {
	return func(client *ZkClient) {
		client.logger = logger
	}
}

// NewClientContext owns initialization, both registry loops, every dial and
// their exact close completion. It never registers a process-global resolver.
func NewClientContext(parent context.Context, zkServers []string, zkRoot string, options ...ZkOption) (result *ZkClient, resultErr error) {
	return newClientContext(parent, zkServers, zkRoot, realOwnedDial, options...)
}

func newClientContext(parent context.Context, zkServers []string, zkRoot string, dial ownedZKDial, options ...ZkOption) (result *ZkClient, resultErr error) {
	if parent == nil {
		return nil, errors.New("owned discovery context required")
	}
	client := &ZkClient{zkServers: zkServers, zkRoot: "/", scheme: zkRoot, timeout: timeout,
		localConns: make(map[string][]*grpc.ClientConn), resolvers: make(map[string]map[*Resolver]struct{}), lock: &sync.Mutex{},
		logger: zk.DefaultLogger, owned: ownedLifecycle{connections: make(map[*grpc.ClientConn]struct{}), closeDone: make(chan struct{})}}
	baseCtx, cancel := context.WithCancel(parent)
	client.ctx = baseCtx
	client.cancel = cancel
	client.ticker = time.NewTicker(defaultFreq)
	client.owned.initializing.Add(1)
	for _, option := range options {
		option(client)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
			resultErr = errors.Join(resultErr, client.CloseOwned(ctx))
			done()
		}
	}()
	defer client.owned.initializing.Done()
	// Initialization RPCs in the locked zk API do not accept contexts. Closing
	// their exact real connection is the cancellation mechanism, not a fake
	// completed flag. This callback is removed after successful initialization;
	// normal runtime pool ordering remains the owner's explicit CloseOwned step.
	stopInitializationCancel := context.AfterFunc(baseCtx, func() {
		ctx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		_ = client.CloseOwned(ctx)
	})
	defer stopInitializationCancel()
	conn, eventChan, err := dial(zkServers, time.Duration(client.timeout)*time.Second, client.logger)
	if err != nil {
		return nil, err
	}
	client.owned.mu.Lock()
	client.conn = conn
	client.eventChan = eventChan
	closing := client.owned.closing
	client.owned.mu.Unlock()
	if closing {
		return nil, ErrClosed
	}
	wait := time.NewTimer(5 * time.Second)
	defer wait.Stop()
	connected := false
	for !connected {
		select {
		case <-baseCtx.Done():
			return nil, baseCtx.Err()
		case event, ok := <-eventChan:
			if !ok {
				return nil, errors.New("owned discovery connection closed before ready")
			}
			connected = event.State == zk.StateConnected
		case <-wait.C:
			return nil, errors.New("owned discovery connection timeout")
		}
	}
	if client.userName != "" && client.password != "" {
		if err := conn.AddAuth("digest", []byte(client.userName+":"+client.password)); err != nil {
			return nil, err
		}
	}
	client.zkRoot += zkRoot
	var rootErr error
	for attempt := 0; attempt < 300; attempt++ {
		if err := baseCtx.Err(); err != nil {
			return nil, err
		}
		rootErr = client.ensureRoot()
		if rootErr == nil {
			break
		}
		if err := waitRetry(baseCtx, time.Second); err != nil {
			return nil, err
		}
	}
	if rootErr != nil {
		return nil, rootErr
	}
	if err := baseCtx.Err(); err != nil {
		return nil, err
	}
	client.owned.mu.Lock()
	if client.owned.closing {
		client.owned.mu.Unlock()
		return nil, ErrClosed
	}
	client.owned.workers.Add(2)
	client.owned.mu.Unlock()
	go func() { defer client.owned.workers.Done(); client.refresh(baseCtx) }()
	go func() { defer client.owned.workers.Done(); client.watch(baseCtx) }()
	if !stopInitializationCancel() || baseCtx.Err() != nil {
		return nil, ErrClosed
	}
	succeeded = true
	return client, nil
}

func NewClient(zkServers []string, zkRoot string, options ...ZkOption) (*ZkClient, error) {
	return NewClientContext(context.Background(), zkServers, zkRoot, options...)
}

func (s *ZkClient) ensureAndCreate(node string) error {
	exists, _, err := s.conn.Exists(node)
	if err != nil {
		return err
	}
	if !exists {
		_, err := s.conn.Create(node, []byte(""), 0, zk.WorldACL(zk.PermAll))
		if err != nil && err != zk.ErrNodeExists {
			return err
		}
	}
	return nil
}

func (s *ZkClient) refresh(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.ticker.C:
			s.lock.Lock()
			names := make([]string, 0, len(s.resolvers))
			for rpcName := range s.resolvers {
				names = append(names, rpcName)
			}
			// Preserve original connection ownership while invalidating lookups.
			s.localConns = make(map[string][]*grpc.ClientConn)
			s.lock.Unlock()
			// Network reads/UpdateState may reenter Close; never run under the set lock.
			for _, rpcName := range names {
				s.flushResolver(rpcName)
			}
		}
	}
}

func (s *ZkClient) flushResolverAndDeleteLocal(serviceName string) {
	s.logger.Printf("start flush %s", serviceName)
	s.lock.Lock()
	delete(s.localConns, serviceName)
	s.lock.Unlock()
	s.flushResolver(serviceName)
}

func (s *ZkClient) flushResolver(serviceName string) {
	s.lock.Lock()
	rows := make([]*Resolver, 0, len(s.resolvers[serviceName]))
	for r := range s.resolvers[serviceName] {
		if !r.closed.Load() {
			rows = append(rows, r)
		}
	}
	s.lock.Unlock()
	for _, r := range rows {
		r.ResolveNowZK(resolver.ResolveNowOptions{})
	}
}

func (s *ZkClient) GetZkConn() *zk.Conn {
	connection, _ := s.conn.(*zk.Conn)
	return connection
}

func (s *ZkClient) GetRootPath() string {
	return s.zkRoot
}

func (s *ZkClient) GetNode() string {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.node
}

func (s *ZkClient) ensureRoot() error {
	return s.ensureAndCreate(s.zkRoot)
}

func (s *ZkClient) ensureName(rpcRegisterName string) error {
	return s.ensureAndCreate(s.getPath(rpcRegisterName))
}

func (s *ZkClient) getPath(rpcRegisterName string) string {
	return s.zkRoot + "/" + rpcRegisterName
}

func (s *ZkClient) getAddr(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

func (s *ZkClient) AddOption(opts ...grpc.DialOption) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.options = append(s.options, opts...)
}

func (s *ZkClient) GetClientLocalConns() map[string][]*grpc.ClientConn {
	s.lock.Lock()
	defer s.lock.Unlock()
	copy := make(map[string][]*grpc.ClientConn, len(s.localConns))
	for key, connections := range s.localConns {
		copy[key] = append([]*grpc.ClientConn{}, connections...)
	}
	return copy
}
