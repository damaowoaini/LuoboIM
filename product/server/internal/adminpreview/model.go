package adminpreview

import (
	"context"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"time"
)

type Request struct {
	UserID         string `json:"userID"`
	ConversationID string `json:"conversationID"`
	Keyword        string `json:"keyword"`
	StartTime      int64  `json:"startTime"`
	EndTime        int64  `json:"endTime"`
	PageNumber     int    `json:"pageNumber"`
	PageSize       int    `json:"pageSize"`
	Cursor         string `json:"cursor"`
}
type Conversation struct {
	ConversationID    string `json:"conversationID"`
	ConversationType  int32  `json:"conversationType"`
	UserID            string `json:"userID"`
	GroupID           string `json:"groupID"`
	ShowName          string `json:"showName"`
	FaceURL           string `json:"faceURL"`
	IsPinned          bool   `json:"isPinned"`
	LatestMsgSendTime int64  `json:"latestMsgSendTime"`
}
type ConversationsResponse struct {
	Conversations []Conversation `json:"conversations"`
	Total         int            `json:"total"`
	PageNumber    int            `json:"pageNumber"`
	PageSize      int            `json:"pageSize"`
}
type SearchResponse struct {
	Messages          []map[string]any `json:"messages"`
	NextCursor        string           `json:"nextCursor"`
	HasMore           bool             `json:"hasMore"`
	ScannedCount      int              `json:"scannedCount"`
	UnsearchableCount int              `json:"unsearchableCount"`
	SearchComplete    bool             `json:"searchComplete"`
}
type Source interface {
	List(context.Context, string) ([]Conversation, error)
	Get(context.Context, string, string) (*relation.ConversationModel, error)
	Bounds(context.Context, *relation.ConversationModel) (int64, int64, error)
	Read(context.Context, string, string, []int64) ([]*sdkws.MsgData, error)
	Key(context.Context, string, int32) (string, error)
}
type Auditor interface {
	Begin(context.Context, string, string, Request) (string, error)
	Finish(context.Context, string, string, int, int) error
}
type Service struct {
	Source    Source
	Audit     Auditor
	CursorKey []byte
	Now       func() time.Time
}
