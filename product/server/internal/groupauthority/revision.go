package groupauthority

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// CheckReady is read-only and primary-directed. No automatic DDL or repair of
// unknown catalogs. Activation must first drain all old unfenced group writers.
func (s *Store) CheckReady(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	primary := s.db.Client().Database(s.db.Name(), options.Database().SetReadPreference(readpref.Primary()))
	var hello bson.Raw
	if err := primary.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil || !strongTopology(hello) {
		return ErrUnavailable
	}
	if err := s.checkInvalidationCollection(ctx, primary); err != nil {
		return err
	}
	cursor, err := primary.RunCommandCursor(ctx, bson.D{{Key: "listIndexes", Value: "group"}, {Key: "cursor", Value: bson.D{}}})
	if err != nil {
		return ErrUnavailable
	}
	defer cursor.Close(ctx)
	found := false
	n := 0
	for cursor.Next(ctx) {
		n++
		if n > 128 {
			return ErrUnavailable
		}
		var row struct {
			Key       bson.D   `bson:"key"`
			Unique    bool     `bson:"unique"`
			Sparse    bool     `bson:"sparse"`
			Partial   bson.Raw `bson:"partialFilterExpression"`
			Collation bson.Raw `bson:"collation"`
		}
		if cursor.Decode(&row) != nil {
			return ErrUnavailable
		}
		if len(row.Key) != 1 || row.Key[0].Key != "group_id" {
			continue
		}
		if !row.Unique || row.Sparse || len(row.Partial) != 0 || len(row.Collation) != 0 || !ascending(row.Key[0].Value) {
			return ErrUnavailable
		}
		found = true
	}
	if cursor.Err() != nil || cursor.Close(ctx) != nil || !found {
		return ErrUnavailable
	}
	return s.checkInvalidationCatalog(ctx, primary)
}

// The durable retry collection must be explicitly created by the operator. An
// absent collection or an unknown index that could expire/constrain pending
// records is unavailable; runtime insertion never implicitly prepares storage.
func (s *Store) checkInvalidationCatalog(ctx context.Context, primary *mongo.Database) error {
	cursor, err := primary.RunCommandCursor(ctx, bson.D{{Key: "listIndexes", Value: InvalidationsCollection}, {Key: "cursor", Value: bson.D{}}})
	if err != nil {
		return ErrUnavailable
	}
	defer cursor.Close(ctx)
	count := 0
	for cursor.Next(ctx) {
		count++
		var row struct {
			Name          string   `bson:"name"`
			Key           bson.D   `bson:"key"`
			TTL           any      `bson:"expireAfterSeconds"`
			Partial       bson.Raw `bson:"partialFilterExpression"`
			Sparse        bool     `bson:"sparse"`
			PrepareUnique bool     `bson:"prepareUnique"`
		}
		if cursor.Decode(&row) != nil || row.Name != "_id_" || len(row.Key) != 1 || row.Key[0].Key != "_id" || !ascending(row.Key[0].Value) || row.TTL != nil || len(row.Partial) != 0 || row.Sparse || row.PrepareUnique || count > 1 {
			return ErrUnavailable
		}
	}
	if cursor.Err() != nil || cursor.Close(ctx) != nil || count != 1 {
		return ErrUnavailable
	}
	return nil
}

func ascending(value any) bool {
	switch n := value.(type) {
	case int32:
		return n == 1
	case int64:
		return n == 1
	case int:
		return n == 1
	case float64:
		return n == 1
	default:
		return false
	}
}

func revision(raw bson.Raw) (int64, bool, error) {
	elements, err := raw.Elements()
	if err != nil {
		return 0, false, ErrInvalidRevision
	}
	count := 0
	for _, element := range elements {
		if element.Key() == RevisionField {
			count++
		}
	}
	if count > 1 {
		return 0, true, ErrInvalidRevision
	}
	value := raw.Lookup(RevisionField)
	if value.Type == 0 {
		return 0, false, nil // Legacy absent field, never guessed from Ex/owner.
	}
	if value.Type != bsontype.Int64 {
		return 0, true, ErrInvalidRevision
	}
	n := value.Int64()
	if n < 0 || n == math.MaxInt64 {
		return 0, true, ErrInvalidRevision
	}
	return n, true, nil
}

func (s *Store) fence(ctx context.Context, target Target) error {
	if err := strongSession(ctx, s.db.Client()); err != nil {
		return err
	}
	collection := s.db.Collection("group")
	var raw bson.Raw
	err := collection.FindOne(ctx, bson.M{"group_id": target.GroupID}).Decode(&raw)
	if target.Create {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil // Inserting group+revision1 will conflict on the required unique group_id.
		}
		if err != nil {
			return err
		}
		return ErrConflict
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrMissing
	}
	if err != nil {
		return err
	}
	n, exists, err := revision(raw)
	if err != nil {
		return err
	}
	var id any
	if raw.Lookup("_id").Unmarshal(&id) != nil || id == nil {
		return ErrInvalidRevision
	}
	filter := bson.M{"_id": id, "group_id": target.GroupID}
	if exists {
		filter[RevisionField] = n
	} else {
		filter[RevisionField] = bson.M{"$exists": false}
	}
	result, err := collection.UpdateOne(ctx, filter, bson.M{"$inc": bson.M{RevisionField: int64(1)}})
	if err != nil {
		return err // Driver retries transient write conflicts and runs authorization again.
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}

// NewGroupDocument is used only by the guarded GroupModel.Create writer. A
// request-supplied revision never becomes authority. Other full/group patch
// writers must reject the reserved field through CheckFields.
func NewGroupDocument(ctx context.Context, db *mongo.Database, value any) (bson.M, error) {
	raw, err := bson.Marshal(value)
	if err != nil {
		return nil, err
	}
	var fields bson.M
	if err := bson.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	id, ok := fields["group_id"].(string)
	if !ok {
		return nil, ErrInvalidScope
	}
	if err := requireCreation(ctx, db, id); err != nil {
		return nil, err
	}
	fields[RevisionField] = int64(1)
	return fields, nil
}

func CheckFields(fields map[string]any) error {
	for key := range fields {
		if strings.ContainsAny(key, ".$") || strings.EqualFold(key, RevisionField) || strings.EqualFold(key, "group_id") || strings.EqualFold(key, "user_id") {
			return ErrInvalidScope
		}
	}
	return nil
}

func (s *Store) checkCreated(ctx context.Context, groupID string) error {
	var raw bson.Raw
	if err := s.db.Collection("group").FindOne(ctx, bson.M{"group_id": groupID}).Decode(&raw); err != nil {
		return err
	}
	n, exists, err := revision(raw)
	if err != nil || !exists || n != 1 {
		return ErrInvalidRevision
	}
	return nil
}

// Indexes alone do not establish durability: capped/timeseries/expiry/view or
// validator policies can drop or reject obligations. First version accepts only
// an ordinary explicitly prepared collection with empty options. Unknown
// options are an operator review condition, never silently treated as safe.
func (s *Store) checkInvalidationCollection(ctx context.Context, primary *mongo.Database) error {
	cursor, err := primary.RunCommandCursor(ctx, bson.D{{Key: "listCollections", Value: 1}, {Key: "filter", Value: bson.D{{Key: "name", Value: InvalidationsCollection}}}, {Key: "nameOnly", Value: false}, {Key: "cursor", Value: bson.D{}}})
	if err != nil {
		return ErrUnavailable
	}
	defer cursor.Close(ctx)
	count := 0
	for cursor.Next(ctx) {
		count++
		var row struct {
			Name    string `bson:"name"`
			Type    string `bson:"type"`
			Options bson.M `bson:"options"`
			Info    struct {
				ReadOnly *bool `bson:"readOnly"`
			} `bson:"info"`
		}
		if cursor.Decode(&row) != nil || row.Name != InvalidationsCollection || row.Type != "collection" || row.Options == nil || len(row.Options) != 0 || row.Info.ReadOnly == nil || *row.Info.ReadOnly || count != 1 {
			return ErrUnavailable
		}
	}
	if cursor.Err() != nil || cursor.Close(ctx) != nil || count != 1 {
		return ErrUnavailable
	}
	return nil
}

func topologyInteger(value bson.RawValue) (int64, bool) {
	switch value.Type {
	case bsontype.Int32:
		return int64(value.Int32()), true
	case bsontype.Int64:
		return value.Int64(), true
	default:
		return 0, false
	}
}

// Read-only capability admission, never a standalone fallback. Transaction
// operations still validate the live same-client strong session themselves.
func strongTopology(hello bson.Raw) bool {
	primary := hello.Lookup("isWritablePrimary")
	if primary.Type != bsontype.Boolean || !primary.Boolean() {
		return false
	}
	setName, _ := hello.Lookup("setName").StringValueOK()
	message, _ := hello.Lookup("msg").StringValueOK()
	if setName == "" && message != "isdbgrid" {
		return false
	}
	wire, wireOK := topologyInteger(hello.Lookup("maxWireVersion"))
	sessions, sessionsOK := topologyInteger(hello.Lookup("logicalSessionTimeoutMinutes"))
	return wireOK && wire >= 7 && sessionsOK && sessions > 0
}
