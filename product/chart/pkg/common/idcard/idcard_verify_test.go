package idcard

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdentityVerificationNeverApprovesMissingProvider(t *testing.T) {
	for _, cfg := range []IDCardVerifyConfig{{}, {APIUrl: "https://example.com"}, {APIKey: "secret"}, {APIUrl: "http://example.com", APIKey: "secret"}} {
		if ok, err := NewIDCardVerifier(cfg).Verify("test", "identity"); ok || err == nil {
			t.Fatal("unavailable or insecure provider approved identity")
		}
	}
}

func TestIdentityVerificationRequiresProviderSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		accepted   bool
	}{
		{"match", `{"error_code":0,"result":{"res":1}}`, 200, true},
		{"mismatch", `{"error_code":0,"result":{"res":2}}`, 200, false},
		{"provider error", `{"error_code":1,"reason":"provider failure"}`, 200, false},
		{"HTTP failure", `{"error_code":0,"result":{"res":1}}`, 500, false},
		{"malformed", `not JSON`, 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); w.Write([]byte(tc.body)) }))
			defer server.Close()
			v := NewIDCardVerifier(IDCardVerifyConfig{APIUrl: server.URL, APIKey: "secret"})
			v.client = server.Client()
			ok, _ := v.Verify("test", "identity")
			if ok != tc.accepted {
				t.Fatalf("accepted=%v want=%v", ok, tc.accepted)
			}
		})
	}
}
