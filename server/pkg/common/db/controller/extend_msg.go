package controller

import (
	"context"
	"time"

	unRelationTb "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
)

type ExtendMsgDatabase interface {
	JudgeMessageReactionExist(ctx context.Context, conversationID, clientMsgID string) (isExist bool, err error)
	SetMessageTypeKeyValue(ctx context.Context, conversationID, clientMsgID string, typeKey, value string) error
	SetMessageReactionExpire(ctx context.Context, conversationID string, clientMsgID string, expiration time.Duration) error
	GetMessageTypeKeyValue(ctx context.Context, conversationID string, clientMsgID string, typeKey string) (string, error)
	GetOneMessageAllReactions(ctx context.Context, conversationID, clientMsgID string) (map[string]string, error)
	DeleteOneMessageKey(ctx context.Context, conversationID string, clientMsgID string, subKey string) error

	// Persistence
	PresistCreateExtendMsgSet(ctx context.Context, set *unRelationTb.ExtendMsgSetModel) error
	GetAllExtendMsgSetFromPersistence(ctx context.Context, ID string, opts *unRelationTb.GetAllExtendMsgSetOpts) (sets []*unRelationTb.ExtendMsgSetModel, err error)
	GetExtendMsgSetFromPersistence(ctx context.Context, conversationID string, sessionType int32, maxMsgUpdateTime int64) (*unRelationTb.ExtendMsgSetModel, error)
	PresistInsertExtendMsg(ctx context.Context, conversationID string, sessionType int32, msg *unRelationTb.ExtendMsgModel) error
	PresistInsertOrUpdateReactionExtendMsgSet(
		ctx context.Context,
		conversationID string,
		sessionType int32,
		clientMsgID string,
		msgFirstModifyTime int64,
		reactionExtensionList map[string]*unRelationTb.KeyValueModel,
	) error
	PresistDeleteReactionExtendMsgSet(
		ctx context.Context,
		conversationID string,
		sessionType int32,
		clientMsgID string,
		msgFirstModifyTime int64,
		reactionExtensionList map[string]*unRelationTb.KeyValueModel,
	) error
	GetExtendMsgFromPersistence(ctx context.Context, conversationID string, clientMsgID string, maxMsgUpdateTime int64) (extendMsg *unRelationTb.ExtendMsgModel, err error)
}

type extendMsgDatabase struct {
}

// func NewExtendMsgDatabase() ExtendMsgDatabase {
// 	return &extendMsgDatabase{}
// }

// func (e *extendMsgDatabase) JudgeMessageReactionExist(ctx context.Context, conversationID, clientMsgID string) (isExist bool, err error) {
// 	return
// }

// func (e *extendMsgDatabase) SetMessageTypeKeyValue(ctx context.Context, conversationID, clientMsgID string, typeKey, value string) error {

// }

// func (e *extendMsgDatabase) SetMessageReactionExpire(ctx context.Context, conversationID string, clientMsgID string, expiration time.Duration) error {

// }

// func (e *extendMsgDatabase) GetMessageTypeKeyValue(ctx context.Context, conversationID string, clientMsgID string, typeKey string) (string, error) {
// }

// func (e *extendMsgDatabase) GetOneMessageAllReactions(ctx context.Context, conversationID, clientMsgID string) (map[string]string, error) {
// }

// func (e *extendMsgDatabase) DeleteOneMessageKey(ctx context.Context, conversationID string, clientMsgID string, subKey string) error {
// }

// // Persistence
// func (e *extendMsgDatabase) PresistCreateExtendMsgSet(ctx context.Context, set *unRelationTb.ExtendMsgSetModel) error {
// }

// func (e *extendMsgDatabase) GetAllExtendMsgSetFromPersistence(ctx context.Context, ID string, opts *unRelationTb.GetAllExtendMsgSetOpts) (sets []*unRelationTb.ExtendMsgSetModel, err error) {
// }

// func (e *extendMsgDatabase) GetExtendMsgSetFromPersistence(ctx context.Context, conversationID string, sessionType int32, maxMsgUpdateTime int64) (*unRelationTb.ExtendMsgSetModel, error) {
// }

// func (e *extendMsgDatabase) PresistInsertExtendMsg(ctx context.Context, conversationID string, sessionType int32, msg *unRelationTb.ExtendMsgModel) error {
// }

// func (e *extendMsgDatabase) PresistInsertOrUpdateReactionExtendMsgSet(ctx context.Context, conversationID string, sessionType int32, clientMsgID string, msgFirstModifyTime int64,
// reactionExtensionList map[string]*unRelationTb.KeyValueModel) error {
// }

// func (e *extendMsgDatabase) PresistDeleteReactionExtendMsgSet(ctx context.Context, conversationID string, sessionType int32, clientMsgID string, msgFirstModifyTime int64, reactionExtensionList
// map[string]*unRelationTb.KeyValueModel) error {
// }

// func (e *extendMsgDatabase) GetExtendMsgFromPersistence(ctx context.Context, conversationID string, clientMsgID string, maxMsgUpdateTime int64) (extendMsg *unRelationTb.ExtendMsgModel, err error) {
// }
