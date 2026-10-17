package messagedelivery

import (
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"go.mongodb.org/mongo-driver/bson"
)

func noticeTestStoreAndHeader() (*Store, memberNoticeHeader, MemberRemovalReceipt) {
	epoch := "11111111-1111-4111-8111-111111111111"
	command := "22222222-2222-4222-8222-222222222222"
	effect := "33333333-3333-4333-8333-333333333333"
	s := &Store{epoch: epoch}
	at := time.Unix(10, 0).UTC()
	info := groupauthority.MemberRemovalInfo{Command: groupauthority.NoticeCommandInfo{ID: command, Epoch: epoch, Actor: "actor", GroupID: "g", Action: groupauthority.NoticeKick, CreatedAt: at}, GroupRevision: 2, Members: []string{"member"}}
	h := memberNoticeHeader{ID: key(epoch, "member-notice-header", effect), Kind: "member-notice-header-v1", Epoch: epoch, EffectID: effect, CommandID: command, GroupID: "g", Actor: "actor", Action: "kick", RemovalInputSHA256: canonicalRecordSHA(info), SourcePayloadSHA256: hash([]byte("source")), ProfileSHA256: hash([]byte("profile")), TargetsSHA256: hash([]byte("targets")), GroupRevision: 2, Owners: []string{"member"}, CreatedAt: at}
	r := MemberRemovalReceipt{ID: key(epoch, "membership-removal", command), Kind: "membership-removal-pair-v2", Epoch: epoch, CommandID: command, InputSHA256: canonicalRecordSHA(info), GroupID: "g", ConversationID: "sg_g", NotificationCID: "n_g", GroupRevision: 2, Upper: 4, NotificationUpper: 7, CounterRevision: 2, NotificationCounterRevision: 3, MemberCount: 1, CreatedAt: at}
	return s, h, r
}

func TestMemberNoticePrivateCommandKeepsIndependentNamespaceHeads(t *testing.T) {
	s, header, removal := noticeTestStoreAndHeader()
	if err := s.noticeHeaderMatchesRemoval(header, removal); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"different-owner", "different-actor", "different-command", "different-action", "different-revision", "legacy-single-bound", "wrong-notification-domain"} {
		t.Run(name, func(t *testing.T) {
			h, r := header, removal
			h.Owners = append([]string(nil), header.Owners...)
			switch name {
			case "different-owner":
				h.Owners[0] = "foreign"
			case "different-actor":
				h.Actor = "foreign"
			case "different-command":
				h.CommandID = "44444444-4444-4444-8444-444444444444"
			case "different-action":
				h.Action = "quit"
			case "different-revision":
				h.GroupRevision++
			case "legacy-single-bound":
				r.Kind = "membership-removal-v1"
			case "wrong-notification-domain":
				r.NotificationCID = r.ConversationID
			}
			if s.noticeHeaderMatchesRemoval(h, r) == nil {
				t.Fatal("different original business authority accepted")
			}
		})
	}
}

func TestMemberNoticeStrictSourceReceiptRejectsAmbiguousStoredShape(t *testing.T) {
	s, _, _ := noticeTestStoreAndHeader()
	source := "33333333-3333-4333-8333-333333333333"
	branch := "44444444-4444-4444-8444-444444444444"
	r := Receipt{ID: key(s.epoch, "source", source), Epoch: s.epoch, SourceID: source, InputSHA256: hash([]byte("envelope")), AcceptedAt: time.Unix(12, 0).UTC(), Branches: []AcceptedBranch{{ID: branch, ConversationID: "n_g", Seq: 8, Persist: true}}}
	raw, err := bson.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := s.decodeNoticeSourceReceipt(raw)
	if err != nil || canonicalRecordSHA(decoded) != canonicalRecordSHA(r) {
		t.Fatal("valid receipt changed", err)
	}
	for _, name := range []string{"top-duplicate", "top-unknown", "branch-duplicate", "branch-unknown", "seq-int32", "Seq0-history", "positive-ephemeral"} {
		t.Run(name, func(t *testing.T) {
			var doc bson.D
			if err := bson.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			nested := bson.D{{Key: "id", Value: branch}, {Key: "conversation_id", Value: "n_g"}, {Key: "seq", Value: int64(8)}, {Key: "persist", Value: true}, {Key: "hidden", Value: false}}
			switch name {
			case "top-duplicate":
				doc = append(doc, bson.E{Key: "_id", Value: r.ID})
			case "top-unknown":
				doc = append(doc, bson.E{Key: "unexpected", Value: true})
			case "branch-duplicate":
				nested = append(nested, bson.E{Key: "seq", Value: int64(9)})
			case "branch-unknown":
				nested = append(nested, bson.E{Key: "unexpected", Value: true})
			case "seq-int32":
				nested[2].Value = int32(8)
			case "Seq0-history":
				nested[2].Value = int64(0)
			case "positive-ephemeral":
				nested[3].Value = false
			}
			if name != "top-duplicate" && name != "top-unknown" {
				for i := range doc {
					if doc[i].Key == "branches" {
						doc[i].Value = bson.A{nested}
					}
				}
			}
			bad, err := bson.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.decodeNoticeSourceReceipt(bad); err == nil {
				t.Fatal("ambiguous or non-history receipt accepted")
			}
		})
	}
}

func TestMemberNoticeHeaderArrayAndHeadCannotBecomeIntervalGrant(t *testing.T) {
	s, h, _ := noticeTestStoreAndHeader()
	raw, err := bson.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decodeNoticeHeader(raw); err != nil {
		t.Fatal("valid typed owner header rejected", err)
	}
	for _, name := range []string{"duplicate-field", "duplicate-owner", "non-string-owner", "foreign-kind"} {
		t.Run(name, func(t *testing.T) {
			var d bson.D
			if err := bson.Unmarshal(raw, &d); err != nil {
				t.Fatal(err)
			}
			if name == "duplicate-field" {
				d = append(d, bson.E{Key: "owners", Value: bson.A{"member"}})
			}
			for i := range d {
				if d[i].Key == "owners" {
					if name == "duplicate-owner" {
						d[i].Value = bson.A{"member", "member"}
					}
					if name == "non-string-owner" {
						d[i].Value = bson.A{int64(1)}
					}
				}
				if d[i].Key == "kind" && name == "foreign-kind" {
					d[i].Value = "foreign"
				}
			}
			bad, err := bson.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.decodeNoticeHeader(bad); err == nil {
				t.Fatal("ambiguous owner list accepted")
			}
		})
	}
	e := memberNoticeEntitlement{ID: noticeEntitlementID(s.epoch, "n_g", "member", 100), Kind: "member-notice-entitlement-v1", Epoch: s.epoch, UserID: "member", ConversationID: "n_g", PendingID: hash([]byte("pending")), PendingSHA256: hash([]byte("pending-body")), ReceiptID: hash([]byte("receipt")), ReceiptSHA256: hash([]byte("receipt-body")), Seq: 100}
	b, err := bson.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decodeNoticeEntitlement(b); err != nil {
		t.Fatal(err)
	}
	e.Seq = 99 // An entitlement for 100 is not an upper-bound grant through 100.
	b, err = bson.Marshal(e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.decodeNoticeEntitlement(b); err == nil {
		t.Fatal("sequence identity changed without changing the exact entitlement")
	}
}
