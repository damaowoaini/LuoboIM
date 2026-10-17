package groupauthority

import (
	"context"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
)

func noticeTestActor(actor string) context.Context {
	return context.WithValue(context.Background(), constant.OpUserID, actor)
}

func TestNoticeCommandIsServerGeneratedAndCannotBorrowCallerIdentity(t *testing.T) {
	epoch := uuid.NewString()
	ctx := noticeTestActor("synthetic-owner")
	first, a, err := NewNoticeCommand(ctx, epoch, NoticeMuteGroup, "g")
	if err != nil { t.Fatal(err) }
	_, b, err := NewNoticeCommand(ctx, epoch, NoticeMuteGroup, "g")
	if err != nil || a.ID == b.ID || a.Actor != "synthetic-owner" || a.Epoch != epoch || a.CreatedAt.IsZero() { t.Fatal("separate RPC intents were merged or lost binding") }
	if _, _, err := NewNoticeCommand(first, epoch, NoticeMuteGroup, "g"); err == nil { t.Fatal("nested intent replaced the first command") }
	changed := context.WithValue(first, constant.OpUserID, "another-owner")
	if _, err := commandForMutation(changed, []Target{{GroupID: "g"}}); err == nil { t.Fatal("command borrowed another actor") }
}

func TestNoticeCommandNeverWidensGroupOnlyWriterScope(t *testing.T) {
	ctx, _, err := NewNoticeCommand(noticeTestActor("owner"), uuid.NewString(), NoticeMuteGroup, "g")
	if err != nil { t.Fatal(err) }
	for _, target := range []Target{{GroupID: "foreign"}, {GroupID: "g", Members: []string{"member"}}, {GroupID: "g", AllMembers: true}, {GroupID: "g", Create: true}} {
		if _, err := commandForMutation(ctx, []Target{target}); err == nil { t.Fatal("invalid command scope accepted", target.GroupID) }
	}
	if _, err := commandForMutation(ctx, []Target{{GroupID: "g"}}); err != nil { t.Fatal(err) }
}

func TestNoticeCommandRejectsMissingEpochActorOrUnknownAction(t *testing.T) {
	for _, value := range []struct { actor, epoch, group string; action NoticeAction }{
		{"", uuid.NewString(), "g", NoticeMuteGroup},
		{"owner", "", "g", NoticeMuteGroup},
		{"owner", uuid.Nil.String(), "g", NoticeMuteGroup},
		{"owner", uuid.NewString(), "", NoticeMuteGroup},
		{"owner", uuid.NewString(), "g", NoticeAction("arbitrary-broadcast")},
	} {
		if _, _, err := NewNoticeCommand(noticeTestActor(value.actor), value.epoch, value.action, value.group); err == nil { t.Fatal("invalid typed intent accepted") }
	}
}
