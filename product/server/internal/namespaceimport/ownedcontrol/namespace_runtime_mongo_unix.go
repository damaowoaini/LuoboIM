//go:build linux || darwin

package ownedcontrol

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"time"
)

type NamespaceRuntimeMongoCollection struct {
	Name      string                    `json:"name"`
	UUID      string                    `json:"uuid"`
	RawSHA256 string                    `json:"raw_sha256"`
	Source    namespaceimport.SourceRef `json:"source"`
}

func NamespaceRuntimeMongoRoles() []string {
	return []string{"api", "crontask", "msgtransfer-managed", "rpc-conversation", "rpc-encryption", "rpc-friend", "rpc-group", "rpc-msg", "rpc-rtc", "rpc-third", "rpc-user"}
}
func NamespaceRuntimeUsesMongo(role string) bool {
	for _, v := range NamespaceRuntimeMongoRoles() {
		if v == role {
			return true
		}
	}
	return false
}
func NamespaceRuntimeMongoCatalogSHA256(rows []NamespaceRuntimeMongoCollection) (string, error) {
	names := namespaceinventory.OriginalMongoCatalogCollections()
	if len(rows) != len(names) {
		return "", ErrOwnedEvidence
	}
	seen := map[string]bool{}
	for at, row := range rows {
		id, e := hex.DecodeString(row.UUID)
		r := row.Source
		if row.Name != names[at] || e != nil || len(id) != 16 || hex.EncodeToString(id) != row.UUID || row.UUID == "00000000000000000000000000000000" || seen[row.UUID] || !isSHA(row.RawSHA256) || row.RawSHA256 != r.RecordSHA256 || !isSHA(r.ObjectSHA256) || !isSHA(r.RecordSHA256) || r.Kind != "mongo:@catalog:"+sha([]byte(row.Name)) {
			return "", ErrOwnedEvidence
		}
		seen[row.UUID] = true
	}
	raw, e := json.Marshal(rows)
	if e != nil {
		return "", e
	}
	return sha(raw), nil
}
func namespaceRuntimeUniqueBSON(raw bson.Raw) bool {
	if raw.Validate() != nil {
		return false
	}
	elements, e := raw.Elements()
	if e != nil {
		return false
	}
	seen := map[string]bool{}
	for _, field := range elements {
		if seen[field.Key()] {
			return false
		}
		seen[field.Key()] = true
	}
	return true
}

// Accept a real named collection and its BSON UUID, never an absence row,
// view, stringified UUID, name-only list or a fresh invented source reference.
func namespaceRuntimeCatalogUUID(raw bson.Raw, name string) (string, error) {
	if !namespaceRuntimeUniqueBSON(raw) {
		return "", ErrOwnedEvidence
	}
	n, ok := raw.Lookup("name").StringValueOK()
	kind, kok := raw.Lookup("type").StringValueOK()
	info, iok := raw.Lookup("info").DocumentOK()
	if !ok || n != name || !kok || kind != "collection" || !iok || !namespaceRuntimeUniqueBSON(info) {
		return "", ErrOwnedEvidence
	}
	subtype, id, uok := info.Lookup("uuid").BinaryOK()
	if !uok || subtype != 4 || len(id) != 16 {
		return "", ErrOwnedEvidence
	}
	value := hex.EncodeToString(id)
	if value == "00000000000000000000000000000000" {
		return "", ErrOwnedEvidence
	}
	return value, nil
}
func namespaceRuntimeTargetSHA256(h namespaceimport.Header, rows []NamespaceRuntimeMongoCollection) string {
	catalog, e := NamespaceRuntimeMongoCatalogSHA256(rows)
	if e != nil || h.Validate() != nil {
		return ""
	}
	raw, _ := json.Marshal([]string{"same-original-mongo-target-v1", h.SHA256(), catalog})
	return sha(raw)
}
func namespaceRuntimeHello(raw bson.Raw, h namespaceimport.Header) error {
	if !namespaceRuntimeUniqueBSON(raw) {
		return ErrOwnedEvidence
	}
	primary, pok := raw.Lookup("isWritablePrimary").BooleanOK()
	set, sok := raw.Lookup("setName").StringValueOK()
	top, tok := raw.Lookup("topologyVersion").DocumentOK()
	if !pok || !primary || !sok || set != h.ReplicaSet || !tok || !namespaceRuntimeUniqueBSON(top) {
		return ErrOwnedEvidence
	}
	pid, iok := top.Lookup("processId").ObjectIDOK()
	counter, cok := top.Lookup("counter").Int64OK()
	if !iok || pid.IsZero() || pid.Hex() != h.MongoProcess || !cok || counter < 0 || counter != h.MongoTopology {
		return ErrOwnedEvidence
	}
	return nil
}
func namespaceRuntimeStorage(raw bson.Raw, h namespaceimport.Header) error {
	if !namespaceRuntimeUniqueBSON(raw) {
		return ErrOwnedEvidence
	}
	id, iok := raw.Lookup("_id").StringValueOK()
	instance, sok := raw.Lookup("instance").StringValueOK()
	tenant, tok := raw.Lookup("tenant").StringValueOK()
	if !iok || id != "message-visibility-v1" || !sok || instance != h.StorageInstance || !tok || tenant != h.Tenant {
		return ErrOwnedEvidence
	}
	return nil
}

// Read only the actual primary target; the second hello closes restart/drift
// during listCollections. Collection UUIDs come from the held sealed snapshot.
func namespaceRuntimeReadMongoTarget(ctx context.Context, db *mongo.Database, h namespaceimport.Header, expected []NamespaceRuntimeMongoCollection) error {
	if ctx == nil || ctx.Err() != nil || db == nil || db.Client() == nil || db.Name() != h.Database || mongo.SessionFromContext(ctx) != nil || namespaceRuntimeTargetSHA256(h, expected) == "" {
		return ErrOwnedEvidence
	}
	current, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	db = db.Client().Database(db.Name(), options.Database().SetReadPreference(readpref.Primary()))
	var hello bson.Raw
	if e := db.RunCommand(current, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); e != nil {
		return e
	}
	if e := namespaceRuntimeHello(hello, h); e != nil {
		return e
	}
	var marker bson.Raw
	if e := db.Collection("luobo_storage_identity").FindOne(current, bson.M{"_id": "message-visibility-v1"}).Decode(&marker); e != nil {
		return e
	}
	if e := namespaceRuntimeStorage(marker, h); e != nil {
		return e
	}
	names := namespaceinventory.OriginalMongoCatalogCollections()
	cursor, e := db.ListCollections(current, bson.M{"name": bson.M{"$in": names}}, options.ListCollections().SetNameOnly(false).SetBatchSize(16))
	if e != nil {
		return e
	}
	defer cursor.Close(current)
	seen := map[string]bool{}
	want := map[string]string{}
	for _, row := range expected {
		want[row.Name] = row.UUID
	}
	for cursor.Next(current) {
		if len(seen) >= len(names) {
			return ErrOwnedEvidence
		}
		raw := cursor.Current
		name, ok := raw.Lookup("name").StringValueOK()
		if !ok || want[name] == "" || seen[name] {
			return ErrOwnedEvidence
		}
		uuid, e := namespaceRuntimeCatalogUUID(raw, name)
		if e != nil || uuid != want[name] {
			return ErrOwnedEvidence
		}
		seen[name] = true
	}
	if e = cursor.Err(); e != nil {
		return e
	}
	if len(seen) != len(names) {
		return ErrOwnedEvidence
	}
	if e = cursor.Close(current); e != nil {
		return e
	}
	var final bson.Raw
	if e = db.RunCommand(current, bson.D{{Key: "hello", Value: 1}}).Decode(&final); e != nil {
		return e
	}
	return namespaceRuntimeHello(final, h)
}

// Invoked by the leaf Mongo constructor before returning the same real client.
// A failed/unknown observation is sticky, so a second client cannot repair it
// behind the original nonce or change a registration's target.
func (g *NamespaceRuntimeStartupGate) ValidateOwnedMongoTarget(ctx context.Context, db *mongo.Database) error {
	if g == nil {
		return ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if ctx == nil || ctx.Err() != nil || g.retired || g.mongoUnconfirmed || g.registrationSHA256 != "" || !NamespaceRuntimeUsesMongo(g.bootstrap.Requirement.Role) || db == nil || db.Name() != g.bootstrap.Header.Database || g.mongoDB != nil && (g.mongoDB.Client() != db.Client() || g.mongoDB.Name() != db.Name()) {
		return ErrOwnedEvidence
	}
	g.mongoUnconfirmed = true
	if e := namespaceRuntimeReadMongoTarget(ctx, db, g.bootstrap.Header, g.bootstrap.MongoCatalog); e != nil {
		return e
	}
	g.mongoDB = db
	g.mongoTargetSHA256 = namespaceRuntimeTargetSHA256(g.bootstrap.Header, g.bootstrap.MongoCatalog)
	g.mongoUnconfirmed = false
	return nil
}

// Source factories may use another *Database view from the same original
// client/name; their Store binding still checks the exact original DAO pointer.
func (g *NamespaceRuntimeStartupGate) MongoTargetForDatabase(ctx context.Context, db *mongo.Database) (string, error) {
	if g == nil || ctx == nil || ctx.Err() != nil || db == nil {
		return "", ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.retired || g.mongoUnconfirmed || g.mongoDB == nil || g.mongoDB.Client() != db.Client() || g.mongoDB.Name() != db.Name() || !isSHA(g.mongoTargetSHA256) {
		return "", ErrOwnedEvidence
	}
	return g.mongoTargetSHA256, nil
}
func validateNamespaceRuntimeRegistrationMongo(r NamespaceRuntimeRegistration, b NamespaceRuntimeBootstrap) error {
	var actual NamespaceRuntimeBindings
	if namespaceRuntimeDecodeCanonical(r.BindingsJSON, &actual) != nil {
		return ErrOwnedEvidence
	}
	expected := ""
	if NamespaceRuntimeUsesMongo(r.Role) {
		expected = namespaceRuntimeTargetSHA256(b.Header, b.MongoCatalog)
		if !isSHA(expected) {
			return ErrOwnedEvidence
		}
	}
	if actual.MongoTargetSHA256 != expected {
		return ErrOwnedEvidence
	}
	return nil
}
