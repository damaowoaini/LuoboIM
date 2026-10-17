package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Global clear/floor cancels pending source plaintext in the SAME transaction.
// It neither waits for unknown external After nor replays a business callback.
// Owner-specific clears retain body for the other authorized owners and bind
// the denied owner through the cut; source read cannot restore that owner.
func (s *Store) redactOwnedPendingSources(ctx context.Context, c table.PendingVisibilityChange) error {
	if c.Below == 0 || len(c.Owners) > 0 {
		return nil
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return err
	}
	cursor, err := s.db.Collection(groupeffects.Collection).Find(ctx, bson.M{"kind": groupeffects.Notification, "state": groupeffects.Pending, "producer_version": bson.M{"$in": bson.A{"group-action-v1", "group-role-v1"}}, "$or": bson.A{bson.M{"owned_visibility.conversation_id": c.ConversationID}, bson.M{"owned_visibility.notification.conversation_id": c.ConversationID}}}, options.Find().SetLimit(1001).SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	var ids []struct {
		ID string `bson:"_id"`
	}
	err = cursor.All(ctx, &ids)
	closeErr := cursor.Close(ctx)
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	if len(ids) > 1000 {
		return ErrRedactionNeedsBatch
	}
	for _, id := range ids {
		cap, err := effects.ReadOwnedNotificationForRedaction(ctx, id.ID)
		if err != nil {
			return err
		}
		f, t, p, sha, producer := cap.Snapshot()
		in := ownedActionInput{store: s, effects: effects, cap: cap, frozen: f, targets: t, profile: p, profileSHA: sha, producer: producer}
		_, all, err := in.denied(ctx, true)
		if err != nil {
			return err
		}
		if !all {
			continue
		}
		r, exists, err := s.ownedAccepted(ctx, id.ID)
		if err != nil {
			return err
		}
		if !exists {
			r, err = s.acceptOwnedAction(ctx, in)
			if err != nil {
				return err
			}
		}
		body, err := json.Marshal(r)
		if err != nil {
			return ErrConflict
		}
		if err = effects.AcknowledgeOwnedNotificationDelivery(ctx, cap, s.epoch, r.InputSHA256, hash(body)); err != nil {
			return err
		}
	}
	return nil
}
