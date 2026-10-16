package totp

import (
	"encoding/base32"
	"net/url"
	"testing"
)

func TestRFC6238SHA1Vectors(t *testing.T) {
	secret := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))
	for _, tc := range []struct {
		at   int64
		code string
	}{
		{59, "287082"}, {1111111109, "081804"}, {1111111111, "050471"},
		{1234567890, "005924"}, {2000000000, "279037"}, {20000000000, "353130"},
	} {
		if got := generateCode(secret, tc.at); got != tc.code {
			t.Fatalf("at %d: got %s, want %s", tc.at, got, tc.code)
		}
	}
}

func TestRejectInvalidCodesAndSecrets(t *testing.T) {
	for _, tc := range []struct{ secret, code string }{
		{"invalid!", ""}, {"", ""}, {"", "123456"}, {"invalid!", "123456"},
		{GenerateSecret(), "12345"}, {GenerateSecret(), "１２３４５６"}, {GenerateSecret(), "12a456"},
	} {
		if ValidateCode(tc.secret, tc.code, 1) {
			t.Fatal("invalid authentication was accepted")
		}
	}
	secret := GenerateSecret()
	if !ValidateCode(secret, GetCurrentCode(secret), 1) {
		t.Fatal("valid code was rejected")
	}
}

func TestSecretAndQR(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		s := GenerateSecret()
		b, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(s)
		if err != nil || len(b) != 20 || seen[s] {
			t.Fatal("invalid or repeated secret")
		}
		seen[s] = true
	}
	u, err := url.Parse(GenerateQRCodeURL("萝卜 & IM", "a+b@example.com", "ABC"))
	if err != nil || u.Query().Get("issuer") != "萝卜 & IM" || u.Query().Get("secret") != "ABC" {
		t.Fatal("QR fields were not encoded safely")
	}
}
