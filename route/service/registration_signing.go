package service

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type registrationContextKey struct{}

func registrationContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, registrationContextKey{}, id)
}
func ParseUpstreamSecrets(raw, fallback string) (map[string]string, error) {
	secrets := map[string]string{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &secrets); err != nil {
			return nil, errors.New("ROUTE_UPSTREAM_SECRETS must be a JSON serverID-to-secret map")
		}
	}
	if fallback != "" {
		secrets["*"] = fallback
	}
	for id, key := range secrets {
		if strings.TrimSpace(id) == "" || len(key) < 32 {
			return nil, errors.New("route upstream secret requires a serverID and at least 32 characters")
		}
	}
	return secrets, nil
}
func (f *HTTPForwarder) registrationSecret(serverID string) string {
	if s := f.Secrets[serverID]; s != "" {
		return s
	}
	return f.Secrets["*"]
}
func (f *HTTPForwarder) RegistrationConfigured(serverID string) bool {
	return len(f.registrationSecret(serverID)) >= 32
}
func (f *HTTPForwarder) signRegistration(req *http.Request, body []byte, serverID, id string) error {
	secret := f.registrationSecret(serverID)
	if len(secret) < 32 {
		return errors.New("该租户尚未配置安全注册通道")
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return errors.New("无法签署注册请求")
	}
	nonce := hex.EncodeToString(random[:])
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	digest := sha256.Sum256(body)
	path := strings.TrimPrefix(req.URL.Path, "/chat")
	if path != "/account/register" && path != "/internal/registration/receipt" {
		return errors.New("不支持的内部注册路径")
	}
	canonical := timestamp + "\n" + nonce + "\n" + req.Method + "\n" + path + "\n" + id + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	req.Header.Set("X-Luobo-Route-Registration-ID", id)
	req.Header.Set("X-Luobo-Route-Timestamp", timestamp)
	req.Header.Set("X-Luobo-Route-Nonce", nonce)
	req.Header.Set("X-Luobo-Route-Signature", hex.EncodeToString(mac.Sum(nil)))
	return nil
}
