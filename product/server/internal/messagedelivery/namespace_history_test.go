package messagedelivery

import (
	"context"
	"math"
	"testing"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
)

func TestNamespaceHistoryKeepsAmbiguousGroupAndDirectAuthoritySeparate(t *testing.T) {
	tip := msgprocessor.WithOptions(msgprocessor.NewMsgOptions(), msgprocessor.WithNotNotification(false))
	g, err := DescribeNamespace(&sdkws.MsgData{SendID: "sender", RecvID: "collision_peer", GroupID: "collision_peer", SessionType: constant.SuperGroupChatType, Options: tip})
	if err != nil {
		t.Fatal(err)
	}
	d, err := DescribeNamespace(&sdkws.MsgData{SendID: "collision", RecvID: "peer", SessionType: constant.SingleChatType, Options: tip})
	if err != nil {
		t.Fatal(err)
	}
	if g.CID != d.CID {
		t.Fatal("fixture must preserve the real protocol collision")
	}
	epoch := "11111111-1111-4111-8111-111111111111"
	command := "22222222-2222-4222-8222-222222222222"
	base := MembershipWindow{ID: key(epoch, "membership-window", g.CID+"\x00member"), Kind: "membership-window-v1", Epoch: epoch, ConversationID: g.CID, GroupID: g.GroupID, UserID: "member", Lower: 3, Closed: true, Upper: 5, Revision: 2, GroupRevision: 2, LastCommand: command, SeedEvidenceSHA256: hash([]byte("actual-source-evidence"))}
	for _, name := range []string{"group-member", "group-is-not-direct-owner", "direct-original-owner", "direct-stranger", "missing-group-window", "foreign-group-window", "inclusive-lower", "inclusive-upper", "after-leave", "closed-empty", "Seq0", "foreign-origin-hash"} {
		t.Run(name, func(t *testing.T) {
			w := base
			p := namespaceHistoryPolicy{origin: g, owner: "member", window: &w}
			seq := int64(4)
			want := true
			switch name {
			case "group-is-not-direct-owner":
				p.origin = d
				p.window = nil
				want = false
			case "direct-original-owner":
				p.origin = d
				p.owner = "collision"
				p.window = nil
				p.direct = &directHistoryPolicy{base: directNamespaceWindow{Kind: "direct-namespace-window-v1", ConversationID: d.CID, UserID: p.owner, OriginSHA256: d.SHA256}, seed: 8, head: 8}
			case "direct-stranger":
				p.origin = d
				p.owner = "other"
				p.window = nil
				want = false
			case "missing-group-window":
				p.window = nil
				want = false
			case "foreign-group-window":
				w.GroupID = "other"
				want = false
			case "inclusive-lower":
				seq = 3
			case "inclusive-upper":
				seq = 5
			case "after-leave":
				seq = 6
				want = false
			case "closed-empty":
				w.Upper = 0
				seq = 1
				want = false
			case "Seq0":
				seq = 0
				want = false
			case "foreign-origin-hash":
				p.origin.SHA256 = hash([]byte("foreign"))
				want = false
			}
			if got := p.allows(seq); got != want {
				t.Fatalf("allows(%d)=%v want %v", seq, got, want)
			}
			if p.allows(0) || p.allows(math.MaxInt64) {
				t.Fatal("non-history sequence granted")
			}
		})
	}
}

func TestNamespaceHistoryExplicitLegacyAndInvalidPageContracts(t *testing.T) {
	var legacy *HistoryWindowReader
	allowed, err := legacy.FilterNamespaceHistorySequences(context.Background(), "legacy", "owner", []int64{1, 9})
	if err != nil || len(allowed) != 2 || !allowed[1] || !allowed[9] {
		t.Fatal("explicit legacy contract changed", allowed, err)
	}
	if closed, upper, err := legacy.ReadNamespaceHistoryUpper(context.Background(), "legacy", "owner"); err != nil || closed || upper != 0 {
		t.Fatal("legacy upper changed", err)
	}
	for _, name := range []string{"zero", "negative", "duplicate", "maximum", "page-overflow", "invalid-required-binding"} {
		t.Run(name, func(t *testing.T) {
			r := legacy
			seqs := []int64{1}
			switch name {
			case "zero":
				seqs = []int64{0}
			case "negative":
				seqs = []int64{-1}
			case "duplicate":
				seqs = []int64{1, 1}
			case "maximum":
				seqs = []int64{math.MaxInt64}
			case "page-overflow":
				seqs = make([]int64, 65)
				for i := range seqs {
					seqs[i] = int64(i + 1)
				}
			case "invalid-required-binding":
				r = &HistoryWindowReader{}
			}
			if _, err := r.FilterNamespaceHistorySequences(context.Background(), "cid", "owner", seqs); err == nil {
				t.Fatal("invalid page/binding accepted")
			}
		})
	}
}
