package messagedelivery

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// This preparation precedes the immutable source capture. It has no namespace
// counter, owner-window, membership-ready or namespace-cutover write. A bare
// launcher hold cannot authorize it: the actual agent must first join every
// old writer, drain both queue planes and prove old credentials ineffective.
func (w *NamespaceImportWriter) PrepareHeldMetadata(ctx context.Context, hold *ownedcontrol.AdmissionHold, h namespaceimport.Header) error {
	if !w.validHeader(h) || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	check := func() error {
		if hold == nil || hold.ValidateExclusion() != nil || hold.HeaderSHA256() != h.SHA256() || !validSHA(hold.LeaseID()) || !validSHA(hold.RecordSHA256()) {
			return ErrUnprepared
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	current, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	if err := w.checkLiveTarget(current, false); err != nil {
		return err
	}
	for _, name := range []string{Sequences, Receipts, Effects, "msg", "conversation", "group", "group_member", "group_request", "group_membership_history", "message_visibility_floors", "luobo_storage_identity", groupauthority.InvalidationsCollection, groupeffects.Collection} {
		if err := check(); err != nil {
			return err
		}
		if err := w.preparePlainImportCollection(current, name); err != nil {
			return err
		}
	}
	// An incompatible existing index or duplicate original doc_id fails here.
	// Preparation never drops indexes, removes documents or silently repairs a
	// validator/TTL/capped collection in order to make readiness pass.
	if _, err := w.store.db.Collection("msg").Indexes().CreateOne(current, mongo.IndexModel{Keys: bson.D{{Key: "doc_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		return err
	}
	if _, err := w.store.db.Collection("group").Indexes().CreateOne(current, mongo.IndexModel{Keys: bson.D{{Key: "group_id", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	if err := w.store.transaction(current, func(tx context.Context) error {
		if err := check(); err != nil {
			return err
		}
		if err := w.prepareImportStorageIdentity(tx); err != nil {
			return err
		}
		marker := bson.D{{Key: "_id", Value: SchemaID}, {Key: "version", Value: int32(1)}, {Key: "epoch", Value: h.Epoch}, {Key: "activation_sha256", Value: h.ActivationSHA256}, {Key: "legacy_writers_retired", Value: true}}
		for _, name := range []string{Sequences, Receipts, Effects} {
			if err := immutableImportDocument(tx, w.store.db.Collection(name), SchemaID, marker); err != nil {
				return err
			}
		}
		// This is schema-only metadata under the same held exclusion, not a
		// namespace grant or Ready publication. The real effects store requires
		// its immutable three-field sentinel in addition to an ordinary catalog.
		// A foreign existing row fails the SAME transaction; never overwrite it.
		effectSchema := bson.D{{Key: "_id", Value: groupeffects.SchemaID}, {Key: "kind", Value: "group-effects-schema-v1"}, {Key: "version", Value: int32(1)}}
		return immutableImportDocument(tx, w.store.db.Collection(groupeffects.Collection), groupeffects.SchemaID, effectSchema)
	}); err != nil {
		return err
	}
	// Only the previously absent server revision is initialized. Existing
	// revisions are retained exactly and validated against raw source by the
	// origin/window validator; an existing zero or invalid type is not rewritten.
	// Each small transaction records its before/after metadata identity. If the
	// bounded bootstrap is interrupted, the next held attempt finds only rows
	// still absent, rather than restarting an ever-growing prefix of the table.
	for {
		if err := check(); err != nil {
			return err
		}
		var raw bson.Raw
		err := w.store.db.Collection("group").FindOne(current, bson.M{groupauthority.RevisionField: bson.M{"$exists": false}}).Decode(&raw)
		if errors.Is(err, mongo.ErrNoDocuments) {
			break
		}
		if err != nil {
			return err
		}
		if !importUniqueFields(raw) {
			return ErrConflict
		}
		gid, ok := raw.Lookup("group_id").StringValueOK()
		var objectID any
		if !ok || !identity(gid) || raw.Lookup("_id").Unmarshal(&objectID) != nil || objectID == nil {
			return ErrConflict
		}
		if err := w.store.transaction(current, func(tx context.Context) error {
			if err := check(); err != nil {
				return err
			}
			if err := w.store.requireTX(tx); err != nil {
				return err
			}
			var actual bson.Raw
			if err := w.store.db.Collection("group").FindOne(tx, bson.M{"_id": objectID, "group_id": gid, groupauthority.RevisionField: bson.M{"$exists": false}}).Decode(&actual); err != nil {
				return err
			}
			if !equalImportDocuments(actual, raw) {
				return ErrConflict
			}
			result, err := w.store.db.Collection("group").UpdateOne(tx, bson.M{"_id": objectID, "group_id": gid, groupauthority.RevisionField: bson.M{"$exists": false}}, bson.M{"$set": bson.M{groupauthority.RevisionField: int64(1)}})
			if err != nil {
				return err
			}
			if result.MatchedCount != 1 || result.ModifiedCount != 1 {
				return ErrConflict
			}
			id := key(h.Epoch, "namespace-import-group-metadata", h.MigrationID+"\x00"+gid)
			row := bson.D{{Key: "_id", Value: id}, {Key: "kind", Value: "namespace-import-group-metadata-v1"}, {Key: "header_sha256", Value: h.SHA256()}, {Key: "group_id", Value: gid}, {Key: "before_document_sha256", Value: hash(raw)}, {Key: "revision_was_absent", Value: true}, {Key: "baseline_revision", Value: int64(1)}}
			return immutableImportDocument(tx, w.store.db.Collection(Receipts), id, row)
		}); err != nil {
			return err
		}
	}
	if err := check(); err != nil {
		return err
	}
	// This checks only the base catalog. The historical migration and runtime
	// reader/writer binding gates remain separate and are not published here.
	if err := w.CheckImportCatalogReady(current); err != nil {
		return err
	}
	groups, err := groupauthority.NewStore(w.store.db)
	if err != nil {
		return err
	}
	if err := groups.CheckReady(current); err != nil {
		return err
	}
	effects, err := groupeffects.NewStore(w.store.db)
	if err != nil {
		return err
	}
	if err := effects.CheckReady(current); err != nil {
		return err
	}
	if err := w.store.transaction(current, func(tx context.Context) error {
		if err := check(); err != nil {
			return err
		}
		if err := w.store.requireTX(tx); err != nil {
			return err
		}
		id := key(h.Epoch, "namespace-import-metadata", h.MigrationID)
		row := bson.D{{Key: "_id", Value: id}, {Key: "kind", Value: "namespace-import-metadata-v1"}, {Key: "header_sha256", Value: h.SHA256()}, {Key: "lease_id", Value: hold.LeaseID()}, {Key: "hold_record_sha256", Value: hold.RecordSHA256()}}
		return immutableImportDocument(tx, w.store.db.Collection(Receipts), id, row)
	}); err != nil {
		return err
	}
	return check()
}

func (w *NamespaceImportWriter) preparePlainImportCollection(ctx context.Context, name string) error {
	rows, err := w.store.db.ListCollections(ctx, bson.D{{Key: "name", Value: name}}, options.ListCollections().SetNameOnly(false))
	if err != nil {
		return err
	}
	count, compatible := 0, true
	for rows.Next(ctx) {
		count++
		var row struct {
			Name    string   `bson:"name"`
			Type    string   `bson:"type"`
			Options bson.Raw `bson:"options"`
			Info    struct {
				ReadOnly *bool `bson:"readOnly"`
			} `bson:"info"`
		}
		if rows.Decode(&row) != nil || row.Name != name || row.Type != "collection" || row.Info.ReadOnly == nil || *row.Info.ReadOnly {
			compatible = false
			continue
		}
		elements, e := row.Options.Elements()
		if e != nil || len(elements) != 0 {
			compatible = false
		}
	}
	queryErr, closeErr := rows.Err(), rows.Close(ctx)
	if queryErr != nil || closeErr != nil {
		return errors.Join(queryErr, closeErr)
	}
	if count > 1 || !compatible {
		return ErrConflict
	}
	if count == 0 {
		return w.store.db.CreateCollection(ctx, name)
	}
	return nil
}

func (w *NamespaceImportWriter) prepareImportStorageIdentity(ctx context.Context) error {
	collection := w.store.db.Collection("luobo_storage_identity")
	var raw bson.Raw
	err := collection.FindOne(ctx, bson.M{"_id": "message-visibility-v1"}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_, err = collection.InsertOne(ctx, bson.D{{Key: "_id", Value: "message-visibility-v1"}, {Key: "instance", Value: w.header.StorageInstance}, {Key: "tenant", Value: w.header.Tenant}})
		return err
	}
	if err != nil {
		return err
	}
	if !importUniqueFields(raw) {
		return ErrConflict
	}
	fields, e := raw.Elements()
	if e != nil || len(fields) != 2 && len(fields) != 3 {
		return ErrConflict
	}
	id, iok := raw.Lookup("_id").StringValueOK()
	instance, sok := raw.Lookup("instance").StringValueOK()
	if !iok || id != "message-visibility-v1" || !sok || instance != w.header.StorageInstance || !validUUID(instance) {
		return ErrConflict
	}
	if raw.Lookup("tenant").Type != 0 {
		return w.checkStorageIdentity(ctx, true)
	}
	if len(fields) != 2 {
		return ErrConflict
	}
	result, err := collection.UpdateOne(ctx, bson.M{"_id": id, "instance": instance, "tenant": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"tenant": w.header.Tenant}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}

// Compare exact BSON types/values after rejecting duplicate/unknown fields.
// This is an immutable insert-or-inspect, never an upsert that overwrites an
// old migration, epoch, seed, claim, receipt or storage identity.
func immutableImportDocument(ctx context.Context, collection *mongo.Collection, id string, expected any) error {
	body, err := bson.Marshal(expected)
	if err != nil || !importUniqueFields(body) {
		return ErrInvalid
	}
	var raw bson.Raw
	err = collection.FindOne(ctx, bson.M{"_id": id}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_, err = collection.InsertOne(ctx, expected)
		return err
	}
	if err != nil {
		return err
	}
	if !equalImportDocuments(raw, body) {
		return ErrConflict
	}
	return nil
}

func equalImportDocuments(actual, expected bson.Raw) bool {
	if !importUniqueFields(actual) || !importUniqueFields(expected) {
		return false
	}
	got, ge := actual.Elements()
	want, we := expected.Elements()
	if ge != nil || we != nil || len(got) != len(want) {
		return false
	}
	for _, field := range want {
		a, b := actual.Lookup(field.Key()), field.Value()
		if a.Type != b.Type {
			return false
		}
		if a.Type == bsontype.EmbeddedDocument {
			if !equalImportDocuments(bson.Raw(a.Value), bson.Raw(b.Value)) {
				return false
			}
		} else if !bytes.Equal(a.Value, b.Value) {
			return false
		}
	}
	return true
}
