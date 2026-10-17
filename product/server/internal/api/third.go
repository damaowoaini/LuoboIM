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

package api

import (
	"context"
	"encoding/json"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	config2 "github.com/openimsdk/open-im-server/v3/pkg/common/config"

	"github.com/gin-gonic/gin"

	"github.com/OpenIMSDK/protocol/third"
	"github.com/OpenIMSDK/tools/a2r"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/internal/admincapability"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify"
	evidencepb "github.com/openimsdk/open-im-server/v3/internal/objectverify/pb"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"

	"github.com/openimsdk/open-im-server/v3/pkg/rpcclient"
)

type ThirdApi rpcclient.Third

func NewThirdApi(client rpcclient.Third) ThirdApi {
	return ThirdApi(client)
}

// This endpoint accepts service credentials only. The global capability Guard
// verifies the HTTP MAC+nonce; authenticate additionally verifies live token
// status. No browser delegated admin or ordinary business token is sufficient.
func registerEvidenceVerificationRoute(router *gin.Engine, authenticate gin.HandlerFunc, discovery discoveryregistry.SvcDiscoveryRegistry) {
	conn, err := discovery.GetConn(context.Background(), config2.Config.RpcRegisterName.OpenImThirdName)
	if err != nil {
		panic(err)
	}
	router.POST(objectverify.HTTPPath, authenticate, evidenceVerificationHandler(evidencepb.NewEvidenceVerifierClient(conn)))
}

func evidenceVerificationHandler(client evidencepb.EvidenceVerifierClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		value, present := c.Get(admincapability.ContextClaims)
		claims, valid := value.(*authverify.ClassifiedClaims)
		if !present || !valid || claims == nil || claims.Purpose != authverify.ServicePurpose {
			apiresp.GinError(c, errs.ErrNoPermission.Wrap("投诉图片服务请求未获授权"))
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 49152)
		var input struct {
			OwnerUserID string   `json:"ownerUserID"`
			URLs        []string `json:"urls"`
		}
		decoder := json.NewDecoder(c.Request.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(&input) != nil {
			apiresp.GinError(c, errs.ErrArgs.Wrap("投诉图片请求无效"))
			return
		}
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			apiresp.GinError(c, errs.ErrArgs.Wrap("投诉图片请求无效"))
			return
		}
		request := &evidencepb.VerifyRequest{OwnerUserId: input.OwnerUserID, Urls: input.URLs}
		ctx, cancel := context.WithTimeout(c, 3*time.Second)
		defer cancel()
		ctx, err := objectverify.WithRPCProof(ctx, []byte(config2.Config.Secret), c.GetHeader("token"), request, time.Now())
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		result, err := client.Verify(ctx, request)
		if err != nil {
			apiresp.GinError(c, err)
			return
		}
		if result == nil || !result.Valid {
			apiresp.GinError(c, errs.ErrInternalServer.Wrap("投诉图片校验暂不可用"))
			return
		}
		apiresp.GinSuccess(c, gin.H{"valid": true})
	}
}

func (o *ThirdApi) FcmUpdateToken(c *gin.Context) {
	a2r.Call(third.ThirdClient.FcmUpdateToken, o.Client, c)
}

func (o *ThirdApi) SetAppBadge(c *gin.Context) {
	a2r.Call(third.ThirdClient.SetAppBadge, o.Client, c)
}

// #################### s3 ####################

func (o *ThirdApi) PartLimit(c *gin.Context) {
	a2r.Call(third.ThirdClient.PartLimit, o.Client, c)
}

func (o *ThirdApi) PartSize(c *gin.Context) {
	a2r.Call(third.ThirdClient.PartSize, o.Client, c)
}

func (o *ThirdApi) InitiateMultipartUpload(c *gin.Context) {
	a2r.Call(third.ThirdClient.InitiateMultipartUpload, o.Client, c)
}

func (o *ThirdApi) AuthSign(c *gin.Context) {
	a2r.Call(third.ThirdClient.AuthSign, o.Client, c)
}

func (o *ThirdApi) CompleteMultipartUpload(c *gin.Context) {
	a2r.Call(third.ThirdClient.CompleteMultipartUpload, o.Client, c)
}

func (o *ThirdApi) AccessURL(c *gin.Context) {
	a2r.Call(third.ThirdClient.AccessURL, o.Client, c)
}

func (o *ThirdApi) InitiateFormData(c *gin.Context) {
	a2r.Call(third.ThirdClient.InitiateFormData, o.Client, c)
}

func (o *ThirdApi) CompleteFormData(c *gin.Context) {
	a2r.Call(third.ThirdClient.CompleteFormData, o.Client, c)
}

func (o *ThirdApi) ObjectRedirect(c *gin.Context) {
	name := c.Param("name")
	if name == "" {
		c.String(http.StatusBadRequest, "name is empty")
		return
	}
	if name[0] == '/' {
		name = name[1:]
	}
	operationID := c.Query("operationID")
	if operationID == "" {
		operationID = strconv.Itoa(rand.Int())
	}
	ctx := mcontext.SetOperationID(c, operationID)
	query := make(map[string]string)
	for key, values := range c.Request.URL.Query() {
		if len(values) == 0 {
			continue
		}
		query[key] = values[0]
	}
	resp, err := o.Client.AccessURL(ctx, &third.AccessURLReq{Name: name, Query: query})
	if err != nil {
		if errs.ErrArgs.Is(err) {
			c.String(http.StatusBadRequest, err.Error())
			return
		}
		if errs.ErrRecordNotFound.Is(err) {
			c.String(http.StatusNotFound, err.Error())
			return
		}
		c.String(http.StatusInternalServerError, err.Error())
		return
	}
	c.Redirect(http.StatusFound, resp.Url)
}

// #################### logs ####################.
func (o *ThirdApi) UploadLogs(c *gin.Context) {
	a2r.Call(third.ThirdClient.UploadLogs, o.Client, c)
}

func (o *ThirdApi) DeleteLogs(c *gin.Context) {
	a2r.Call(third.ThirdClient.DeleteLogs, o.Client, c)
}

func (o *ThirdApi) SearchLogs(c *gin.Context) {
	a2r.Call(third.ThirdClient.SearchLogs, o.Client, c)
}

func GetPrometheus(c *gin.Context) {
	c.Redirect(http.StatusFound, config2.Config.Prometheus.GrafanaUrl)
}
