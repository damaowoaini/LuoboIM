package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	drivermysql "github.com/go-sql-driver/mysql"
	"io"
	"net/http"
	"openim-route-service/service"
	"time"
)

func strictProvisionJSON(body []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func registerAdminProvisioning(api *gin.RouterGroup, secrets map[string]string) {
	for _, path := range []string{service.AdminProvisionCommitPath, service.AdminProvisionQueryPath} {
		logicalPath := path
		api.POST(path[len("/api"):], func(c *gin.Context) {
			body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10))
			if err != nil {
				envelope(c, 400, "开户回执格式无效", nil)
				return
			}
			serverID, err := service.VerifyAdminProvisionSignature(secrets, c.Request.Header, logicalPath, body, time.Now())
			if err != nil {
				c.JSON(http.StatusUnauthorized, gin.H{"errCode": 401, "errMsg": "开户通道认证失败"})
				return
			}
			var request service.AdminProvisionRequest
			var query struct {
				ReceiptID string `json:"receiptID"`
			}
			if logicalPath == service.AdminProvisionCommitPath {
				err = strictProvisionJSON(body, &request)
				if err == nil {
					err = service.ValidateAdminProvision(request)
				}
				if err == nil && request.ServerID != serverID {
					err = service.ErrProvisionAuthentication
				}
			} else {
				err = strictProvisionJSON(body, &query)
				if err == nil && !service.ValidAdminProvisionReceiptID(query.ReceiptID) {
					err = errors.New("invalid receipt ID")
				}
			}
			if err != nil {
				envelope(c, 400, "开户回执格式或归属无效", nil)
				return
			}
			if err = service.ConsumeAdminProvisionNonce(c.Request.Context(), serverID, c.GetHeader("X-Luobo-Route-Nonce"), time.Now()); err != nil {
				var duplicate *drivermysql.MySQLError
				if errors.As(err, &duplicate) && duplicate.Number == 1062 {
					c.JSON(http.StatusUnauthorized, gin.H{"errCode": 401, "errMsg": "开户通道认证失败"})
				} else {
					envelope(c, 500, "开户通道存储暂时不可用", nil)
				}
				return
			}
			var result *service.AdminProvisionResult
			if logicalPath == service.AdminProvisionCommitPath {
				result, err = service.CommitAdminProvision(c.Request.Context(), request)
			} else {
				result, err = service.QueryAdminProvision(c.Request.Context(), serverID, query.ReceiptID)
			}
			if errors.Is(err, service.ErrProvisionConflict) {
				envelope(c, 409, "开户回执与原请求冲突", nil)
				return
			}
			if err != nil {
				envelope(c, 500, "开户归属暂未确认，请查询回执后恢复", nil)
				return
			}
			envelope(c, 0, "", result)
		})
	}
}
