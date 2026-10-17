package office

import (
	"context"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

type TagSendLog struct {
	ID         primitive.ObjectID `bson:"_id"`
	SendUserID string             `bson:"send_user_id"`
	PlatformID int32              `bson:"platform_id"`
	UserIDs    []string           `bson:"userIDs"`
	TagIDs     []string           `bson:"tagIDs"`
	GroupIDs   []string           `bson:"group_ids"`
	Content    string             `bson:"content"`
	SendTime   time.Time          `bson:"send_time"`
}

type SendTagLogInterface interface {
	Page(ctx context.Context, userID string, pageNumber int32, showNumber int32) ([]*TagSendLog, error)
	FindSendUserID(ctx context.Context, ids []string) (map[string]string, error)
	Delete(ctx context.Context, ids []string) error
	Create(ctx context.Context, tag *TagSendLog) error
}
