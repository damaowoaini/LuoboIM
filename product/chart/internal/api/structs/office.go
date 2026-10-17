package structs

import (
	"github.com/OpenIMSDK/chat/pkg/proto/common"
	"github.com/OpenIMSDK/protocol/sdkws"
)

type Tag struct {
	TagID      string                   `json:"tagID"`
	TagName    string                   `json:"tagName"`
	Users      []*common.UserPublicInfo `json:"users"`
	Groups     []*sdkws.GroupInfo       `json:"groups"`
	CreateTime int64                    `json:"createTime"`
}

type TagSendLog struct {
	Id       string                   `json:"id"`
	Tags     []*Tag                   `json:"tags"`
	Users    []*common.UserPublicInfo `json:"users"`
	Groups   []*sdkws.GroupInfo       `json:"groups"`
	Content  string                   `json:"content"`
	SendTime int64                    `json:"sendTime"`
}
