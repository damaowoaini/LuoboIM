package office

import (
	"context"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"time"
)

type Meta struct {
	Original string `bson:"original"`
	Thumb    string `bson:"thumb"`
	Width    int32  `bson:"width"`
	Height   int32  `bson:"height"`
}

type WorkMomentContent struct {
	Metas []*Meta `bson:"metas"`
	Text  string  `bson:"text"`
	Type  int32   `bson:"type"`
}

type Comment struct {
	CommentID   string    `bson:"comment_id"`
	UserID      string    `bson:"user_id"`
	ReplyUserID string    `bson:"reply_user_id"`
	Content     string    `bson:"content"`
	CreateTime  time.Time `bson:"create_time"`
}

type LikeUser struct {
	UserID     string    `bson:"user_id"`
	CreateTime time.Time `bson:"create_time"`
}

type WorkMoment struct {
	WorkMomentID      primitive.ObjectID `bson:"_id"`
	UserID            string             `bson:"user_id"`
	Content           *WorkMomentContent `bson:"content"`
	LikeUsers         []*LikeUser        `bson:"like_users"`
	AtUserIDs         []string           `bson:"at_user_ids"`
	PermissionUserIDs []string           `bson:"permission_user_ids"`
	Comments          []*Comment         `bson:"comments"`
	Permission        int32              `bson:"permission"`
	CreateTime        time.Time          `bson:"create_time"`
}

type WorkMomentInterface interface {
	Create(ctx context.Context, workMoment *WorkMoment) error
	Delete(ctx context.Context, workMomentID string) error
	DeleteComment(ctx context.Context, workMomentID string, commentID string) error
	Get(ctx context.Context, workMomentID string) (*WorkMoment, error)
	SetWorkMomentLike(ctx context.Context, workMomentID string, likeUserID string, like bool, time time.Time) (*WorkMoment, error)
	FindPushComment(ctx context.Context, workMomentID string, comment *Comment) (*WorkMoment, error)
	GetUserSend(ctx context.Context, userID string, opUserID string, showNumber, pageNumber int32) ([]*WorkMoment, error)
	GetUserRecv(ctx context.Context, userID string, friendIDs []string, showNumber, pageNumber int32) ([]*WorkMoment, error)
	FindRelevant(ctx context.Context, userID string, time *time.Time, showNumber, pageNumber int32) ([]*WorkMoment, error)
	GetUnreadCount(ctx context.Context, userID string, time *time.Time) (int32, error)
}
