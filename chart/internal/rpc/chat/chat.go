// Copyright © 2023 OpenIM open source community. All rights reserved.
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

package chat

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/OpenIMSDK/chat/pkg/common/credentialmigration"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"google.golang.org/grpc"

	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/db/cache"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	chat2 "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/dbconn"
	"github.com/OpenIMSDK/chat/pkg/common/profilesync"
	"github.com/OpenIMSDK/chat/pkg/email"
	"github.com/OpenIMSDK/chat/pkg/proto/chat"
	chatClient "github.com/OpenIMSDK/chat/pkg/rpclient/chat"
	"github.com/OpenIMSDK/chat/pkg/sms"
)

func Start(discov discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	db, err := dbconn.NewGormDB()
	if err != nil {
		return err
	}
	tables := []any{
		credentialmigration.FormatUpgrade{},
		registration.Receipt{},
		accountstate.Cancellation{},
		accountstate.CredentialRevocation{},
		profilesync.Job{},
		adminprovision.Intent{}, adminprovision.Job{}, adminprovision.RecoveryAudit{},
		chat2.Register{},
		chat2.Attribute{},
		chat2.VerifyCode{},
		chat2.UserLoginRecord{},
		chat2.Log{},
	}
	if err := credentialmigration.MigrateSchema(context.Background(), db, "accounts", chat2.Account{}, chat2.AccountMetadataMigration{}, tables...); err != nil {
		return err
	}
	s, err := sms.New()
	if err != nil {
		return err
	}
	email, err := email.NewMail()
	if err != nil {
		return err
	}
	if err := discov.CreateRpcRootNodes([]string{config.Config.RpcRegisterName.OpenImAdminName, config.Config.RpcRegisterName.OpenImChatName}); err != nil {
		panic(err)
	}
	rdb, err := cache.NewRedis()
	if err != nil {
		return err
	}
	ownership := func(ctx context.Context, id string) error { return adminprovision.RequireReady(ctx, db, id) }
	credentials := &accountstate.CredentialService{Ownership: ownership, Store: accountstate.NewSQLCredentialStore(db), Sessions: accountstate.NewRedisSessions(rdb, apicall.NewCallerInterface())}
	go credentials.Run(context.Background())
	profileSync := &profilesync.Service{Ownership: ownership, DB: db, IM: apicall.NewCallerInterface()}
	go profileSync.Run(context.Background())
	adminClient := chatClient.NewAdminClient(discov)
	provisioning, err := newAdminProvisioning(db, adminClient)
	if err != nil {
		return err
	}
	go provisioning.Run(context.Background())
	chat.RegisterChatServer(server, &chatSvr{
		Provisioning:         provisioning,
		Credentials:          credentials,
		ProfileSync:          profileSync,
		Database:             database.NewChatDatabase(db),
		Admin:                adminClient,
		SMS:                  s,
		Mail:                 email,
		ImApiCaller:          apicall.NewCallerInterface(),
		FriendAddLimit:       cache.NewFriendAddLimitInterface(rdb),
		LoginRegisterIPLimit: cache.NewLoginRegisterIPLimitInterface(rdb),
		LoginFailLock:        cache.NewLoginFailLock(rdb),
	})
	return nil
}

type chatSvr struct {
	Provisioning         *adminprovision.Service
	Credentials          *accountstate.CredentialService
	ProfileSync          *profilesync.Service
	Database             database.ChatDatabaseInterface
	Admin                *chatClient.AdminClient
	SMS                  sms.SMS
	Mail                 email.Mail
	ImApiCaller          apicall.CallerInterface
	FriendAddLimit       cache.FriendAddLimitInterface
	LoginRegisterIPLimit cache.LoginRegisterIPLimitInterface
	LoginFailLock        cache.LoginFailLockInterface
}
