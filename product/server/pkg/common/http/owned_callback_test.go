package http

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestOwnedCallbackTransportPreservesPostAndClosesOnlyOriginalOwnedIdleConnection(t *testing.T) {
	var mu sync.Mutex
	addresses := map[string]string{}
	closed := map[string]bool{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil || string(raw) != "{\"same\":\"body\"}" || r.Header.Get("original-header") != "kept" || r.Method != http.MethodPost {
			t.Error("original Post contract changed")
		}
		mu.Lock()
		addresses[r.Header.Get("scope")] = r.RemoteAddr
		mu.Unlock()
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	server.Config.ConnState = func(c net.Conn, state http.ConnState) {
		if state == http.StateClosed {
			mu.Lock()
			closed[c.RemoteAddr().String()] = true
			mu.Unlock()
		}
	}
	server.Start()
	defer server.Close()
	owned, err := NewOwnedCallbackHTTP()
	if err != nil {
		t.Fatal(err)
	}
	defer owned.Close()
	post := func(ctx context.Context, scope string) {
		t.Helper()
		raw, err := Post(ctx, server.URL, map[string]string{"original-header": "kept", "scope": scope}, map[string]string{"same": "body"}, 1)
		if err != nil || string(raw) != `{"ok":true}` {
			t.Fatal("original signed-callback Post path failed", err)
		}
	}
	post(context.Background(), "normal")
	post(owned.Bind(context.Background()), "owned")
	mu.Lock()
	normal, own := addresses["normal"], addresses["owned"]
	mu.Unlock()
	if normal == "" || own == "" || normal == own {
		t.Fatal("owned callback borrowed global connection")
	}
	if err := owned.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		mu.Lock()
		done, foreign := closed[own], closed[normal]
		mu.Unlock()
		if foreign {
			t.Fatal("owned shutdown closed normal caller's transport")
		}
		if done {
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("owned idle socket not closed")
		case <-tick.C:
		}
	}
	post(context.Background(), "normal")
	mu.Lock()
	defer mu.Unlock()
	if addresses["normal"] != normal {
		t.Fatal("normal default Post connection was replaced by owned shutdown")
	}
}
