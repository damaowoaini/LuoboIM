package apicall

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"io"
	"net/http"
)

type AdminPreviewRequest struct {
	UserID         string `json:"userID"`
	ConversationID string `json:"conversationID,omitempty"`
	Keyword        string `json:"keyword,omitempty"`
	StartTime      int64  `json:"startTime,omitempty"`
	EndTime        int64  `json:"endTime,omitempty"`
	PageNumber     int    `json:"pageNumber,omitempty"`
	PageSize       int    `json:"pageSize,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
}

func ReadAdminPreview(ctx context.Context, caller CallerInterface, credential, operation string, req AdminPreviewRequest) (json.RawMessage, error) {
	if operation != "conversations" && operation != "search" {
		return nil, errs.ErrArgs.Wrap("未知预览操作")
	}
	serviceToken, err := caller.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(req)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, config.Config.OpenIMUrl+"/admin/message_preview/"+operation, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("preview upstream unavailable")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("token", serviceToken)
	if err := addServiceProof(request, "/admin/message_preview/"+operation, serviceToken, body); err != nil {
		return nil, err
	}
	request.Header.Set("X-Luobo-Chart-Admin-Token", credential)
	if op, _ := ctx.Value(constant.OperationID).(string); op != "" {
		request.Header.Set(constant.OperationID, op)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errs.ErrInternalServer.Wrap("预览服务暂不可用")
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 || response.StatusCode != http.StatusOK {
		return nil, errs.ErrInternalServer.Wrap("预览响应无效")
	}
	var envelope struct {
		ErrCode int             `json:"errCode"`
		ErrMsg  string          `json:"errMsg"`
		Data    json.RawMessage `json:"data"`
	}
	if json.Unmarshal(data, &envelope) != nil {
		return nil, errs.ErrInternalServer.Wrap("预览响应无效")
	}
	if envelope.ErrCode != 0 {
		return nil, errs.NewCodeError(envelope.ErrCode, envelope.ErrMsg)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return nil, errs.ErrInternalServer.Wrap("预览响应缺少数据")
	}
	return envelope.Data, nil
}
