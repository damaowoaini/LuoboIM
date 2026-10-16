package controller

import (
	"context"

	"github.com/OpenIMSDK/protocol/encryption"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
)

type EncryptionDatabase interface {
	GetConversationEncryptionAllKey(ctx context.Context, conversationID string) ([]*encryption.VersionKey, error)
	GetConversationVersionKey(ctx context.Context, conversationID string, version int32) (string, error)
	GenConversationNextVersionKey(ctx context.Context, conversationID string, fn func() (string, error)) error
	GetConversationMaxVersionKey(ctx context.Context, conversationID string) (*encryption.VersionKey, error)
}

type encryptionDatabase struct {
	cache cache.EncryptionCache
}

func NewEncryptionDatabase(cache cache.EncryptionCache) *encryptionDatabase {
	return &encryptionDatabase{cache: cache}
}

func (e encryptionDatabase) GetConversationEncryptionAllKey(ctx context.Context, conversationID string) ([]*encryption.VersionKey, error) {
	return e.cache.GetConversationEncryptionAllKey(ctx, conversationID)
}

func (e encryptionDatabase) GetConversationVersionKey(ctx context.Context, conversationID string, version int32) (string, error) {
	return e.cache.GetConversationVersionKey(ctx, conversationID, version)
}

func (e encryptionDatabase) GenConversationNextVersionKey(ctx context.Context, conversationID string, fn func() (string, error)) error {
	return e.cache.GenConversationNextVersionKey(ctx, conversationID, fn)
}

func (e encryptionDatabase) GetConversationMaxVersionKey(ctx context.Context, conversationID string) (*encryption.VersionKey, error) {
	return e.cache.GetConversationMaxVersionKey(ctx, conversationID)
}
