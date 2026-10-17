package messagedelivery

import (
	"errors"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// This is the QUERY-bound decision only. It supplies neither a permission nor
// a fake source/receipt/closure. All four production callers must first verify
// the original finite endpoint's admission/generation and positive receipt.
// Full old signed-publication/Mongo composition remains separately pending.
func TestGroupHistoryBoundKeepsVerifiedFiniteLegacyProgressWithoutExtendingLeaveGap(t *testing.T) {
	epoch := "41111111-1111-4111-8111-111111111111"
	command := "51111111-1111-4111-8111-111111111111"
	nextCommand := "61111111-1111-4111-8111-111111111111"
	d, e := DescribeNamespace(&sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", SessionType: constant.GroupChatType})
	if e != nil {
		t.Fatal(e)
	}
	unavailable := errors.New("fixed unavailable reader")
	for _, test := range []struct {
		name, mode                string
		finite, lower, head, want int64
		reject                    bool
	}{
		{"same-generation-retains-modern-head", "same", 5, 1, 9, 9, false},
		{"ended-legacy-keeps-only-old-finite-range", "closed", 5, 1, 9, 5, false},
		{"rejoined-generation-does-not-fill-gap", "changed", 5, 1, 9, 5, false},
		{"new-static-row-still-follows-same-generation", "same", 0, 1, 9, 9, false},
		{"static-row-needs-original-closure", "closed", 0, 1, 9, 0, true},
		{"missing-window-cannot-authorize-finite-row", "missing", 5, 1, 9, 0, true},
		{"malformed-window-cannot-fallback", "malformed", 5, 1, 9, 0, true},
		{"foreign-window-cannot-fallback", "foreign", 5, 1, 9, 0, true},
		{"existing-bad-closure-cannot-fallback", "bad-closure", 5, 1, 9, 0, true},
		{"finite-endpoint-cannot-exceed-real-head", "same", 10, 1, 9, 0, true},
		{"finite-endpoint-cannot-precede-original-lower", "changed", 1, 2, 9, 0, true},
		{"closed-real-head-cannot-precede-finite-endpoint", "closed-before", 5, 1, 9, 0, true},
		{"read-failure-cannot-fallback", "unavailable", 5, 1, 9, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			window := MembershipWindow{ID: key(epoch, "membership-window", d.CID+"\x00owner"), Kind: "membership-window-v1", Epoch: epoch, ConversationID: d.CID, GroupID: d.GroupID, UserID: "owner", Revision: 2, GroupRevision: 2, LastCommand: command, SeedEvidenceSHA256: hash([]byte("original typed membership proof"))}
			switch test.mode {
			case "changed":
				window.LastCommand = nextCommand
			case "closed", "closed-before":
				window.LastCommand = nextCommand
				window.Closed = true
				window.Upper = 7
				if test.mode == "closed-before" {
					window.Upper = 4
				}
			case "foreign":
				window.UserID = "foreign"
			}
			read := func(collection, id string) (bson.Raw, error) {
				if collection == Receipts && id == historyClosureID(epoch, d.CID, "owner", command) {
					if test.mode == "bad-closure" {
						return bson.Raw{1}, nil
					}
					return nil, mongo.ErrNoDocuments
				}
				if collection != Sequences || id != window.ID {
					t.Fatal("unexpected original-record lookup", collection, id)
				}
				if test.mode == "missing" {
					return nil, mongo.ErrNoDocuments
				}
				if test.mode == "unavailable" {
					return nil, unavailable
				}
				if test.mode == "malformed" {
					return bson.Raw{1}, nil
				}
				body, e := bson.Marshal(window)
				if e != nil {
					t.Fatal(e)
				}
				return body, nil
			}
			actual, e := groupHistoryBound(epoch, d, "owner", command, test.lower, test.head, false, test.finite, read)
			if test.reject {
				if e == nil {
					t.Fatal("unproved finite endpoint accepted", actual)
				}
				return
			}
			if e != nil || actual != test.want {
				t.Fatal("wrong exact original-generation query bound", actual, e)
			}
		})
	}
}
