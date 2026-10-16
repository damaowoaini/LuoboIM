package messagedelivery

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func one(v any) bool {
	switch n := v.(type) {
	case int32:
		return n == 1
	case int64:
		return n == 1
	case int:
		return n == 1
	case float64:
		return n == 1
	}
	return false
}

// CheckReady is primary/read-only with its own bounded lifetime. It never
// creates collections/indexes or silently treats an unsafe legacy store as new.
// Shared seq/receipt/effect collections require only their native _id_ index;
// the existing OpenIM msg collection must retain its simple unique doc_id index.
func (s *Store) CheckReady(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnprepared
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var hello struct {
		Writable bool   `bson:"isWritablePrimary"`
		SetName  string `bson:"setName"`
		Msg      string `bson:"msg"`
		Sessions *int64 `bson:"logicalSessionTimeoutMinutes"`
	}
	if s.db.RunCommand(current, bson.D{{Key: "hello", Value: 1}}).Decode(&hello) != nil || !hello.Writable || hello.Sessions == nil || hello.SetName == "" && hello.Msg != "isdbgrid" {
		return ErrUnprepared
	}
	for _, name := range []string{Sequences, Receipts, Effects, "msg", "message_visibility_floors"} {
		cursor, err := s.db.RunCommandCursor(current, bson.D{{Key: "listCollections", Value: 1}, {Key: "filter", Value: bson.M{"name": name}}, {Key: "cursor", Value: bson.D{}}})
		if err != nil {
			return ErrUnprepared
		}
		count := 0
		valid := true
		for cursor.Next(current) {
			count++
			var row struct {
				Name    string   `bson:"name"`
				Type    string   `bson:"type"`
				Options bson.Raw `bson:"options"`
				Info    *struct {
					ReadOnly *bool `bson:"readOnly"`
				} `bson:"info"`
			}
			if cursor.Decode(&row) != nil || row.Name != name || row.Type != "collection" || row.Info == nil || row.Info.ReadOnly == nil || *row.Info.ReadOnly {
				valid = false
				continue
			}
			fields, e := row.Options.Elements()
			if e != nil || len(fields) != 0 {
				valid = false
			}
		}
		cursorErr, closeErr := cursor.Err(), cursor.Close(current)
		if !valid || count != 1 || cursorErr != nil || closeErr != nil {
			return ErrUnprepared
		}
		indexes, err := s.db.RunCommandCursor(current, bson.D{{Key: "listIndexes", Value: name}, {Key: "cursor", Value: bson.D{}}})
		if err != nil {
			return ErrUnprepared
		}
		seenID, seenDoc := false, false
		count = 0
		valid = true
		for indexes.Next(current) {
			count++
			var row struct {
				Name          string   `bson:"name"`
				Key           bson.D   `bson:"key"`
				Unique        bool     `bson:"unique"`
				PrepareUnique bool     `bson:"prepareUnique"`
				Sparse        bool     `bson:"sparse"`
				Hidden        bool     `bson:"hidden"`
				TTL           any      `bson:"expireAfterSeconds"`
				Partial       bson.Raw `bson:"partialFilterExpression"`
				Collation     bson.Raw `bson:"collation"`
			}
			if indexes.Decode(&row) != nil {
				valid = false
				continue
			}
			unsafe := row.PrepareUnique || row.Sparse || row.TTL != nil || len(row.Partial) != 0 || len(row.Collation) != 0
			if row.Name == "_id_" {
				if seenID || unsafe || row.Hidden || len(row.Key) != 1 || row.Key[0].Key != "_id" || !one(row.Key[0].Value) {
					valid = false
				}
				seenID = true
				continue
			}
			if name == "msg" && len(row.Key) == 1 && row.Key[0].Key == "doc_id" && one(row.Key[0].Value) {
				if seenDoc || unsafe || row.Hidden || !row.Unique {
					valid = false
				}
				seenDoc = true
				continue
			}
			if name != "msg" || row.Unique || row.PrepareUnique || row.TTL != nil {
				valid = false
			}
		}
		cursorErr, closeErr = indexes.Err(), indexes.Close(current)
		if !valid || !seenID || name != "msg" && count != 1 || name == "msg" && !seenDoc || cursorErr != nil || closeErr != nil {
			return ErrUnprepared
		}
	}
	return s.checkEpoch(current)
}

func (s *Store) checkEpoch(ctx context.Context) error {
	for _, name := range []string{Sequences, Receipts, Effects} {
		var row bson.Raw
		if err := s.db.Collection(name).FindOne(ctx, bson.M{"_id": SchemaID}).Decode(&row); err != nil {
			return errors.Join(ErrUnprepared, err)
		}
		fields, err := row.Elements()
		if err != nil || len(fields) != 5 {
			return ErrUnprepared
		}
		seen := map[string]bool{}
		for _, field := range fields {
			if seen[field.Key()] {
				return ErrUnprepared
			}
			seen[field.Key()] = true
		}
		version, versionOK := row.Lookup("version").Int32OK()
		epoch, epochOK := row.Lookup("epoch").StringValueOK()
		activation, activationOK := row.Lookup("activation_sha256").StringValueOK()
		retired, retiredOK := row.Lookup("legacy_writers_retired").BooleanOK()
		id, idOK := row.Lookup("_id").StringValueOK()
		if !versionOK || version != 1 || !epochOK || epoch != s.epoch || !activationOK || activation != s.activationSHA || !retiredOK || !retired || !idOK || id != SchemaID {
			return ErrUnprepared
		}
	}
	return nil
}
