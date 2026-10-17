// Package admincapability enforces HTTP-only administrator capabilities before
// any API route, including routes with their own token middleware.
package admincapability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	pathpkg "path"
	"strings"
	"time"

	"github.com/OpenIMSDK/tools/apiresp"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/redis/go-redis/v9"
)

const ContextClaims = "luobo.classified.claims"
const ContextAdmin = "luobo.delegated.admin"
const ContextUploadOwner = "luobo.delegated.uploadOwner"

type Validator interface {
	Validate(context.Context, string, string, []byte) (string, int32, error)
}
type HTTPValidator struct {
	URL               string
	Secret            []byte
	AllowLoopbackHTTP bool
	Client            *http.Client
}

func (v *HTTPValidator) Validate(ctx context.Context, raw, path string, body []byte) (string, int32, error) {
	u, e := url.Parse(v.URL)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/internal/admin_delegation/validate" || u.RawPath != "" || u.Host == "" {
		return "", 0, errors.New("invalid delegation validator configuration")
	}
	loopback := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback && v.AllowLoopbackHTTP) {
		return "", 0, errors.New("delegation validator requires HTTPS")
	}
	sum := sha256.Sum256(body)
	payload, _ := json.Marshal(struct {
		Method   string `json:"method"`
		Path     string `json:"path"`
		BodyHash string `json:"bodyHash"`
	}{http.MethodPost, path, hex.EncodeToString(sum[:])})
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, v.URL, bytes.NewReader(payload))
	if e != nil {
		return "", 0, e
	}
	h, e := serviceauth.Sign(v.Secret, serviceauth.Validation, http.MethodPost, "/internal/admin_delegation/validate", raw, payload, time.Now())
	if e != nil {
		return "", 0, e
	}
	request.Header = h
	request.Header.Set("token", raw)
	request.Header.Set("Content-Type", "application/json")
	operationID := mcontext.GetOperationID(ctx)
	if operationID == "" || len(operationID) > 128 {
		operationID = "admin-capability-" + uuid.NewString()
	}
	request.Header.Set("operationID", operationID)
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	response, e := client.Do(request)
	if e != nil {
		return "", 0, errors.New("administrator validator unavailable")
	}
	defer response.Body.Close()
	content, e := io.ReadAll(io.LimitReader(response.Body, 4097))
	if e != nil || len(content) > 4096 || response.StatusCode != http.StatusOK {
		return "", 0, errors.New("administrator validator failed")
	}
	var envelope struct {
		ErrCode int `json:"errCode"`
		Data    struct {
			AdminID string `json:"adminID"`
			Level   int32  `json:"level"`
		} `json:"data"`
	}
	if json.Unmarshal(content, &envelope) != nil || envelope.ErrCode != 0 || envelope.Data.AdminID == "" || envelope.Data.Level < 1 || envelope.Data.Level > 100 {
		return "", 0, errors.New("administrator delegation rejected")
	}
	return envelope.Data.AdminID, envelope.Data.Level, nil
}

// Whitelist comes from admin-im-capability-contracts.json. Message history,
// user credentials, encryption, conversations and inactive legacy cloud keys
// deliberately have no browser capability.
var allowed = map[string]bool{
	"/user/get_users": true, "/user/get_users_online_token_detail": true,
	"/friend/get_friend_list": true, "/friend/delete_friend": true,
	"/auth/force_logout": true,
	"/group/get_groups":  true, "/group/get_group_member_list": true, "/group/get_group_members_info": true,
	"/group/create_group": true, "/group/set_group_info": true, "/group/mute_group": true, "/group/cancel_mute_group": true, "/group/dismiss_group": true, "/group/kick_group": true, "/group/invite_user_to_group": true, "/group/mute_group_member": true, "/group/cancel_mute_group_member": true, "/group/set_group_member_info": true, "/group/transfer_group": true,
	"/sensitive_word/search": true, "/sensitive_word/add": true, "/sensitive_word/update": true, "/sensitive_word/delete": true, "/sensitive_word/reload": true,
	"/msg/revoke_msg": true, "/msg/batch_send_msg": true,
	"/object/part_limit": true, "/object/part_size": true, "/object/initiate_multipart_upload": true, "/object/auth_sign": true, "/object/complete_multipart_upload": true,
}

func validBody(value any, depth int) bool {
	if depth > 20 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		seen := map[string]bool{}
		for k, item := range v {
			folded := strings.ToLower(k)
			if seen[folded] {
				return false
			}
			seen[folded] = true
			if strings.EqualFold(k, "pagination") {
				p, ok := item.(map[string]any)
				if !ok {
					return false
				}
				page, ok := p["pageNumber"].(float64)
				if !ok || page < 1 || page > 100000 || page != float64(int(page)) {
					return false
				}
				size, ok := p["showNumber"].(float64)
				if !ok || size < 1 || size > 1000 || size != float64(int(size)) {
					return false
				}
			}
			if !validBody(item, depth+1) {
				return false
			}
		}
	case []any:
		if len(v) > 1000 {
			return false
		}
		for _, item := range v {
			if !validBody(item, depth+1) {
				return false
			}
		}
	case string:
		return len(v) <= 1024*1024
	}
	return true
}
func Guard(rdb redis.UniversalClient, validator Validator) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader("token")
		if raw == "" {
			c.Next()
			return
		}
		deny := func() { apiresp.GinError(c, errs.ErrNoPermission.Wrap("管理凭证或请求能力无效")); c.Abort() }
		// Peek only selects this additional management boundary; it never grants
		// authentication. Ordinary/invalid business credentials retain each route's
		// existing authenticator and error contract, including favorite routes.
		var hint authverify.ClassifiedClaims
		if len(raw) > 16384 {
			apiresp.GinError(c, errs.ErrTokenUnknown.Wrap())
			c.Abort()
			return
		}
		_, _, peekErr := new(jwt.Parser).ParseUnverified(raw, &hint)
		if peekErr != nil || (hint.Purpose == "" && !authverify.IsManagerUserID(hint.UserID)) {
			c.Next()
			return
		}
		claims, e := authverify.ParseClassified(raw)
		if e != nil {
			deny()
			return
		}
		c.Set(ContextClaims, claims)
		if claims.Purpose == "" {
			c.Next()
			return
		}
		p := c.Request.URL.Path
		if c.Request.URL.RawPath != "" || c.Request.URL.RawQuery != "" || !serviceauth.CanonicalPath(p) || c.Request.Method != http.MethodPost {
			deny()
			return
		}
		max := int64(8 * 1024 * 1024)
		if claims.Purpose == authverify.DelegatedPurpose {
			max = 1024 * 1024
		}
		body, e := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, max))
		if e != nil {
			deny()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		if claims.Purpose == authverify.ServicePurpose {
			key, e := serviceauth.Verify([]byte(config.Config.Secret), serviceauth.Internal, c.Request.Method, p, raw, body, c.Request.Header, time.Now())
			if e != nil {
				deny()
				return
			}
			ctx, cancel := context.WithTimeout(c, 2*time.Second)
			ok, e := rdb.SetNX(ctx, key, "1", serviceauth.ReplayTTL).Result()
			cancel()
			if e != nil || !ok {
				deny()
				return
			}
			c.Next()
			return
		}
		if !allowed[p] || validator == nil {
			deny()
			return
		}
		var input map[string]any
		if json.Unmarshal(body, &input) != nil || input == nil || !validBody(input, 0) {
			deny()
			return
		}
		if p == "/msg/batch_send_msg" {
			if sender, ok := input["sendID"].(string); !ok || sender != claims.UserID {
				deny()
				return
			}
		}
		actor, _, e := validator.Validate(c, raw, p, body)
		if e != nil || actor != claims.Delegation.AdminID {
			deny()
			return
		}
		c.Set(ContextAdmin, actor)
		if strings.HasPrefix(p, "/object/") {
			sum := sha256.Sum256([]byte(claims.Issuer + "\n" + actor))
			owner := "luobo-admin-" + hex.EncodeToString(sum[:24])
			c.Set(ContextUploadOwner, owner)
			if name, ok := input["name"].(string); ok {
				if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "/") || pathpkg.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n") || strings.HasPrefix(name, "../") {
					deny()
					return
				}
				input["name"] = owner + "/" + strings.TrimPrefix(name, claims.UserID+"/")
				body, _ = json.Marshal(input)
				c.Request.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
		c.Next()
	}
}
