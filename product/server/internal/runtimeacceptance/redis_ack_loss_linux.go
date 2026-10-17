//go:build linux

package runtimeacceptance

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
)

// RedisACKLoss is a real RESP transport between the one original push client
// and its real Redis server. It never substitutes a Redis command result. The
// sole fault discards the server's actual integer1 reply AFTER the exact DEL.
// No request/reply/auth/token bytes are logged, persisted or returned in stats.
type RedisACKLoss struct {
	listener    net.Listener
	upstream    string
	key         []byte
	ctx         context.Context
	cancel      context.CancelFunc
	mu          sync.Mutex
	connections map[net.Conn]bool
	closed      bool
	workers     sync.WaitGroup
	done        chan struct{}
	armed       atomic.Bool
	matched     atomic.Int64
	deleted     atomic.Bool
	dropped     atomic.Bool
	failed      atomic.Bool
}
type RedisACKLossObservation struct {
	Version                  string `json:"version"`
	TargetKeySHA256          string `json:"targetKeySHA256"`
	ExactDeleteRequests      int64  `json:"exactDeleteRequests"`
	ActualUpstreamIntegerOne bool   `json:"actualUpstreamIntegerOne"`
	ActualReplyDiscarded     bool   `json:"actualReplyDiscarded"`
	TransportFailure         bool   `json:"transportFailure"`
}

func loopbackAddress(value string) bool {
	h, p, e := net.SplitHostPort(value)
	n, e2 := strconv.Atoi(p)
	ip := net.ParseIP(h)
	return e == nil && e2 == nil && n > 0 && n <= 65535 && ip != nil && ip.IsLoopback()
}
func NewRedisACKLoss(parent context.Context, listen, upstream, key string) (*RedisACKLoss, error) {
	if parent == nil || parent.Done() == nil || !loopbackAddress(listen) || !loopbackAddress(upstream) || listen == upstream || !strings.HasPrefix(key, "FCM_TOKEN:fullgraph_") || len(key) > 512 {
		return nil, ownedcontrol.ErrOwnedEvidence
	}
	listener, e := net.Listen("tcp", listen)
	if e != nil {
		return nil, e
	}
	ctx, cancel := context.WithCancel(parent)
	p := &RedisACKLoss{listener: listener, upstream: upstream, key: []byte(key), ctx: ctx, cancel: cancel, connections: map[net.Conn]bool{}, done: make(chan struct{})}
	go p.accept()
	return p, nil
}
func (p *RedisACKLoss) Address() string { return p.listener.Addr().String() }
func (p *RedisACKLoss) Arm() error {
	if p == nil || p.ctx.Err() != nil || p.matched.Load() != 0 || !p.armed.CompareAndSwap(false, true) {
		return ownedcontrol.ErrOwnedEvidence
	}
	return nil
}
func (p *RedisACKLoss) Observation() RedisACKLossObservation {
	return RedisACKLossObservation{"real-redis-exact-del-ack-loss-v1", digestBytes(p.key), p.matched.Load(), p.deleted.Load(), p.dropped.Load(), p.failed.Load()}
}
func (p *RedisACKLoss) accept() {
	defer close(p.done)
	for {
		conn, e := p.listener.Accept()
		if e != nil {
			if p.ctx.Err() == nil {
				p.failed.Store(true)
			}
			return
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			_ = conn.Close()
			continue
		}
		p.connections[conn] = true
		p.workers.Add(1)
		p.mu.Unlock()
		go p.serve(conn)
	}
}
func (p *RedisACKLoss) serve(client net.Conn) {
	defer p.workers.Done()
	defer func() { _ = client.Close(); p.mu.Lock(); delete(p.connections, client); p.mu.Unlock() }()
	dial := net.Dialer{Timeout: 3 * time.Second}
	up, e := dial.DialContext(p.ctx, "tcp", p.upstream)
	if e != nil {
		p.failed.Store(true)
		return
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		_ = up.Close()
		return
	}
	p.connections[up] = true
	p.mu.Unlock()
	defer func() { _ = up.Close(); p.mu.Lock(); delete(p.connections, up); p.mu.Unlock() }()
	reader, server := bufio.NewReaderSize(client, 64<<10), bufio.NewReaderSize(up, 64<<10)
	for {
		request, e := readRESPFrame(reader)
		if e != nil {
			if !errors.Is(e, io.EOF) && p.ctx.Err() == nil {
				p.failed.Store(true)
			}
			return
		}
		exact := false
		if p.armed.Load() {
			parts, e := respCommand(request)
			if e != nil {
				p.failed.Store(true)
				return
			}
			exact = len(parts) == 2 && strings.EqualFold(string(parts[0]), "DEL") && bytes.Equal(parts[1], p.key)
		}
		if exact && p.matched.Add(1) != 1 {
			p.failed.Store(true)
			return
		}
		if _, e = up.Write(request); e != nil {
			p.failed.Store(true)
			return
		}
		response, e := readRESPFrame(server)
		if e != nil {
			p.failed.Store(true)
			return
		}
		if exact {
			if !bytes.Equal(response, []byte(":1\r\n")) {
				p.failed.Store(true)
				return
			}
			p.deleted.Store(true)
			p.dropped.Store(true)
			// The real server already removed the key. Close without forwarding that
			// actual ACK. The original client's no-retry source observer must retain
			// unknown; this fixture never calls the RPC/DEL a second time.
			return
		}
		if _, e = client.Write(response); e != nil {
			return
		}
	}
}
func (p *RedisACKLoss) Close(ctx context.Context) error {
	if p == nil || ctx == nil {
		return ownedcontrol.ErrOwnedEvidence
	}
	p.cancel()
	_ = p.listener.Close()
	p.mu.Lock()
	p.closed = true
	connections := make([]net.Conn, 0, len(p.connections))
	for c := range p.connections {
		connections = append(connections, c)
	}
	p.mu.Unlock()
	for _, c := range connections {
		_ = c.Close()
	}
	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	joined := make(chan struct{})
	go func() { p.workers.Wait(); close(joined) }()
	select {
	case <-joined:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Bounded, byte-preserving RESP2/3 framing for the actual client's HELLO/AUTH,
// CLIENT and ordinary request/reply flow. Maps/attributes count twice; null,
// bool, bignum and error frames retain their exact original bytes. Requests are
// never decoded as JSON and no body content enters fixture evidence.
func readRESPFrame(reader *bufio.Reader) ([]byte, error) {
	var out bytes.Buffer
	elements := 0
	var read func(int) error
	read = func(depth int) error {
		if depth > 16 || elements >= 8192 {
			return ownedcontrol.ErrOwnedEvidence
		}
		elements++
		bytesLine, e := reader.ReadSlice('\n')
		if e != nil {
			return e
		}
		line := string(bytesLine)
		if len(line) < 3 || len(line) > 65536 || !strings.HasSuffix(line, "\r\n") || out.Len()+len(line) > 4<<20 {
			return ownedcontrol.ErrOwnedEvidence
		}
		out.WriteString(line)
		switch line[0] {
		case '+', '-', ':', '_', '#', '(', ',':
			return nil
		case '$', '!', '=':
			n, e := strconv.ParseInt(line[1:len(line)-2], 10, 64)
			if e != nil || n < -1 || n > 4<<20 {
				return ownedcontrol.ErrOwnedEvidence
			}
			if n < 0 {
				return nil
			}
			if int64(out.Len())+n+2 > 4<<20 {
				return ownedcontrol.ErrOwnedEvidence
			}
			body := make([]byte, int(n)+2)
			if _, e = io.ReadFull(reader, body); e != nil {
				return e
			}
			if !bytes.HasSuffix(body, []byte("\r\n")) {
				return ownedcontrol.ErrOwnedEvidence
			}
			out.Write(body)
			return nil
		case '*', '~', '>', '%', '|':
			n, e := strconv.ParseInt(line[1:len(line)-2], 10, 64)
			if e != nil || n < -1 || n > 4096 {
				return ownedcontrol.ErrOwnedEvidence
			}
			if n < 0 {
				return nil
			}
			if line[0] == '%' || line[0] == '|' {
				n *= 2
			}
			for at := int64(0); at < n; at++ {
				if e = read(depth + 1); e != nil {
					return e
				}
			}
			return nil
		default:
			return ownedcontrol.ErrOwnedEvidence
		}
	}
	if e := read(0); e != nil {
		return nil, e
	}
	return out.Bytes(), nil
}
func respCommand(raw []byte) ([][]byte, error) {
	reader := bufio.NewReader(bytes.NewReader(raw))
	first, e := reader.ReadString('\n')
	if e != nil || len(first) < 4 || first[0] != '*' {
		return nil, ownedcontrol.ErrOwnedEvidence
	}
	n, e := strconv.Atoi(first[1 : len(first)-2])
	if e != nil || n < 1 || n > 4096 {
		return nil, ownedcontrol.ErrOwnedEvidence
	}
	out := make([][]byte, 0, n)
	for at := 0; at < n; at++ {
		head, e := reader.ReadString('\n')
		if e != nil || len(head) < 4 || head[0] != '$' {
			return nil, ownedcontrol.ErrOwnedEvidence
		}
		size, e := strconv.Atoi(head[1 : len(head)-2])
		if e != nil || size < 0 || size > 4<<20 {
			return nil, ownedcontrol.ErrOwnedEvidence
		}
		part := make([]byte, size+2)
		if _, e = io.ReadFull(reader, part); e != nil || !bytes.HasSuffix(part, []byte("\r\n")) {
			return nil, ownedcontrol.ErrOwnedEvidence
		}
		out = append(out, part[:size])
	}
	if reader.Buffered() != 0 {
		return nil, ownedcontrol.ErrOwnedEvidence
	}
	return out, nil
}
