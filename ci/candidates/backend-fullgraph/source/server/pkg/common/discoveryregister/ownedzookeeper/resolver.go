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
	"strings"
	"sync"
	"sync/atomic"

	"github.com/OpenIMSDK/tools/log"

	"google.golang.org/grpc/resolver"
)

type Resolver struct {
	mu          sync.Mutex
	closed      atomic.Bool
	owner       *ZkClient
	serviceName string
	target      resolver.Target
	cc          resolver.ClientConn
	addrs       []resolver.Address

	getConnsRemote func(serviceName string) (conns []resolver.Address, err error)
}

func (r *Resolver) ResolveNowZK(o resolver.ResolveNowOptions) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed.Load() {
		return
	}
	log.ZDebug(
		context.Background(),
		"start resolve now",
		"target",
		r.target,
		"serviceName",
		strings.TrimLeft(r.target.URL.Path, "/"),
	)
	newConns, err := r.getConnsRemote(strings.TrimLeft(r.target.URL.Path, "/"))
	if err != nil {
		log.ZError(context.Background(), "resolve now error", err, "target", r.target)
		return
	}
	if r.closed.Load() {
		return
	}
	r.addrs = newConns
	if err := r.cc.UpdateState(resolver.State{Addresses: newConns}); err != nil {
		log.ZError(
			context.Background(),
			"UpdateState error, conns is nil from svr",
			err,
			"conns",
			newConns,
			"zk path",
			r.target.URL.Path,
		)
		return
	}
	log.ZDebug(context.Background(), "resolve now finished", "target", r.target, "conns", r.addrs)
}

func (r *Resolver) ResolveNow(o resolver.ResolveNowOptions) {}

func (r *Resolver) Close() {
	if r.closed.Swap(true) {
		return
	}
	if r.owner == nil {
		return
	}
	r.owner.lock.Lock()
	rows := r.owner.resolvers[r.serviceName]
	delete(rows, r)
	if len(rows) == 0 {
		delete(r.owner.resolvers, r.serviceName)
	}
	r.owner.lock.Unlock()
}

func (s *ZkClient) Build(
	target resolver.Target,
	cc resolver.ClientConn,
	opts resolver.BuildOptions,
) (resolver.Resolver, error) {
	done, err := s.begin()
	if err != nil {
		return nil, err
	}
	defer done()
	s.logger.Printf("build resolver: %+v, cc: %+v", target, cc.UpdateState)
	serviceName := strings.TrimLeft(target.URL.Path, "/")
	r := &Resolver{owner: s, serviceName: serviceName}
	r.target = target
	r.cc = cc
	r.getConnsRemote = s.GetConnsRemote
	// Publish this exact instance before its first remote snapshot. A child
	// registration arriving during that snapshot must see it in the broadcast
	// set; the instance mutex orders the initial and event-driven lookups.
	s.lock.Lock()
	if s.isClosing() || r.closed.Load() {
		s.lock.Unlock()
		r.Close()
		return nil, ErrClosed
	}
	if s.resolvers[serviceName] == nil {
		s.resolvers[serviceName] = make(map[*Resolver]struct{})
	}
	s.resolvers[serviceName][r] = struct{}{}
	s.lock.Unlock()
	// Network and UpdateState stay outside the collection lock. Close can retire
	// this instance during the first lookup without renewing its ownership.
	r.ResolveNowZK(resolver.ResolveNowOptions{})
	if s.isClosing() || r.closed.Load() {
		r.Close()
		return nil, ErrClosed
	}
	s.logger.Printf("build resolver finished: %+v, cc: %+v, key: %s", target, cc.UpdateState, serviceName)
	return r, nil
}

func (s *ZkClient) Scheme() string { return s.scheme }
