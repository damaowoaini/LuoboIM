// Copyright © 2023 OpenIM. All rights reserved.
// SPDX-License-Identifier: Apache-2.0
// Owned lifecycle adaptation of the locked OpenIMSDK/tools v0.0.23 sources.
package zookeeper

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/go-zookeeper/zk"
	"google.golang.org/grpc"
)

var ErrClosed = errors.New("owned discovery is closed")
var ErrClosePending = errors.New("owned discovery close is unconfirmed")

type ownedZKConnection interface {
	AddAuth(string, []byte) error
	Exists(string) (bool, *zk.Stat, error)
	Create(string, []byte, int32, []zk.ACL) (string, error)
	ChildrenW(string) ([]string, *zk.Stat, <-chan zk.Event, error)
	Children(string) ([]string, *zk.Stat, error)
	Get(string) ([]byte, *zk.Stat, error)
	CreateProtectedEphemeralSequential(string, []byte, []zk.ACL) (string, error)
	Delete(string, int32) error
	Close()
}
type ownedZKDial func([]string, time.Duration, Logger) (ownedZKConnection, <-chan zk.Event, error)

func realOwnedDial(servers []string, timeout time.Duration, logger Logger) (ownedZKConnection, <-chan zk.Event, error) {
	return zk.Connect(servers, timeout, zk.WithLogInfo(true), zk.WithLogger(logger))
}

// This state belongs to exactly one constructed registry. No global resolver
// registry or external client can lend it connections or completion receipts.
type ownedLifecycle struct {
	mu           sync.Mutex
	closing      bool
	operations   sync.WaitGroup
	initializing sync.WaitGroup
	workers      sync.WaitGroup
	connections  map[*grpc.ClientConn]struct{}
	closeOnce    sync.Once
	closeDone    chan struct{}
	closeErr     error
}

func (s *ZkClient) begin() (func(), error) {
	s.owned.mu.Lock()
	defer s.owned.mu.Unlock()
	if s.owned.closing {
		return nil, ErrClosed
	}
	s.owned.operations.Add(1)
	return s.owned.operations.Done, nil
}

func (s *ZkClient) track(connection *grpc.ClientConn) error {
	s.owned.mu.Lock()
	if s.owned.closing {
		s.owned.mu.Unlock()
		_ = connection.Close()
		return ErrClosed
	}
	s.owned.connections[connection] = struct{}{}
	s.owned.mu.Unlock()
	return nil
}

func (s *ZkClient) isClosing() bool {
	s.owned.mu.Lock()
	defer s.owned.mu.Unlock()
	return s.owned.closing
}

// Close keeps the existing interface, but production owned callers must use
// CloseOwned and check its result. A timed-out caller does not restart cleanup.
func (s *ZkClient) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = s.CloseOwned(ctx)
}

func (s *ZkClient) CloseOwned(ctx context.Context) error {
	if ctx == nil {
		return ErrClosePending
	}
	s.owned.closeOnce.Do(func() {
		s.owned.mu.Lock()
		s.owned.closing = true
		connections := make([]*grpc.ClientConn, 0, len(s.owned.connections))
		for connection := range s.owned.connections {
			connections = append(connections, connection)
		}
		s.owned.mu.Unlock()
		s.cancel()
		s.ticker.Stop()
		go func() {
			var result error
			// Closing the real ZK connection unblocks in-flight Exists/ChildrenW/etc.
			// Its event channel closes only after loop/flushRequests/invalidateWatches
			// return in the locked go-zookeeper v1.0.3 implementation.
			s.owned.mu.Lock()
			connection := s.conn
			s.owned.mu.Unlock()
			firstConnection := connection
			if connection != nil {
				connection.Close()
			}
			// A cancellation during AddAuth/ensureRoot closes the published real
			// connection immediately; a late dial is published and closed by the same
			// original constructor before it can finish initialization.
			s.owned.initializing.Wait()
			s.owned.mu.Lock()
			connection = s.conn
			events := s.eventChan
			s.owned.mu.Unlock()
			if connection != nil && connection != firstConnection {
				connection.Close()
			}
			for _, connection := range connections {
				if err := connection.Close(); err != nil && !errors.Is(err, grpc.ErrClientConnClosing) {
					result = errors.Join(result, err)
				}
			}
			s.owned.operations.Wait()
			s.owned.workers.Wait()
			if events != nil {
				for range events {
				}
			}
			s.lock.Lock()
			s.localConns = make(map[string][]*grpc.ClientConn)
			s.resolvers = make(map[string]*Resolver)
			s.lock.Unlock()
			s.owned.mu.Lock()
			s.owned.connections = make(map[*grpc.ClientConn]struct{})
			s.owned.closeErr = result
			s.owned.mu.Unlock()
			close(s.owned.closeDone)
		}()
	})
	select {
	case <-s.owned.closeDone:
		s.owned.mu.Lock()
		err := s.owned.closeErr
		s.owned.mu.Unlock()
		return err
	case <-ctx.Done():
		return errors.Join(ErrClosePending, ctx.Err())
	}
}

func waitRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
