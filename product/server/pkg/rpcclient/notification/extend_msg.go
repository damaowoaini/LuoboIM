package notification

// import (
// 	"context"

// 	"github.com/OpenIMSDK/Open-IM-Server/pkg/common/constant"
// 	"github.com/OpenIMSDK/Open-IM-Server/pkg/discoveryregistry"
// 	"github.com/OpenIMSDK/Open-IM-Server/pkg/proto/extendmsg"
// 	"github.com/OpenIMSDK/Open-IM-Server/pkg/rpcclient"
// )

// type ExtendMsgNotificationSender struct {
// 	*rpcclient.NotificationSender
// }

// func NewNotificationSender(client discoveryregistry.SvcDiscoveryRegistry) *ExtendMsgNotificationSender {
// 	return &ExtendMsgNotificationSender{rpcclient.NewNotificationSender()}
// }

// func (e *ExtendMsgNotificationSender) ExtendMessageUpdatedNotification(ctx context.Context, userID, recvID, groupID, conversationID, clientMsgID string, msgFirstModifyTime int64,
// 	keyValueResp []*extendmsg.KeyValueResp, isHistory, isReactionFromCache, isExternalExtensions, isReact bool) {
// 	var notification extendmsg.ReactionMessageModifierNotification
// 	notification.ConversationID = conversationID
// 	notification.UserID = userID
// 	keyMap := make(map[string]*extendmsg.KeyValue)
// 	for _, valueResp := range keyValueResp {
// 		if valueResp.ErrCode == 0 {
// 			keyMap[valueResp.KeyValue.TypeKey] = valueResp.KeyValue
// 		}
// 	}
// 	if len(keyMap) == 0 {
// 		return
// 	}
// 	notification.SuccessReactionExtensions = keyMap
// 	notification.ClientMsgID = clientMsgID
// 	notification.IsReact = isReact
// 	notification.IsExternalExtensions = isExternalExtensions
// 	notification.MsgFirstModifyTime = msgFirstModifyTime
// 	e.Notification(ctx, userID, constant.ReactionMessageModifierNotification, &notification, isHistory, isReactionFromCache)
// }

// func (e *ExtendMsgNotificationSender) ExtendMessageDeleteNotification(ctx context.Context, sendID string, conversationID string, sessionType int32,
// 	req *extendmsg.DeleteMessagesReactionExtensionsReq, resp *extendmsg.DeleteMessagesReactionExtensionsResp, isHistory bool, isReactionFromCache bool) {
// 	var content extendmsg.ReactionMessageDeleteNotification
// 	content.ConversationID = req.ConversationID
// 	content.OpUserID = req.OpUserID
// 	content.SessionType = req.SessionType
// 	keyMap := make(map[string]*extendmsg.KeyValue)
// 	for _, valueResp := range resp.Result {
// 		if valueResp.ErrCode == 0 {
// 			keyMap[valueResp.KeyValue.TypeKey] = valueResp.KeyValue
// 		}
// 	}
// 	if len(keyMap) == 0 {
// 		return
// 	}
// 	content.SuccessReactionExtensions = keyMap
// 	content.ClientMsgID = req.ClientMsgID
// 	content.MsgFirstModifyTime = req.MsgFirstModifyTime
// 	e.messageReactionSender(ctx, sendID, conversationID, sessionType, constant.ReactionMessageDeleter, &content, isHistory, isReactionFromCache)
// }

// func (e *ExtendMsgNotificationSender) ExtendMessageAddedNotification(ctx context.Context, conversationID string, userID string) {
// }
