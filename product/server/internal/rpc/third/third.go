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

package third

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/objectverify"
	evidencepb "github.com/openimsdk/open-im-server/v3/internal/objectverify/pb"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/mgo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/cos"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/minio"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/oss"

	"google.golang.org/grpc"

	"github.com/OpenIMSDK/protocol/third"
	"github.com/OpenIMSDK/tools/discoveryregistry"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/intent"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

func Start(client discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	owned := startrpc.OwnedRPCContext(server)
	mongo, err := unrelation.NewProcessRuntimeMongo(owned)
	if mongo != nil {
		if e := startrpc.OwnRPCDatabase(server, mongo.GetDatabase().Name()); e != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return errors.Join(e, mongo.GetClient().Disconnect(closeCtx))
		}
		if e := startrpc.OwnRPCResource(server, "mongo", mongo.GetClient().Disconnect); e != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return errors.Join(e, mongo.GetClient().Disconnect(closeCtx))
		}
	}
	if err != nil {
		return err
	}
	startup := context.Background()
	if owned != nil {
		startup = owned
	}
	logdb, err := mgo.NewLogMongoWithContext(startup, mongo.GetDatabase())
	if err != nil {
		return err
	}
	s3db, err := mgo.NewS3MongoWithContext(startup, mongo.GetDatabase())
	if err != nil {
		return err
	}
	apiURL := config.Config.Object.ApiURL
	if apiURL == "" {
		return fmt.Errorf("api url is empty")
	}
	if _, err := url.Parse(config.Config.Object.ApiURL); err != nil {
		return err
	}
	if apiURL[len(apiURL)-1] != '/' {
		apiURL += "/"
	}
	apiURL += "object/"
	rdb, err := cache.NewProcessRuntimeRedis(owned)
	if err != nil {
		return err
	}
	if e := startrpc.OwnRPCResource(server, "redis", func(context.Context) error { return rdb.Close() }); e != nil {
		return errors.Join(e, rdb.Close())
	}
	// 根据配置文件策略选择 oss 方式
	enable := config.Config.Object.Enable
	var o s3.Interface
	if owned != nil {
		transport, e := newThirdObjectTransport(owned, enable)
		if e != nil {
			return e
		}
		// Registered BEFORE MinIO bucket initialization can issue any HTTP write.
		if e = startrpc.OwnRPCResource(server, "object-provider", transport.Close); e != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return errors.Join(e, transport.Close(closeCtx))
		}
		o, err = newThirdOwnedProvider(owned, enable, cache.NewMinioCache(rdb), transport)
	} else {
		switch config.Config.Object.Enable {
		case "minio":
			o, err = minio.NewMinio(cache.NewMinioCache(rdb))
		case "cos":
			o, err = cos.NewCos()
		case "oss":
			o, err = oss.NewOSS()
		default:
			err = fmt.Errorf("invalid object enable: %s", enable)
		}
	}
	if err != nil {
		return err
	}
	intents, err := intent.New(startup, mongo.GetDatabase())
	if err != nil {
		return err
	}
	third.RegisterThirdServer(server, &thirdServer{
		apiURL:        apiURL,
		thirdDatabase: controller.NewThirdDatabase(cache.NewMsgCacheModel(rdb), logdb),
		userRpcClient: rpcclient.NewUserRpcClient(client),
		s3dataBase:    controller.NewS3Database(rdb, o, s3db, intents),
		defaultExpire: time.Hour * 24 * 7,
		intents:       intents,
	})
	// Local private service, not an addition to the external SDK's Third proto.
	// A signed HTTP caller must also prove this exact RPC body independently.
	evidencepb.RegisterEvidenceVerifierServer(server, &objectverify.RPCServer{
		Verifier: &objectverify.Verifier{Objects: s3db, Engine: o.Engine(), APIBase: apiURL},
		Tokens:   cache.NewMsgCacheModel(rdb),
		Nonces:   objectverify.RedisNonces{Client: rdb},
		Secret:   []byte(config.Config.Secret),
	})
	return nil
}

type thirdServer struct {
	apiURL        string
	thirdDatabase controller.ThirdDatabase
	s3dataBase    controller.S3Database
	userRpcClient rpcclient.UserRpcClient
	defaultExpire time.Duration
	intents       *intent.Store
}

func (t *thirdServer) FcmUpdateToken(ctx context.Context, req *third.FcmUpdateTokenReq) (resp *third.FcmUpdateTokenResp, err error) {
	err = t.thirdDatabase.FcmUpdateToken(ctx, req.Account, int(req.PlatformID), req.FcmToken, req.ExpireTime)
	if err != nil {
		return nil, err
	}
	return &third.FcmUpdateTokenResp{}, nil
}

func (t *thirdServer) SetAppBadge(ctx context.Context, req *third.SetAppBadgeReq) (resp *third.SetAppBadgeResp, err error) {
	err = t.thirdDatabase.SetAppBadge(ctx, req.UserID, int(req.AppUnreadCount))
	if err != nil {
		return nil, err
	}
	return &third.SetAppBadgeResp{}, nil
}
