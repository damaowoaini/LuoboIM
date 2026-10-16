package office

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
)

func TestSelfOrAdmin(t *testing.T) {
	user := mctx.WithOpUserID(context.Background(), "u1", constant.NormalUser)
	admin := mctx.WithOpUserID(context.Background(), "a1", constant.AdminUser)
	if _, err := selfOrAdmin(context.Background(), ""); err == nil {
		t.Fatal("missing identity and empty userID must be rejected")
	}

	if got, err := selfOrAdmin(user, ""); err != nil || got != "u1" {
		t.Fatalf("empty userID should resolve to self, got %q %v", got, err)
	}
	if got, err := selfOrAdmin(user, "u1"); err != nil || got != "u1" {
		t.Fatalf("own userID should pass, got %q %v", got, err)
	}
	if _, err := selfOrAdmin(user, "u2"); err == nil {
		t.Fatal("normal user acting as someone else must be rejected")
	}
	if got, err := selfOrAdmin(admin, "u2"); err != nil || got != "u2" {
		t.Fatalf("admin may act on behalf of a user, got %q %v", got, err)
	}
}
