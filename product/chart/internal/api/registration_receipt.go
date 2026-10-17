package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const routeRegistrationHeader = "X-Luobo-Route-Registration-ID"

func routeRegistrationAuthentication(rdb redis.UniversalClient, required bool, logicalPath string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(routeRegistrationHeader)
		supplied := id != "" || c.GetHeader("X-Luobo-Route-Signature") != "" || c.GetHeader("X-Luobo-Route-Timestamp") != "" || c.GetHeader("X-Luobo-Route-Nonce") != ""
		if !supplied && !required {
			return
		}
		reject := func() { c.AbortWithStatusJSON(403, gin.H{"errCode": 20013, "errMsg": "无效的路由服务凭证"}) }
		secret := os.Getenv("LUOBO_ROUTE_SHARED_SECRET")
		if len(secret) < 32 || !registration.ValidID(id) || c.Request.Method != "POST" {
			reject()
			return
		}
		ts, nonce := c.GetHeader("X-Luobo-Route-Timestamp"), c.GetHeader("X-Luobo-Route-Nonce")
		at, err := strconv.ParseInt(ts, 10, 64)
		if err != nil || time.Since(time.Unix(at, 0)) > 5*time.Minute || time.Until(time.Unix(at, 0)) > 5*time.Minute {
			reject()
			return
		}
		nonceBytes, err := hex.DecodeString(nonce)
		if err != nil || len(nonceBytes) != 16 {
			reject()
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1024*1024+1))
		if err != nil || len(body) > 1024*1024 {
			reject()
			return
		}
		digest := sha256.Sum256(body)
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(ts + "\n" + nonce + "\nPOST\n" + logicalPath + "\n" + id + "\n" + hex.EncodeToString(digest[:])))
		signature, err := hex.DecodeString(c.GetHeader("X-Luobo-Route-Signature"))
		if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
			reject()
			return
		}
		keyID := sha256.Sum256([]byte(secret))
		claimed, err := rdb.SetNX(c, "ROUTE_REGISTRATION_NONCE:"+hex.EncodeToString(keyID[:8])+":"+nonce, "1", 10*time.Minute).Result()
		if err != nil || !claimed {
			reject()
			return
		}
		// Only this verified API boundary can attach the trusted RPC metadata.
		c.Request = c.Request.WithContext(registration.WithID(c.Request.Context(), id))
		// gin.Context falls back to request context only when enabled. Explicitly set
		// both keys to preserve the existing API-to-RPC context contract.
		c.Set(registration.MetadataKey, []string{id})
		headers, _ := c.Value(constant.RpcCustomHeader).([]string)
		c.Set(constant.RpcCustomHeader, append(headers, registration.MetadataKey))
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
	}
}

type registrationAPI struct {
	db     *gorm.DB
	caller apicall.CallerInterface
}
type receiptResponse struct {
	RegistrationID     string `json:"registrationID"`
	NormalizedIdentity string `json:"normalizedIdentity"`
	UserID             string `json:"userID,omitempty"`
	Registered         bool   `json:"registered"`
	IMProvisioned      bool   `json:"imProvisioned"`
}

func (a *registrationAPI) find(c *gin.Context, id, identity string) (*registration.Receipt, error) {
	var receipt registration.Receipt
	err := a.db.WithContext(c).Where("registration_id = ?", id).Take(&receipt).Error
	if err != nil {
		return nil, err
	}
	if receipt.NormalizedIdentity != identity {
		return nil, errs.ErrNoPermission.Wrap("注册意图与账号不匹配")
	}
	return &receipt, nil
}
func (a *registrationAPI) ensureIM(c *gin.Context, receipt *registration.Receipt) error {
	if receipt.IMProvisionedAt != nil {
		return nil
	}
	if receipt.ManagedByOutbox {
		// The row-locked worker confirms the account, immutable IM marker and
		// defaults together. A signed receipt retry cannot bypass that boundary.
		return errs.ErrNoPermission.Wrap("注册正在恢复，请稍后使用原密码登录")
	}
	var attribute chat.Attribute
	if err := a.db.WithContext(c).Where("user_id = ?", receipt.UserID).Take(&attribute).Error; err != nil {
		return err
	}
	marker, _ := json.Marshal(map[string]string{"luoboRegistrationID": receipt.RegistrationID})
	info := &sdkws.UserInfo{UserID: receipt.UserID, Nickname: attribute.Nickname, FaceURL: attribute.FaceURL, Ex: string(marker), CreateTime: receipt.CreatedAt.UnixMilli()}
	// Historical signed receipts use the same purpose-scoped initial creation
	// proof as the durable worker. Knowledge of the register secret alone cannot
	// set the now protected IM marker after the receiver is upgraded.
	token, err := a.caller.ImAdminTokenWithDefaultAdmin(c)
	if err != nil {
		return err
	}
	serviceCtx := mctx.WithApiToken(c, token)
	if err := a.caller.RegisterUser(serviceCtx, []*sdkws.UserInfo{info}); err != nil {
		// A lost response can follow a successful IM insert. Accept only the same
		// immutable registration marker, never an unrelated account with this ID.
		finder, ok := a.caller.(interface {
			FindRegisteredUsers(context.Context, []string) ([]*sdkws.UserInfo, error)
		})
		if !ok {
			return err
		}
		users, lookupErr := finder.FindRegisteredUsers(serviceCtx, []string{receipt.UserID})
		if lookupErr != nil || len(users) != 1 || users[0].UserID != receipt.UserID || users[0].Ex != string(marker) {
			return err
		}
	}
	now := time.Now().UTC()
	if err := a.db.WithContext(c).Model(&registration.Receipt{}).Where("registration_id = ? AND user_id = ?", receipt.RegistrationID, receipt.UserID).Update("im_provisioned_at", now).Error; err != nil {
		return err
	}
	receipt.IMProvisionedAt = &now
	return nil
}
func (a *registrationAPI) Query(c *gin.Context) {
	var req struct {
		RegistrationID     string `json:"registrationID"`
		NormalizedIdentity string `json:"normalizedIdentity"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.RegistrationID != registration.ID(c) || req.NormalizedIdentity == "" {
		apiresp.GinError(c, errs.ErrArgs.Wrap("注册意图参数无效"))
		return
	}
	response := receiptResponse{RegistrationID: req.RegistrationID, NormalizedIdentity: req.NormalizedIdentity}
	receipt, err := a.find(c, req.RegistrationID, req.NormalizedIdentity)
	if err == gorm.ErrRecordNotFound {
		apiresp.GinSuccess(c, &response)
		return
	}
	if err != nil {
		apiresp.GinError(c, err)
		return
	}
	response.UserID, response.Registered = receipt.UserID, true
	if err := a.ensureIM(c, receipt); err == nil {
		response.IMProvisioned = true
	}
	apiresp.GinSuccess(c, &response)
}

// Retried authenticated registration has already consumed its OTP. Return the
// matching receipt and require a fresh login instead of authenticating twice.
func (a *registrationAPI) retry(c *gin.Context, email, area, phone string) (bool, error) {
	id := registration.ID(c)
	if id == "" {
		return false, nil
	}
	identity, err := registration.Identity(email, area, phone)
	if err != nil {
		return true, err
	}
	receipt, err := a.find(c, id, identity)
	if err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if err := a.ensureIM(c, receipt); err != nil {
		registrationCommittedFailure(c, err)
		return true, nil
	}
	apiresp.GinSuccess(c, gin.H{"userID": receipt.UserID, "registrationID": id, "registered": true, "imProvisioned": true, "requiresLogin": true})
	return true, nil
}
