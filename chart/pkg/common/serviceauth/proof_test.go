package serviceauth

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServiceProofBindsPurposeActorPathMethodBodyAndTime(t *testing.T) {
	now := time.Unix(1800000000, 0)
	secret := []byte(strings.Repeat("s", 32))
	body := []byte(`{"userID":"synthetic"}`)
	header, err := Sign(secret, Internal, http.MethodPost, "/user/get_users", "synthetic-token", body, now)
	if err != nil {
		t.Fatal(err)
	}
	key, err := Verify(secret, Internal, http.MethodPost, "/user/get_users", "synthetic-token", body, header, now)
	if err != nil || !strings.HasPrefix(key, "LUOBO_SERVICE_NONCE:") {
		t.Fatal("valid proof denied")
	}
	for _, change := range []struct {
		purpose, method, path, token string
		body                         []byte
		when                         time.Time
	}{{Validation, "POST", "/user/get_users", "synthetic-token", body, now}, {Internal, "GET", "/user/get_users", "synthetic-token", body, now}, {Internal, "POST", "/msg/search_msg", "synthetic-token", body, now}, {Internal, "POST", "/user/get_users", "different-token", body, now}, {Internal, "POST", "/user/get_users", "synthetic-token", []byte(`{}`), now}, {Internal, "POST", "/user/get_users", "synthetic-token", body, now.Add(301 * time.Second)}} {
		if _, err = Verify(secret, change.purpose, change.method, change.path, change.token, change.body, header, change.when); err == nil {
			t.Fatal("tampered proof accepted")
		}
	}
	if _, err = Verify(secret, Internal, "POST", "/user/get_users", "synthetic-token", body, http.Header{}, now); err == nil {
		t.Fatal("unsigned service request accepted")
	}
}
func TestServiceProofRejectsNonCanonicalPathsAndWeakConfiguration(t *testing.T) {
	for _, p := range []string{"", "user/get", "/user/../msg/search_msg", "/user//get_users", "/user/%67et_users", "/user\\get_users", "/user/get_users\x00"} {
		if CanonicalPath(p) {
			t.Fatal("noncanonical path accepted")
		}
	}
	if _, err := Sign([]byte("weak"), Internal, "POST", "/user/get_users", "token", nil, time.Now()); err == nil {
		t.Fatal("weak key accepted")
	}
}

func TestNonceLifetimeCoversFutureSkewSecondBoundary(t *testing.T) {
	first := time.Unix(1000000, 100000000)
	key := []byte(strings.Repeat("synthetic", 5))
	header, err := Sign(key, Internal, "POST", "/user/get_users", "token", []byte(`{}`), time.Unix(first.Unix()+300, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Verify(key, Internal, "POST", "/user/get_users", "token", []byte(`{}`), header, first); err != nil {
		t.Fatal("future clock bound rejected unexpectedly")
	}
	// Once Redis may expire the nonce, timestamp validity must already have
	// ended, including Unix-second rounding at the last accepted instant.
	if _, err = Verify(key, Internal, "POST", "/user/get_users", "token", []byte(`{}`), header, first.Add(ReplayTTL)); err == nil {
		t.Fatal("nonce can expire while timestamp is still accepted")
	}
}
