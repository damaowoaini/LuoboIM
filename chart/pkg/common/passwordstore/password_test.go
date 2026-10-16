package passwordstore

import (
	"context"
	"strings"
	"testing"
)

func TestPasswordStorageSaltedVerifierAndLegacyCompatibility(t *testing.T) {
	ctx := context.Background()
	wire := "0123456789abcdef0123456789abcdef"
	first, err := Hash(ctx, wire)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Hash(ctx, wire)
	if err != nil {
		t.Fatal(err)
	}
	if first == second || !strings.HasPrefix(first, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Fatal("password hashes were not independently salted")
	}
	for _, stored := range []string{wire, first, second} {
		valid, legacy, err := Verify(ctx, stored, strings.ToUpper(wire))
		if err != nil || !valid || legacy != (stored == wire) {
			t.Fatal("existing MD5 wire protocol failed verification")
		}
		if valid, _, _ := Verify(ctx, stored, "abcdef0123456789abcdef0123456789"); valid {
			t.Fatal("wrong password accepted")
		}
	}
}

func TestPasswordStorageMalformedAndResourceAbuseAreRejected(t *testing.T) {
	for _, stored := range []string{"", "plain-password", "$argon2id$v=18$m=65536,t=3,p=4$a$b", "$argon2id$v=19$m=4294967295,t=3,p=4$a$b", "$argon2id$v=19$m=65536,t=999,p=4$a$b", "$argon2id$v=19$m=65536,t=3,p=0$a$b", "$argon2id$v=19$m=65536,t=3,p=4$invalid!$invalid!"} {
		if valid, _, _ := Verify(context.Background(), stored, "0123456789abcdef0123456789abcdef"); valid {
			t.Fatal("malformed verifier authenticated")
		}
	}
	if _, err := Hash(context.Background(), "plaintext"); err == nil {
		t.Fatal("nonprotocol credential accepted")
	}
	for i := 0; i < cap(capacity); i++ {
		capacity <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(capacity); i++ {
			<-capacity
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Hash(ctx, "0123456789abcdef0123456789abcdef"); err != context.Canceled {
		t.Fatalf("cancelled waiter allocated memory: %v", err)
	}
}
