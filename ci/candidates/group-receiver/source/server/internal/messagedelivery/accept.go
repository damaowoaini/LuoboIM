package messagedelivery

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// AcceptCommitted is the owned, bounded transaction entry point for BOTH
// ordinary inputs and frozen notification branches. No HTTP/cache/push callback
// is inside transaction retries. An unknown commit never returns a success
// receipt even if the work function happened to fill its local result value.
func (s *Store) AcceptCommitted(ctx context.Context, input Envelope) (Receipt, error) {
	prepared, _, err := freezeEnvelope(input)
	if err != nil {
		return Receipt{}, err
	}
	if err := s.CheckReady(ctx); err != nil {
		return Receipt{}, err
	}
	var result Receipt
	err = s.transaction(ctx, func(tx context.Context) error { var err error; result, err = s.Accept(tx, prepared); return err })
	if err != nil {
		return Receipt{}, err
	}
	return result, nil
}

// Resolve reads the original exact minimal receipt. In particular it never
// tests visible message absence, initializes a new sequence or dispatches push.
// A not-found result is not a proof that an earlier commit rolled back: callers
// retain the SAME frozen input and source ID until the uncertainty is resolved.
func (s *Store) Resolve(ctx context.Context, input Envelope) (Receipt, error) {
	e, digest, err := freezeEnvelope(input)
	if err != nil {
		return Receipt{}, err
	}
	if ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return Receipt{}, ErrUnprepared
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return Receipt{}, err
	}
	var r Receipt
	err = s.db.Collection(Receipts).FindOne(current, bson.M{"_id": key(s.epoch, "source", e.ID)}).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Receipt{}, ErrCommitUnconfirmed
	}
	if err != nil {
		return Receipt{}, err
	}
	if err := s.validateReceipt(r, e, digest); err != nil {
		return Receipt{}, err
	}
	return r, nil
}

func (s *Store) validateReceipt(r Receipt, e Envelope, digest string) error {
	if r.ID != key(s.epoch, "source", e.ID) || r.Epoch != s.epoch || r.SourceID != e.ID || r.InputSHA256 != digest || r.AcceptedAt.IsZero() || len(r.Branches) != len(e.Branches) {
		return ErrConflict
	}
	for i, b := range r.Branches {
		original := e.Branches[i]
		if b.ID != original.ID || b.ConversationID != original.ConversationID || b.Persist != original.Persist || b.Persist && b.Seq < 1 || !b.Persist && (b.Seq != 0 || b.Hidden) {
			return ErrConflict
		}
	}
	return nil
}

// PrepareSequence is an explicit migration/new-CID action, never an Accept
// fallback. Before this is called the operator/initializer must bind the real
// drained writer set and known raw Mongo+Redis maxima into the activation plan.
func (s *Store) PrepareSequence(ctx context.Context, cid string, seed Seed) error {
	if err := s.CheckReady(ctx); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx context.Context) error { return s.InitializeSequence(tx, cid, seed) })
}
