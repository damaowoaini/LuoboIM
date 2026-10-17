package groupcleanup

import (
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

func noticeFixture(t *testing.T, kind int32) groupeffects.Frozen {
	t.Helper()
	message := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", SessionType: constant.SuperGroupChatType, MsgFrom: constant.SysMsgType, ContentType: kind, Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(false), msgprocessor.WithHistory(true), msgprocessor.WithPersistent())}
	f, err := groupeffects.NewNotification(uuid.NewString(), 0, "g", "owner", message, time.Now().UTC().Truncate(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func TestDismissIntentIsStableAndRequiresActualFrozenDismissNotice(t *testing.T) {
	frozen := noticeFixture(t, constant.GroupDismissedNotification)
	first, err := IntentFromDismissNotification(frozen)
	if err != nil || !first.valid() {
		t.Fatal("valid terminal notice rejected", err)
	}
	second, err := IntentFromDismissNotification(frozen)
	if err != nil || first != second {
		t.Fatal("same source retry invented a new cleanup ID", err)
	}
	if _, err := IntentFromDismissNotification(noticeFixture(t, constant.GroupMutedNotification)); err == nil {
		t.Fatal("mute was admitted as terminal cleanup")
	}
	changed := frozen
	changed.GroupID = "foreign"
	if _, err := IntentFromDismissNotification(changed); err == nil {
		t.Fatal("foreign metadata borrowed a dismissal proof")
	}
	changed = frozen
	changed.Payload = append([]byte(nil), frozen.Payload...)
	changed.Payload[0] ^= 1
	if _, err := IntentFromDismissNotification(changed); err == nil {
		t.Fatal("changed frozen payload was accepted")
	}
}
func TestDismissMinimalReceiptBindsOriginalRosterButDoesNotRetainIt(t *testing.T) {
	intent, err := IntentFromDismissNotification(noticeFixture(t, constant.GroupDismissedNotification))
	if err != nil {
		t.Fatal(err)
	}
	input := []string{"z", "a"}
	members, memberSHA, err := canonicalMembers(input)
	if err != nil || input[0] != "z" || members[0] != "a" {
		t.Fatal("canonicalization borrowed or changed caller roster", err)
	}
	epoch := uuid.NewString()
	row := obligation{ID: intent.ID, Epoch: epoch, Intent: intent, InputSHA: digest([]byte("synthetic accepted envelope")), Branches: []branchProof{{ID: uuid.NewString(), CID: "n_g"}}, Members: members, MemberSHA: memberSHA, MemberCount: 2, State: "pending", Revision: 1}
	row.FrozenSHA = row.proof()
	if !row.valid(epoch) {
		t.Fatal("prepared roster proof rejected")
	}
	changed := row
	changed.Members = []string{"foreign", "z"}
	if changed.valid(epoch) {
		t.Fatal("changed pending roster passed original proof")
	}
	changed = row
	changed.Branches = []branchProof{{ID: uuid.NewString(), CID: "n_foreign"}}
	changed.FrozenSHA = changed.proof()
	if changed.valid(epoch) {
		t.Fatal("same receipt expanded to a foreign group")
	}
	row.State = "done"
	row.Revision = 2
	row.Members = nil
	row.CompletedAt = time.Now().UTC().Truncate(time.Millisecond)
	if !row.valid(epoch) || row.MemberCount != 2 || row.MemberSHA != memberSHA {
		t.Fatal("minimal receipt lost original roster binding")
	}
	row.Members = []string{"a"}
	if row.valid(epoch) {
		t.Fatal("completed receipt retained a private roster")
	}
	for _, bad := range [][]string{{"a", "a"}, {""}, {" a"}, {"a\x00foreign"}} {
		if _, _, err := canonicalMembers(bad); err == nil {
			t.Fatal("ambiguous roster admitted")
		}
	}
}
