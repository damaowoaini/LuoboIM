package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type memLock struct{ m map[string]int64 }

func (l *memLock) Count(_ context.Context, k string) (int64, error) { return l.m[k], nil }
func (l *memLock) Incr(_ context.Context, k string, _ time.Duration) (int64, error) {
	l.m[k]++
	return l.m[k], nil
}
func (l *memLock) Reset(_ context.Context, ks ...string) error {
	for _, k := range ks {
		delete(l.m, k)
	}
	return nil
}

func newLimitedEngine(lock *memLock, perAccountIP, perIP int64) (*gin.Engine, *string) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	seen := new(string)
	r.POST("/login", adminAttemptLimit(lock, perAccountIP, perIP, 15*time.Minute), func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		*seen = string(b)
		c.String(200, "ok")
	})
	return r, seen
}

func post(r *gin.Engine, remote, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
	req.RemoteAddr = remote
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAdminAttemptLimitPerAccountIP(t *testing.T) {
	lock := &memLock{m: map[string]int64{}}
	r, seen := newLimitedEngine(lock, 3, 100)
	for i := 0; i < 3; i++ {
		w := post(r, "203.0.113.9:1", `{"account":"admin","password":"x"}`)
		if w.Body.String() != "ok" {
			t.Fatalf("attempt %d blocked: %s", i, w.Body.String())
		}
	}
	if *seen != `{"account":"admin","password":"x"}` {
		t.Fatalf("handler must still see the full body, got %q", *seen)
	}
	if w := post(r, "203.0.113.9:1", `{"account":"admin","password":"x"}`); w.Body.String() == "ok" {
		t.Fatal("4th attempt from same ip+account must be blocked")
	}
	if w := post(r, "203.0.113.10:1", `{"account":"admin","password":"x"}`); w.Body.String() != "ok" {
		t.Fatal("another ip must not be blocked by per-account-ip limit")
	}
}

func TestAdminAttemptLimitPerIP(t *testing.T) {
	lock := &memLock{m: map[string]int64{}}
	r, _ := newLimitedEngine(lock, 100, 2)
	post(r, "203.0.113.9:1", `{"account":"a1"}`)
	post(r, "203.0.113.9:1", `{"account":"a2"}`)
	if w := post(r, "203.0.113.9:1", `{"account":"a3"}`); w.Body.String() == "ok" {
		t.Fatal("ip spraying many accounts must be blocked")
	}
}

func TestAdminAttemptLimitSpoofedHeadersIgnored(t *testing.T) {
	lock := &memLock{m: map[string]int64{}}
	r, _ := newLimitedEngine(lock, 1, 100)
	req := func(fake string) string {
		rq := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"account":"admin"}`))
		rq.RemoteAddr = "203.0.113.9:1"
		rq.Header.Set("X-Real-IP", fake)
		rq.Header.Set("X-Forwarded-For", fake)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, rq)
		return w.Body.String()
	}
	if req("1.1.1.1") != "ok" {
		t.Fatal("first attempt should pass")
	}
	if req("2.2.2.2") == "ok" {
		t.Fatal("rotating spoofed headers must not reset the limit")
	}
}
