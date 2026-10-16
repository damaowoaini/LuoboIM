package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"gorm.io/gorm"
	"net/http"
	"openim-route-service/db"
	"openim-route-service/model"
	"regexp"
	"strconv"
	"time"
)

const AdminProvisionCommitPath = "/api/internal/admin_provisioning/commit"
const AdminProvisionQueryPath = "/api/internal/admin_provisioning/receipt"

var ErrProvisionAuthentication = errors.New("开户通道认证失败")
var nonceHex = regexp.MustCompile(`^[0-9a-f]{32}$`)

func AdminProvisionSignature(secret, serverID, timestamp, nonce, path string, body []byte) string {
	derive := hmac.New(sha256.New, []byte(secret))
	derive.Write([]byte("luobo-admin-provisioning/v1"))
	sum := sha256.Sum256(body)
	canonical := timestamp + "\n" + nonce + "\nPOST\n" + path + "\n" + serverID + "\n" + hex.EncodeToString(sum[:])
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte(canonical))
	return hex.EncodeToString(mac.Sum(nil))
}
func VerifyAdminProvisionSignature(secrets map[string]string, header http.Header, path string, body []byte, now time.Time) (string, error) {
	id := header.Get("X-Luobo-Route-Server-ID")
	secret := secrets[id]
	if id == "" || id == "*" || len(id) > 50 || len(secret) < 32 || path != AdminProvisionCommitPath && path != AdminProvisionQueryPath {
		return "", ErrProvisionAuthentication
	}
	ts, nonce, sig := header.Get("X-Luobo-Route-Timestamp"), header.Get("X-Luobo-Route-Nonce"), header.Get("X-Luobo-Route-Signature")
	stamp, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || strconv.FormatInt(stamp, 10) != ts || stamp < now.Unix()-60 || stamp > now.Unix()+60 || !nonceHex.MatchString(nonce) {
		return "", ErrProvisionAuthentication
	}
	decoded, err := hex.DecodeString(sig)
	if err != nil || len(decoded) != 32 || !hmac.Equal([]byte(sig), []byte(AdminProvisionSignature(secret, id, ts, nonce, path, body))) {
		return "", ErrProvisionAuthentication
	}
	return id, nil
}
func ConsumeAdminProvisionNonce(ctx context.Context, serverID, nonce string, now time.Time) error {
	sum := sha256.Sum256([]byte(serverID + "\n" + nonce))
	return db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Timestamp checks use whole Unix seconds. Keep the nonce beyond the
		// entire ±60-second window, including its last fractional second.
		if err := tx.Create(&model.AdminProvisionNonce{Digest: hex.EncodeToString(sum[:]), ExpiresAt: now.Add(3 * time.Minute)}).Error; err != nil {
			return err
		}
		return tx.Where("expires_at < ?", now).Delete(&model.AdminProvisionNonce{}).Error
	})
}
