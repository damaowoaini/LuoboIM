package controller

import (
	"context"
	"errors"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	unrelationtb "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
)

type ownedMessageProducerFactory func(context.Context, []string, string) (*kafka.Producer, error)

// Register the closer of this same database before creating the first producer.
// A later constructor failure keeps every actual returned partial handle under
// that original closer. No new message send, retry or delivery authority exists.
func NewOwnedCommonMsgDatabase(ctx context.Context, model unrelationtb.MsgDocModelInterface, cacheModel cache.MsgModel, register func(func(context.Context) error) error, readers ...GroupHistoryWindowReader) (CommonMsgDatabase, error) {
	return newOwnedCommonMsgDatabase(ctx, model, cacheModel, register, kafka.NewOwnedKafkaProducer, readers...)
}

func newOwnedCommonMsgDatabase(ctx context.Context, model unrelationtb.MsgDocModelInterface, cacheModel cache.MsgModel, register func(func(context.Context) error) error, create ownedMessageProducerFactory, readers ...GroupHistoryWindowReader) (CommonMsgDatabase, error) {
	if ctx == nil || ctx.Done() == nil || ctx.Err() != nil || register == nil || create == nil {
		return nil, errors.New("owned message producer owner required")
	}
	db := &commonMsgDatabase{groupHistory: selectGroupHistoryReader(readers), msgDocDatabase: model, cache: cacheModel}
	if err := register(func(context.Context) error { return db.CloseOwnedProducers() }); err != nil {
		return nil, err
	}
	// Preserve the original topics and exact producer field associations.
	for _, item := range []struct {
		topic  string
		target **kafka.Producer
	}{
		{config.Config.Kafka.LatestMsgToRedis.Topic, &db.producer},
		{config.Config.Kafka.MsgToMongo.Topic, &db.producerToMongo},
		{config.Config.Kafka.MsgToPush.Topic, &db.producerToPush},
	} {
		producer, err := create(ctx, config.Config.Kafka.Addr, item.topic)
		*item.target = producer // retain even a constructor's returned partial handle
		if err != nil {
			return nil, err
		}
		if producer == nil || ctx.Err() != nil {
			return nil, errors.Join(kafka.ErrOwnedProducer, ctx.Err())
		}
	}
	return db, nil
}
