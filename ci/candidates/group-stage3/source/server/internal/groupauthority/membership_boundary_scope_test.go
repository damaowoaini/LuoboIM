package groupauthority

import (
	"context"
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
)

func TestMemberRemovalCannotBeBorrowedFromActorMetadataOrZeroCapability(t *testing.T) {
	ctx := context.WithValue(context.Background(), constant.OpUserID, "owner")
	if _, err := CompletedMemberRemoval(ctx, nil, "g"); !errors.Is(err, ErrInvalidScope) {
		t.Fatal("actor metadata granted a member removal capability", err)
	}
	if _, err := (&MemberRemovalScope{}).Inspect(ctx, nil); !errors.Is(err, ErrInvalidScope) {
		t.Fatal("empty capability granted a writer bound", err)
	}
}
