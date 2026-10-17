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

package getui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/OpenIMSDK/tools/utils/splitter"

	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	http2 "github.com/openimsdk/open-im-server/v3/pkg/common/http"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"

	"github.com/OpenIMSDK/tools/utils"
)

var (
	ErrTokenExpire = errors.New("token expire")
	ErrUserIDEmpty = errors.New("userIDs is empty")
)

const (
	pushURL      = "/push/single/alias"
	authURL      = "/auth"
	taskURL      = "/push/list/message"
	batchPushURL = "/push/list/alias"

	// codes.
	tokenExpireCode = 10001
	tokenExpireTime = 60 * 60 * 23
	taskIDTTL       = 1000 * 60 * 60 * 24
)

type Client struct {
	cache           cache.MsgModel
	tokenExpireTime int64
	taskIDTTL       int64
}

func NewClient(cache cache.MsgModel) *Client {
	return &Client{cache: cache, tokenExpireTime: tokenExpireTime, taskIDTTL: taskIDTTL}
}

func (g *Client) Push(ctx context.Context, userIDs []string, title, content string, opts *offlinepush.Opts) error {
	if ownedsource.FromContext(ctx) == nil {
		log.ZError(ctx, "========== 个推推送开始 ==========", nil, "userIDs", userIDs, "title", title, "content", content)
	}

	token, err := g.cache.GetGetuiToken(ctx)
	if err != nil {
		if errs.Unwrap(err) == redis.Nil {
			if ownedsource.FromContext(ctx) == nil {
				log.ZWarn(ctx, "getui token not exist in redis", nil)
			}
			token, err = g.getTokenAndSave2Redis(ctx)
			if err != nil {
				if ownedsource.FromContext(ctx) == nil {
					log.ZError(ctx, "获取个推Token失败", err)
				}
				return err
			}
			if ownedsource.FromContext(ctx) == nil {
				log.ZWarn(ctx, "个推Token获取成功", nil, "token", token[:20]+"...")
			}
		} else {
			if ownedsource.FromContext(ctx) == nil {
				log.ZError(ctx, "从Redis获取个推Token失败", err)
			}
			return err
		}
	} else {
		if ownedsource.FromContext(ctx) == nil {
			log.ZWarn(ctx, "从Redis获取个推Token成功", nil, "token", token[:20]+"...")
		}
	}
	pushReq := newPushReq(title, content)
	pushReq.setPushChannel(title, content)
	if ownedsource.FromContext(ctx) == nil {
		log.ZWarn(ctx, "推送请求构建完成", nil, "userCount", len(userIDs))
	}

	if len(userIDs) > 1 {
		maxNum := 999
		if len(userIDs) > maxNum {
			if ownedsource.FromContext(ctx) == nil {
				log.ZWarn(ctx, "用户数量超过999，使用分批推送", nil, "totalUsers", len(userIDs))
			}
			s := splitter.NewSplitter(maxNum, userIDs)
			wg := sync.WaitGroup{}
			var errorMu sync.Mutex
			wg.Add(len(s.GetSplitResult()))
			for i, v := range s.GetSplitResult() {
				go func(index int, userIDs []string) {
					defer wg.Done()
					if ownedsource.FromContext(ctx) == nil {
						log.ZWarn(ctx, "执行批量推送", nil, "batch", index, "userIDs", userIDs)
					}
					if err2 := g.batchPush(ctx, token, userIDs, pushReq); err2 != nil {
						if ownedsource.FromContext(ctx) == nil {
							log.ZError(ctx, "batchPush failed", err2, "index", index, "token", token, "req", pushReq)
						}
						errorMu.Lock()
						err = err2
						errorMu.Unlock()
					} else {
						if ownedsource.FromContext(ctx) == nil {
							log.ZWarn(ctx, "批量推送成功", nil, "batch", index, "userCount", len(userIDs))
						}
					}
				}(i, v.Item)
			}
			wg.Wait()
		} else {
			if ownedsource.FromContext(ctx) == nil {
				log.ZWarn(ctx, "执行批量推送", nil, "userIDs", userIDs)
			}
			err = g.batchPush(ctx, token, userIDs, pushReq)
			if err == nil {
				if ownedsource.FromContext(ctx) == nil {
					log.ZWarn(ctx, "批量推送成功", nil, "userCount", len(userIDs))
				}
			}
		}
	} else if len(userIDs) == 1 {
		if ownedsource.FromContext(ctx) == nil {
			log.ZWarn(ctx, "执行单个用户推送", nil, "userID", userIDs[0])
		}
		err = g.singlePush(ctx, token, userIDs[0], pushReq)
		if err == nil {
			if ownedsource.FromContext(ctx) == nil {
				log.ZWarn(ctx, "单个用户推送成功", nil, "userID", userIDs[0])
			}
		}
	} else {
		if ownedsource.FromContext(ctx) == nil {
			log.ZWarn(ctx, "用户ID列表为空，无法推送", ErrUserIDEmpty)
		}
		return ErrUserIDEmpty
	}
	switch err {
	case ErrTokenExpire:
		if ownedsource.FromContext(ctx) != nil {
			return ErrTokenExpire
		}
		if ownedsource.FromContext(ctx) == nil {
			log.ZWarn(ctx, "个推Token已过期，重新获取", err)
		}
		token, err = g.getTokenAndSave2Redis(ctx)
	}

	if err != nil {
		if ownedsource.FromContext(ctx) == nil {
			log.ZError(ctx, "========== 个推推送失败 ==========", err, "userIDs", userIDs)
		}
	} else {
		if ownedsource.FromContext(ctx) == nil {
			log.ZError(ctx, "========== 个推推送完成 ==========", nil, "userIDs", userIDs)
		}
	}
	return err
}

func (g *Client) Auth(ctx context.Context, timeStamp int64) (token string, expireTime int64, err error) {
	h := sha256.New()
	h.Write(
		[]byte(config.Config.Push.GeTui.AppKey + strconv.Itoa(int(timeStamp)) + config.Config.Push.GeTui.MasterSecret),
	)
	sign := hex.EncodeToString(h.Sum(nil))
	reqAuth := AuthReq{
		Sign:      sign,
		Timestamp: strconv.Itoa(int(timeStamp)),
		AppKey:    config.Config.Push.GeTui.AppKey,
	}
	respAuth := AuthResp{}
	err = g.request(ctx, authURL, reqAuth, "", &respAuth)
	if err != nil {
		return "", 0, err
	}
	expire, err := strconv.Atoi(respAuth.ExpireTime)
	return respAuth.Token, int64(expire), err
}

func (g *Client) GetTaskID(ctx context.Context, token string, pushReq PushReq) (string, error) {
	respTask := TaskResp{}
	ttl := int64(1000 * 60 * 5)
	pushReq.Settings = &Settings{TTL: &ttl}
	err := g.request(ctx, taskURL, pushReq, token, &respTask)
	if err != nil {
		return "", utils.Wrap(err, "")
	}
	return respTask.TaskID, nil
}

// max num is 999.
func (g *Client) batchPush(ctx context.Context, token string, userIDs []string, pushReq PushReq) error {
	if ownedsource.FromContext(ctx) == nil {
		log.ZWarn(ctx, "获取个推TaskID", nil, "userCount", len(userIDs))
	}
	taskID, err := g.GetTaskID(ctx, token, pushReq)
	if err != nil {
		if ownedsource.FromContext(ctx) == nil {
			log.ZError(ctx, "获取个推TaskID失败", err)
		}
		return err
	}
	if ownedsource.FromContext(ctx) == nil {
		log.ZWarn(ctx, "获取个推TaskID成功", nil, "taskID", taskID)
	}

	pushReq = newBatchPushReq(userIDs, taskID)
	if ownedsource.FromContext(ctx) == nil {
		log.ZWarn(ctx, "调用个推批量推送API", nil, "userIDs", userIDs, "taskID", taskID, "url", config.Config.Push.GeTui.PushUrl+batchPushURL)
	}
	return g.request(ctx, batchPushURL, pushReq, token, nil)
}

func (g *Client) singlePush(ctx context.Context, token, userID string, pushReq PushReq) error {
	operationID := mcontext.GetOperationID(ctx)
	pushReq.RequestID = &operationID
	pushReq.Audience = &Audience{Alias: []string{userID}}
	if ownedsource.FromContext(ctx) == nil {
		log.ZWarn(ctx, "调用个推单推API", nil, "userID", userID, "alias", userID, "url", config.Config.Push.GeTui.PushUrl+pushURL)
	}
	return g.request(ctx, pushURL, pushReq, token, nil)
}

func (g *Client) request(ctx context.Context, url string, input any, token string, output any) error {
	header := map[string]string{"token": token}
	resp := &Resp{}
	resp.Data = output

	// 详细日志：打印配置和拼接过程
	if ownedsource.FromContext(ctx) == nil {
		log.ZError(ctx, "========== 个推请求详细信息 ==========", nil)
	}
	if ownedsource.FromContext(ctx) == nil {
		log.ZError(ctx, "配置的PushUrl", nil, "pushUrl", config.Config.Push.GeTui.PushUrl)
	}
	if ownedsource.FromContext(ctx) == nil {
		log.ZError(ctx, "请求的路径", nil, "path", url)
	}
	fullURL := config.Config.Push.GeTui.PushUrl + url
	if ownedsource.FromContext(ctx) == nil {
		log.ZError(ctx, "拼接后的完整URL", nil, "fullURL", fullURL)
	}
	if ownedsource.FromContext(ctx) == nil {
		log.ZError(ctx, "========================================", nil)
	}

	err := g.postReturn(ctx, fullURL, header, input, resp, 3)
	if err != nil {
		if ownedsource.FromContext(ctx) == nil {
			log.ZError(ctx, "个推HTTP请求失败", err, "fullURL", fullURL, "pushUrl", config.Config.Push.GeTui.PushUrl, "path", url)
		}
	} else {
		if ownedsource.FromContext(ctx) == nil {
			log.ZError(ctx, "个推HTTP请求成功", nil, "url", fullURL, "respCode", resp.Code, "respMsg", resp.Msg)
		}
	}
	return err
}

func (g *Client) postReturn(
	ctx context.Context,
	url string,
	header map[string]string,
	input any,
	output RespI,
	timeout int,
) error {
	err := http2.PostReturn(ctx, url, header, input, output, timeout)
	if err != nil {
		return err
	}
	return output.parseError()
}

func (g *Client) getTokenAndSave2Redis(ctx context.Context) (token string, err error) {
	token, _, err = g.Auth(ctx, time.Now().UnixNano()/1e6)
	if err != nil {
		return
	}
	err = g.cache.SetGetuiToken(ctx, token, 60*60*23)
	if err != nil {
		return
	}
	return token, nil
}

func (g *Client) GetTaskIDAndSave2Redis(ctx context.Context, token string, pushReq PushReq) (taskID string, err error) {
	pushReq.Settings = &Settings{TTL: &g.taskIDTTL}
	taskID, err = g.GetTaskID(ctx, token, pushReq)
	if err != nil {
		return
	}
	err = g.cache.SetGetuiTaskID(ctx, taskID, g.tokenExpireTime)
	if err != nil {
		return
	}
	return token, nil
}
