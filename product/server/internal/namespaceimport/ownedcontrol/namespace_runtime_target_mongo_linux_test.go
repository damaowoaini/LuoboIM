//go:build linux

package ownedcontrol_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func targetMongoSHA(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

// This explicit task-owned identity fixture exercises actual Mongo UUID/hello/
// marker observations. Its synthetic private bootstrap is NOT an import, a
// signed-inventory capability, fourteen-role graph or a ReadReady publication.
// The real producer's sealed-catalog provenance has a separate FS source test.
func TestNamespaceRuntimeActualMongoTargetRejectsReplacementAndForeignClient(t *testing.T) {
	ctx, db, h := mongoCompositionDB(t)
	names := namespaceinventory.OriginalMongoCatalogCollections()
	for _, name := range names {
		if e := db.CreateCollection(ctx, name); e != nil {
			t.Fatal("owned fixture collection create failed")
		}
	}
	if _, e := db.Collection("luobo_storage_identity").InsertOne(ctx, bson.M{"_id": "message-visibility-v1", "instance": h.StorageInstance, "tenant": h.Tenant}); e != nil {
		t.Fatal("owned fixture storage identity insert failed")
	}
	cursor, e := db.ListCollections(ctx, bson.M{"name": bson.M{"$in": names}}, options.ListCollections().SetNameOnly(false))
	if e != nil {
		t.Fatal("owned fixture catalog unavailable")
	}
	found := map[string]ownedcontrol.NamespaceRuntimeMongoCollection{}
	for cursor.Next(ctx) {
		raw := append(bson.Raw(nil), cursor.Current...)
		name, ok := raw.Lookup("name").StringValueOK()
		info, iok := raw.Lookup("info").DocumentOK()
		if !ok || !iok {
			t.Fatal("actual catalog shape")
		}
		sub, id, uok := info.Lookup("uuid").BinaryOK()
		if !uok || sub != 4 || len(id) != 16 {
			t.Fatal("actual collection UUID absent")
		}
		sum := targetMongoSHA(raw)
		found[name] = ownedcontrol.NamespaceRuntimeMongoCollection{Name: name, UUID: hex.EncodeToString(id), RawSHA256: sum, Source: namespaceimport.SourceRef{ObjectSHA256: targetMongoSHA([]byte("synthetic-identity-only-" + name)), RecordSHA256: sum, Kind: "mongo:@catalog:" + targetMongoSHA([]byte(name))}}
	}
	if e = cursor.Err(); e != nil {
		t.Fatal("actual catalog incomplete")
	}
	if e = cursor.Close(ctx); e != nil {
		t.Fatal(e)
	}
	var catalog []ownedcontrol.NamespaceRuntimeMongoCollection
	for _, name := range names {
		row, ok := found[name]
		if !ok {
			t.Fatal("actual catalog missing collection")
		}
		catalog = append(catalog, row)
	}
	sum := strings.Repeat("a", 64)
	key := ed25519.NewKeyFromSeed(make([]byte, 32))
	dir := t.TempDir()
	if e = os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
	r := ownedcontrol.NamespaceRuntimeRequirement{Role: "rpc-user", BinarySHA256: sum, SourceSHA256: sum, BuildReceiptSHA256: sum, BootstrapSHA256: sum, ConfigurationSHA256: sum, EnvironmentSHA256: sum, ContractSHA256: ownedcontrol.NamespaceRuntimeContractSHA256("rpc-user"), Services: []string{"synthetic-target-only"}, Components: []string{"source-ownership"}}
	catalogSHA, e := ownedcontrol.NamespaceRuntimeMongoCatalogSHA256(catalog)
	if e != nil {
		t.Fatal(e)
	}
	b := ownedcontrol.NamespaceRuntimeBootstrap{Version: "namespace-runtime-private-bootstrap-v1", Header: h, HistoryPreparationID: sum, HistoryPreparationSHA256: sum, InventorySHA256: sum, ManifestSHA256: sum, MongoCatalog: catalog, MongoCatalogSHA256: catalogSHA, RuntimePreparationID: sum, RuntimePreparationSHA256: sum, RuntimeToken: sum, RequirementsSHA256: sum, Requirement: r, ExpectedInstanceID: "44444444-4444-4444-8444-444444444444", RegistrationPath: filepath.Join(dir, "registration.json"), ReleasePath: filepath.Join(dir, "release.json"), ReleaseKeyID: "synthetic-target-key", ReleasePublicKey: key.Public().(ed25519.PublicKey)}
	path := filepath.Join(dir, "target-bootstrap.json")
	pin, e := ownedcontrol.WriteNamespaceRuntimeBootstrap(path, b)
	if e != nil {
		t.Fatal(e)
	}
	newGate := func() *ownedcontrol.NamespaceRuntimeStartupGate {
		g, e := ownedcontrol.NewNamespaceRuntimeStartupGate(path, pin, r.Role, b.ExpectedInstanceID, r.BootstrapSHA256, r.ConfigurationSHA256, r.EnvironmentSHA256, sum)
		if e != nil {
			t.Fatal(e)
		}
		return g
	}
	gate := newGate()
	t.Run("same-original-client-is-observed-without-minting-readiness", func(t *testing.T) {
		if e := gate.ValidateOwnedMongoTarget(ctx, db); e != nil {
			t.Fatal("actual original target rejected", e)
		}
		if _, e := gate.MongoTargetForDatabase(ctx, db.Client().Database(db.Name())); e != nil {
			t.Fatal("same-client DAO wrapper rejected", e)
		}
		if _, _, e := gate.KnownPublication(); e == nil {
			t.Fatal("target observation minted namespace readiness")
		}
	})
	t.Run("another-client-cannot-borrow-the-original-registration", func(t *testing.T) {
		client, e := mongo.Connect(ctx, options.Client().ApplyURI(os.Getenv("LUOBO_STORAGE_TEST_MONGO_URI")).SetDirect(true).SetRetryWrites(false))
		if e != nil {
			t.Fatal("owned second handle connect failed")
		}
		defer func() {
			end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if e := client.Disconnect(end); e != nil {
				t.Error("owned second client disconnect unconfirmed")
			}
		}()
		if e := gate.ValidateOwnedMongoTarget(ctx, client.Database(db.Name())); e == nil {
			t.Fatal("another client borrowed the original DAO proof")
		}
	})
	t.Run("same-name-new-collection-UUID-refuses-registration", func(t *testing.T) {
		name := names[0]
		if e := db.Collection(name).Drop(ctx); e != nil {
			t.Fatal("owned fixture collection drop failed")
		}
		if e := db.CreateCollection(ctx, name); e != nil {
			t.Fatal("owned fixture collection recreate failed")
		}
		if _, e := gate.RecordPrepared(ctx, r.Services, r.Components, ownedcontrol.NamespaceRuntimeBindings{Role: r.Role, HeaderSHA256: h.SHA256(), Resources: []string{"mongo"}}); e == nil {
			t.Fatal("replacement collection borrowed original UUID evidence")
		}
		if _, e := os.Lstat(b.RegistrationPath); !os.IsNotExist(e) {
			t.Fatal("target refusal emitted registration")
		}
	})
	t.Run("foreign-tenant-remains-unknown-and-nonpermit", func(t *testing.T) {
		g := newGate()
		if _, e := db.Collection("luobo_storage_identity").UpdateOne(ctx, bson.M{"_id": "message-visibility-v1"}, bson.M{"$set": bson.M{"tenant": "foreign-tenant"}}); e != nil {
			t.Fatal("owned tenant fixture change failed")
		}
		if e := g.ValidateOwnedMongoTarget(ctx, db); e == nil {
			t.Fatal("foreign tenant accepted")
		}
		if _, _, e := g.KnownPublication(); e == nil {
			t.Fatal("unknown target became Ready")
		}
		if _, e := g.MongoTargetForDatabase(ctx, db); e == nil {
			t.Fatal("failed target observation left a usable client binding")
		}
	})
}
