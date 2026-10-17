package groupauthority

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
)

func TestMemberApplicationCommandBindsOriginalActorApplicantsAndAcceptedAdmission(t *testing.T) {
	ids := []string{"b", "a"}
	epoch := uuid.NewString()
	actor := context.WithValue(context.Background(), constant.OpUserID, "owner")
	ctx, info, err := NewMemberApplicationCommand(actor, epoch, "g", ids, MemberApplicationCreated)
	if err != nil {
		t.Fatal(err)
	}
	ids[0] = "foreign"
	command := ctx.Value(memberApplicationKey{}).(*memberApplicationCommand)
	if command.applicants[0] != "a" || command.applicants[1] != "b" || info.Actor != "owner" {
		t.Fatal("caller changed actual application intent")
	}
	accepted, one, err := NewMemberApplicationCommand(actor, epoch, "g", []string{"a"}, MemberApplicationAccepted)
	if err != nil {
		t.Fatal(err)
	}
	linked, two, err := NewMemberAdmissionCommand(accepted, epoch, "g", []string{"a"}, MemberAdmissionApprove)
	if err != nil || one != two {
		t.Fatal("actual admission and accepted notice detached", err)
	}
	if linked.Value(memberApplicationKey{}) != accepted.Value(memberApplicationKey{}) {
		t.Fatal("link lost original request capability")
	}
	_, three, err := NewMemberApplicationCommand(actor, epoch, "g", []string{"a"}, MemberApplicationAccepted)
	if err != nil || three.ID == one.ID {
		t.Fatal("new explicit application intent reused an old ID", err)
	}
}

func TestMemberApplicationCommandRejectsUnscopedNestedOrChangedAdmission(t *testing.T) {
	epoch := uuid.NewString()
	actor := context.WithValue(context.Background(), constant.OpUserID, "owner")
	for _, ids := range [][]string{nil, {""}, {"a", "a"}, {"a\n"}} {
		if _, _, err := NewMemberApplicationCommand(actor, epoch, "g", ids, MemberApplicationCreated); err == nil {
			t.Fatal("malformed application scope accepted")
		}
	}
	ctx, _, err := NewMemberApplicationCommand(actor, epoch, "g", []string{"a"}, MemberApplicationAccepted)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = NewMemberApplicationCommand(ctx, epoch, "g", []string{"a"}, MemberApplicationAccepted); err == nil {
		t.Fatal("nested application renewed command")
	}
	for _, change := range []struct {
		ctx      context.Context
		gid, uid string
		kind     MemberAdmissionKind
	}{{ctx, "g", "b", MemberAdmissionApprove}, {ctx, "foreign", "a", MemberAdmissionApprove}, {context.WithValue(ctx, constant.OpUserID, "foreign"), "g", "a", MemberAdmissionApprove}, {ctx, "g", "a", MemberAdmissionJoin}} {
		if _, _, err = NewMemberAdmissionCommand(change.ctx, epoch, change.gid, []string{change.uid}, change.kind); err == nil {
			t.Fatal("changed admission borrowed accepted request")
		}
	}
	if _, err := (&MemberApplicationScope{}).Inspect(ctx, nil); err == nil {
		t.Fatal("tuple-only application granted notification")
	}
}
