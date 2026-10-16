// Copyright © 2023 OpenIM. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package msggateway

import (
	"context"
	"sync"

	"github.com/OpenIMSDK/tools/errs"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/rtc"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"

	"github.com/OpenIMSDK/protocol/push"
	"github.com/OpenIMSDK/tools/discoveryregistry"

	"github.com/go-playground/validator/v10"
	"google.golang.org/protobuf/proto"

	"github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/utils"

	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

type Req struct {
	ReqIdentifier int32  `json:"reqIdentifier" validate:"required"`
	Token         string `json:"token"`
	SendID        string `json:"sendID"        validate:"required"`
	OperationID   string `json:"operationID"   validate:"required"`
	MsgIncr       string `json:"msgIncr"       validate:"required"`
	Data          []byte `json:"data"`
}

func (r *Req) String() string {
	var tReq Req
	tReq.ReqIdentifier = r.ReqIdentifier
	tReq.Token = r.Token
	tReq.SendID = r.SendID
	tReq.OperationID = r.OperationID
	tReq.MsgIncr = r.MsgIncr
	return utils.StructToJsonString(tReq)
}

var reqPool = sync.Pool{
	New: func() any {
		return new(Req)
	},
}

func getReq() *Req {
	req := reqPool.Get().(*Req)
	req.Data = nil
	req.MsgIncr = ""
	req.OperationID = ""
	req.ReqIdentifier = 0
	req.SendID = ""
	req.Token = ""
	return req
}

func freeReq(req *Req) {
	reqPool.Put(req)
}

type Resp struct {
	ReqIdentifier int32  `json:"reqIdentifier"`
	MsgIncr       string `json:"msgIncr"`
	OperationID   string `json:"operationID"`
	ErrCode       int    `json:"errCode"`
	ErrMsg        string `json:"errMsg"`
	Data          []byte `json:"data"`
}

func (r *Resp) String() string {
	var tResp Resp
	tResp.ReqIdentifier = r.ReqIdentifier
	tResp.MsgIncr = r.MsgIncr
	tResp.OperationID = r.OperationID
	tResp.ErrCode = r.ErrCode
	tResp.ErrMsg = r.ErrMsg
	return utils.StructToJsonString(tResp)
}

type MessageHandler interface {
	GetSeq(context context.Context, data *Req) ([]byte, error)
	SendMessage(context context.Context, data *Req) ([]byte, error)
	SendSignalMessage(context context.Context, data *Req) ([]byte, error)
	PullMessageBySeqList(context context.Context, data *Req) ([]byte, error)
	UserLogout(context context.Context, data *Req) ([]byte, error)
	SetUserDeviceBackground(context context.Context, data *Req) ([]byte, bool, error)
}

var _ MessageHandler = (*GrpcHandler)(nil)

type GrpcHandler struct {
	msgRpcClient *rpcclient.MessageRpcClient
	pushClient   *rpcclient.PushRpcClient
	signalClient *rpcclient.SignalRpcClient
	validate     *validator.Validate
}

func NewGrpcHandler(validate *validator.Validate, client discoveryregistry.SvcDiscoveryRegistry) *GrpcHandler {
	msgRpcClient := rpcclient.NewMessageRpcClient(client)
	pushRpcClient := rpcclient.NewPushRpcClient(client)
	signalClient := rpcclient.NewSignalRpcClient(client)
	return &GrpcHandler{
		msgRpcClient: &msgRpcClient,
		pushClient:   &pushRpcClient, validate: validate,
		signalClient: &signalClient,
	}
}

func (g GrpcHandler) GetSeq(context context.Context, data *Req) ([]byte, error) {
	req := sdkws.GetMaxSeqReq{}
	if err := proto.Unmarshal(data.Data, &req); err != nil {
		return nil, err
	}
	if err := g.validate.Struct(&req); err != nil {
		return nil, err
	}
	resp, err := g.msgRpcClient.GetMaxSeq(context, &req)
	if err != nil {
		return nil, err
	}
	c, err := proto.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (g GrpcHandler) SendMessage(context context.Context, data *Req) ([]byte, error) {
	msgData := sdkws.MsgData{}
	if err := proto.Unmarshal(data.Data, &msgData); err != nil {
		return nil, err
	}
	if err := g.validate.Struct(&msgData); err != nil {
		return nil, err
	}
	if err := validateClientMessage(context, &msgData); err != nil {
		return nil, err
	}
	req := msg.SendMsgReq{MsgData: &msgData}
	resp, err := g.msgRpcClient.SendMsg(context, &req)
	if err != nil {
		return nil, err
	}
	c, err := proto.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (g GrpcHandler) SendSignalMessage(ctx context.Context, data *Req) ([]byte, error) {
	signalReq := &rtc.SignalReq{}
	if err := proto.Unmarshal(data.Data, signalReq); err != nil {
		return nil, err
	}
	resp, err := g.signalClient.Client.SignalMessageAssemble(ctx, &rtc.SignalMessageAssembleReq{SignalReq: signalReq})
	if err != nil {
		return nil, err
	}
	if resp == nil || resp.SignalResp == nil {
		return nil, errs.ErrInternalServer.Wrap("RTC service returned no signal response")
	}
	// The deployed Enterprise core accepts the assembly envelope. Preserve
	// that proven wire format; replacement cores must decode its SignalResp.
	c, err := proto.Marshal(resp)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	return c, nil
}

func (g GrpcHandler) PullMessageBySeqList(context context.Context, data *Req) ([]byte, error) {
	req := sdkws.PullMessageBySeqsReq{}
	if err := proto.Unmarshal(data.Data, &req); err != nil {
		return nil, err
	}
	if err := g.validate.Struct(data); err != nil {
		return nil, err
	}
	resp, err := g.msgRpcClient.PullMessageBySeqList(context, &req)
	if err != nil {
		return nil, err
	}
	c, err := proto.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (g GrpcHandler) UserLogout(context context.Context, data *Req) ([]byte, error) {
	req := push.DelUserPushTokenReq{}
	if err := proto.Unmarshal(data.Data, &req); err != nil {
		return nil, err
	}
	resp, err := g.pushClient.DelUserPushToken(context, &req)
	if err != nil {
		return nil, err
	}
	c, err := proto.Marshal(resp)
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (g GrpcHandler) SetUserDeviceBackground(_ context.Context, data *Req) ([]byte, bool, error) {
	req := sdkws.SetAppBackgroundStatusReq{}
	if err := proto.Unmarshal(data.Data, &req); err != nil {
		return nil, false, err
	}
	if err := g.validate.Struct(data); err != nil {
		return nil, false, err
	}
	return nil, req.IsBackground, nil
}

// func (g GrpcHandler) call[T any](ctx context.Context, data Req, m proto.Message, rpc func(ctx context.Context, req
// proto.Message)) ([]byte, error) {
//	if err := proto.Unmarshal(data.Data, m); err != nil {
//		return nil, err
//	}
//	if err := g.validate.Struct(m); err != nil {
//		return nil, err
//	}
//	rpc(ctx, m)
//	req := msg.SendMsgReq{MsgData: &msgData}
//	resp, err := g.notification.Msg.SendMsg(context, &req)
//	if err != nil {
//		return nil, err
//	}
//	c, err := proto.Marshal(resp)
//	if err != nil {
//		return nil, err
//	}
//	return c, nil
//}

func validateClientMessage(ctx context.Context, message *sdkws.MsgData) error {
	if message == nil {
		return errs.ErrArgs.Wrap("message is empty")
	}
	if err := authverify.CheckAccessV3(ctx, message.SendID); err != nil {
		return err
	}
	if !authverify.IsAppManagerUid(ctx) && (message.MsgFrom != constant.UserMsgType || message.ContentType >= constant.NotificationBegin || message.SessionType == constant.NotificationChatType) {
		return errs.ErrNoPermission.Wrap("clients cannot publish system notifications")
	}
	return nil
}
