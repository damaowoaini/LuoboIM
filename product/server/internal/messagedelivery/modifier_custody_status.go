package messagedelivery

import (
	"context"
	"go.mongodb.org/mongo-driver/bson"
	"time"
)

type UnsupportedCustodySummary struct {
	Held, RedactedUnapplied, ExpiredUnresolved int64
	ProjectionApplied                          bool
}

// A separate visible unresolved obligation, not the ordinary delivery backlog
// and not a declaration that a clean consumer Stop applied old modifiers.
// This metadata query works even if no current raw-custody policy is enabled.
func (s *Store) UnsupportedCustodySummary(ctx context.Context) (UnsupportedCustodySummary, error) {
	if s == nil || ctx == nil {
		return UnsupportedCustodySummary{}, ErrInvalid
	}
	if e := s.CheckReady(ctx); e != nil {
		return UnsupportedCustodySummary{}, e
	}
	now := time.Now().UTC()
	filter := func(state string) bson.M {
		return bson.M{"kind": unsupportedCustodyKind, "epoch": s.epoch, "state": state}
	}
	live := filter("held-no-projection")
	live["deadline"] = bson.M{"$gt": now}
	held, e := s.db.Collection(Receipts).CountDocuments(ctx, live)
	if e != nil {
		return UnsupportedCustodySummary{}, e
	}
	redacted, e := s.db.Collection(Receipts).CountDocuments(ctx, filter("redacted-unapplied"))
	if e != nil {
		return UnsupportedCustodySummary{}, e
	}
	expired := bson.M{"kind": unsupportedCustodyKind, "epoch": s.epoch, "$or": bson.A{bson.M{"state": "expired-unresolved"}, bson.M{"state": "held-no-projection", "deadline": bson.M{"$lte": now}}}}
	count, e := s.db.Collection(Receipts).CountDocuments(ctx, expired)
	if e != nil {
		return UnsupportedCustodySummary{}, e
	}
	return UnsupportedCustodySummary{Held: held, RedactedUnapplied: redacted, ExpiredUnresolved: count, ProjectionApplied: false}, nil
}
