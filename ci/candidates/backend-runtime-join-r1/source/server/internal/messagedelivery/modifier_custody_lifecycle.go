package messagedelivery

import (
	"context"
	"errors"
	"math"
	"time"

	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const unsupportedCustodySchema = "schema:unsupported-modifier-custody-v1"
const unsupportedCustodyTTL = "unsupported_modifier_absolute_deadline_v1"

func (c *OwnedUnsupportedKafkaCustody) CheckReady(ctx context.Context) error {
	if c == nil || c.store == nil || ctx == nil {
		return ErrUnprepared
	}
	if e := c.policyCurrent(); e != nil {
		return e
	}
	s := c.store
	if e := s.CheckReady(ctx); e != nil {
		return e
	}
	collections, e := s.db.ListCollections(ctx, bson.M{"name": UnsupportedCustodyCollection})
	if e != nil {
		return e
	}
	collectionCount := 0
	for collections.Next(ctx) {
		var row struct {
			Name    string   `bson:"name"`
			Type    string   `bson:"type"`
			Options bson.Raw `bson:"options"`
			Info    *struct {
				ReadOnly *bool `bson:"readOnly"`
			} `bson:"info"`
		}
		if collections.Decode(&row) != nil || row.Name != UnsupportedCustodyCollection || row.Type != "collection" || row.Info == nil || row.Info.ReadOnly == nil || *row.Info.ReadOnly {
			e = ErrUnprepared
			break
		}
		fields, err := row.Options.Elements()
		if err != nil || len(fields) != 0 {
			e = ErrUnprepared
			break
		}
		collectionCount++
	}
	if e == nil {
		e = collections.Err()
	}
	closed := collections.Close(ctx)
	if e != nil || closed != nil {
		return errors.Join(e, closed)
	}
	if collectionCount != 1 {
		return ErrUnprepared
	}
	var marker bson.Raw
	if s.db.Collection(UnsupportedCustodyCollection).FindOne(ctx, bson.M{"_id": unsupportedCustodySchema}).Decode(&marker) != nil {
		return ErrUnprepared
	}
	fields, e := marker.Elements()
	if e != nil || len(fields) != 5 {
		return ErrUnprepared
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Key()] {
			return ErrUnprepared
		}
		seen[f.Key()] = true
	}
	for key, value := range map[string]string{"_id": unsupportedCustodySchema, "kind": unsupportedCustodyKind, "epoch": s.epoch, "activation_sha256": s.activationSHA} {
		actual, ok := marker.Lookup(key).StringValueOK()
		if !ok || actual != value {
			return ErrUnprepared
		}
	}
	v, ok := marker.Lookup("version").Int32OK()
	if !ok || v != 1 {
		return ErrUnprepared
	}
	generation, e := c.store.readUnsupportedCustodyGeneration(ctx)
	if e != nil {
		return e
	}
	if generation.HeaderSHA != c.policy.HeaderSHA256 {
		return ErrConflict
	}
	cursor, e := s.db.Collection(UnsupportedCustodyCollection).Indexes().List(ctx)
	if e != nil {
		return e
	}
	count := 0
	id, ttl := false, false
	for cursor.Next(ctx) {
		var row struct {
			Name          string   `bson:"name"`
			Keys          bson.D   `bson:"key"`
			TTL           *int64   `bson:"expireAfterSeconds"`
			PrepareUnique bool     `bson:"prepareUnique"`
			Unique        bool     `bson:"unique"`
			Sparse        bool     `bson:"sparse"`
			Hidden        bool     `bson:"hidden"`
			Partial       bson.Raw `bson:"partialFilterExpression"`
			Collation     bson.Raw `bson:"collation"`
		}
		if cursor.Decode(&row) != nil || row.PrepareUnique || row.Sparse || row.Hidden || len(row.Partial) != 0 || len(row.Collation) != 0 {
			e = ErrUnprepared
			break
		}
		count++
		if row.Name == "_id_" && len(row.Keys) == 1 && row.Keys[0].Key == "_id" && one(row.Keys[0].Value) && row.TTL == nil && !id {
			id = true
			continue
		}
		if row.Name == unsupportedCustodyTTL && len(row.Keys) == 1 && row.Keys[0].Key == "deadline" && one(row.Keys[0].Value) && row.TTL != nil && *row.TTL == 0 && !row.Unique && !ttl {
			ttl = true
			continue
		}
		e = ErrUnprepared
		break
	}
	if e == nil {
		e = cursor.Err()
	}
	closed = cursor.Close(ctx)
	if e != nil || closed != nil {
		return errors.Join(e, closed)
	}
	if count != 2 || !id || !ttl {
		return ErrUnprepared
	}
	return nil
}
func (s *Store) finishUnsupportedCustody(ctx context.Context, r UnsupportedCustodyReceipt, state string) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	if state != "redacted-unapplied" && state != "expired-unresolved" || r.State != "held-no-projection" || r.Revision >= math.MaxInt64-1 {
		return ErrConflict
	}
	// Raw deletion and minimal state/hash receipt share the actual visibility
	// or expiry transaction. There is never a successful projection outcome.
	deleted, e := s.db.Collection(UnsupportedCustodyCollection).DeleteOne(ctx, bson.M{"_id": r.ID, "kind": unsupportedCustodyKind, "epoch": s.epoch, "raw_sha256": r.RawSHA})
	if e != nil {
		return e
	}
	if state == "redacted-unapplied" && time.Now().UTC().Before(r.Deadline) && deleted.DeletedCount != 1 {
		return ErrConflict
	}
	result, e := s.db.Collection(Receipts).UpdateOne(ctx, bson.M{"_id": r.ID, "kind": unsupportedCustodyKind, "epoch": s.epoch, "raw_sha256": r.RawSHA, "revision": r.Revision, "state": "held-no-projection"}, bson.M{"$set": bson.M{"state": state}, "$inc": bson.M{"revision": int64(1)}})
	if e != nil {
		return e
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}

// The deadline is a custody policy, NOT a guessed original message expiry.
// TTL additionally removes raw while an application is down; this bounded
// worker records the explicit unresolved metadata outcome while it is up.
func (c *OwnedUnsupportedKafkaCustody) Expire(ctx context.Context, limit int64) error {
	return c.expireAt(ctx, limit, time.Now().UTC())
}
func (c *OwnedUnsupportedKafkaCustody) expireAt(ctx context.Context, limit int64, now time.Time) error {
	if c == nil || c.store == nil || ctx == nil || limit < 1 || limit > 32 {
		return ErrInvalid
	}
	s := c.store
	return s.transaction(ctx, func(tx context.Context) error {
		cursor, e := s.db.Collection(Receipts).Find(tx, bson.M{"kind": unsupportedCustodyKind, "epoch": s.epoch, "state": "held-no-projection", "deadline": bson.M{"$lte": now}}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit).SetProjection(bson.M{"source_id": 1}))
		if e != nil {
			return e
		}
		var rows []struct {
			Source string `bson:"source_id"`
		}
		e = cursor.All(tx, &rows)
		closed := cursor.Close(tx)
		if e != nil || closed != nil {
			return errors.Join(e, closed)
		}
		for _, row := range rows {
			r, e := c.readReceipt(tx, row.Source)
			if e != nil {
				return e
			}
			if now.Before(r.Deadline) {
				return ErrConflict
			}
			if e = s.finishUnsupportedCustody(tx, r, "expired-unresolved"); e != nil {
				return e
			}
		}
		return nil
	})
}

// Call after the authenticated Clear and privacy-generation mutation in the
// SAME TX. A Seq0 modifier is not falsely bound to a positive target sequence.
// Exact target expiry/delete cannot be guessed from an abandoned wire DTO;
// those unknown raw records are bounded by the explicit custody deadline.
func (s *Store) redactUnsupportedCustody(ctx context.Context, change table.PendingVisibilityChange) error {
	if change.Below == 0 || change.FloorOnly {
		return nil
	}
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	if e := s.revokeUnsupportedCustodyGeneration(ctx); e != nil {
		return e
	}
	filter := bson.M{"kind": unsupportedCustodyKind, "epoch": s.epoch, "state": "held-no-projection", "source_cids": change.ConversationID}
	// No trustworthy abandoned-modifier roster exists. Any authenticated
	// Clear of this actual CID removes the WHOLE unresolved raw frame; owner
	// hashes are custody metadata, never proof that another member may retain it.
	cursor, e := s.db.Collection(Receipts).Find(ctx, filter, options.Find().SetLimit(1001).SetProjection(bson.M{"source_id": 1}))
	if e != nil {
		return e
	}
	var rows []struct {
		Source string `bson:"source_id"`
	}
	e = cursor.All(ctx, &rows)
	closed := cursor.Close(ctx)
	if e != nil || closed != nil {
		return errors.Join(e, closed)
	}
	if len(rows) > 1000 {
		return ErrRedactionNeedsBatch
	}
	c := &OwnedUnsupportedKafkaCustody{store: s}
	for _, row := range rows {
		r, e := c.readReceipt(ctx, row.Source)
		if e != nil {
			return e
		}
		if e = s.finishUnsupportedCustody(ctx, r, "redacted-unapplied"); e != nil {
			return e
		}
	}
	return nil
}
