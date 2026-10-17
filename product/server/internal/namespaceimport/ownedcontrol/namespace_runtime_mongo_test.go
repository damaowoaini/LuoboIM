//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/hex"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"os"
	"strings"
	"testing"
)

func namespaceRuntimeCatalogFixture(t *testing.T) []NamespaceRuntimeMongoCollection {
	t.Helper()
	var out []NamespaceRuntimeMongoCollection
	for at, name := range namespaceinventory.OriginalMongoCatalogCollections() {
		id := make([]byte, 16)
		id[15] = byte(at + 1)
		raw, e := bson.Marshal(bson.D{{Key: "name", Value: name}, {Key: "type", Value: "collection"}, {Key: "info", Value: bson.D{{Key: "uuid", Value: primitive.Binary{Subtype: 4, Data: id}}}}})
		if e != nil {
			t.Fatal(e)
		}
		sum := sha(raw)
		out = append(out, NamespaceRuntimeMongoCollection{Name: name, UUID: hex.EncodeToString(id), RawSHA256: sum, Source: namespaceimport.SourceRef{ObjectSHA256: sha([]byte("synthetic-catalog-object-" + name)), RecordSHA256: sum, Kind: "mongo:@catalog:" + sha([]byte(name)), Position: uint64(at)}})
	}
	return out
}
func namespaceRuntimeHelloFixture(t *testing.T, h namespaceimport.Header) bson.Raw {
	t.Helper()
	pid, e := primitive.ObjectIDFromHex(h.MongoProcess)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := bson.Marshal(bson.D{{Key: "isWritablePrimary", Value: true}, {Key: "setName", Value: h.ReplicaSet}, {Key: "topologyVersion", Value: bson.D{{Key: "processId", Value: pid}, {Key: "counter", Value: h.MongoTopology}}}})
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func TestNamespaceRuntimeMongoTargetRequiresOriginalCatalogAndIdentity(t *testing.T) {
	b, _, _ := namespaceRuntimeFixture(t)
	h := b.Header
	catalog := b.MongoCatalog
	t.Run("original-catalog-and-header-are-both-bound", func(t *testing.T) {
		sum, e := NamespaceRuntimeMongoCatalogSHA256(catalog)
		if e != nil || sum != b.MongoCatalogSHA256 || !isSHA(namespaceRuntimeTargetSHA256(h, catalog)) {
			t.Fatal("exact source catalog rejected", e)
		}
		other := h
		other.StorageInstance = "99999999-9999-4999-8999-999999999999"
		if namespaceRuntimeTargetSHA256(h, catalog) == namespaceRuntimeTargetSHA256(other, catalog) {
			t.Fatal("physical storage header was not bound")
		}
	})
	t.Run("foreign-RS-process-topology-storage-and-tenant-refused", func(t *testing.T) {
		raw := namespaceRuntimeHelloFixture(t, h)
		if namespaceRuntimeHello(raw, h) != nil {
			t.Fatal("real hello shape rejected")
		}
		bad := []namespaceimport.Header{h, h, h}
		bad[0].ReplicaSet = "other-rs"
		bad[1].MongoProcess = strings.Repeat("1", 24)
		bad[2].MongoTopology++
		for _, want := range bad {
			if namespaceRuntimeHello(raw, want) == nil {
				t.Fatal("foreign live process accepted")
			}
		}
		marker, _ := bson.Marshal(bson.D{{Key: "_id", Value: "message-visibility-v1"}, {Key: "instance", Value: h.StorageInstance}, {Key: "tenant", Value: h.Tenant}})
		if namespaceRuntimeStorage(marker, h) != nil {
			t.Fatal("storage fixture rejected")
		}
		other := h
		other.Tenant = "another-tenant"
		if namespaceRuntimeStorage(marker, other) == nil {
			t.Fatal("tenant mismatch accepted")
		}
		other = h
		other.StorageInstance = "99999999-9999-4999-8999-999999999999"
		if namespaceRuntimeStorage(marker, other) == nil {
			t.Fatal("storage UUID mismatch accepted")
		}
	})
	t.Run("missing-UUID-view-duplicate-and-source-mismatch-refused", func(t *testing.T) {
		for _, raw := range []bson.D{{{Key: "name", Value: catalog[0].Name}, {Key: "type", Value: "collection"}}, {{Key: "name", Value: catalog[0].Name}, {Key: "type", Value: "view"}}, {{Key: "name", Value: catalog[0].Name}, {Key: "name", Value: catalog[0].Name}}} {
			body, _ := bson.Marshal(raw)
			if _, e := namespaceRuntimeCatalogUUID(body, catalog[0].Name); e == nil {
				t.Fatal("catalog without exact original UUID accepted")
			}
		}
		changed := append([]NamespaceRuntimeMongoCollection(nil), catalog...)
		changed[0].Source.RecordSHA256 = strings.Repeat("e", 64)
		if _, e := NamespaceRuntimeMongoCatalogSHA256(changed); e == nil {
			t.Fatal("catalog body divorced from original raw reference")
		}
		changed = append([]NamespaceRuntimeMongoCollection(nil), catalog...)
		changed[1].UUID = changed[0].UUID
		if _, e := NamespaceRuntimeMongoCatalogSHA256(changed); e == nil {
			t.Fatal("collection UUID alias accepted")
		}
	})
	t.Run("Mongo-role-never-registers-without-actual-client", func(t *testing.T) {
		copy := b
		copy.Requirement.Role = "rpc-user"
		copy.Requirement.ContractSHA256 = NamespaceRuntimeContractSHA256("rpc-user")
		g := &NamespaceRuntimeStartupGate{bootstrap: copy, registration: NamespaceRuntimeRegistration{InstanceID: b.ExpectedInstanceID}}
		bindings := NamespaceRuntimeBindings{Role: "rpc-user", HeaderSHA256: h.SHA256(), Resources: []string{"mongo"}}
		if _, e := g.RecordPrepared(context.Background(), copy.Requirement.Services, copy.Requirement.Components, bindings); e == nil {
			t.Fatal("DB name/config hash became physical target proof")
		}
		if _, e := os.Lstat(b.RegistrationPath); !os.IsNotExist(e) {
			t.Fatal("refusal wrote a registration")
		}
	})
	t.Run("three-nonMongo-roles-cannot-forge-target-evidence", func(t *testing.T) {
		if len(NamespaceRuntimeMongoRoles()) != 11 {
			t.Fatal("actual Mongo roles changed")
		}
		for _, role := range []string{"rpc-auth", "push", "msggateway"} {
			if NamespaceRuntimeUsesMongo(role) {
				t.Fatal("no-Mongo role invented a target")
			}
			bindings := NamespaceRuntimeBindings{Role: role, HeaderSHA256: h.SHA256(), Resources: []string{"synthetic-original-resource"}, MongoTargetSHA256: strings.Repeat("a", 64)}
			if bindings.ValidateShape(role, h.SHA256()) == nil {
				t.Fatal("no-Mongo role claimed a DB")
			}
		}
	})
	t.Run("retired-and-unknown-observation-cannot-retry-a-new-client", func(t *testing.T) {
		client, e := mongo.NewClient(options.Client())
		if e != nil {
			t.Fatal(e)
		}
		copy := b
		copy.Requirement.Role = "rpc-user"
		for _, g := range []*NamespaceRuntimeStartupGate{{bootstrap: copy, retired: true}, {bootstrap: copy, mongoUnconfirmed: true}} {
			if e := g.ValidateOwnedMongoTarget(context.Background(), client.Database(h.Database)); e == nil {
				t.Fatal("stale source resurrected target observation")
			}
		}
	})
}
