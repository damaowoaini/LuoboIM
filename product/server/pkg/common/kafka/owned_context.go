package kafka

import (
	"context"
	"github.com/IBM/sarama"
)

type ownedMQContext struct {
	context.Context
	header context.Context
}

func (c ownedMQContext) Value(key any) any {
	if v := c.Context.Value(key); v != nil {
		return v
	}
	return c.header.Value(key)
}

// The original lifecycle/source observer wins. Only the existing positional
// protocol metadata can be inherited; malformed headers never panic a worker.
func OwnedMQContext(parent context.Context, headers []*sarama.RecordHeader) (out context.Context, err error) {
	if parent == nil || parent.Done() == nil || len(headers) > 4 {
		return nil, ErrOwnedConsumer
	}
	for _, h := range headers {
		if h == nil || len(h.Key) > 128 || len(h.Value) > 4096 {
			return nil, ErrOwnedConsumer
		}
	}
	defer func() {
		if recover() != nil {
			out = nil
			err = ErrOwnedConsumer
		}
	}()
	return ownedMQContext{parent, GetContextWithMQHeader(headers)}, nil
}
