// Package favorite implements the legacy IM-domain favorite contract. An owner
// items/categories and replica-set transactions preserve atomic batch edits.
package favorite

import (
	"context"
	"github.com/OpenIMSDK/tools/errs"
	"time"
)

const DefaultCategoryID = "default"

type Favorite struct {
	RetentionBlocked bool     `json:"retentionBlocked,omitempty" bson:"-"`
	FavoriteID       string   `json:"favoriteID" bson:"favoriteID"`
	UserID           string   `json:"userID" bson:"userID"`
	FavoriteType     int      `json:"favoriteType" bson:"favoriteType"`
	SourceID         string   `json:"sourceID" bson:"sourceID"`
	ConversationID   string   `json:"conversationID" bson:"conversationID"`
	Title            string   `json:"title" bson:"title"`
	Content          string   `json:"content" bson:"content"`
	ThumbnailURL     string   `json:"thumbnailURL" bson:"thumbnailURL"`
	CategoryID       string   `json:"categoryID" bson:"categoryID"`
	Tags             []string `json:"tags" bson:"tags"`
	Notes            string   `json:"notes" bson:"notes"`
	CreateTime       int64    `json:"createTime" bson:"createTime"`
	UpdateTime       int64    `json:"updateTime" bson:"updateTime"`
	Ex               string   `json:"ex" bson:"ex"`
}
type Category struct {
	NameKey       string `json:"-" bson:"nameKey"`
	Version       int64  `json:"-" bson:"version"`
	CategoryID    string `json:"categoryID" bson:"categoryID"`
	UserID        string `json:"userID" bson:"userID"`
	CategoryName  string `json:"categoryName" bson:"categoryName"`
	CategoryColor string `json:"categoryColor" bson:"categoryColor"`
	SortOrder     int    `json:"sortOrder" bson:"sortOrder"`
	FavoriteCount int    `json:"favoriteCount" bson:"-"`
	CreateTime    int64  `json:"createTime" bson:"createTime"`
	UpdateTime    int64  `json:"updateTime" bson:"updateTime"`
	Ex            string `json:"ex" bson:"ex"`
}
type State struct {
	UserID     string     `bson:"_id"`
	Version    int64      `bson:"version"`
	Favorites  []Favorite `bson:"favorites"`
	Categories []Category `bson:"categories"`
}
type Store interface {
	Execute(context.Context, string, string, Request, time.Time) (any, error)
}

type Request struct {
	UserID         string    `json:"userID"`
	FavoriteID     string    `json:"favoriteID"`
	FavoriteIDs    []string  `json:"favoriteIDs"`
	FavoriteType   int       `json:"favoriteType"`
	SourceID       string    `json:"sourceID"`
	ConversationID string    `json:"conversationID"`
	Title          *string   `json:"title"`
	Content        string    `json:"content"`
	ThumbnailURL   string    `json:"thumbnailURL"`
	CategoryID     *string   `json:"categoryID"`
	Tags           *[]string `json:"tags"`
	Notes          *string   `json:"notes"`
	Ex             *string   `json:"ex"`
	Keyword        string    `json:"keyword"`
	Pagination     struct {
		PageNumber int `json:"pageNumber"`
		ShowNumber int `json:"showNumber"`
	} `json:"pagination"`
	Operation        int      `json:"operation"`
	TargetCategoryID string   `json:"targetCategoryID"`
	TargetTags       []string `json:"targetTags"`
	CategoryName     *string  `json:"categoryName"`
	CategoryColor    *string  `json:"categoryColor"`
	SortOrder        *int     `json:"sortOrder"`
	MoveToDefault    *bool    `json:"moveToDefault"`
}

func argument(message string) error { return errs.ErrArgs.Wrap(message) }
func missing() error                { return errs.ErrRecordNotFound.Wrap("收藏或分类不存在") }
