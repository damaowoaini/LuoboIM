package extendmsg

import (
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"google.golang.org/grpc"
)

type ExtendMsgServer struct {
}

func Start(client discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	// rdb, err := cache.NewRedis()
	// if err != nil {
	// 	return err
	// }
	// mongo, err := unrelation.NewMongo()
	// if err != nil {
	// 	return err
	// }
	// if err := mongo.CreateMsgIndex(); err != nil {
	// 	return err
	// }
	// cacheModel := cache.NewMsgCacheModel(rdb)
	// msgDocModel := unrelation.NewMsgMongoDriver(mongo.GetDatabase())
	// msgDatabase := controller.NewCommonMsgDatabase(msgDocModel, cacheModel)
	// conversationClient := rpcclient.NewConversationRpcClient(client)
	// userRpcClient := rpcclient.NewUserRpcClient(client)
	// groupRpcClient := rpcclient.NewGroupRpcClient(client)
	// friendRpcClient := rpcclient.NewFriendRpcClient(client)
	// s := &msgServer{
	// 	Conversation:           &conversationClient,
	// 	User:                   &userRpcClient,
	// 	Group:                  &groupRpcClient,
	// 	MsgDatabase:            msgDatabase,
	// 	RegisterCenter:         client,
	// 	GroupLocalCache:        localcache.NewGroupLocalCache(&groupRpcClient),
	// 	ConversationLocalCache: localcache.NewConversationLocalCache(&conversationClient),
	// 	friend:                 &friendRpcClient,
	// 	MessageLocker:          NewLockerMessage(cacheModel),
	// }
	// s.notificationSender = rpcclient.NewNotificationSender(rpcclient.WithLocalSendMsg(s.SendMsg))
	// s.addInterceptorHandler(MessageHasReadEnabled)
	// s.initPrometheus()
	// msg.RegisterMsgServer(server, s)
	return nil
}
