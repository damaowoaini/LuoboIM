package messagedelivery

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

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
)

type deliveryDropCommitKey struct{}
type deliveryWireFact struct {
	command, lsidSHA string
	txn              int64
	transaction      bool
}
type commitReplyDropper struct {
	mu                               sync.Mutex
	requests                         map[int32]bool
	facts                            []deliveryWireFact
	all                              bool
	marked, dropped, sequenceUpdates atomic.Int32
}

func (d *commitReplyDropper) monitor() *event.CommandMonitor {
	return &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(deliveryDropCommitKey{}) != true {
			return
		}
		if e.CommandName == "update" {
			name, _ := e.Command.Lookup("update").StringValueOK()
			if name == Sequences {
				d.sequenceUpdates.Add(1)
			} else {
				return
			}
		} else if e.CommandName != "commitTransaction" {
			return
		}
		auto, ok := e.Command.Lookup("autocommit").BooleanOK()
		txn, txnOK := e.Command.Lookup("txnNumber").Int64OK()
		lsid, lsidOK := e.Command.Lookup("lsid").DocumentOK()
		fact := deliveryWireFact{command: e.CommandName, txn: txn, transaction: ok && !auto && txnOK && lsidOK, lsidSHA: hash(lsid)}
		d.mu.Lock()
		d.facts = append(d.facts, fact)
		d.mu.Unlock()
		if e.CommandName == "commitTransaction" {
			n := d.marked.Add(1)
			if d.all || n == 1 {
				d.mu.Lock()
				d.requests[int32(e.RequestID)] = true
				d.mu.Unlock()
			}
		}
	}}
}
func (d *commitReplyDropper) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "tcp" || address != "127.0.0.1:37017" {
		return nil, errors.New("task wire refuses other endpoint")
	}
	c, e := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, network, address)
	if e != nil {
		return nil, e
	}
	return &commitDropConnection{Conn: c, owner: d}, nil
}

type commitDropConnection struct {
	net.Conn
	mu     sync.Mutex
	buffer []byte
	owner  *commitReplyDropper
}

func (c *commitDropConnection) Read(out []byte) (int, error) {
	if len(out) == 0 {
		return 0, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.buffer) == 0 {
		header := make([]byte, 16)
		if _, e := io.ReadFull(c.Conn, header); e != nil {
			return 0, e
		}
		size := int(binary.LittleEndian.Uint32(header[:4]))
		if size < 16 || size > 16<<20 {
			return 0, errors.New("task wire frame outside bound")
		}
		frame := make([]byte, size)
		copy(frame, header)
		if _, e := io.ReadFull(c.Conn, frame[16:]); e != nil {
			return 0, e
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
		c.buffer = frame
	}
	n := copy(out, c.buffer)
	c.buffer = c.buffer[n:]
	return n, nil
}

func assertSingleCommittedDeliveryWire(t *testing.T, d *commitReplyDropper) {
	t.Helper()
	d.mu.Lock()
	facts := append([]deliveryWireFact(nil), d.facts...)
	d.mu.Unlock()
	if d.sequenceUpdates.Load() != 1 || d.dropped.Load() < 1 || len(facts) < 2 {
		t.Fatal("fault did not prove one actual TX work update plus commit ACK loss")
	}
	var work *deliveryWireFact
	commits := 0
	for i := range facts {
		f := &facts[i]
		if !f.transaction {
			t.Fatal("wire operation was not actually in a transaction")
		}
		if f.command == "update" {
			work = f
		} else {
			commits++
		}
	}
	if work == nil || commits < 1 {
		t.Fatal("missing actual work or commit wire")
	}
	for _, f := range facts {
		if f.txn != work.txn || f.lsidSHA != work.lsidSHA {
			t.Fatal("lost commit restarted business in another transaction")
		}
	}
}

func TestMessageDeliveryMongoLostCommitACKRetriesCommitWithoutSecondAllocation(t *testing.T) {
	drop := &commitReplyDropper{requests: map[int32]bool{}}
	ctx, _, s, db := sequenceMongoTransport(t, drop.monitor(), drop)
	initializeErr := s.PrepareSequence(ctx, "sg_g", Seed{MongoMax: 0, RedisMax: 0, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic new-CID floor"))})
	if initializeErr != nil {
		t.Fatal(initializeErr)
	}
	input := sequenceFixture(t)
	r, err := s.AcceptCommitted(context.WithValue(ctx, deliveryDropCommitKey{}, true), input)
	if err != nil || len(r.Branches) != 1 || r.Branches[0].Seq != 1 {
		t.Fatal("lost ACK failed exact commit recovery", err)
	}
	assertSingleCommittedDeliveryWire(t, drop)
	count, err := db.Collection(Receipts).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}})
	if err != nil || count != 1 {
		t.Fatal("commit retry duplicated accepted input", err)
	}
}

func TestMessageDeliveryMongoUnknownCommitRestartResolvesOriginalReceiptAndSequence(t *testing.T) {
	drop := &commitReplyDropper{requests: map[int32]bool{}, all: true}
	ctx, _, s, db := sequenceMongoTransport(t, drop.monitor(), drop)
	if err := s.PrepareSequence(ctx, "sg_g", Seed{MongoMax: 0, RedisMax: 0, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("synthetic new-CID floor"))}); err != nil {
		t.Fatal(err)
	}
	input := sequenceFixture(t)
	r, err := s.AcceptCommitted(context.WithValue(ctx, deliveryDropCommitKey{}, true), input)
	if !errors.Is(err, ErrCommitUnconfirmed) || r.ID != "" {
		t.Fatal("unconfirmed commit returned a success/local work receipt", err)
	}
	assertSingleCommittedDeliveryWire(t, drop)
	restarted, err := NewStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := restarted.Resolve(ctx, input)
	if err != nil || resolved.SourceID != input.ID || resolved.Branches[0].Seq != 1 {
		t.Fatal("restart did not resolve original durable acceptance", err)
	}
	replayed, err := restarted.AcceptCommitted(ctx, input)
	if err != nil || replayed.ID != resolved.ID || replayed.Branches[0].Seq != 1 {
		t.Fatal("same exact input was reallocated after unknown commit", err)
	}
	for _, name := range []string{Receipts, Effects} {
		count, err := db.Collection(name).CountDocuments(ctx, bson.M{"_id": bson.M{"$ne": SchemaID}})
		if err != nil || count != 1 {
			t.Fatal("restart duplicated durable work", name, err)
		}
	}
}
