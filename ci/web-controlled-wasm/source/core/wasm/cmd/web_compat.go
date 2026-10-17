//go:build js && wasm

// Web-only adapters. Keep native SDK build inputs and wire contracts unchanged.
package main

import (
	"github.com/openimsdk/openim-sdk-core/v3/wasm/wasm_wrapper"
	"syscall/js"
)

func registerControlledWebCompat(common *wasm_wrapper.WrapperCommon) {
	wrapper := wasm_wrapper.NewWrapperWebCompat(common)
	js.Global().Set("getGroupMessageReaderList", js.FuncOf(wrapper.GetGroupMessageReaderList))
	js.Global().Set("getSignalingInvitationInfoStartApp", js.FuncOf(wrapper.GetSignalingInvitationInfoStartApp))
	js.Global().Set("sendGroupMessageReadReceipt", js.FuncOf(wrapper.SendGroupMessageReadReceipt))
	js.Global().Set("setConversationIsMsgDestruct", js.FuncOf(wrapper.SetConversationIsMsgDestruct))
	js.Global().Set("setConversationMsgDestructTime", js.FuncOf(wrapper.SetConversationMsgDestructTime))
	js.Global().Set("setSelfInfoEx", js.FuncOf(wrapper.SetSelfInfoEx))
	js.Global().Set("signalingAccept", js.FuncOf(wrapper.SignalingAccept))
	js.Global().Set("signalingCancel", js.FuncOf(wrapper.SignalingCancel))
	js.Global().Set("signalingCloseRoom", js.FuncOf(wrapper.SignalingCloseRoom))
	js.Global().Set("signalingCreateMeeting", js.FuncOf(wrapper.SignalingCreateMeeting))
	js.Global().Set("signalingGetMeetings", js.FuncOf(wrapper.SignalingGetMeetings))
	js.Global().Set("signalingGetRoomByGroupID", js.FuncOf(wrapper.SignalingGetRoomByGroupID))
	js.Global().Set("signalingGetTokenByRoomID", js.FuncOf(wrapper.SignalingGetTokenByRoomID))
	js.Global().Set("signalingHungUp", js.FuncOf(wrapper.SignalingHungUp))
	js.Global().Set("signalingInvite", js.FuncOf(wrapper.SignalingInvite))
	js.Global().Set("signalingInviteInGroup", js.FuncOf(wrapper.SignalingInviteInGroup))
	js.Global().Set("signalingJoinMeeting", js.FuncOf(wrapper.SignalingJoinMeeting))
	js.Global().Set("signalingOperateStream", js.FuncOf(wrapper.SignalingOperateStream))
	js.Global().Set("signalingReject", js.FuncOf(wrapper.SignalingReject))
	js.Global().Set("signalingSendCustomSignal", js.FuncOf(wrapper.SignalingSendCustomSignal))
	js.Global().Set("signalingUpdateMeetingInfo", js.FuncOf(wrapper.SignalingUpdateMeetingInfo))
	// sendMessageByBuffer belongs to the separately reviewed real buffer/File
	// adapter. No empty registration may satisfy the 145-function ABI gate.
}
