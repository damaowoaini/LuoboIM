//go:build js && wasm

package wasm_wrapper

import (
	"github.com/openimsdk/openim-sdk-core/v3/open_im_sdk"
	"github.com/openimsdk/openim-sdk-core/v3/wasm/event_listener"
	"syscall/js"
)

// These are parameter-preserving wrappers around the already reviewed custom
// protocol core. They do not grant identity, implement provider success, or
// change the native SDK entry points.
type WrapperWebCompat struct{ *WrapperCommon }

func NewWrapperWebCompat(common *WrapperCommon) *WrapperWebCompat {
	return &WrapperWebCompat{WrapperCommon: common}
}

func (w *WrapperWebCompat) GetGroupMessageReaderList(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("getGroupMessageReaderList", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.GetGroupMessageReaderList, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) GetSignalingInvitationInfoStartApp(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("getSignalingInvitationInfoStartApp", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.GetSignalingInvitationInfoStartApp, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SendGroupMessageReadReceipt(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("sendGroupMessageReadReceipt", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SendGroupMessageReadReceipt, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SetConversationIsMsgDestruct(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("setConversationIsMsgDestruct", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SetConversationIsMsgDestruct, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SetConversationMsgDestructTime(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("setConversationMsgDestructTime", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SetConversationMsgDestructTime, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SetSelfInfoEx(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("setSelfInfoEx", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SetSelfInfoEx, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingAccept(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingAccept", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingAccept, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingCancel(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingCancel", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingCancel, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingCloseRoom(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingCloseRoom", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingCloseRoom, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingCreateMeeting(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingCreateMeeting", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingCreateMeeting, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingGetMeetings(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingGetMeetings", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingGetMeetings, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingGetRoomByGroupID(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingGetRoomByGroupID", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingGetRoomByGroupID, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingGetTokenByRoomID(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingGetTokenByRoomID", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingGetTokenByRoomID, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingHungUp(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingHungUp", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingHungUp, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingInvite(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingInvite", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingInvite, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingInviteInGroup(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingInviteInGroup", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingInviteInGroup, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingJoinMeeting(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingJoinMeeting", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingJoinMeeting, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingOperateStream(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingOperateStream", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingOperateStream, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingReject(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingReject", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingReject, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingSendCustomSignal(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingSendCustomSignal", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingSendCustomSignal, callback, &args).AsyncCallWithCallback()
}

func (w *WrapperWebCompat) SignalingUpdateMeetingInfo(_ js.Value, args []js.Value) interface{} {
	callback := event_listener.NewBaseCallback("signalingUpdateMeetingInfo", w.commonFunc)
	return event_listener.NewCaller(open_im_sdk.SignalingUpdateMeetingInfo, callback, &args).AsyncCallWithCallback()
}
