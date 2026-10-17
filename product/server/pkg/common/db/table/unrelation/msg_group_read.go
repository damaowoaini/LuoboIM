package unrelation

import (
	"context"
	"time"
)

type MsgRead struct {
}

type GroupMsgReadModel struct {
	ConversationID string                           `bson:"conversation_id"`
	Index          int                              `bson:"index"`
	Msgs           map[string]map[string]*time.Time `bson:"msgs"`
}

func (g *GroupMsgReadModel) TableName() string {
	return "group_msg_read"
}

type GroupMsgReadInterface interface {
	Insert(ctx context.Context, model *GroupMsgReadModel) error
	InsertMsg(ctx context.Context, conversationID string, index int, clientMsgID string, users map[string]*time.Time) error
	SetMsgRead(ctx context.Context, conversationID string, index int, clientMsgID string, userID string, readTime *time.Time) error
	GetMsgRead(ctx context.Context, conversationID string, index int, clientMsgID string) (map[string]*time.Time, error)
	GetMsgIndex(ctx context.Context, conversationID string, clientMsgID string) (int, error)
	GetMaxIndex(ctx context.Context, conversationID string) (index int, num int, err error)
}
