package messagedelivery

import (
	"context"
	"encoding/json"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// GroupEnvelopeProof is pure. It confers no accepted-source or recipient
// authority; Queue/Accept still require the typed source and private scope.
func GroupEnvelopeProof(f groupeffects.Frozen, targets []groupeffects.NotificationTargets) (Envelope, string, error) {
	plans, err := groupeffects.PlanNotificationBranches(f)
	if err != nil {
		return Envelope{}, "", err
	}
	if len(plans) != len(targets) {
		return Envelope{}, "", ErrInvalid
	}
	rosters := map[string][]string{}
	for _, t := range targets {
		if !validUUID(t.BranchID) {
			return Envelope{}, "", ErrInvalid
		}
		if _, ok := rosters[t.BranchID]; ok {
			return Envelope{}, "", ErrInvalid
		}
		rosters[t.BranchID] = append([]string(nil), t.UserIDs...)
	}
	e := Envelope{ID: f.ID}
	for _, p := range plans {
		roster, ok := rosters[p.ID]
		if !ok || !p.Persist {
			return Envelope{}, "", ErrUnprepared
		}
		e.Branches = append(e.Branches, Branch{ID: p.ID, ConversationID: p.ConversationID, Payload: p.Payload, Recipients: roster, Persist: true})
	}
	return freezeEnvelope(e)
}

// ReadSourceReceipt checks the exact immutable proof frozen in the parent
// notification. It reads no message body/cache and cannot allocate or push.
// A strong outer session can join a typed cleanup transaction; weaker/bare
// sessions cannot turn a snapshot from another client into commit proof.
func (s *Store) ReadSourceReceipt(ctx context.Context, sourceID, inputSHA, receiptSHA string) (Receipt, error) {
	if s == nil || ctx == nil || !validUUID(sourceID) || !validSHA(inputSHA) || !validSHA(receiptSHA) {
		return Receipt{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if mongo.SessionFromContext(current) == nil {
		if err := s.CheckReady(current); err != nil {
			return Receipt{}, err
		}
	} else if err := s.requireTX(current); err != nil {
		return Receipt{}, err
	}
	var r Receipt
	if err := s.db.Collection(Receipts).FindOne(current, bson.M{"_id": key(s.epoch, "source", sourceID)}).Decode(&r); err != nil {
		return Receipt{}, err
	}
	body, err := json.Marshal(r)
	if err != nil || hash(body) != receiptSHA || r.ID != key(s.epoch, "source", sourceID) || r.Epoch != s.epoch || r.SourceID != sourceID || r.InputSHA256 != inputSHA || r.AcceptedAt.IsZero() || len(r.Branches) < 1 || len(r.Branches) > 2 {
		return Receipt{}, ErrConflict
	}
	seen := map[string]bool{}
	for _, b := range r.Branches {
		if !validUUID(b.ID) || seen[b.ID] || !identity(b.ConversationID) || !b.Persist || b.Seq < 1 {
			return Receipt{}, ErrConflict
		}
		seen[b.ID] = true
	}
	return r, nil
}
