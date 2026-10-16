package dbconn

import (
	"context"
	"fmt"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mw/specialerror"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

func NewMongo() (*mongo.Client, error) {
	specialerror.AddReplace(mongo.ErrNoDocuments, errs.ErrRecordNotFound)
	uri := "mongodb://sample.host:27017/?maxPoolSize=20&w=majority"
	if *config.Config.Mongo.Uri != "" {
		// example: mongodb://$user:$password@mongo1.mongo:27017,mongo2.mongo:27017,mongo3.mongo:27017/$DBDatabase/?replicaSet=rs0&readPreference=secondary&authSource=admin&maxPoolSize=$DBMaxPoolSize
		uri = *config.Config.Mongo.Uri
	} else {
		//mongodb://mongodb1.example.com:27317,mongodb2.example.com:27017/?replicaSet=mySet&authSource=authDB
		mongodbHosts := ""
		for i, v := range *config.Config.Mongo.Address {
			if i == len(*config.Config.Mongo.Address)-1 {
				mongodbHosts += v
			} else {
				mongodbHosts += v + ","
			}
		}
		if *config.Config.Mongo.Password != "" && *config.Config.Mongo.Username != "" {
			uri = fmt.Sprintf("mongodb://%s:%s@%s/%s?maxPoolSize=%d&authSource=admin",
				*config.Config.Mongo.Username, *config.Config.Mongo.Password, mongodbHosts,
				*config.Config.Mongo.Database, *config.Config.Mongo.MaxPoolSize)
		} else {
			uri = fmt.Sprintf("mongodb://%s/%s/?maxPoolSize=%d&authSource=admin",
				mongodbHosts, *config.Config.Mongo.Database,
				*config.Config.Mongo.MaxPoolSize)
		}
	}
	fmt.Println("mongo:", uri)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()
	mongoClient, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	return mongoClient, nil
}
