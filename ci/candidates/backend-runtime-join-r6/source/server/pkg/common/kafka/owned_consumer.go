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

var ErrOwnedConsumer = errors.New("original owned Kafka consumer is unconfirmed")

type OwnedConsumerGroup struct {
	sarama.ConsumerGroup
	client   sarama.Client
	dialer   *ownedProducerDialer
	once     sync.Once
	closeErr error
}

// The real old raw topics/offset policy are preserved only in legacy mode by
// callers. This constructor does not grant a cut, receipt, writer or Ready.
// No asynchronous initializer is abandoned, even when construction is canceled.
func NewOwnedConsumerGroup(parent context.Context, addresses []string, groupID string) (*OwnedConsumerGroup, error) {
	if parent == nil || parent.Done() == nil || parent.Err() != nil || ownedsource.FromContext(parent) == nil || groupID == "" || len(addresses) == 0 {
		return nil, ErrOwnedConsumer
	}
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_0_0_0
	cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	cfg.Consumer.Return.Errors = true
	if config.Config.Kafka.Username != "" && config.Config.Kafka.Password != "" {
		cfg.Net.SASL.Enable = true
		cfg.Net.SASL.User = config.Config.Kafka.Username
		cfg.Net.SASL.Password = config.Config.Kafka.Password
	}
	SetupTLSConfig(cfg)
	cfg.Net.DialTimeout = 3 * time.Second
	cfg.Net.ReadTimeout = 3 * time.Second
	cfg.Net.WriteTimeout = 3 * time.Second
	cfg.Metadata.Timeout = 5 * time.Second
	cfg.Metadata.Retry.Max = 0
	dialCtx, cancel := context.WithCancel(parent)
	dialer := &ownedProducerDialer{ctx: dialCtx, cancel: cancel, dialer: net.Dialer{Timeout: cfg.Net.DialTimeout, KeepAlive: cfg.Net.KeepAlive, LocalAddr: cfg.Net.LocalAddr}, connections: make(map[*ownedProducerConn]struct{})}
	cfg.Net.Proxy.Enable = true
	cfg.Net.Proxy.Dialer = dialer
	owner := &OwnedConsumerGroup{dialer: dialer}
	initial, stop := context.WithTimeout(parent, 10*time.Second)
	defer stop()
	joined := make(chan struct{})
	stopClose := context.AfterFunc(initial, func() { _ = dialer.closeAll(); close(joined) })
	var err error
	owner.client, err = sarama.NewClient(append([]string{}, addresses...), cfg)
	if err == nil && owner.client != nil {
		owner.ConsumerGroup, err = sarama.NewConsumerGroupFromClient(groupID, owner.client)
	}
	if !stopClose() {
		<-joined
	}
	if err != nil || owner.ConsumerGroup == nil || initial.Err() != nil || parent.Err() != nil {
		return owner, errors.Join(ErrOwnedConsumer, initial.Err(), parent.Err(), owner.Close())
	}
	return owner, nil
}
func (o *OwnedConsumerGroup) Close() error {
	if o == nil {
		return nil
	}
	o.once.Do(func() {
		if o.ConsumerGroup != nil {
			o.closeErr = o.ConsumerGroup.Close()
		}
		// Sarama FromClient owns a nopCloser wrapper. This is its unique raw client.
		if o.client != nil && !o.client.Closed() {
			o.closeErr = errors.Join(o.closeErr, o.client.Close())
		}
		if o.dialer != nil {
			o.closeErr = errors.Join(o.closeErr, o.dialer.closeAll())
		}
	})
	return o.closeErr
}
