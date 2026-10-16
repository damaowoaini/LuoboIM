package deliveryprofile

import (
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
	base := VisibilityCut{Version: "owned-visibility-cut-v2", ConversationID: "sg_g", Owners: map[string]int64{}, Members: map[string]string{}, Notification: &PrivacyScopeCut{ConversationID: "n_sg_g", Owners: map[string]int64{}}}
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
