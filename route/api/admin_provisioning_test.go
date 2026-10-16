package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"openim-route-service/service"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProvisionEndpointRejectsUnauthenticatedUnprovisionedAndCredentialFields(t *testing.T) {
	secret := strings.Repeat("test-key-", 5)
	router := SetupRouterWithConfig(RouterConfig{UpstreamSecrets: map[string]string{"tenant-a": secret}})
	base := `{"version":1,"receiptID":"00000000-0000-4000-8000-000000000001","serverID":"tenant-a","userID":"synthetic-user","actorUserID":"synthetic-admin","imProvisioned":true,"accounts":[{"email":"synthetic@fixture.example"}]}`
	for _, test := range []struct {
		name, body string
		signed     bool
		code       int
	}{
		{"unsigned", base, false, 401},
		{"credential_field", strings.TrimSuffix(base, "}") + `,"password":"forbidden"}`, true, 400},
		{"im_not_ready", strings.Replace(base, `"imProvisioned":true`, `"imProvisioned":false`, 1), true, 400},
		{"tenant_body_mismatch", strings.Replace(base, `"serverID":"tenant-a"`, `"serverID":"tenant-b"`, 1), true, 400},
		{"trailing_json", base + `{}`, true, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, service.AdminProvisionCommitPath, bytes.NewBufferString(test.body))
			req.Header.Set("Content-Type", "application/json")
			if test.signed {
				stamp := strconv.FormatInt(time.Now().Unix(), 10)
				nonce := strings.Repeat("a", 32)
				req.Header.Set("X-Luobo-Route-Server-ID", "tenant-a")
				req.Header.Set("X-Luobo-Route-Timestamp", stamp)
				req.Header.Set("X-Luobo-Route-Nonce", nonce)
				req.Header.Set("X-Luobo-Route-Signature", service.AdminProvisionSignature(secret, "tenant-a", stamp, nonce, service.AdminProvisionCommitPath, []byte(test.body)))
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			var value struct {
				ErrCode int `json:"errCode"`
			}
			if json.Unmarshal(w.Body.Bytes(), &value) != nil || value.ErrCode != test.code {
				t.Fatalf("unexpected rejection %d", value.ErrCode)
			}
		})
	}
}
