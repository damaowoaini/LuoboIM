package deliveryprofile

import (
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"math"
	"strings"
	"testing"
)

func TestOwnedCallbackDefaultTimeoutKeepsSignedBinding(t *testing.T) {
	for _, timeout := range []int{-1, 0, 1, 15, 20} {
		c := Callback{Binding: strings.Repeat("a", 64), Mode: "default-stop", TimeoutSeconds: timeout}
		if !c.Valid() {
			t.Fatal("legacy signed callback timeout contract rejected", timeout)
		}
	}
	if (Callback{Mode: "continue"}).Valid() || (Callback{TimeoutSeconds: 1}).Valid() || (Callback{Binding: "unknown", Mode: "continue"}).Valid() {
		t.Fatal("disabled or unknown callback gained a binding")
	}
}
func TestOwnedCutFrozenHeadShapeCannotOverflow(t *testing.T) {
	base := VisibilityCut{Version: "owned-visibility-cut-v2", ConversationID: "sg_g", Owners: map[string]int64{}, Members: map[string]string{}, Notification: &PrivacyScopeCut{ConversationID: "n_g", Owners: map[string]int64{}}}
	if !base.Valid() {
		t.Fatal("known empty cut shape rejected")
	}
	raw, err := bson.Marshal(base)
	var decoded VisibilityCut
	if err != nil || bson.Unmarshal(raw, &decoded) != nil {
		t.Fatal("complete persisted cut cannot be read")
	}
	var fields bson.M
	if bson.Unmarshal(raw, &fields) != nil {
		t.Fatal("cut fixture unavailable")
	}
	delete(fields, "source_head")
	incomplete, _ := bson.Marshal(fields)
	if bson.Unmarshal(incomplete, &decoded) == nil {
		t.Fatal("missing original head silently decoded as zero")
	}

	for _, head := range []int64{-1, math.MaxInt64 - 1, math.MaxInt64} {
		copy := base
		copy.SourceHead = head
		if copy.Valid() {
			t.Fatal("unsafe head admitted", head)
		}
	}
	copy := base
	n := *base.Notification
	n.SourceHead = -1
	copy.Notification = &n
	if copy.Valid() {
		t.Fatal("negative n_ head admitted")
	}
}

// Compare the cut with the real OpenIM message/CID functions, not another
// fixture-specific concatenation. Correct persisted cuts round-trip; a wrong
// pair remains invalid and is never rewritten into a fresh authorization cut.
func TestOwnedCutNotificationCIDMatchesOpenIMProtocol(t *testing.T) {
	for _, groupID := range []string{"g", "group_with_underscores", "sg_g"} {
		message := &sdkws.MsgData{SessionType: constant.SuperGroupChatType, GroupID: groupID}
		normal := msgprocessor.GetChatConversationIDByMsg(message)
		notification := msgprocessor.GetNotificationConversationIDByMsg(message)
		mapped, err := ownedNotificationCID(normal)
		if err != nil || mapped != notification || mapped != msgprocessor.GetNotificationConversationIDByConversationID(normal) {
			t.Fatal("owned notification mapping differs from OpenIM protocol", groupID, err)
		}
		base := VisibilityCut{Version: "owned-visibility-cut-v2", ConversationID: normal, Owners: map[string]int64{}, Members: map[string]string{}, Notification: &PrivacyScopeCut{ConversationID: notification, Owners: map[string]int64{}}}
		if !base.Valid() {
			t.Fatal("canonical group notification cut rejected", groupID)
		}
		raw, err := bson.Marshal(base)
		var restored VisibilityCut
		if err != nil || bson.Unmarshal(raw, &restored) != nil || restored.Notification.ConversationID != notification {
			t.Fatal("canonical persisted notification cut lost wire CID", groupID, err)
		}
		for _, wrong := range []string{"n_" + normal, "n_foreign"} {
			bad := base
			copy := *base.Notification
			copy.ConversationID = wrong
			bad.Notification = &copy
			if bad.Valid() {
				t.Fatal("wrong normal/notification pair acquired a cut", groupID)
			}
			badRaw, err := bson.Marshal(bad)
			originalSHA := SHA(badRaw)
			if err != nil || bson.Unmarshal(badRaw, &restored) == nil || SHA(badRaw) != originalSHA {
				t.Fatal("unknown persisted mapping was accepted or rewritten", groupID, err)
			}
		}
	}
	for _, normal := range []string{"", "sg_", "n_g", "si_a_b", "sg_g\x00foreign", "sg_g\n", "sg_ g"} {
		if _, err := ownedNotificationCID(normal); err == nil {
			t.Fatal("noncanonical normal group CID admitted")
		}
	}
}
