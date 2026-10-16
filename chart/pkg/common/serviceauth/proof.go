// Package serviceauth defines the versioned, purpose-separated HTTP request
// proof shared by the chart and IM modules. Token possession alone is not a
// service credential. Receivers must atomically consume NonceKey before use.
package serviceauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

const (
	Internal        = "internal-im-v1"
	Validation      = "admin-validation-v1"
	TimestampHeader = "X-Luobo-Service-Timestamp"
	NonceHeader     = "X-Luobo-Service-Nonce"
	SignatureHeader = "X-Luobo-Service-Signature"
	ReplayTTL       = 11 * time.Minute
)

func validPurpose(p string) bool { return p == Internal || p == Validation }
func CanonicalPath(p string) bool {
	return p != "" && strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.ContainsAny(p, "\\%\r\n\x00")
}
func digest(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func signature(secret []byte, purpose, ts, nonce, method, logicalPath, token string, body []byte) ([]byte, error) {
	if len(secret) < 32 || !validPurpose(purpose) || !CanonicalPath(logicalPath) || method == "" || token == "" {
		return nil, errors.New("invalid service proof configuration")
	}
	derive := hmac.New(sha256.New, secret)
	derive.Write([]byte("luobo-service-auth-kdf-v1\n" + purpose))
	mac := hmac.New(sha256.New, derive.Sum(nil))
	mac.Write([]byte("v1\n" + purpose + "\n" + ts + "\n" + nonce + "\n" + method + "\n" + logicalPath + "\n" + digest([]byte(token)) + "\n" + digest(body)))
	return mac.Sum(nil), nil
}
func Sign(secret []byte, purpose, method, logicalPath, token string, body []byte, now time.Time) (http.Header, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	ts, n := strconv.FormatInt(now.Unix(), 10), hex.EncodeToString(nonce)
	sig, err := signature(secret, purpose, ts, n, method, logicalPath, token, body)
	if err != nil {
		return nil, err
	}
	return http.Header{TimestampHeader: []string{ts}, NonceHeader: []string{n}, SignatureHeader: []string{hex.EncodeToString(sig)}}, nil
}
func Verify(secret []byte, purpose, method, logicalPath, token string, body []byte, header http.Header, now time.Time) (string, error) {
	ts, nonce := header.Get(TimestampHeader), header.Get(NonceHeader)
	at, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || at < now.Unix()-300 || at > now.Unix()+300 {
		return "", errors.New("service proof timestamp invalid")
	}
	rawNonce, err := hex.DecodeString(nonce)
	if err != nil || len(rawNonce) != 16 || len(nonce) != 32 {
		return "", errors.New("service proof nonce invalid")
	}
	supplied, err := hex.DecodeString(header.Get(SignatureHeader))
	if err != nil || len(supplied) != 32 {
		return "", errors.New("service proof signature invalid")
	}
	expected, err := signature(secret, purpose, ts, nonce, method, logicalPath, token, body)
	if err != nil || !hmac.Equal(expected, supplied) {
		return "", errors.New("service proof denied")
	}
	return "LUOBO_SERVICE_NONCE:" + digest([]byte(purpose+"\n"+digest([]byte(token))+"\n"+nonce)), nil
}
