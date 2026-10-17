package groupauthority

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
)

func TestMemberAdmissionCommandFreezesActorMembersAndIndependentIntent(t *testing.T) {
	ids := []string{"b", "a"}
	epoch := uuid.NewString()
	base := context.WithValue(context.Background(), constant.OpUserID, "owner")
	ctx, one, err := NewMemberAdmissionCommand(base, epoch, "g", ids, MemberAdmissionInvite)
	if err != nil {
		t.Fatal(err)
	}
	ids[0] = "foreign"
	command := ctx.Value(memberAdmissionKey{}).(*memberAdmissionCommand)
	if command.members[0] != "a" || command.members[1] != "b" || command.info.Actor != "owner" {
		t.Fatal("caller mutated captured intent")
	}
	_, two, err := NewMemberAdmissionCommand(base, epoch, "g", []string{"a", "b"}, MemberAdmissionInvite)
	if err != nil || one.ID == two.ID {
		t.Fatal("new explicit action reused an old intent", err)
	}
	if _, _, err := NewMemberAdmissionCommand(ctx, epoch, "g", []string{"a"}, MemberAdmissionJoin); err == nil {
		t.Fatal("nested action renewed an old command")
	}
	if _, err := (&MemberAdmissionScope{}).Inspect(ctx, nil); err == nil {
		t.Fatal("tuple-only scope granted admission")
	}
}

func TestMemberAdmissionCommandRejectsInvalidOrDuplicateIdentityBeforeStorage(t *testing.T) {
	for _, ids := range [][]string{nil, {""}, {"a", "a"}, {"a\n"}} {
		if _, _, err := NewMemberAdmissionCommand(noticeTestActor("owner"), uuid.NewString(), "g", ids, MemberAdmissionJoin); err == nil {
			t.Fatal("invalid member intent accepted")
		}
	}
	if _, _, err := NewMemberAdmissionCommand(noticeTestActor("owner"), uuid.NewString(), "g", []string{"a"}, MemberAdmissionKind("arbitrary-grant")); err == nil {
		t.Fatal("unknown admission action accepted")
	}
	if _, err := BeginMemberAdmission(nil, nil, "g"); err == nil {
		t.Fatal("nil scope accepted")
	}
}
