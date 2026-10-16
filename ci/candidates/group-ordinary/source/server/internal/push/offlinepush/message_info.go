package offlinepush

import (
	"errors"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/utils"
)

var ErrFrozenMessageInfo=errors.New("invalid frozen offline message")

// Shared with the actual legacy Pusher entry. No mutable configuration or
// provider method is consulted when deriving the message's original defaults.
func MessageOptions(msg *sdkws.MsgData)(*Opts,error){
	if msg==nil{return nil,ErrFrozenMessageInfo}
	opts:=&Opts{Signal:&Signal{}}
	if msg.OfflinePushInfo!=nil{opts.IOSBadgeCount=msg.OfflinePushInfo.IOSBadgeCount;opts.IOSPushSound=msg.OfflinePushInfo.IOSPushSound;opts.Ex=msg.OfflinePushInfo.Ex}
	return opts,nil
}

func MessageInfo(conversationID string,msg *sdkws.MsgData)(title,content string,opts *Opts,err error){
	opts,err=MessageOptions(msg);if err!=nil{return}
	if msg.OfflinePushInfo!=nil{title=msg.OfflinePushInfo.Title;content=msg.OfflinePushInfo.Desc}
	if title==""{
		switch msg.ContentType{
		case constant.Text,constant.Picture,constant.Voice,constant.Video,constant.File:title=constant.ContentType2PushContent[int64(msg.ContentType)]
		case constant.AtText:
			var at struct{Text string `json:"text"`;AtUserList []string `json:"atUserList"`;IsAtSelf bool `json:"isAtSelf"`}
			_ = utils.JsonStringToStruct(string(msg.Content),&at)
			if utils.IsContain(conversationID,at.AtUserList){title=constant.ContentType2PushContent[constant.AtText]+constant.ContentType2PushContent[constant.Common]}else{title=constant.ContentType2PushContent[constant.GroupMsg]}
		case constant.SignalingNotification:title=constant.ContentType2PushContent[constant.SignalMsg]
		default:title=constant.ContentType2PushContent[constant.Common]
		}
	}
	if content==""{content=title};return
}
