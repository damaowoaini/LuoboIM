package groupeffects

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/event"
)

type effectDropReplyKey struct{}
type replyDropper struct {
	mu       sync.Mutex
	requests map[int32]bool
	dropped  atomic.Int32
}

func (d *replyDropper) monitor() *event.CommandMonitor {
	return &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(effectDropReplyKey{}) != true || e.CommandName != "update" {
			return
		}
		collection, _ := e.Command.Lookup("update").StringValueOK()
		if collection != Collection {
			return
		}
		d.mu.Lock()
		d.requests[int32(e.RequestID)] = true
		d.mu.Unlock()
	}}
}
func (d *replyDropper) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" || address != "127.0.0.1:37017" {
		return nil, errors.New("wire fixture refuses non-task endpoint")
	}
	connection, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return &dropReplyConnection{Conn: connection, owner: d}, nil
}

// No command is changed and no server failpoint is used. One complete actual
// server reply (identified by its wire replyTo/requestID) is consumed locally
// then replaced with EOF before the driver receives any byte of that reply.
type dropReplyConnection struct {
	net.Conn
	owner  *replyDropper
	mu     sync.Mutex
	buffer []byte
}

func (c *dropReplyConnection) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buffer) == 0 {
		header := make([]byte, 16)
		if _, err := io.ReadFull(c.Conn, header); err != nil {
			return 0, err
		}
		size := int(binary.LittleEndian.Uint32(header[:4]))
		if size < 16 || size > 4*MaxPayload {
			return 0, errors.New("task wire message outside bound")
		}
		message := make([]byte, size)
		copy(message, header)
		if _, err := io.ReadFull(c.Conn, message[16:]); err != nil {
			return 0, err
		}
		replyTo := int32(binary.LittleEndian.Uint32(header[8:12]))
		c.owner.mu.Lock()
		drop := c.owner.requests[replyTo]
		delete(c.owner.requests, replyTo)
		c.owner.mu.Unlock()
		if drop {
			c.owner.dropped.Add(1)
			return 0, io.EOF
		}
		c.buffer = message
	}
	n := copy(out, c.buffer)
	c.buffer = c.buffer[n:]
	return n, nil
}
func TestGroupEffectsMongoLostClaimACKDoesNotGrantDispatchCapability(t *testing.T) {
	drop := &replyDropper{requests: map[int32]bool{}}
	ctx, authority, s, db := effectsMongoTransport(t, drop.monitor(), drop)
	f := prepareQueuedCallback(t, ctx, authority, s, db, nil)
	capability, err := s.BeginCallback(context.WithValue(ctx, effectDropReplyKey{}, true), f.ID, 1, f.PayloadSHA256)
	if capability != nil || !errors.Is(err, ErrUnconfirmed) || drop.dropped.Load() != 1 {
		t.Fatal("actual lost claim ACK granted a send capability or was not exercised", err)
	}
	state, err := s.Inspect(ctx, f.ID)
	if err != nil || state.State != Dispatching || state.Revision != 2 || state.Attempts != 1 {
		t.Fatal("actual acknowledged server write was not retained after dropped response", err)
	}
	if _, err := s.BeginCallback(ctx, f.ID, state.Revision, f.PayloadSHA256); !errors.Is(err, ErrConflict) {
		t.Fatal("uncertain claim was automatically reclaimed", err)
	}
}
func TestGroupEffectsMongoLostCompletionACKIsResolvedWithoutRepeatingCallback(t *testing.T) {
	drop := &replyDropper{requests: map[int32]bool{}}
	ctx, authority, s, db := effectsMongoTransport(t, drop.monitor(), drop)
	f := prepareQueuedCallback(t, ctx, authority, s, db, nil)
	capability, err := s.BeginCallback(ctx, f.ID, 1, f.PayloadSHA256)
	if err != nil {
		t.Fatal(err)
	}
	ack := digest([]byte("synthetic already observed remote ACK"))
	if err := capability.Finish(context.WithValue(ctx, effectDropReplyKey{}, true), Acknowledged, ack); !errors.Is(err, ErrUnconfirmed) || drop.dropped.Load() != 1 {
		t.Fatal("completion lost-ACK fault was not exercised", err)
	}
	state, err := s.Inspect(ctx, f.ID)
	if err != nil || state.State != Done || state.OutcomeSHA256 != ack || state.Attempts != 1 {
		t.Fatal("inspection did not recover the exact known completion", err)
	}
	if _, err := s.BeginCallback(ctx, f.ID, state.Revision, f.PayloadSHA256); !errors.Is(err, ErrConflict) {
		t.Fatal("completed callback was replayed after completion ACK loss", err)
	}
}
