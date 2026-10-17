//go:build linux

package ownedcontrol

import (
	"strings"
	"testing"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"go.mongodb.org/mongo-driver/bson"
)

func TestHistoricalSegmentLocatorsUseActualCollectionAndNeverInferEpochOrRange(t *testing.T) {
	ref := namespaceimport.SourceRef{ObjectSHA256: strings.Repeat("a", 64), RecordSHA256: strings.Repeat("b", 64), Kind: "original-private-row", Position: 1}
	epoch := "11111111-1111-4111-8111-111111111111"
	kinds := []string{"namespace-owner-prospective-v1", "namespace-owner-live-history-v1", "namespace-owner-history-admission-v1", "namespace-owner-imported-history-v1", "member-notice-entitlement-v1", "namespace-import-boundary-v1"}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			collection := "message_delivery_sequences_v1"
			if kind == "member-notice-entitlement-v1" || kind == "namespace-import-boundary-v1" {
				collection = "message_delivery_receipts_v1"
			}
			raw := bson.D{{Key: "_id", Value: "original_" + kind}, {Key: "kind", Value: kind}, {Key: "conversation_id", Value: "n_g_with_underscore"}, {Key: "user_id", Value: "owner_with_underscore"}, {Key: "lower", Value: int64(0)}, {Key: "upper", Value: int64(9223372036854775807)}}
			if kind == "namespace-import-boundary-v1" {
				raw = append(raw, bson.E{Key: "header_sha256", Value: strings.Repeat("c", 64)})
			} else {
				raw = append(raw, bson.E{Key: "epoch", Value: epoch})
			}
			body, e := bson.Marshal(raw)
			if e != nil {
				t.Fatal(e)
			}
			facts, e := decodeHistoricalMongo(namespaceinventory.MongoRow{Collection: collection, Raw: body}, ref, nil)
			if e != nil {
				t.Fatal(e)
			}
			sources := 0
			for _, fact := range facts {
				if fact.Kind != "original-history-segment-source" {
					continue
				}
				sources++
				if fact.SegmentSource == nil || fact.SegmentSource.Kind != kind || fact.SegmentSource.ID != "original_"+kind || fact.CID != "n_g_with_underscore" || fact.OwnerID != "owner_with_underscore" || fact.Source != ref || fact.Window != nil || fact.Head != nil || fact.Origin != nil || fact.Role != nil {
					t.Fatal("typed locator became a range/origin grant")
				}
				if kind == "namespace-import-boundary-v1" {
					if fact.SegmentSource.Epoch != "" || fact.SegmentSource.HeaderSHA256 != strings.Repeat("c", 64) {
						t.Fatal("boundary epoch guessed")
					}
				} else if fact.SegmentSource.Epoch != epoch {
					t.Fatal("raw original epoch replaced")
				}
			}
			if sources != 1 {
				t.Fatal("actual source kind not observed exactly once", sources)
			}
		})
	}
	t.Run("boundary rejects a forged extra epoch", func(t *testing.T) {
		raw, _ := bson.Marshal(bson.M{"_id": "boundary", "kind": "namespace-import-boundary-v1", "header_sha256": strings.Repeat("c", 64), "epoch": epoch, "conversation_id": "n_g", "user_id": "u"})
		if _, e := decodeHistoricalSegmentLocator(raw, ref); e == nil {
			t.Fatal("boundary accepted an invented epoch")
		}
	})
	t.Run("missing epoch is not renewed from the target", func(t *testing.T) {
		raw, _ := bson.Marshal(bson.M{"_id": "history", "kind": "namespace-owner-live-history-v1", "conversation_id": "n_g", "user_id": "u"})
		facts, e := decodeHistoricalMongo(namespaceinventory.MongoRow{Collection: "message_delivery_sequences_v1", Raw: raw}, ref, nil)
		if e != nil || len(facts) != 1 || facts[0].Kind != "unresolved-source-row" || facts[0].CID != "n_g" {
			t.Fatal("invalid source lost its actual CID or became authority", e)
		}
	})
	t.Run("history revocation holds the old base without a numeric grant", func(t *testing.T) {
		raw, _ := bson.Marshal(bson.M{"_id": "floor", "kind": "namespace-owner-historical-floor-v1", "epoch": epoch, "conversation_id": "n_g", "user_id": "u", "floor": int64(15)})
		facts, e := decodeHistoricalMongo(namespaceinventory.MongoRow{Collection: "message_delivery_sequences_v1", Raw: raw}, ref, nil)
		if e != nil || len(facts) != 1 || facts[0].Kind != "original-prospective-history-needs-lineage" || facts[0].Window != nil || facts[0].Floor != nil {
			t.Fatal("unverified revocation turned into a bare floor/grant", e)
		}
	})
}
