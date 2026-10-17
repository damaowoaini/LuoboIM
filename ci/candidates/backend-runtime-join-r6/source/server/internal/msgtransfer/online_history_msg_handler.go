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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"

	"github.com/OpenIMSDK/tools/errs"

	"github.com/IBM/sarama"
	"github.com/go-redis/redis"
	"google.golang.org/protobuf/proto"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/kafka"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

const (
	ConsumerMsgs   = 3
	SourceMessages = 4
	MongoMessages  = 5
	ChannelNum     = 100
)

type MsgChannelValue struct {
	uniqueKey  string
	ctx        context.Context
	ctxMsgList []*ContextMsg
}

type TriggerChannelValue struct {
	ctx      context.Context
	cMsgList []*sarama.ConsumerMessage
}

type Cmd2Value struct {
	Cmd   int
	Value any
}
type ContextMsg struct {
	message *sdkws.MsgData
	ctx     context.Context
}

type OnlineHistoryRedisConsumerHandler struct {
	owned                *ownedLegacySource
	durableIntake        *DurableKafkaIntake
	historyConsumerGroup *kafka.MConsumerGroup
	chArrays             [ChannelNum]chan Cmd2Value
	msgDistributionCh    chan Cmd2Value

	singleMsgSuccessCount      uint64
	singleMsgFailedCount       uint64
	singleMsgSuccessCountMutex sync.Mutex
	singleMsgFailedCountMutex  sync.Mutex

	msgDatabase           controller.CommonMsgDatabase
	conversationRpcClient *rpcclient.ConversationRpcClient
	groupRpcClient        *rpcclient.GroupRpcClient
	encryptionRpcClient   *rpcclient.EncryptionRpcClient
}

func NewOnlineHistoryRedisConsumerHandler(
	database controller.CommonMsgDatabase,
	conversationRpcClient *rpcclient.ConversationRpcClient,
	groupRpcClient *rpcclient.GroupRpcClient,
	encryptionRpcClient *rpcclient.EncryptionRpcClient,
) *OnlineHistoryRedisConsumerHandler {
	var och OnlineHistoryRedisConsumerHandler
	och.msgDatabase = database
	och.msgDistributionCh = make(chan Cmd2Value) // no buffer channel
	go och.MessagesDistributionHandle()
	for i := 0; i < ChannelNum; i++ {
		och.chArrays[i] = make(chan Cmd2Value, 50)
		go och.Run(i)
	}
	och.conversationRpcClient = conversationRpcClient
	och.groupRpcClient = groupRpcClient
	och.encryptionRpcClient = encryptionRpcClient
	och.historyConsumerGroup = kafka.NewMConsumerGroup(&kafka.MConsumerGroupConfig{
		KafkaVersion:   sarama.V2_0_0_0,
		OffsetsInitial: sarama.OffsetNewest, IsReturnErr: false,
	}, []string{config.Config.Kafka.LatestMsgToRedis.Topic},
		config.Config.Kafka.Addr, config.Config.Kafka.ConsumerGroupID.MsgToRedis)
	// statistics.NewStatistics(&och.singleMsgSuccessCount, config.Config.ModuleName.MsgTransferName, fmt.Sprintf("%d
	// second singleMsgCount insert to mongo", constant.StatisticsTimeInterval), constant.StatisticsTimeInterval)
	return &och
}

func (och *OnlineHistoryRedisConsumerHandler) Run(channelID int) {
	for {
		select {
		case cmd := <-och.chArrays[channelID]:
			switch cmd.Cmd {
			case SourceMessages:
				msgChannelValue := cmd.Value.(MsgChannelValue)
				ctxMsgList := msgChannelValue.ctxMsgList
				ctx := msgChannelValue.ctx
				log.ZDebug(
					ctx,
					"msg arrived channel",
					"channel id",
					channelID,
					"msgList length",
					len(ctxMsgList),
					"uniqueKey",
					msgChannelValue.uniqueKey,
				)
				storageMsgList, notStorageMsgList, storageNotificationList, notStorageNotificationList, modifyMsgList := och.getPushStorageMsgList(
					ctxMsgList,
				)
				log.ZDebug(
					ctx,
					"msg lens",
					"storageMsgList",
					len(storageMsgList),
					"notStorageMsgList",
					len(notStorageMsgList),
					"storageNotificationList",
					len(storageNotificationList),
					"notStorageNotificationList",
					len(notStorageNotificationList),
					"modifyMsgList",
					len(modifyMsgList),
				)
				conversationIDMsg := msgprocessor.GetChatConversationIDByMsg(ctxMsgList[0].message)
				conversationIDNotification := msgprocessor.GetNotificationConversationIDByMsg(ctxMsgList[0].message)
				och.handleMsg(ctx, msgChannelValue.uniqueKey, conversationIDMsg, storageMsgList, notStorageMsgList)
				och.handleNotification(
					ctx,
					msgChannelValue.uniqueKey,
					conversationIDNotification,
					storageNotificationList,
					notStorageNotificationList,
				)
				if err := och.msgDatabase.MsgToModifyMQ(ctx, msgChannelValue.uniqueKey, conversationIDNotification, modifyMsgList); err != nil {
					log.ZError(
						ctx,
						"msg to modify mq error",
						err,
						"uniqueKey",
						msgChannelValue.uniqueKey,
						"modifyMsgList",
						modifyMsgList,
					)
				}
			}
		}
	}
}

// 获取消息/通知 存储的消息列表， 不存储并且推送的消息列表，.
func (och *OnlineHistoryRedisConsumerHandler) getPushStorageMsgList(
	totalMsgs []*ContextMsg,
) (storageMsgList, notStorageMsgList, storageNotificatoinList, notStorageNotificationList, modifyMsgList []*sdkws.MsgData) {
	messages := make([]*sdkws.MsgData, 0, len(totalMsgs))
	for _, value := range totalMsgs {
		messages = append(messages, value.message)
	}
	return msgprocessor.SplitStorageMessages(messages)
}

func (och *OnlineHistoryRedisConsumerHandler) handleNotification(
	ctx context.Context,
	key, conversationID string,
	storageList, notStorageList []*sdkws.MsgData,
) {
	if och.stopOwnedWork(ctx) {
		return
	}
	och.toPushTopic(ctx, key, conversationID, notStorageList)
	if och.stopOwnedWork(ctx) {
		return
	}
	if len(storageList) > 0 {
		lastSeq, _, err := och.msgDatabase.BatchInsertChat2Cache(ctx, conversationID, storageList)
		if och.owned != nil && och.owned.Unconfirmed() {
			och.failOwned()
			return
		}
		if err != nil {
			och.failOwned()
			if och.owned != nil {
				return
			}
			if och.owned == nil {
				log.ZError(
					ctx,
					"notification batch insert to redis error",
					err,
					"conversationID",
					conversationID,
					"storageList",
					storageList,
				)
			}
			return
		}
		log.ZDebug(ctx, "success to next topic", "conversationID", conversationID)
		err = och.msgDatabase.MsgToMongoMQ(ctx, key, conversationID, storageList, lastSeq)
		if err != nil {
			och.failOwned()
			if och.owned != nil {
				return
			}
			log.ZError(ctx, "MsgToMongoMQ error", err)
		}
		och.toPushTopic(ctx, key, conversationID, storageList)
	}
}

func (och *OnlineHistoryRedisConsumerHandler) toPushTopic(
	ctx context.Context,
	key, conversationID string,
	msgs []*sdkws.MsgData,
) {
	for _, v := range msgs {
		if och.stopOwnedWork(ctx) {
			return
		}
		_, _, err := och.msgDatabase.MsgToPushMQ(ctx, key, conversationID, v)
		if err != nil {
			och.failOwned()
			if och.owned != nil {
				return
			}
		}
	}
}

func (och *OnlineHistoryRedisConsumerHandler) handleMsg(
	ctx context.Context,
	key, conversationID string,
	storageList, notStorageList []*sdkws.MsgData,
) {
	if och.stopOwnedWork(ctx) {
		return
	}
	och.toPushTopic(ctx, key, conversationID, notStorageList)
	if och.stopOwnedWork(ctx) {
		return
	}
	if len(storageList) > 0 {
		lastSeq, isNewConversation, err := och.msgDatabase.BatchInsertChat2Cache(ctx, conversationID, storageList)
		if och.owned != nil && och.owned.Unconfirmed() {
			och.failOwned()
			return
		}
		if err != nil && errs.Unwrap(err) != redis.Nil {
			och.failOwned()
			if och.owned != nil {
				return
			}
			if och.owned == nil {
				log.ZError(ctx, "batch data insert to redis err", err, "storageMsgList", storageList)
			}
			return
		}
		if isNewConversation {
			if config.Config.LongConnSvr.IsEncryption {
				if err := och.encryptionRpcClient.GenEncryptionKey(ctx, conversationID); err != nil {
					och.failOwned()
					if och.owned != nil {
						return
					}
					log.ZError(ctx, "gen encryption key error", err, "conversationID", conversationID)
				}
			}
			switch storageList[0].SessionType {
			case constant.SuperGroupChatType:
				log.ZInfo(ctx, "group chat first create conversation", "conversationID",
					conversationID)
				userIDs, err := och.groupRpcClient.GetGroupMemberIDs(ctx, storageList[0].GroupID)
				if err != nil {
					och.failOwned()
					if och.owned != nil {
						return
					}
					log.ZWarn(ctx, "get group member ids error", err, "conversationID",
						conversationID)
				} else {
					if err := och.conversationRpcClient.GroupChatFirstCreateConversation(ctx,
						storageList[0].GroupID, userIDs); err != nil {
						och.failOwned()
						if och.owned != nil {
							return
						}
						log.ZWarn(ctx, "single chat first create conversation error", err,
							"conversationID", conversationID)
					}
				}
			case constant.SingleChatType, constant.NotificationChatType:
				if err := och.conversationRpcClient.SingleChatFirstCreateConversation(ctx, storageList[0].RecvID,
					storageList[0].SendID, conversationID, storageList[0].SessionType); err != nil {
					och.failOwned()
					if och.owned != nil {
						return
					}
					log.ZWarn(ctx, "single chat or notification first create conversation error", err,
						"conversationID", conversationID, "sessionType", storageList[0].SessionType)
				}
			default:
				och.failOwned()
				if och.owned != nil {
					return
				}
				log.ZWarn(ctx, "unknown session type", nil, "sessionType",
					storageList[0].SessionType)
			}
		}

		log.ZDebug(ctx, "success incr to next topic")
		err = och.msgDatabase.MsgToMongoMQ(ctx, key, conversationID, storageList, lastSeq)
		if err != nil {
			och.failOwned()
			if och.owned != nil {
				return
			}
			log.ZError(ctx, "MsgToMongoMQ error", err)
		}
		och.toPushTopic(ctx, key, conversationID, storageList)
	}
}

func (och *OnlineHistoryRedisConsumerHandler) MessagesDistributionHandle() {
	for {
		aggregationMsgs := make(map[string][]*ContextMsg, ChannelNum)
		select {
		case cmd := <-och.msgDistributionCh:
			switch cmd.Cmd {
			case ConsumerMsgs:
				triggerChannelValue := cmd.Value.(TriggerChannelValue)
				ctx := triggerChannelValue.ctx
				consumerMessages := triggerChannelValue.cMsgList
				// Aggregation map[userid]message list
				log.ZDebug(ctx, "batch messages come to distribution center", "length", len(consumerMessages))
				for i := 0; i < len(consumerMessages); i++ {
					ctxMsg := &ContextMsg{}
					msgFromMQ := &sdkws.MsgData{}
					err := proto.Unmarshal(consumerMessages[i].Value, msgFromMQ)
					if err != nil {
						log.ZError(ctx, "msg_transfer Unmarshal msg err", err, string(consumerMessages[i].Value))
						continue
					}
					var arr []string
					for i, header := range consumerMessages[i].Headers {
						arr = append(arr, strconv.Itoa(i), string(header.Key), string(header.Value))
					}
					log.ZInfo(
						ctx,
						"consumer.kafka.GetContextWithMQHeader",
						"len",
						len(consumerMessages[i].Headers),
						"header",
						strings.Join(arr, ", "),
					)
					ctxMsg.ctx = kafka.GetContextWithMQHeader(consumerMessages[i].Headers)
					ctxMsg.message = msgFromMQ
					log.ZDebug(
						ctx,
						"single msg come to distribution center",
						"message",
						msgFromMQ,
						"key",
						string(consumerMessages[i].Key),
					)
					// aggregationMsgs[string(consumerMessages[i].Key)] =
					// append(aggregationMsgs[string(consumerMessages[i].Key)], ctxMsg)
					if oldM, ok := aggregationMsgs[string(consumerMessages[i].Key)]; ok {
						oldM = append(oldM, ctxMsg)
						aggregationMsgs[string(consumerMessages[i].Key)] = oldM
					} else {
						m := make([]*ContextMsg, 0, 100)
						m = append(m, ctxMsg)
						aggregationMsgs[string(consumerMessages[i].Key)] = m
					}
				}
				log.ZDebug(ctx, "generate map list users len", "length", len(aggregationMsgs))
				for uniqueKey, v := range aggregationMsgs {
					if len(v) >= 0 {
						hashCode := utils.GetHashCode(uniqueKey)
						channelID := hashCode % ChannelNum
						newCtx := withAggregationCtx(ctx, v)
						log.ZDebug(
							newCtx,
							"generate channelID",
							"hashCode",
							hashCode,
							"channelID",
							channelID,
							"uniqueKey",
							uniqueKey,
						)
						och.chArrays[channelID] <- Cmd2Value{Cmd: SourceMessages, Value: MsgChannelValue{uniqueKey: uniqueKey, ctxMsgList: v, ctx: newCtx}}
					}
				}
			}
		}
	}
}

func withAggregationCtx(ctx context.Context, values []*ContextMsg) context.Context {
	var allMessageOperationID string
	for i, v := range values {
		if opid := mcontext.GetOperationID(v.ctx); opid != "" {
			if i == 0 {
				allMessageOperationID += opid
			} else {
				allMessageOperationID += "$" + opid
			}
		}
	}
	return mcontext.SetOperationID(ctx, allMessageOperationID)
}

func (och *OnlineHistoryRedisConsumerHandler) Setup(_ sarama.ConsumerGroupSession) error { return nil }
func (och *OnlineHistoryRedisConsumerHandler) Cleanup(_ sarama.ConsumerGroupSession) error {
	return nil
}

func (och *OnlineHistoryRedisConsumerHandler) ConsumeClaim(
	sess sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error { // a instance in the consumer group
	if och.durableIntake != nil {
		err := och.durableIntake.consumeClaim(sess, claim)
		if err != nil && (sess == nil || sess.Context() == nil || sess.Context().Err() == nil) {
			och.durableIntake.recordFailure(err)
		}
		return err
	}
	for {
		if sess == nil {
			log.ZWarn(context.Background(), "sess == nil, waiting", nil)
			time.Sleep(100 * time.Millisecond)
		} else {
			break
		}
	}
	log.ZDebug(context.Background(), "online new session msg come", "highWaterMarkOffset",
		claim.HighWaterMarkOffset(), "topic", claim.Topic(), "partition", claim.Partition())

	split := 1000
	rwLock := new(sync.RWMutex)
	messages := make([]*sarama.ConsumerMessage, 0, 1000)
	ticker := time.NewTicker(time.Millisecond * 100)

	go func() {
		for {
			select {
			case <-ticker.C:
				if len(messages) == 0 {
					continue
				}

				rwLock.Lock()
				buffer := make([]*sarama.ConsumerMessage, 0, len(messages))
				buffer = append(buffer, messages...)

				// reuse slice, set cap to 0
				messages = messages[:0]
				rwLock.Unlock()

				start := time.Now()
				ctx := mcontext.WithTriggerIDContext(context.Background(), utils.OperationIDGenerator())
				log.ZDebug(ctx, "timer trigger msg consumer start", "length", len(buffer))
				for i := 0; i < len(buffer)/split; i++ {
					och.msgDistributionCh <- Cmd2Value{Cmd: ConsumerMsgs, Value: TriggerChannelValue{
						ctx: ctx, cMsgList: buffer[i*split : (i+1)*split],
					}}
				}
				if (len(buffer) % split) > 0 {
					och.msgDistributionCh <- Cmd2Value{Cmd: ConsumerMsgs, Value: TriggerChannelValue{
						ctx: ctx, cMsgList: buffer[split*(len(buffer)/split):],
					}}
				}

				log.ZDebug(ctx, "timer trigger msg consumer end",
					"length", len(buffer), "time_cost", time.Since(start),
				)
			}
		}
	}()

	for msg := range claim.Messages() {
		if len(msg.Value) == 0 {
			continue
		}

		rwLock.Lock()
		messages = append(messages, msg)
		rwLock.Unlock()

		sess.MarkMessage(msg, "")
	}

	return nil
}

// The old unowned handler keeps its existing error/log policy. The actual
// owned processor uses this sticky original source outcome and stops before a
// queue record could be marked/retried after an incomplete side effect.
func (och *OnlineHistoryRedisConsumerHandler) failOwned() {
	if och.owned != nil {
		och.owned.Fail()
	}
}

// Nil is the untouched ordinary default. An owned source cannot issue the next
// side effect after its original work failed or its lifetime retired.
func (och *OnlineHistoryRedisConsumerHandler) stopOwnedWork(ctx context.Context) bool {
	if och.owned == nil {
		return false
	}
	if ctx == nil || ctx.Err() != nil || och.owned.Unconfirmed() {
		och.failOwned()
		return true
	}
	return false
}
