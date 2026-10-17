package office

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	"github.com/OpenIMSDK/chat/pkg/common/dbconn"
	"github.com/OpenIMSDK/chat/pkg/proto/common"
	"github.com/OpenIMSDK/chat/pkg/proto/office"
	"github.com/OpenIMSDK/chat/pkg/rpclient/chat"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"google.golang.org/grpc"
)

func Start(zk discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	mgo, err := dbconn.NewMongo()
	if err != nil {
		return err
	}
	db, err := database.NewOfficeDatabase(mgo.Database(*config.Config.Mongo.Database))
	if err != nil {
		return err
	}
	office.RegisterOfficeServer(server, &officeServer{
		db:   db,
		user: chat.NewChatClient(zk),
	})
	return nil
}

type officeServer struct {
	db   database.OfficeDatabaseInterface
	user publicUserReader
}

type publicUserReader interface {
	FindUserPublicInfo(context.Context, []string) ([]*common.UserPublicInfo, error)
	MapUserPublicInfo(context.Context, []string) (map[string]*common.UserPublicInfo, error)
}
