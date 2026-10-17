// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package msgtransfer

import (
	"context"
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"google.golang.org/protobuf/proto"

	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/tools/log"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	kfk "github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/common/prommetrics"
)

type OnlineHistoryMongoConsumerHandler struct {
	historyConsumerGroup *kfk.MConsumerGroup
	msgDatabase          controller.CommonMsgDatabase
}

func NewOnlineHistoryMongoConsumerHandler(database controller.CommonMsgDatabase) *OnlineHistoryMongoConsumerHandler {
	mc := &OnlineHistoryMongoConsumerHandler{
		historyConsumerGroup: kfk.NewMConsumerGroup(&kfk.MConsumerGroupConfig{
			KafkaVersion:   sarama.V2_0_0_0,
			OffsetsInitial: sarama.OffsetNewest, IsReturnErr: false,
		}, []string{config.Config.Kafka.MsgToMongo.Topic},
			config.Config.Kafka.Addr, config.Config.Kafka.ConsumerGroupID.MsgToMongo),
		msgDatabase: database,
	}
	return mc
}

func (mc *OnlineHistoryMongoConsumerHandler) handleChatWs2Mongo(
	ctx context.Context,
	cMsg *sarama.ConsumerMessage,
	key string,
	session sarama.ConsumerGroupSession,
) error {
	msg := cMsg.Value
	msgFromMQ := pbmsg.MsgDataToMongoByMQ{}
	err := proto.Unmarshal(msg, &msgFromMQ)
	if err != nil {
		return fmt.Errorf("decode persisted-message queue record: %w", err)
	}
	if len(msgFromMQ.MsgData) == 0 {
		return fmt.Errorf("persisted-message queue batch is empty")
	}
	err = mc.msgDatabase.BatchInsertChat2DB(ctx, msgFromMQ.ConversationID, msgFromMQ.MsgData, msgFromMQ.LastSeq)
	if err != nil {
		prommetrics.MsgInsertMongoFailedCounter.Inc()
		return fmt.Errorf("persist message batch: %w", err)
	}
	prommetrics.MsgInsertMongoSuccessCounter.Inc()
	var seqs []int64
	for _, msg := range msgFromMQ.MsgData {
		if msg != nil && msg.Seq > 0 {
			seqs = append(seqs, msg.Seq)
		}
	}
	err = mc.msgDatabase.DeleteMessagesFromCache(ctx, msgFromMQ.ConversationID, seqs)
	if err != nil {
		return fmt.Errorf("retire persisted message cache: %w", err)
	}
	if err := mc.msgDatabase.DelUserDeleteMsgsList(ctx, msgFromMQ.ConversationID, seqs); err != nil {
		return fmt.Errorf("retire per-user message deletion cache: %w", err)
	}
	return nil
}

func (OnlineHistoryMongoConsumerHandler) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (OnlineHistoryMongoConsumerHandler) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }

func (mc *OnlineHistoryMongoConsumerHandler) ConsumeClaim(
	sess sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error { // a instance in the consumer group
	log.ZDebug(context.Background(), "online new session msg come", "highWaterMarkOffset",
		claim.HighWaterMarkOffset(), "topic", claim.Topic(), "partition", claim.Partition())
	for msg := range claim.Messages() {
		ctx, cancel := context.WithCancel(mc.historyConsumerGroup.GetContextFromMsg(msg))
		stop := context.AfterFunc(sess.Context(), cancel)
		attempt := 0
		for {
			if err := ctx.Err(); err != nil {
				stop()
				cancel()
				return err
			}
			if err := mc.handleChatWs2Mongo(ctx, msg, string(msg.Key), sess); err == nil {
				break
			}
			// Do not advance this partition beyond an unpersisted record. The
			// existing batch writes are idempotent, so retries and a new consumer
			// after restart can safely finish it. Poison records remain pending
			// for explicit operator recovery rather than silently losing data.
			attempt++
			log.ZWarn(ctx, "Message persistence pending; retaining cache and Kafka offset", nil, "topic", msg.Topic, "partition", msg.Partition, "offset", msg.Offset, "attempt", attempt)
			delay := time.Second * time.Duration(1<<min(attempt-1, 5))
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				stop()
				cancel()
				return ctx.Err()
			case <-timer.C:
			}
		}
		sess.MarkMessage(msg, "")
		stop()
		cancel()
	}
	return nil
}
