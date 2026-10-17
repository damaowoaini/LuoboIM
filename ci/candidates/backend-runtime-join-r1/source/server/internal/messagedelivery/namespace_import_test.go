package messagedelivery

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

func TestImportSetCheckpointMatchesSignedRootsAcrossPageBoundaries(t *testing.T) {
	for _, size := range []int{0, 1, 2, 3, 5, 7, 31, 63, 64, 65, 127, 128, 129, 1025} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			values := make([]string, size)
			for i := range values {
				values[i] = hash([]byte(fmt.Sprintf("owner-%d", i)))
			}
			sort.Strings(values)
			want, err := namespaceimport.SortedSetRoot(values)
			if err != nil {
				t.Fatal(err)
			}
			var got importSetFrontier
			for i, value := range values {
				if err := got.append(value); err != nil {
					t.Fatal(err)
				}
				if (i+1)%64 == 0 {
					body, err := json.Marshal(got)
					if err != nil {
						t.Fatal(err)
					}
					var restored importSetFrontier
					if decodeImportJSON(string(body), &restored) != nil || restored != got || !restored.valid() {
						t.Fatal("bounded checkpoint lost its exact tree")
					}
					got = restored
				}
			}
			root, err := got.root()
			if err != nil || root != want || got.Count != int64(size) {
				t.Fatal("actual staged set differs from signed source root", root, want, err)
			}
		})
	}
}

func TestImportBSONBindingRejectsTypeDriftDuplicatesAndArrayReordering(t *testing.T) {
	encode := func(value any) bson.Raw {
		body, err := bson.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	want := encode(bson.D{{Key: "_id", Value: "immutable"}, {Key: "version", Value: int32(1)}, {Key: "revision", Value: int64(1)}, {Key: "participants", Value: bson.A{"first", "second"}}})
	for _, tc := range []struct {
		name  string
		value bson.D
	}{
		{"schema-int64", bson.D{{Key: "_id", Value: "immutable"}, {Key: "version", Value: int64(1)}, {Key: "revision", Value: int64(1)}, {Key: "participants", Value: bson.A{"first", "second"}}}},
		{"revision-int32", bson.D{{Key: "_id", Value: "immutable"}, {Key: "version", Value: int32(1)}, {Key: "revision", Value: int32(1)}, {Key: "participants", Value: bson.A{"first", "second"}}}},
		{"duplicate", bson.D{{Key: "_id", Value: "immutable"}, {Key: "version", Value: int32(1)}, {Key: "revision", Value: int64(1)}, {Key: "revision", Value: int64(1)}, {Key: "participants", Value: bson.A{"first", "second"}}}},
		{"extra", bson.D{{Key: "_id", Value: "immutable"}, {Key: "version", Value: int32(1)}, {Key: "revision", Value: int64(1)}, {Key: "participants", Value: bson.A{"first", "second"}}, {Key: "ready", Value: true}}},
		{"array-reordered", bson.D{{Key: "_id", Value: "immutable"}, {Key: "version", Value: int32(1)}, {Key: "revision", Value: int64(1)}, {Key: "participants", Value: bson.A{"second", "first"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if equalImportDocuments(encode(tc.value), want) {
				t.Fatal("immutable stored identity drift accepted")
			}
		})
	}
	reordered := encode(bson.D{{Key: "participants", Value: bson.A{"first", "second"}}, {Key: "revision", Value: int64(1)}, {Key: "version", Value: int32(1)}, {Key: "_id", Value: "immutable"}})
	if !equalImportDocuments(reordered, want) {
		t.Fatal("BSON field order became authority")
	}
}

func TestHistoricalNamespaceClaimKeepsCanonicalGroupScope(t *testing.T) {
	const gid = "collision_peer"
	base := historicalNamespaceClaim{Kind: historicalNamespaceKind, OriginKind: "group", GroupID: gid, Epoch: "12345678-1234-4234-9234-123456789012", ActivationSHA256: hash([]byte("activation")), MigrationID: "12345678-1234-4234-9234-123456789013", HeaderSHA256: hash([]byte("header")), InventorySHA256: hash([]byte("inventory")), SeedEvidenceSHA256: hash([]byte("original-heads")), CreatedAt: time.Unix(100, 0).UTC()}
	base.OriginSHA256 = groupImportTestOriginSHA(gid)
	for _, cid := range []string{"g_" + gid, "sg_" + gid, "n_" + gid} {
		t.Run(cid, func(t *testing.T) {
			row := base
			row.ConversationID = cid
			row.ID = namespaceClaimID(cid)
			// Descriptor hashes retain the original typed ID tuple, never a split CID.
			d := NamespaceOriginDescriptor{CID: cid, Kind: "group", GroupID: gid, OriginIDs: []string{gid}}
			row.OriginSHA256 = groupImportTestOriginSHA(gid)
			d.SHA256 = row.OriginSHA256
			if err := validateNamespaceDescriptor(d); err != nil {
				t.Fatal(err)
			}
			encoded, err := bson.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeHistoricalNamespaceClaim(encoded, cid)
			if err != nil || !got.CreatedAt.Equal(row.CreatedAt) {
				t.Fatal("canonical historical claim rejected", err)
			}
			got.CreatedAt = row.CreatedAt
			if got != row {
				t.Fatal("historical claim fields changed")
			}
			for _, name := range []string{"foreign-cid", "foreign-group", "direct-kind-with-group", "foreign-origin"} {
				t.Run(name, func(t *testing.T) {
					bad := row
					switch name {
					case "foreign-cid":
						bad.ConversationID = "n_other"
					case "foreign-group":
						bad.GroupID = "other"
					case "direct-kind-with-group":
						bad.OriginKind = "single"
					case "foreign-origin":
						bad.OriginSHA256 = hash([]byte("foreign"))
					}
					encoded, err := bson.Marshal(bad)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := decodeHistoricalNamespaceClaim(encoded, cid); err == nil {
						t.Fatal("foreign origin adopted")
					}
				})
			}
		})
	}
}

func TestImportSetCheckpointRejectsIncompleteOrInventedFrontier(t *testing.T) {
	for _, name := range []string{"negative-count", "missing-leaf", "extra-leaf", "malformed-leaf"} {
		t.Run(name, func(t *testing.T) {
			var value importSetFrontier
			switch name {
			case "negative-count":
				value.Count = -1
			case "missing-leaf":
				value.Count = 1
			case "extra-leaf":
				value.Levels[2] = hash([]byte("invented"))
			case "malformed-leaf":
				value.Count = 1
				value.Levels[0] = "bad"
			}
			if value.valid() {
				t.Fatal("broken checkpoint became a new empty frontier")
			}
			if _, err := value.root(); err == nil {
				t.Fatal("broken checkpoint published a root")
			}
			if err := value.append(hash([]byte("next"))); err == nil {
				t.Fatal("broken checkpoint resumed")
			}
		})
	}
}

func groupImportTestOriginSHA(gid string) string {
	// Same canonical original tuple used by DescribeNamespace; explicit tuple
	// keeps the fixture independent of imported claim serialization.
	return hash([]byte(fmt.Sprintf("[\"group\",[%q]]", gid)))
}

func TestImportPublicPortsRejectUnboundWriterWithoutIO(t *testing.T) {
	ctx := context.Background()
	var writer *NamespaceImportWriter
	if writer.CheckTarget(ctx, namespaceimport.Header{}) == nil {
		t.Fatal("nil target accepted")
	}
	if writer.PrepareHeldMetadata(ctx, nil, namespaceimport.Header{}) == nil {
		t.Fatal("nil owned hold accepted")
	}
	if _, err := writer.ApplyPage(ctx, nil, namespaceimport.VerifiedPage{}); err == nil {
		t.Fatal("unminted page accepted")
	}
	if _, err := writer.InspectPage(ctx, namespaceimport.Header{}, namespaceimport.PageIdentity{}); err == nil {
		t.Fatal("nil inspection target accepted")
	}
	if _, err := writer.PublishCutover(ctx, nil, namespaceimport.VerifiedManifest{}); err == nil {
		t.Fatal("unminted manifest accepted")
	}
	if _, err := writer.InspectCutover(ctx, namespaceimport.Header{}); err == nil {
		t.Fatal("nil cutover inspection target accepted")
	}
}
