package kafka

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func TestOwnedKafkaActualInitializationCancellationClosesAndJoinsOriginalSocket(t *testing.T) {
	for _, variant := range []string{"already-canceled", "blocked-metadata"} {
		t.Run(variant, func(t *testing.T) {
			previous := config.Config.Kafka
			t.Cleanup(func() { config.Config.Kafka = previous })
			config.Config.Kafka.TLS = nil
			config.Config.Kafka.ProducerAck = "wait_for_all"
			config.Config.Kafka.CompressType = "snappy"
			for _, key := range []string{"KAFKA_ADDRESS", "KAFKA_PORT", "KAFKA_USERNAME", "KAFKA_PASSWORD"} {
				t.Setenv(key, "")
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if variant == "already-canceled" {
				cancel()
				producer, err := NewOwnedKafkaProducer(ctx, []string{"127.0.0.1:1"}, "synthetic-owned-topic")
				if producer != nil || !errors.Is(err, context.Canceled) {
					t.Fatal("canceled owner entered initialization", err)
				}
				return
			}
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			entered, serverDone, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var mu sync.Mutex
			var accepted net.Conn
			type outcome struct {
				producer *Producer
				err      error
			}
			result := make(chan outcome, 1)
			t.Cleanup(func() {
				cancel()
				_ = listener.Close()
				mu.Lock()
				if accepted != nil {
					_ = accepted.Close()
				}
				mu.Unlock()
				for _, done := range []<-chan struct{}{serverDone, returned} {
					select {
					case <-done:
					case <-time.After(3 * time.Second):
						t.Error("owned Kafka original constructor/socket did not join")
					}
				}
			})
			go func() {
				defer close(serverDone)
				connection, err := listener.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				accepted = connection
				mu.Unlock()
				defer connection.Close()
				var header [4]byte
				if _, err := io.ReadFull(connection, header[:]); err != nil {
					return
				}
				close(entered) // real Sarama metadata/API handshake is now awaiting a response
				_, _ = io.Copy(io.Discard, connection)
			}()
			go func() {
				defer close(returned)
				producer, err := NewOwnedKafkaProducer(ctx, []string{listener.Addr().String()}, "synthetic-owned-topic")
				result <- outcome{producer, err}
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("actual owned producer did not reach the original socket")
			}
			cancel()
			var actual outcome
			select {
			case actual = <-result:
			case <-time.After(2 * time.Second):
				t.Fatal("owner cancellation waited for the unrelated 3s read/10s startup deadline")
			}
			if actual.producer == nil || !errors.Is(actual.err, context.Canceled) {
				t.Fatal("canceled partial producer was lost or reported ready", actual.err)
			}
			if actual.producer.config.Producer.RequiredAcks != sarama.WaitForAll || actual.producer.config.Producer.Compression != sarama.CompressionSnappy || actual.producer.topic != "synthetic-owned-topic" {
				t.Fatal("owned startup changed original delivery configuration")
			}
			select {
			case <-serverDone:
			case <-time.After(time.Second):
				t.Fatal("original actual socket was not closed before constructor return")
			}
			// Re-closing that exact returned partial handle is safe; no replacement
			// client is made and this cannot convert the constructor error to ready.
			_ = actual.producer.Close()
		})
	}
}
