//go:build linux

package ownedcontrol

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
)

func factualDescribe(message *sdkws.MsgData) (namespaceimport.NamespaceOriginDescriptor, error) {
	ids := []string{message.SendID, message.RecvID}
	kind, gid := "single", ""
	switch message.SessionType {
	case constant.SuperGroupChatType, constant.GroupChatType:
		kind = "group"
		gid = message.GroupID
		ids = []string{gid}
	case constant.NotificationChatType:
		kind = "server-notification"
	case constant.SingleChatType:
		if ids[0] > ids[1] {
			ids[0], ids[1] = ids[1], ids[0]
		}
	default:
		return namespaceimport.NamespaceOriginDescriptor{}, ErrOwnedEvidence
	}
	return namespaceimport.NamespaceOriginDescriptor{CID: msgprocessor.GetConversationIDByMsg(message), Kind: kind, GroupID: gid, OriginIDs: ids, SHA256: historicalJSONSHA([]any{kind, ids})}, nil
}
func rawHistorical(t *testing.T, value any) bson.Raw {
	t.Helper()
	b, e := bson.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestHistoricalTypedLineagePreservesOriginalClaimsAndProtocol(t *testing.T) {
	now := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	epoch := "12345678-1234-1234-1234-123456789001"
	a, b := "sender_with_under", "receiver"
	d, e := factualDescribe(&sdkws.MsgData{SendID: a, RecvID: b, SessionType: constant.NotificationChatType, Options: map[string]bool{constant.IsNotNotification: false}})
	if e != nil {
		t.Fatal(e)
	}
	original := originalDirectClaim{ID: "permanent-cid:" + sha([]byte(d.CID)), Kind: "permanent-message-origin-v1", ConversationID: d.CID, OriginKind: d.Kind, OriginSHA256: d.SHA256, Epoch: epoch, ActivationSHA256: historicalJSONSHA("activation"), SeedEvidenceSHA256: historicalJSONSHA("seed"), CreatedAt: now}
	t.Run("exact typed canonical and original ordered direct IDs", func(t *testing.T) {
		got, e := decodeHistoricalClaim(rawHistorical(t, original), factualDescribe)
		body, _ := json.Marshal(original)
		if e != nil || got.CanonicalSHA256 != sha(body) || got.OriginSHA256 != d.SHA256 {
			t.Fatal("original typed identity lost", e)
		}
		tuple := bson.D{{Key: "_id", Value: "permanent-origin-principals:" + sha([]byte(d.CID))}, {Key: "kind", Value: "permanent-origin-principals-v1"}, {Key: "conversation_id", Value: d.CID}, {Key: "origin_kind", Value: d.Kind}, {Key: "origin_sha256", Value: d.SHA256}, {Key: "origin_ids", Value: bson.A{a, b}}, {Key: "claim_sha256", Value: got.CanonicalSHA256}, {Key: "created_epoch", Value: epoch}, {Key: "created_activation_sha256", Value: original.ActivationSHA256}, {Key: "seed_evidence_sha256", Value: original.SeedEvidenceSHA256}, {Key: "created_at", Value: now}}
		principals, e := decodeHistoricalPrincipals(rawHistorical(t, tuple), factualDescribe)
		if e != nil || principals.Origin.OriginIDs[0] != a || principals.ClaimSHA256 != got.CanonicalSHA256 {
			t.Fatal("ordered direct identity changed", e)
		}
		tuple[5].Value = bson.A{b, a}
		if _, e = decodeHistoricalPrincipals(rawHistorical(t, tuple), factualDescribe); e == nil {
			t.Fatal("permuted ordered participants reused original CID")
		}
	})
	t.Run("real sg prefix in GID does not concatenate notification CID", func(t *testing.T) {
		gid := "sg_team_with_under"
		cid := msgprocessor.GetNotificationConversationID(constant.SuperGroupChatType, gid)
		got, e := historicalDescriptor(cid, "group", gid, []string{gid}, factualDescribe)
		if e != nil || got.CID != "n_"+gid {
			t.Fatal(got, e)
		}
		if _, e = historicalDescriptor("n_sg_"+gid, "group", gid, []string{gid}, factualDescribe); e == nil {
			t.Fatal("wrong pair accepted")
		}
	})
	t.Run("fresh group requires exact original reservation identity", func(t *testing.T) {
		gid := "group"
		normal := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, gid)
		claim := originalFreshClaim{ID: "permanent-cid:" + sha([]byte(normal)), Kind: "permanent-new-group-namespace-v1", ConversationID: normal, GroupID: gid, Branch: "normal", Epoch: epoch, ActivationSHA256: historicalJSONSHA("act"), CommandID: "12345678-1234-1234-1234-123456789002", InputSHA256: historicalJSONSHA("input"), CutoverSHA256: historicalJSONSHA("cut"), CreatedAt: now}
		got, e := decodeHistoricalClaim(rawHistorical(t, claim), factualDescribe)
		claim.ID = "permanent-group-origin:" + sha([]byte(gid))
		claim.ConversationID = ""
		claim.Branch = "group-origin"
		reservation, e2 := decodeHistoricalClaim(rawHistorical(t, claim), factualDescribe)
		if e != nil || e2 != nil || got.RequiredGroupReservationSHA256 != reservation.CanonicalSHA256 {
			t.Fatal("real reservation relation lost", e, e2)
		}
	})
	t.Run("duplicate and unknown BSON claim fields rejected", func(t *testing.T) {
		var row bson.D
		if bson.Unmarshal(rawHistorical(t, original), &row) != nil {
			t.Fatal("fixture")
		}
		duplicate := append(append(bson.D(nil), row...), bson.E{Key: "origin_kind", Value: original.OriginKind})
		unknown := append(append(bson.D(nil), row...), bson.E{Key: "authority", Value: true})
		for _, bad := range []bson.D{duplicate, unknown} {
			if _, e := decodeHistoricalClaim(rawHistorical(t, bad), factualDescribe); e == nil {
				t.Fatal("unknown/duplicate claim widened")
			}
		}
	})
}
func TestHistoricalOpenBaseIsClosedAtCutWithoutFillingFutureGap(t *testing.T) {
	ref := namespaceimport.SourceRef{ObjectSHA256: historicalJSONSHA("object"), RecordSHA256: historicalJSONSHA("record"), Kind: "original-row"}
	base := namespaceimport.OwnerWindow{CID: "sg_group", GroupID: "group", OwnerID: "owner", OriginSHA256: historicalJSONSHA("origin"), Lower: 2, Upper: 5, Closed: true, Evidence: []namespaceimport.SourceRef{ref}}
	cases := []struct {
		name                                        string
		window                                      namespaceimport.OwnerWindow
		head, ownerFloor, globalFloor, lower, upper int64
		bad                                         bool
	}{
		{"proved old2to5 stays separate from future11to14", base, 10, 0, 0, 2, 5, false},
		{"original open freezes at actual head", func() namespaceimport.OwnerWindow { x := base; x.Closed = false; x.Upper = 0; return x }(), 10, 0, 0, 2, 10, false},
		{"zero head is explicitly closed empty", func() namespaceimport.OwnerWindow { x := base; x.Closed = false; x.Lower = 0; x.Upper = 0; return x }(), 0, 0, 0, 1, 0, false},
		{"forbid or Clear floor does not open old gap", base, 10, 15, 0, 15, 5, false},
		{"global floor clips only proved interval", base, 10, 0, 4, 4, 5, false},
		{"upper beyond real head rejected", base, 4, 0, 0, 0, 0, true},
		{"negative head rejected", base, -1, 0, 0, 0, 0, true},
		{"head overflow boundary rejected", base, 9223372036854775807, 0, 0, 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := historicalJSONSHA(c.window)
			got, e := freezeHistoricalBase(c.window, c.head, c.ownerFloor, c.globalFloor)
			if c.bad {
				if e == nil {
					t.Fatal("invalid source boundary accepted")
				}
				return
			}
			if e != nil || !got.Closed || got.Lower != c.lower || got.Upper != c.upper || historicalJSONSHA(c.window) != before {
				t.Fatal("base range or original raw changed", got, e)
			}
			for _, seq := range []int64{6, 7, 8, 9, 10} {
				if c.name == "proved old2to5 stays separate from future11to14" && seq >= got.Lower && seq <= got.Upper {
					t.Fatal("leave gap granted")
				}
			}
		})
	}
}
