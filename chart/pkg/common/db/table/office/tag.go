package office

import (
	"context"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

type Tag struct {
	TagID      primitive.ObjectID `bson:"_id"`
	UserID     string             `bson:"user_id"`
	TagName    string             `bson:"tag_name"`
	UserIDs    []string           `bson:"user_ids"`
	GroupIDs   []string           `bson:"group_ids"`
	CreateTime time.Time          `bson:"create_time"`
}

type TagUser struct {
	UserID   string `bson:"user_id"`
	UserName string `bson:"user_name"`
}

type TagInterface interface {
	GetUserTags(ctx context.Context, userID string) ([]*Tag, error)
	Create(ctx context.Context, tag *Tag) error
	Get(ctx context.Context, tagID string) (*Tag, error)
	Find(ctx context.Context, tagIDs []string) ([]*Tag, error)
	Delete(ctx context.Context, tagID string) error
	Update(ctx context.Context, tagID, name string, addUserIDs []string, delUserIDs []string, addGroupIDs []string, delGroupIDs []string) error
}
