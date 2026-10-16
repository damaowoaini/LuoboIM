package msgprocessor

import (
 "github.com/OpenIMSDK/protocol/constant"
 "github.com/OpenIMSDK/protocol/sdkws"
 "google.golang.org/protobuf/proto"
)

// SplitStorageMessages is shared by the existing Kafka Run path and the new
// durable source producer. Its historical visible clone/n_ option mutation is
// deliberate. In particular a silent non-history n_ tip stays Seq0: accepting
// its source durably does not grant permission to replay it as positive history.
func SplitStorageMessages(totalMsgs []*sdkws.MsgData) (storageMsgList, notStorageMsgList, storageNotificatoinList, notStorageNotificationList, modifyMsgList []*sdkws.MsgData) {
	isStorage := func(msg *sdkws.MsgData) bool {
		options2 := Options(msg.Options)
		if options2.IsHistory() {
			return true
		} else {
			// if !(!options2.IsSenderSync() && conversationID == msg.MsgData.SendID) {
			// 	return false
			// }
			return false
		}
	}
	for _, message := range totalMsgs {
		options := Options(message.Options)
		if !options.IsNotNotification() {
			// clone msg from notificationMsg
			if options.IsSendMsg() {
				msg := proto.Clone(message).(*sdkws.MsgData)
				// 消息
				if message.Options != nil {
					msg.Options = NewMsgOptions()
				}
				if options.IsOfflinePush() {
					message.Options = WithOptions(
						message.Options,
						WithOfflinePush(false),
					)
					msg.Options = WithOptions(msg.Options, WithOfflinePush(true))
				}
				if options.IsUnreadCount() {
					message.Options = WithOptions(
						message.Options,
						WithUnreadCount(false),
					)
					msg.Options = WithOptions(msg.Options, WithUnreadCount(true))
				}
				storageMsgList = append(storageMsgList, msg)
			}
			if isStorage(message) {
				storageNotificatoinList = append(storageNotificatoinList, message)
			} else {
				notStorageNotificationList = append(notStorageNotificationList, message)
			}
		} else {
			if isStorage(message) {
				storageMsgList = append(storageMsgList, message)
			} else {
				notStorageMsgList = append(notStorageMsgList, message)
			}
		}
		if message.ContentType == constant.ReactionMessageModifier ||
			message.ContentType == constant.ReactionMessageDeleter {
			modifyMsgList = append(modifyMsgList, message)
		}
	}
	return
}
