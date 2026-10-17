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

package http

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"

	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

var (
	// define http client.
	client = &http.Client{
		Timeout: 15 * time.Second, // max timeout is 15s
	}
)

type responseStatusError struct{ code int }

func (e responseStatusError) Error() string { return "HTTP response status " + strconv.Itoa(e.code) }

func init() {
	// reset http default transport
	http.DefaultTransport.(*http.Transport).MaxConnsPerHost = 100 // default: 2
}

func Get(url string) (response []byte, err error) {
	hclient := http.Client{Timeout: 5 * time.Second}
	resp, err := hclient.Get(url)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

func Post(ctx context.Context, url string, header map[string]string, data any, timeout int) (content []byte, err error) {
	if timeout > 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, time.Second*time.Duration(timeout))
		defer cancel()
	}

	jsonStr, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonStr))
	if err != nil {
		return nil, err
	}

	if operationID, _ := ctx.Value(constant.OperationID).(string); operationID != "" {
		req.Header.Set(constant.OperationID, operationID)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	req.Header.Add("content-type", "application/json; charset=utf-8")

	resp, err := callbackClient(ctx).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, responseStatusError{code: resp.StatusCode}
	}

	result, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func PostReturn(ctx context.Context, url string, header map[string]string, input, output any, timeOutSecond int) error {
	b, err := Post(ctx, url, header, input, timeOutSecond)
	if err != nil {
		return err
	}
	err = json.Unmarshal(b, output)
	return err
}

func callBackPostReturn(ctx context.Context, url, command string, input interface{}, output callbackstruct.CallbackResp, callbackConfig config.CallBackConfig) error {
	defer log.ZDebug(ctx, "callback", "command", command)
	//
	//v := urllib.Values{}
	//v.Set(constant.CallbackCommand, command)
	//url = url + "/" + v.Encode()
	url = url + "/" + command
	headers, err := callbackHeaders(command, input)
	if err != nil {
		return err
	}
	b, err := Post(ctx, url, headers, input, callbackConfig.CallbackTimeOut)
	if err != nil {
		var status responseStatusError
		if errors.As(err, &status) && (status.code == http.StatusUnauthorized || status.code == http.StatusForbidden) {
			return errs.ErrNoPermission.Wrap("callback authentication rejected")
		}
		if callbackConfig.CallbackFailedContinue != nil && *callbackConfig.CallbackFailedContinue {
			log.ZWarn(ctx, "callback failed but continue", err, "url", url)
			return nil
		}
		return errs.ErrNetwork.Wrap(err.Error())
	}

	if err = json.Unmarshal(b, output); err != nil {
		if callbackConfig.CallbackFailedContinue != nil && *callbackConfig.CallbackFailedContinue {
			log.ZWarn(ctx, "callback failed but continue", err, "url", url)
			return nil
		}
		return errs.ErrData.WithDetail(err.Error() + "response format error")
	}

	return output.Parse()
}

func callbackHeaders(command string, input any) (map[string]string, error) {
	if config.Config.Secret == "" {
		return nil, errs.ErrInternalServer.Wrap("callback authentication secret is empty")
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ts, nonceHex := strconv.FormatInt(time.Now().Unix(), 10), hex.EncodeToString(nonce)
	mac := hmac.New(sha256.New, []byte(config.Config.Secret))
	mac.Write([]byte(ts + "\n" + nonceHex + "\n" + command + "\n"))
	mac.Write(body)
	return map[string]string{"X-OpenIM-Callback-Timestamp": ts, "X-OpenIM-Callback-Nonce": nonceHex, "X-OpenIM-Callback-Signature": hex.EncodeToString(mac.Sum(nil))}, nil
}

func CallBackPostReturn(ctx context.Context, url string, req callbackstruct.CallbackReq, resp callbackstruct.CallbackResp, callbackConfig config.CallBackConfig) error {
	return callBackPostReturn(ctx, url, req.GetCallbackCommand(), req, resp, callbackConfig)
}
