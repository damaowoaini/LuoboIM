package controller

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
)

// Observe only actual socket lifetime, not Kafka data or business permissions.
// Sarama's real protocol mock owns/reads these connections, and EOF proves that
// the original producer/client's close reached every socket it had opened.
type ownedMessageObservedListener struct {
	net.Listener
	mu          sync.Mutex
	connections []*ownedMessageObservedConn
}
type ownedMessageObservedConn struct {
	net.Conn
	once     sync.Once
	readDone chan struct{}
}

func (c *ownedMessageObservedConn) Read(body []byte) (int, error) {
	n, err := c.Conn.Read(body)
	if err != nil {
		c.once.Do(func() { close(c.readDone) })
	}
	return n, err
}
func (l *ownedMessageObservedListener) Accept() (net.Conn, error) {
	connection, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	owned := &ownedMessageObservedConn{Conn: connection, readDone: make(chan struct{})}
	l.mu.Lock()
	l.connections = append(l.connections, owned)
	l.mu.Unlock()
	return owned, nil
}

func TestOwnedMessageProducerPartialConstructionKeepsEveryOriginalHandleUnderRegisteredCloser(t *testing.T) {
	for _, failAt := range []int{2, 3} {
		name := "second-producer"
		if failAt == 3 {
			name = "third-producer"
		}
		t.Run(name, func(t *testing.T) {
			previous := config.Config.Kafka
			t.Cleanup(func() { config.Config.Kafka = previous })
			config.Config.Kafka.TLS = nil
			config.Config.Kafka.LatestMsgToRedis.Topic = "synthetic-owned-redis"
			config.Config.Kafka.MsgToMongo.Topic = "synthetic-owned-mongo"
			config.Config.Kafka.MsgToPush.Topic = "synthetic-owned-push"
			for _, key := range []string{"KAFKA_ADDRESS", "KAFKA_PORT", "KAFKA_USERNAME", "KAFKA_PASSWORD"} {
				t.Setenv(key, "")
			}
			raw, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			listener := &ownedMessageObservedListener{Listener: raw}
			broker := sarama.NewMockBrokerListener(t, 0, listener)
			t.Cleanup(broker.Close)
			broker.SetHandlerByMap(map[string]sarama.MockResponse{
				"MetadataRequest":    sarama.NewMockMetadataResponse(t).SetBroker(broker.Addr(), 0),
				"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t),
			})
			config.Config.Kafka.Addr = []string{broker.Addr()}
			ctx, cancel := context.WithCancel(context.Background())
			bound, bindErr := ownedmongo.Bind(ctx, &ownedmongo.Observer{})
			if bindErr != nil {
				t.Fatal(bindErr)
			}
			ctx = bound
			var closeOriginal func(context.Context) error
			registered, calls := 0, 0
			var topics []string
			failure := errors.New("synthetic exact later producer constructor failure")
			t.Cleanup(func() {
				cancel()
				if closeOriginal != nil {
					_ = closeOriginal(context.Background())
				}
			})
			factory := func(call context.Context, addresses []string, topic string) (*kafka.Producer, error) {
				if registered != 1 || closeOriginal == nil || call != ctx {
					t.Fatal("a producer started before its original owner/closer was registered")
				}
				calls++
				topics = append(topics, topic)
				if calls == failAt {
					return nil, failure
				}
				if len(addresses) != 1 || addresses[0] != broker.Addr() {
					t.Fatal("owned constructor changed the exact address")
				}
				return kafka.NewOwnedKafkaProducer(call, addresses, topic)
			}
			database, err := newOwnedCommonMsgDatabase(ctx, nil, nil, func(closeFn func(context.Context) error) error { registered++; closeOriginal = closeFn; return nil }, factory)
			if database != nil || !errors.Is(err, failure) || calls != failAt || registered != 1 {
				t.Fatal("partial constructor was replaced/retried/reported successful", err, calls, registered)
			}
			wanted := []string{"synthetic-owned-redis", "synthetic-owned-mongo", "synthetic-owned-push"}
			for i, topic := range topics {
				if topic != wanted[i] {
					t.Fatal("partial handles were assigned to different original topics")
				}
			}
			if err := closeOriginal(context.Background()); err != nil {
				t.Fatal("same registered database closer did not join actual partial handles", err)
			}
			listener.mu.Lock()
			connections := append([]*ownedMessageObservedConn{}, listener.connections...)
			listener.mu.Unlock()
			if len(connections) < failAt-1 {
				t.Fatal("positive partial baseline never initialized the real prior producers")
			}
			for _, connection := range connections {
				select {
				case <-connection.readDone:
				case <-time.After(2 * time.Second):
					t.Fatal("a real partial producer socket survived the registered closer")
				}
			}
		})
	}
}
