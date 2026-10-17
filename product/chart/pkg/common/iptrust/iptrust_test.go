package iptrust

import (
	"net/http"
	"testing"
)

func mustNew(t *testing.T, proxies, forwarders []string) *Resolver {
	t.Helper()
	r, err := New(proxies, forwarders, "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestDirectPublicPeerIgnoresHeadersAndClaim(t *testing.T) {
	r := mustNew(t, nil, nil)
	got := r.ClientIP("203.0.113.9:5555", hdr("X-Real-IP", "1.1.1.1", "X-Forwarded-For", "2.2.2.2"), "3.3.3.3")
	if got != "203.0.113.9" {
		t.Fatalf("want peer ip, got %s", got)
	}
}

func TestBehindNginxUsesForwardedForNotSpoofedRealIP(t *testing.T) {
	// nginx(内网 172.18.0.5) 用 $remote_addr 覆盖了 XFF，但没覆盖 X-Real-IP
	r := mustNew(t, nil, nil)
	got := r.ClientIP("172.18.0.5:40000", hdr("X-Real-IP", "1.1.1.1", "X-Forwarded-For", "198.51.100.7"), "3.3.3.3")
	if got != "198.51.100.7" {
		t.Fatalf("want nginx-reported peer, got %s", got)
	}
}

func TestXFFTakesRightmostUntrusted(t *testing.T) {
	r := mustNew(t, nil, nil)
	got := r.ClientIP("10.0.0.2:1", hdr("X-Forwarded-For", "6.6.6.6, 198.51.100.7, 10.0.0.3"), "")
	if got != "198.51.100.7" {
		t.Fatalf("want rightmost untrusted, got %s", got)
	}
}

func TestTrustedForwarderMayReportClientIP(t *testing.T) {
	r := mustNew(t, nil, []string{"192.0.2.10"})
	// 路由服务直连
	if got := r.ClientIP("192.0.2.10:1", hdr("X-Real-IP", "198.51.100.20"), ""); got != "198.51.100.20" {
		t.Fatalf("direct forwarder header: got %s", got)
	}
	// 路由服务经 nginx
	if got := r.ClientIP("172.18.0.5:1", hdr("X-Forwarded-For", "192.0.2.10", "X-Real-IP", "198.51.100.21"), ""); got != "198.51.100.21" {
		t.Fatalf("forwarder via nginx: got %s", got)
	}
	// 请求体里代报的 IP 优先于头
	if got := r.ClientIP("192.0.2.10:1", hdr("X-Real-IP", "198.51.100.20"), "198.51.100.22"); got != "198.51.100.22" {
		t.Fatalf("forwarder claim: got %s", got)
	}
	// 非法代报值回退到上游 IP
	if got := r.ClientIP("192.0.2.10:1", hdr("X-Real-IP", "not-an-ip"), "bad"); got != "192.0.2.10" {
		t.Fatalf("invalid claim fallback: got %s", got)
	}
}

func TestNonForwarderClaimIgnored(t *testing.T) {
	r := mustNew(t, nil, []string{"192.0.2.10"})
	if got := r.ClientIP("172.18.0.5:1", hdr("X-Forwarded-For", "198.51.100.7", "X-Real-IP", "1.1.1.1"), "3.3.3.3"); got != "198.51.100.7" {
		t.Fatalf("got %s", got)
	}
}

func TestCustomProxyHeaderFromTrustedProxy(t *testing.T) {
	r, err := New(nil, nil, "CF-Connecting-IP")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.ClientIP("10.0.0.2:1", hdr("CF-Connecting-IP", "198.51.100.30", "X-Forwarded-For", "198.51.100.7"), ""); got != "198.51.100.30" {
		t.Fatalf("got %s", got)
	}
	// 公网直连时不认该头
	if got := r.ClientIP("203.0.113.9:1", hdr("CF-Connecting-IP", "1.1.1.1"), ""); got != "203.0.113.9" {
		t.Fatalf("got %s", got)
	}
}

func TestExplicitProxiesReplaceDefaults(t *testing.T) {
	r := mustNew(t, []string{"203.0.113.0/24"}, nil)
	if got := r.ClientIP("203.0.113.9:1", hdr("X-Forwarded-For", "198.51.100.7"), ""); got != "198.51.100.7" {
		t.Fatalf("got %s", got)
	}
	if got := r.ClientIP("10.0.0.2:1", hdr("X-Forwarded-For", "198.51.100.7"), ""); got != "10.0.0.2" {
		t.Fatalf("private range must not be trusted when proxies are explicit, got %s", got)
	}
}

func TestBadConfig(t *testing.T) {
	if _, err := New([]string{"nope"}, nil, ""); err == nil {
		t.Fatal("expected error")
	}
}
