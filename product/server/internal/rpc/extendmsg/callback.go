package extendmsg

// func callbackSetMessageReactionExtensions(ctx context.Context, setReq *extendmsg.SetMessageReactionExtensionsReq) (resultReactionExtensionList []*extendmsg.KeyValueResp, err error) {
// 	if !config.Config.Callback.CallbackAfterSendGroupMsg.Enable {
// 		return nil, nil
// 	}
// 	req := &cbapi.CallbackBeforeSetMessageReactionExtReq{
// 		OperationID:          mcontext.GetOperationID(ctx),
// 		CallbackCommand:      constant.CallbackBeforeSetMessageReactionExtensionCommand,
// 		ConversationID:       setReq.ConversationID,
// 		OpUserID:             mcontext.GetOpUserID(ctx),
// 		ReactionExtensions:   setReq.ReactionExtensions,
// 		ClientMsgID:          setReq.ClientMsgID,
// 		IsReact:              setReq.IsReact,
// 		IsExternalExtensions: setReq.IsExternalExtensions,
// 		MsgFirstModifyTime:   setReq.MsgFirstModifyTime,
// 	}
// 	resp := &cbapi.CallbackBeforeSetMessageReactionExtResp{}
// 	if err := http.CallBackPostReturn(ctx, config.Config.Callback.CallbackUrl, req, resp, config.Config.Callback.CallbackAfterSendGroupMsg); err != nil {
// 		return nil, err
// 	}
// 	setReq.MsgFirstModifyTime = resp.MsgFirstModifyTime
// 	return resp.ResultReactionExtensions, nil
// }

// func callbackDeleteMessageReactionExtensions(ctx context.Context, setReq *extendmsg.DeleteMessagesReactionExtensionsReq) (resp *cbapi.CallbackDeleteMessageReactionExtResp, err error) {
// 	if !config.Config.Callback.CallbackAfterSendGroupMsg.Enable {
// 		return resp, nil
// 	}
// 	req := &cbapi.CallbackDeleteMessageReactionExtReq{
// 		OperationID:          setReq.OperationID,
// 		CallbackCommand:      constant.CallbackBeforeDeleteMessageReactionExtensionsCommand,
// 		ConversationID:       setReq.ConversationID,
// 		OpUserID:             setReq.OpUserID,
// 		ReactionExtensions:   setReq.ReactionExtensions,
// 		ClientMsgID:          setReq.ClientMsgID,
// 		IsExternalExtensions: setReq.IsExternalExtensions,
// 		MsgFirstModifyTime:   setReq.MsgFirstModifyTime,
// 	}
// 	resp = &cbapi.CallbackDeleteMessageReactionExtResp{}
// 	return resp, http.CallBackPostReturn(ctx, config.Config.Callback.CallbackUrl, req, resp, config.Config.Callback.CallbackAfterSendGroupMsg)
// }

// func callbackGetMessageListReactionExtensions(ctx context.Context, getReq *extendmsg.GetMessagesReactionExtensionsReq) error {
// 	if !config.Config.Callback.CallbackAfterSendGroupMsg.Enable {
// 		return nil
// 	}
// 	req := &cbapi.CallbackGetMessagesReactionExtReq{
// 		OperationID:     mcontext.GetOperationID(ctx),
// 		CallbackCommand: constant.CallbackGetMessageListReactionExtensionsCommand,
// 		ConversationID:  getReq.ConversationID,
// 		OpUserID:        mcontext.GetOperationID(ctx),
// 		TypeKeys:        getReq.TypeKeys,
// 	}
// 	resp := &cbapi.CallbackGetMessagesReactionExtResp{}
// 	return http.CallBackPostReturn(ctx, config.Config.Callback.CallbackUrl, req, resp, config.Config.Callback.CallbackAfterSendGroupMsg)
// }

// func callbackAddMessageReactionExtensions(ctx context.Context, setReq *extendmsg.AddMessageReactionExtensionsReq) error {
// 	req := &cbapi.CallbackAddMessageReactionExtReq{
// 		OperationID:          mcontext.GetOperationID(ctx),
// 		CallbackCommand:      constant.CallbackAddMessageListReactionExtensionsCommand,
// 		ConversationID:       setReq.ConversationID,
// 		OpUserID:             mcontext.GetOperationID(ctx),
// 		ReactionExtensions:   setReq.ReactionExtensions,
// 		ClientMsgID:          setReq.ClientMsgID,
// 		IsReact:              setReq.IsReact,
// 		IsExternalExtensions: setReq.IsExternalExtensions,
// 		MsgFirstModifyTime:   setReq.MsgFirstModifyTime,
// 	}
// 	resp := &cbapi.CallbackAddMessageReactionExtResp{}
// 	return http.CallBackPostReturn(ctx, config.Config.Callback.CallbackUrl, req, resp, config.Config.Callback.CallbackAfterSendGroupMsg)
// }
