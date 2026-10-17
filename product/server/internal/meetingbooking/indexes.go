package meetingbooking

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type indexRequirement struct {
	collection, name string
	key              bson.D
	unique           bool
}

var bookingIndexRequirements = []indexRequirement{
	{IntentsCollection, "booking_intent_owner_request_unique", bson.D{{Key: "tenant", Value: 1}, {Key: "owner", Value: 1}, {Key: "request_id", Value: 1}}, true},
	{BookingsCollection, "booking_owner_created_page", bson.D{{Key: "tenant", Value: 1}, {Key: "owner", Value: 1}, {Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}, false},
}

type indexCatalog interface {
	List(context.Context, string) ([]bson.Raw, error)
	Create(context.Context, indexRequirement) error
}
type mongoIndexCatalog struct{ db *mongo.Database }

func (c mongoIndexCatalog) List(ctx context.Context, collection string) (rows []bson.Raw, err error) {
	// IndexView.List suppresses NamespaceNotFound in driver v1.17.10. Keep the
	// exact server26 via raw listIndexes; an empty successful catalog is NOT absent.
	cursor, err := c.db.RunCommandCursor(ctx, bson.D{{Key: "listIndexes", Value: collection}, {Key: "cursor", Value: bson.D{}}}, options.RunCmd().SetReadPreference(readpref.Primary()))
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := cursor.Close(ctx); closeErr != nil && err == nil {
			rows = nil
			err = closeErr
		}
	}()
	for cursor.Next(ctx) {
		if len(rows) >= 128 {
			return nil, ErrUnavailable
		}
		var raw bson.Raw
		if err = cursor.Decode(&raw); err != nil {
			return nil, err
		}
		rows = append(rows, raw)
	}
	if err = cursor.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func (c mongoIndexCatalog) Create(ctx context.Context, r indexRequirement) error {
	_, err := c.db.Collection(r.collection, options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true)))).Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: r.key, Options: options.Index().SetName(r.name).SetUnique(r.unique).SetSparse(false).SetHidden(false).SetCollation(&options.Collation{Locale: "simple"}),
	})
	return err
}

type indexDescription struct {
	Name          string        `bson:"name"`
	Key           bson.D        `bson:"key"`
	Unique        *bool         `bson:"unique"`
	Sparse        bool          `bson:"sparse"`
	Hidden        bool          `bson:"hidden"`
	PrepareUnique bool          `bson:"prepareUnique"`
	Partial       bson.RawValue `bson:"partialFilterExpression"`
	TTL           bson.RawValue `bson:"expireAfterSeconds"`
	Collation     bson.RawValue `bson:"collation"`
	Build         bson.RawValue `bson:"buildUUID"`
}

func indexDirection(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		if v == 1 || v == -1 {
			return v, true
		}
	case int32:
		if v == 1 || v == -1 {
			return int(v), true
		}
	case int64:
		if v == 1 || v == -1 {
			return int(v), true
		}
	case float64:
		if v == 1 || v == -1 {
			return int(v), true
		}
	}
	return 0, false
}
func sameIndexKey(actual, wanted bson.D) bool {
	if len(actual) != len(wanted) {
		return false
	}
	for i, part := range actual {
		value, ok := indexDirection(part.Value)
		need, _ := indexDirection(wanted[i].Value)
		if !ok || part.Key != wanted[i].Key || value != need {
			return false
		}
	}
	return true
}
func simpleCollation(value bson.RawValue) bool {
	if value.Type == 0 {
		return true
	}
	if value.Type != bsontype.EmbeddedDocument {
		return false
	}
	var doc bson.D
	if bson.Unmarshal(value.Document(), &doc) != nil {
		return false
	}
	return len(doc) == 1 && doc[0].Key == "locale" && doc[0].Value == "simple"
}

// Required indexes must describe the full binary-key domain, not a filtered,
// sparse, TTL, building, or hidden approximation. Mongo's _id_ unique field is
// normally omitted: its intrinsic uniqueness must not be mistaken for false.
func verifyIndexRows(rows []bson.Raw, wanted indexRequirement, allowMissing bool) error {
	seen := map[string]bool{}
	haveID, haveRequired := false, false
	for _, raw := range rows {
		var item indexDescription
		if bson.Unmarshal(raw, &item) != nil || item.Name == "" || seen[item.Name] {
			return ErrUnavailable
		}
		seen[item.Name] = true
		// Any TTL could erase immutable intents or bookings behind a valid index.
		if item.TTL.Type != 0 {
			return ErrUnavailable
		}
		if item.Name != "_id_" && item.Name != wanted.name {
			if item.PrepareUnique || (item.Unique != nil && *item.Unique) {
				return ErrUnavailable
			}
			continue
		}
		if item.Sparse || item.Hidden || item.PrepareUnique || item.Partial.Type != 0 || item.Build.Type != 0 || !simpleCollation(item.Collation) {
			return ErrUnavailable
		}
		if item.Name == "_id_" {
			if !sameIndexKey(item.Key, bson.D{{Key: "_id", Value: 1}}) || (item.Unique != nil && !*item.Unique) {
				return ErrUnavailable
			}
			haveID = true
			continue
		}
		unique := item.Unique != nil && *item.Unique
		if !sameIndexKey(item.Key, wanted.key) || unique != wanted.unique {
			return ErrUnavailable
		}
		haveRequired = true
	}
	if !haveID || (!haveRequired && !allowMissing) {
		return ErrUnavailable
	}
	return nil
}

// CheckReady performs reads only, on every storage entry. It never attempts
// repair/DDL and never caches readiness across an operator maintenance change.
// Operators must still drain requests before changing indexes: list/read alone
// cannot atomically fence an external dropIndex between this check and a Tx.
func (s *MongoStore) CheckReady(ctx context.Context) error {
	if s == nil || s.indexes == nil {
		return ErrUnavailable
	}
	for _, required := range bookingIndexRequirements {
		rows, err := s.indexes.List(ctx, required.collection)
		if err != nil || verifyIndexRows(rows, required, false) != nil {
			return ErrUnavailable
		}
	}
	return nil
}

// PrepareIndexes is an explicit OPERATOR action, never a constructor/request
// action. It may create the two named collections/indexes. It does not drop or
// mutate incompatible indexes. DDL across two collections is not atomic: any
// failure means preparation is unconfirmed; an operator must inspect/retry.
func (s *MongoStore) PrepareIndexes(ctx context.Context) error {
	if s == nil || s.indexes == nil {
		return ErrUnavailable
	}
	for _, required := range bookingIndexRequirements {
		rows, err := s.indexes.List(ctx, required.collection)
		var server mongo.ServerError
		missing := errors.As(err, &server) && server.HasErrorCode(26)
		if err != nil && !missing {
			return ErrUnavailable
		}
		if !missing && verifyIndexRows(rows, required, true) != nil {
			return ErrUnavailable
		}
	}
	for _, required := range bookingIndexRequirements {
		if err := s.indexes.Create(ctx, required); err != nil {
			return ErrUnavailable
		}
	}
	return s.CheckReady(ctx)
}
