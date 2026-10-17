package messagedelivery

import (
	"context"
	"errors"
	"math"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const unsupportedCustodyGenerationKind = "unsupported-modifier-admission-v1"

type unsupportedCustodyGeneration struct {
	ID               string `bson:"_id"`
	Kind             string `bson:"kind"`
	Epoch            string `bson:"epoch"`
	Activation       string `bson:"activation_sha256"`
	HeaderSHA        string `bson:"header_sha256"`
	InitialPolicySHA string `bson:"initial_policy_sha256"`
	HoldSHA          string `bson:"initial_hold_sha256"`
	Generation       int64  `bson:"generation"`
	Serial           int64  `bson:"serial"`
}

func (s *Store) unsupportedCustodyGenerationID() string {
	return key(s.epoch, "unsupported-modifier-admission", "global")
}
func (s *Store) readUnsupportedCustodyGeneration(ctx context.Context) (unsupportedCustodyGeneration, error) {
	var row unsupportedCustodyGeneration
	var raw bson.Raw
	if e := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": s.unsupportedCustodyGenerationID()}).Decode(&raw); e != nil {
		return row, e
	}
	fields, e := raw.Elements()
	if e != nil || len(fields) != 9 {
		return row, ErrConflict
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Key()] {
			return row, ErrConflict
		}
		seen[f.Key()] = true
	}
	for _, name := range []string{"_id", "kind", "epoch", "activation_sha256", "header_sha256", "initial_policy_sha256", "initial_hold_sha256"} {
		if _, ok := raw.Lookup(name).StringValueOK(); !ok {
			return row, ErrConflict
		}
	}
	for _, name := range []string{"generation", "serial"} {
		if _, ok := raw.Lookup(name).Int64OK(); !ok {
			return row, ErrConflict
		}
	}
	if bson.Unmarshal(raw, &row) != nil || row.ID != s.unsupportedCustodyGenerationID() || row.Kind != unsupportedCustodyGenerationKind || row.Epoch != s.epoch || row.Activation != s.activationSHA || !validSHA(row.HeaderSHA) || !validSHA(row.InitialPolicySHA) || !validSHA(row.HoldSHA) || row.Generation < 1 || row.Generation >= math.MaxInt64-1 || row.Serial < 1 || row.Serial >= math.MaxInt64-1 {
		return row, ErrConflict
	}
	return row, nil
}

// NEW custody only. The reviewed generation is immutable in the controller;
// retries may not acquire a post-Clear generation. This real write conflicts
// with every Clear even if the Clear found no raw holds to delete.
func (c *OwnedUnsupportedKafkaCustody) claimUnsupportedCustodyGeneration(ctx context.Context) error {
	s := c.store
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	row, e := s.readUnsupportedCustodyGeneration(ctx)
	if e != nil {
		return e
	}
	if row.HeaderSHA != c.policy.HeaderSHA256 || row.Generation != c.policy.AdmissionGeneration {
		return ErrUnsupportedCustodyScope
	}
	result, e := s.db.Collection(Receipts).UpdateOne(ctx, bson.M{"_id": row.ID, "kind": row.Kind, "epoch": s.epoch, "generation": row.Generation, "serial": row.Serial}, bson.M{"$inc": bson.M{"serial": int64(1)}})
	if e != nil {
		return e
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) revokeUnsupportedCustodyGeneration(ctx context.Context) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	row, e := s.readUnsupportedCustodyGeneration(ctx)
	if errors.Is(e, mongo.ErrNoDocuments) {
		// Custody has never been activated. An ordinary-only deployment need not
		// invent a modifier policy or generation. Future DDL is permitted only
		// while the actual owned Agent has excluded ALL writers.
		if err := s.db.Collection(UnsupportedCustodyCollection).FindOne(ctx, bson.M{"_id": unsupportedCustodySchema}).Err(); !errors.Is(err, mongo.ErrNoDocuments) {
			if err != nil {
				return err
			}
			return ErrUnprepared
		}
		if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"kind": unsupportedCustodyKind, "epoch": s.epoch}).Err(); !errors.Is(err, mongo.ErrNoDocuments) {
			if err != nil {
				return err
			}
			return ErrUnprepared
		}
		return nil
	}
	if e != nil {
		return e
	}
	result, e := s.db.Collection(Receipts).UpdateOne(ctx, bson.M{"_id": row.ID, "kind": row.Kind, "epoch": s.epoch, "generation": row.Generation, "serial": row.Serial}, bson.M{"$inc": bson.M{"generation": int64(1), "serial": int64(1)}})
	if e != nil {
		return e
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
