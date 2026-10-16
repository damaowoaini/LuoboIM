package adminprovision_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	p "github.com/OpenIMSDK/chat/pkg/common/adminprovision"
	"github.com/google/uuid"
)

func TestRouteFrozenIndependentSignatureVector(t *testing.T) {
	body := []byte(`{"version":1,"receiptID":"00000000-0000-4000-8000-000000000001","serverID":"tenant-a","userID":"synthetic-user","actorUserID":"synthetic-admin","imProvisioned":true,"accounts":[{"email":"person@fixture.example"}]}`)
	got := p.Signature([]byte("test-only-kkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkkk"), "tenant-a", p.CommitPath, "1700000000", "0123456789abcdef0123456789abcdef", body)
	if got != "28407abd8841f156ba117ea4e31fb085150f71dde329b5ac1524613d08066596" {
		t.Fatal("route frozen independent Python vector mismatch")
	}
}
func TestRouteRealHTTPHeadersPathsResponseAndRedirect(t *testing.T) {
	secret := []byte(strings.Repeat("synthetic-route-secret-", 2))
	seen := map[string]bool{}
	redirectHits := 0
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectHits++; w.WriteHeader(200) }))
	defer redirected.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		nonce, ts := r.Header.Get("X-Luobo-Route-Nonce"), r.Header.Get("X-Luobo-Route-Timestamp")
		if r.Method != "POST" || r.Header.Get("operationID") == "" || r.Header.Get("X-Luobo-Route-Server-ID") != "tenant-a" || len(nonce) != 32 || seen[nonce] {
			t.Error("missing production header or replayed nonce")
		}
		seen[nonce] = true
		if _, e := strconv.ParseInt(ts, 10, 64); e != nil {
			t.Error(e)
		}
		if r.Header.Get("X-Luobo-Route-Signature") != p.Signature(secret, "tenant-a", r.URL.Path, ts, nonce, body) {
			t.Error("wrong exact-body signature")
		}
		switch r.URL.Path {
		case p.ReceiptPath:
			json.NewEncoder(w).Encode(map[string]any{"errCode": 0, "data": map[string]any{"found": false}})
		case p.CommitPath:
			http.Redirect(w, r, redirected.URL, 307)
		default:
			t.Error("unexpected signed path")
		}
	}))
	defer server.Close()
	c := &p.RouteClient{ServerID: "tenant-a", Base: server.URL, Secret: secret, AllowLoopbackHTTP: true}
	if _, e := c.Query(context.Background(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Query(context.Background(), uuid.NewString()); e != nil {
		t.Fatal(e)
	}
	if _, e := c.Commit(context.Background(), []byte(`{"synthetic":true}`)); e == nil || redirectHits != 0 {
		t.Fatal("redirect followed")
	}
	c.AllowLoopbackHTTP = false
	if c.Validate() == nil {
		t.Fatal("plaintext production upstream allowed")
	}
	for _, base := range []string{"http://example.com", "https://a:b@fixture.invalid", "https://fixture.invalid/path", "https://fixture.invalid/?key=value", "https://fixture.invalid/#fragment"} {
		c.Base = base
		if c.Validate() == nil {
			t.Fatalf("unsafe endpoint accepted: %s", base)
		}
	}
}
