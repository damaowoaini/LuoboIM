package msg

import (
	"context"

	"github.com/OpenIMSDK/protocol/constant"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
)

// A callback may edit content, but cannot move an already authorized group
// message to another actor, device, destination, or message identity.
type groupMessageIdentity struct {
	sender, receiver, group, clientMessage, serverMessage string
	session, source, platform                             int32
}

func groupIdentity(message *sdkws.MsgData) groupMessageIdentity {
	return groupMessageIdentity{
		sender: message.SendID, receiver: message.RecvID, group: message.GroupID,
		clientMessage: message.ClientMsgID, serverMessage: message.ServerMsgID,
		session: message.SessionType, source: message.MsgFrom, platform: message.SenderPlatformID,
	}
}

// This is the common production boundary before group messages enter MQ. The
// injected actions are the real callback chain and the final MQ writer; tests
// can exercise this same boundary without callbacks, Redis, or Kafka on a network.
// Single-chat callbacks and the legacy trusted-manager/notification verifier
// branches retain their existing behavior.
func (m *msgServer) withVerifiedGroupMessage(ctx context.Context, req *pbmsg.SendMsgReq,
	mutate func(context.Context, *pbmsg.SendMsgReq) error,
	commit func(context.Context, *pbmsg.SendMsgReq) error,
) error {
	if req == nil || req.MsgData == nil || req.MsgData.SessionType != constant.SuperGroupChatType || mutate == nil || commit == nil {
		return errs.ErrArgs.Wrap("invalid group message pipeline")
	}
	identity := groupIdentity(req.MsgData)
	initialContentType := req.MsgData.ContentType
	if err := m.messageVerification(ctx, req); err != nil {
		return err
	}
	if err := mutate(ctx, req); err != nil {
		return err
	}
	if req.MsgData == nil || groupIdentity(req.MsgData) != identity {
		return errs.ErrNoPermission.Wrap("group callback changed the authorized message identity")
	}
	// SendMsg's authenticated notification check occurred before callbacks. A
	// body edit must not bypass it by reclassifying an ordinary message afterwards.
	if initialContentType < constant.NotificationBegin && req.MsgData.ContentType >= constant.NotificationBegin {
		return errs.ErrNoPermission.Wrap("group callback cannot create a system notification")
	}
	if err := m.messageVerification(ctx, req); err != nil {
		return err
	}
	return commit(ctx, req)
}
