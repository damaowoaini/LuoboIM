//go:build js && wasm

package open_im_sdk

import (
	"context"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/openim-sdk-core/v3/open_im_sdk_callback"
	"github.com/openimsdk/openim-sdk-core/v3/pkg/sdkerrs"
	"github.com/openimsdk/openim-sdk-core/v3/sdk_struct"
)

// SendBufferMessage uses the existing messageCall callback/progress and the
// original Conversation.SendMessage implementation. The final closure checks
// the frozen session after messageCall's asynchronous scheduling boundary.
// This API exists only in Web WASM; native SendMessage is unchanged.
func SendBufferMessage(callback open_im_sdk_callback.SendMsgCallBack, operationID, message, recvID, groupID, push string, current func() bool) {
	sendBoundBufferMessage(callback, operationID, message, recvID, groupID, push, false, current)
}

func SendOrdinaryMessageWithBufferScope(callback open_im_sdk_callback.SendMsgCallBack, operationID, message, recvID, groupID, push string, online bool, current func() bool) {
	sendBoundBufferMessage(callback, operationID, message, recvID, groupID, push, online, current)
}

func sendBoundBufferMessage(callback open_im_sdk_callback.SendMsgCallBack, operationID, message, recvID, groupID, push string, online bool, current func() bool) {
	mgr := UserForSDK
	if mgr == nil || !current() {
		callback.OnError(sdkerrs.ArgsError, "buffer session expired")
		return
	}
	base, conversation := mgr.BaseCtx(), mgr.Conversation()
	bound := func(ctx context.Context, m *sdk_struct.MsgStruct, recv, group string, info *sdkws.OfflinePushInfo, online bool) (*sdk_struct.MsgStruct, error) {
		if !current() || UserForSDK != mgr || mgr.BaseCtx() != base || mgr.Conversation() != conversation || base.Err() != nil {
			return nil, sdkerrs.ErrArgs.Wrap("buffer session expired before dispatch")
		}
		return conversation.SendMessage(ctx, m, recv, group, info, online)
	}
	messageCall(callback, operationID, bound, message, recvID, groupID, push, online)
}
