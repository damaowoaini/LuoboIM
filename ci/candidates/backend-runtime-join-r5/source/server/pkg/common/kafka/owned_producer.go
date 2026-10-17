package kafka

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/IBM/sarama"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
)

var ErrOwnedProducer = errors.New("owned Kafka producer initialization is unconfirmed")

// NewOwnedKafkaProducer performs one synchronous, finite Sarama initialization.
// It never abandons a goroutine when the original owner's context is canceled.
// The old NewKafkaProducer, its configuration and retry behavior are unchanged.
func NewOwnedKafkaProducer(parent context.Context, addr []string, topic string) (*Producer, error) {
	if parent == nil || parent.Done() == nil || parent.Err() != nil || topic == "" {
		if parent != nil {
			return nil, errors.Join(ErrOwnedProducer, parent.Err())
		}
		return nil, ErrOwnedProducer
	}
	source := ownedsource.FromContext(parent)
	if source == nil {
		return nil, errors.Join(ErrOwnedProducer, ownedsource.ErrOwner)
	}
	p := &Producer{addr: addr, topic: topic, config: sarama.NewConfig(), ownedSource: source}
	p.config.Producer.Return.Successes = true
	p.config.Producer.Return.Errors = true
	p.config.Producer.Partitioner = sarama.NewHashPartitioner
	configureProducerAck(p, config.Config.Kafka.ProducerAck)
	configureCompression(p, config.Config.Kafka.CompressType)
	kafkaUsername := getEnvOrConfig("KAFKA_USERNAME", config.Config.Kafka.Username)
	kafkaPassword := getEnvOrConfig("KAFKA_PASSWORD", config.Config.Kafka.Password)
	p.addr = getKafkaAddrFromEnv(addr)
	if kafkaUsername != "" && kafkaPassword != "" {
		p.config.Net.SASL.Enable = true
		p.config.Net.SASL.User = kafkaUsername
		p.config.Net.SASL.Password = kafkaPassword
	}
	SetupTLSConfig(p.config)
	// All original topic/partition/ACK/compression/SASL/TLS choices remain.
	// Only the explicit owned startup has finite, single-attempt I/O policy.
	p.config.Net.DialTimeout = 3 * time.Second
	p.config.Net.ReadTimeout = 3 * time.Second
	p.config.Net.WriteTimeout = 3 * time.Second
	p.config.Metadata.Timeout = 5 * time.Second
	p.config.Metadata.Retry.Max = 0
	dialContext, cancelDial := context.WithCancel(parent)
	dialer := &ownedProducerDialer{ctx: dialContext, cancel: cancelDial, dialer: net.Dialer{Timeout: p.config.Net.DialTimeout, KeepAlive: p.config.Net.KeepAlive, LocalAddr: p.config.Net.LocalAddr}, connections: make(map[*ownedProducerConn]struct{})}
	// Sarama's existing proxy.Dialer hook replaces only TCP dialing. Sarama
	// still performs its original TLS and SASL on the returned actual socket.
	p.config.Net.Proxy.Enable = true
	p.config.Net.Proxy.Dialer = dialer
	var client sarama.Client
	p.ownedClose = func() error {
		var err error
		// FromClient deliberately does not close this client; own it explicitly.
		if client != nil && !client.Closed() {
			err = client.Close()
		}
		return errors.Join(err, dialer.closeAll())
	}
	initial, cancelInitial := context.WithTimeout(parent, 10*time.Second)
	defer cancelInitial()
	closed := make(chan struct{})
	stopClose := context.AfterFunc(initial, func() { _ = dialer.closeAll(); close(closed) })
	var err error
	client, err = sarama.NewClient(p.addr, p.config)
	if err == nil && client != nil {
		p.producer, err = sarama.NewSyncProducerFromClient(client)
	}
	// Join the cancellation callback, never merely leave it closing a different
	// owner's/newly returned socket later. NewClient/FromClient have both returned.
	if !stopClose() {
		<-closed
	}
	if err != nil || client == nil || p.producer == nil || initial.Err() != nil || parent.Err() != nil {
		return p, errors.Join(ErrOwnedProducer, err, initial.Err(), parent.Err(), p.Close())
	}
	return p, nil
}

type ownedProducerDialer struct {
	ctx         context.Context
	cancel      context.CancelFunc
	dialer      net.Dialer
	mu          sync.Mutex
	closing     bool
	connections map[*ownedProducerConn]struct{}
	once        sync.Once
	closeErr    error
}

func (d *ownedProducerDialer) Dial(network, address string) (net.Conn, error) {
	if err := d.ctx.Err(); err != nil {
		return nil, err
	}
	connection, err := d.dialer.DialContext(d.ctx, network, address)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	if d.closing || d.ctx.Err() != nil {
		d.mu.Unlock()
		return nil, errors.Join(ErrOwnedProducer, connection.Close(), d.ctx.Err())
	}
	owned := &ownedProducerConn{Conn: connection, owner: d}
	d.connections[owned] = struct{}{}
	d.mu.Unlock()
	return owned, nil
}

func (d *ownedProducerDialer) closeAll() error {
	d.once.Do(func() {
		d.mu.Lock()
		d.closing = true
		connections := make([]*ownedProducerConn, 0, len(d.connections))
		for connection := range d.connections {
			connections = append(connections, connection)
		}
		d.mu.Unlock()
		d.cancel()
		for _, connection := range connections {
			d.closeErr = errors.Join(d.closeErr, connection.Close())
		}
	})
	return d.closeErr
}

type ownedProducerConn struct {
	net.Conn
	owner *ownedProducerDialer
	once  sync.Once
	err   error
}

func (c *ownedProducerConn) Close() error {
	c.once.Do(func() {
		c.err = c.Conn.Close()
		c.owner.mu.Lock()
		delete(c.owner.connections, c)
		c.owner.mu.Unlock()
	})
	return c.err
}
