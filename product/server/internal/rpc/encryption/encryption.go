package encryption

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/pkg/common/startrpc"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/encryption"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/grouphistory"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"strconv"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
)

type conversationReader interface {
	GetConversation(context.Context, string, string) (*conversation.Conversation, error)
}
type membershipHistory interface {
	Current(context.Context, string, string) (bool, error)
	ObserveCurrent(context.Context, string, string) error
	LastLeft(context.Context, string, string) (*time.Time, error)
}
type encryptionServer struct {
	conversations        conversationReader
	history              membershipHistory
	rdb                  redis.UniversalClient
	conversationDatabase controller.EncryptionDatabase
}

func Start(discov discoveryregistry.SvcDiscoveryRegistry, server *grpc.Server) error {
	rdb, err := cache.NewProcessRuntimeRedis(startrpc.OwnedRPCContext(server))
	if err != nil {
		return err
	}
	if err := startrpc.OwnRPCResource(server, "redis", func(context.Context) error { return rdb.Close() }); err != nil {
		_ = rdb.Close()
		return err
	}

	conversations := rpcclient.NewConversationRpcClient(discov)
	mongoDB, err := unrelation.NewProcessRuntimeMongo(startrpc.OwnedRPCContext(server))
	if mongoDB != nil {
		if trackErr := startrpc.OwnRPCDatabase(server, mongoDB.GetDatabase().Name()); trackErr != nil {
			_ = mongoDB.GetClient().Disconnect(context.Background())
			return trackErr
		}
		if trackErr := startrpc.OwnRPCResource(server, "mongo", mongoDB.GetClient().Disconnect); trackErr != nil {
			_ = mongoDB.GetClient().Disconnect(context.Background())
			return trackErr
		}
	}
	if err != nil {
		return err
	}

	history, err := grouphistory.New(context.Background(), mongoDB.GetDatabase())
	if err != nil {
		return err
	}
	encryption.RegisterEncryptionServer(server, &encryptionServer{
		conversations: &conversations, history: history, rdb: rdb,
		conversationDatabase: controller.NewEncryptionDatabase(cache.NewEncryptionCacheRedis(rdb, cache.GetDefaultOpt())),
	})
	return nil
}

func (e *encryptionServer) GetEncryptionKey(ctx context.Context, req *encryption.GetEncryptionKeyReq) (resp *encryption.GetEncryptionKeyResp, err error) {
	if req.ConversationID == "" || req.KeyVersion < 0 {
		return nil, errs.ErrArgs.Wrap("invalid encryption key request")
	}
	cutoff, err := e.checkReader(ctx, req.ConversationID)
	if err != nil {
		return nil, err
	}
	if req.KeyVersion == 0 {
		keyList, err := e.conversationDatabase.GetConversationEncryptionAllKey(ctx, req.ConversationID)
		if err != nil {
			return nil, err
		}
		if cutoff != nil {
			allowed := make([]*encryption.VersionKey, 0, len(keyList))
			for _, key := range keyList {
				ok, err := e.historicalVersion(ctx, req.ConversationID, key.Version, *cutoff)
				if err != nil {
					return nil, err
				}
				if ok {
					allowed = append(allowed, key)
				}
			}
			keyList = allowed
		}
		resp := &encryption.GetEncryptionKeyResp{VersionKeyList: keyList}
		return resp, nil
	} else {
		if cutoff != nil {
			ok, err := e.historicalVersion(ctx, req.ConversationID, req.KeyVersion, *cutoff)
			if err != nil {
				return nil, err
			}
			if !ok {
				return nil, errs.ErrNoPermission.Wrap("key version was created after group membership ended")
			}
		}
		key, err := e.conversationDatabase.GetConversationVersionKey(ctx, req.ConversationID, req.KeyVersion)
		if err != nil {
			return nil, err
		}
		resp := &encryption.GetEncryptionKeyResp{VersionKeyList: []*encryption.VersionKey{{
			Version: req.KeyVersion,
			Key:     key,
		}}}
		return resp, nil
	}
}
func (e *encryptionServer) GenEncryptionKey(ctx context.Context, req *encryption.GenEncryptionKeyReq) (*encryption.GenEncryptionKeyResp, error) {
	if err := authverify.CheckAdmin(ctx); err != nil {
		return nil, err
	}
	err := e.conversationDatabase.GenConversationNextVersionKey(ctx, req.ConversationID, generateAESKey)
	if err != nil {
		return nil, err
	}
	return &encryption.GenEncryptionKeyResp{}, nil
}

func (e *encryptionServer) checkReader(ctx context.Context, conversationID string) (*time.Time, error) {
	actor := mcontext.GetOpUserID(ctx)
	if actor == "" {
		return nil, errs.ErrNoPermission.Wrap("missing authenticated user")
	}
	if authverify.IsAppManagerUid(ctx) {
		return nil, nil
	}
	if e.conversations == nil {
		return nil, errs.ErrNoPermission.Wrap("conversation authorization unavailable")
	}
	conv, err := e.conversations.GetConversation(ctx, actor, conversationID)
	if err != nil {
		return nil, err
	}
	if conv == nil || conv.OwnerUserID != actor || conv.ConversationID != conversationID {
		return nil, errs.ErrNoPermission.Wrap("not a conversation participant")
	}
	expected := ""
	switch conv.ConversationType {
	case constant.SingleChatType:
		if conv.UserID != "" {
			expected = msgprocessor.GetConversationIDBySessionType(constant.SingleChatType, actor, conv.UserID)
		}
	case constant.GroupChatType, constant.SuperGroupChatType:
		if conv.GroupID != "" {
			expected = msgprocessor.GetConversationIDBySessionType(int(conv.ConversationType), conv.GroupID)
		}
	}
	if expected == "" || expected != conversationID {
		return nil, errs.ErrNoPermission.Wrap("conversation identity does not match its participants")
	}
	if conv.GroupID != "" {
		if e.history == nil {
			return nil, errs.ErrNoPermission.Wrap("membership authorization unavailable")
		}
		current, err := e.history.Current(ctx, conv.GroupID, actor)
		if err != nil {
			return nil, err
		}
		if current {
			if err := e.history.ObserveCurrent(ctx, conv.GroupID, actor); err != nil {
				return nil, err
			}
			return nil, nil
		}
		left, err := e.history.LastLeft(ctx, conv.GroupID, actor)
		if err != nil || left == nil {
			return nil, errs.ErrNoPermission.Wrap("historical group membership could not be verified")
		}
		return left, nil
	}
	return nil, nil
}
func (e *encryptionServer) historicalVersion(ctx context.Context, conversationID string, version int32, left time.Time) (bool, error) {
	if e.rdb == nil {
		return false, errs.ErrNoPermission.Wrap("key version history unavailable")
	}
	value, err := e.rdb.HGet(ctx, cache.EncryptionKeyPrefix+conversationID, "ISSUED_AT:"+strconv.Itoa(int(version))).Result()
	// Versions created before this upgrade have no timestamp. A verified past
	// member can still decrypt those existing historical keys; no key is reset.
	if err == redis.Nil {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	at, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false, err
	}
	return at <= left.UnixMilli(), nil
}
