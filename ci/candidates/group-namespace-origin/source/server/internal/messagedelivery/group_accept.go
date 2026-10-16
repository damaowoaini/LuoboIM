package messagedelivery

import (
	"context"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// AcceptGroupNotificationCommitted accepts ONLY an existing typed durable row
// from this database. Caller-supplied payload/recipient/opuser values are not
// arguments. The caller uses its managed internal worker, not a public RPC.
func (s *Store) AcceptGroupNotificationCommitted(ctx context.Context, effectID string) (Receipt, error) {
	if s == nil || ctx == nil || !validUUID(effectID) || mongo.SessionFromContext(ctx) != nil {
		return Receipt{}, ErrInvalid
	}
	if err := s.CheckReady(ctx); err != nil {
		return Receipt{}, err
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return Receipt{}, err
	}
	if err := effects.CheckReady(ctx); err != nil {
		return Receipt{}, err
	}
	var result Receipt
	err = s.transaction(ctx, func(tx context.Context) error {
		cap, err := effects.ReadNotificationDelivery(tx, effectID)
		if err != nil {
			return err
		}
		done, epoch, digest, receiptSHA := cap.Accepted()
		if done {
			if epoch != s.epoch || !validSHA(digest) {
				return ErrConflict
			}
			if err := s.db.Collection(Receipts).FindOne(tx, bson.M{"_id": key(s.epoch, "source", effectID)}).Decode(&result); err != nil {
				return err
			}
			body, err := json.Marshal(result)
			if err != nil || hash(body) != receiptSHA {
				return ErrConflict
			}
			if result.ID != key(s.epoch, "source", effectID) || result.SourceID != effectID || result.Epoch != s.epoch || result.InputSHA256 != digest || result.AcceptedAt.IsZero() || len(result.Branches) < 1 || len(result.Branches) > 2 {
				return ErrConflict
			}
			for _, b := range result.Branches {
				if !validUUID(b.ID) || !identity(b.ConversationID) || !b.Persist || b.Seq < 1 {
					return ErrConflict
				}
			}
			return nil // No source reconstruction, new seq, cache or push on replay.
		}
		frozen, targets := cap.Snapshot()
		plans, err := groupeffects.PlanNotificationBranches(frozen)
		if err != nil {
			return err
		}
		rosters := map[string][]string{}
		for _, target := range targets {
			rosters[target.BranchID] = target.UserIDs
		}
		envelope := Envelope{ID: effectID}
		for _, plan := range plans {
			if !plan.Persist {
				return ErrUnprepared
			} // Seq0 reliable replay is prohibited.
			roster, ok := rosters[plan.ID]
			if !ok {
				return ErrConflict
			}
			envelope.Branches = append(envelope.Branches, Branch{ID: plan.ID, ConversationID: plan.ConversationID, Payload: plan.Payload, Recipients: roster, Persist: true})
		}
		_, inputSHA, err := freezeEnvelope(envelope)
		if err != nil {
			return err
		}
		result, err = s.acceptSourceEnvelope(tx, envelope, false, nil)
		if err != nil {
			return err
		}
		receiptBody, err := json.Marshal(result)
		if err != nil {
			return ErrConflict
		}
		return effects.AcknowledgeNotificationDelivery(tx, cap, s.epoch, inputSHA, hash(receiptBody))
	})
	if err != nil {
		return Receipt{}, err
	} // Never return abandoned local result on unknown commit.
	return result, nil
}
