package office

import (
	"context"
	"time"
)

type WorkMomentRead struct {
	UserID    string     `bson:"_id"`
	CountTime *time.Time `bson:"count_time"`
	ListTime  *time.Time `bson:"list_time"`
}

type WorkMomentReadInterface interface {
	Set(ctx context.Context, userID string, time time.Time, setType int32) error
	Get(ctx context.Context, userID string) (*WorkMomentRead, error)
}
