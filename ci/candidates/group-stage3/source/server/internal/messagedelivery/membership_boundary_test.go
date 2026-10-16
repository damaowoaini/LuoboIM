package messagedelivery

import (
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func boundaryWindowFixture() MembershipWindow {
	epoch := "c2fb972f-c1ae-4d34-b937-2ec837d7529d"
	return MembershipWindow{ID: key(epoch, "membership-window", "sg_g\x00member"), Kind: "membership-window-v1", Epoch: epoch, ConversationID: "sg_g", GroupID: "g", UserID: "member", Revision: 1, GroupRevision: 1, SeedEvidenceSHA256: hash([]byte("synthetic owner bound seed"))}
}

func TestMembershipWindowKeepsClosedZeroDistinctFromLegacyUnbounded(t *testing.T) {
	window := boundaryWindowFixture()
	if !window.AllowsSequence(1, 0) || window.AllowsSequence(1, 2) || (MembershipWindow{}).AllowsSequence(1, 0) {
		t.Fatal("invalid/missing window became open or lower floor was lost")
	}
	window.Closed, window.LastCommand = true, "86966e89-8467-4ff0-89b1-870e9b391f53"
	if window.AllowsSequence(1, 0) { t.Fatal("closed empty conversation reopened through upper zero") }
	window.Upper = 2
	if !window.AllowsSequence(2, 2) || window.AllowsSequence(3, 0) || window.AllowsSequence(1, 2) { t.Fatal("upper/lower intersection changed") }
	window.ConversationID = "n_g"
	if window.AllowsSequence(1, 0) { t.Fatal("sg_ window was borrowed for removed-self n_ notification") }
}

func TestMembershipRecordsRejectMissingBooleanAndNumericTypeCoercion(t *testing.T) {
	window := boundaryWindowFixture()
	body, err := bson.Marshal(window)
	if err != nil { t.Fatal(err) }
	var fields bson.M
	if err := bson.Unmarshal(body, &fields); err != nil { t.Fatal(err) }
	delete(fields, "closed")
	body, err = bson.Marshal(fields)
	if err != nil { t.Fatal(err) }
	if exactFields(body, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"upper", "revision", "group_revision"}, []string{"closed"}, "") { t.Fatal("missing closed field was interpreted as open") }
	r := MemberRemovalReceipt{ID: hash([]byte("receipt")), Kind: "membership-removal-v1", Epoch: window.Epoch, CommandID: "86966e89-8467-4ff0-89b1-870e9b391f53", InputSHA256: hash([]byte("input")), GroupID: "g", ConversationID: "sg_g", GroupRevision: 2, Upper: 0, CounterRevision: 2, MemberCount: 1, CreatedAt: time.Unix(1, 0).UTC()}
	body, err = bson.Marshal(r)
	if err != nil { t.Fatal(err) }
	if _, err := decodeRemovalReceipt(body); err != nil { t.Fatal("strict valid record rejected", err) }
	fields = nil
	if err := bson.Unmarshal(body, &fields); err != nil { t.Fatal(err) }
	fields["upper"] = float64(0)
	body, err = bson.Marshal(fields)
	if err != nil { t.Fatal(err) }
	if _, err := decodeRemovalReceipt(body); err == nil { t.Fatal("non-int64 sequence was accepted by coercion") }
}
