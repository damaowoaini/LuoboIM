package groupcleanup

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Runtime never creates or repairs a collection/index. In particular capped,
// view/timeseries/validator/TTL/hidden/partial/prepareUnique catalogs are not
// accepted as durable pending obligations. Operator prepare remains explicit.
func (s *Store) CheckReady(ctx context.Context) error {
	if s == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnavailable
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.authority.CheckReady(current); err != nil {
		return err
	}
	if err := s.delivery.CheckReady(current); err != nil {
		return err
	}
	if err := s.effects.CheckReady(current); err != nil {
		return err
	}
	cursor, err := s.db.RunCommandCursor(current, bson.D{{Key: "listCollections", Value: 1}, {Key: "filter", Value: bson.M{"name": Collection}}, {Key: "cursor", Value: bson.D{}}})
	if err != nil {
		return err
	}
	var rows []struct {
		Name    string   `bson:"name"`
		Type    string   `bson:"type"`
		Options bson.Raw `bson:"options"`
		Info    *struct {
			ReadOnly *bool `bson:"readOnly"`
		} `bson:"info"`
	}
	err = cursor.All(current, &rows)
	closeErr := cursor.Close(current)
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if len(rows) != 1 || rows[0].Name != Collection || rows[0].Type != "collection" || rows[0].Info == nil || rows[0].Info.ReadOnly == nil || *rows[0].Info.ReadOnly {
		return ErrUnavailable
	}
	fields, err := rows[0].Options.Elements()
	if err != nil || len(fields) != 0 {
		return ErrUnavailable
	}
	cursor, err = s.db.RunCommandCursor(current, bson.D{{Key: "listIndexes", Value: Collection}, {Key: "cursor", Value: bson.D{}}})
	if err != nil {
		return err
	}
	var indexes []struct {
		Name          string   `bson:"name"`
		Key           bson.D   `bson:"key"`
		Hidden        bool     `bson:"hidden"`
		Sparse        bool     `bson:"sparse"`
		PrepareUnique bool     `bson:"prepareUnique"`
		TTL           any      `bson:"expireAfterSeconds"`
		Partial       bson.Raw `bson:"partialFilterExpression"`
		Collation     bson.Raw `bson:"collation"`
	}
	err = cursor.All(current, &indexes)
	closeErr = cursor.Close(current)
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if len(indexes) != 1 {
		return ErrUnavailable
	}
	r := indexes[0]
	one := false
	if len(r.Key) == 1 {
		switch v := r.Key[0].Value.(type) {
		case int32:
			one = v == 1
		case int64:
			one = v == 1
		}
	}
	if r.Name != "_id_" || len(r.Key) != 1 || r.Key[0].Key != "_id" || !one || r.Hidden || r.Sparse || r.PrepareUnique || r.TTL != nil || len(r.Partial) != 0 || len(r.Collation) != 0 {
		return ErrUnavailable
	}
	return s.checkSchema(current)
}
func (s *Store) checkSchema(ctx context.Context) error {
	var raw bson.Raw
	if err := s.db.Collection(Collection).FindOne(ctx, bson.M{"_id": SchemaID}).Decode(&raw); err != nil {
		return errors.Join(ErrUnavailable, err)
	}
	fields, err := raw.Elements()
	if err != nil || len(fields) != 5 {
		return ErrUnavailable
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Key()] {
			return ErrUnavailable
		}
		seen[f.Key()] = true
	}
	id, idOK := raw.Lookup("_id").StringValueOK()
	version, versionOK := raw.Lookup("version").Int32OK()
	epoch, epochOK := raw.Lookup("epoch").StringValueOK()
	activation, activationOK := raw.Lookup("activation_sha256").StringValueOK()
	retired, retiredOK := raw.Lookup("legacy_writers_retired").BooleanOK()
	if !idOK || id != SchemaID || !versionOK || version != 1 || !epochOK || epoch != s.epoch || !activationOK || activation != s.activationSHA || !retiredOK || !retired {
		return ErrUnavailable
	}
	return nil
}
