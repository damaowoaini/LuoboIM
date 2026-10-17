package messagedelivery

import (
	"context"
	"errors"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"math"
)

// Called in the source Clear/retention transaction after its exact cut update.
// The extra scan covers positive n_ duties as well as Seq0. It never blindly
// suppresses newer retained messages: Compare uses actual accepted sequence,
// or the immutable shared normal head+1 only when no normal branch was stored.
func (s *Store) redactOwnedDeliveryCuts(ctx context.Context, c table.PendingVisibilityChange) error {
	filter := bson.M{"epoch": s.epoch, "producer_version": ownedGroupProducer, "state": "ready", "hidden": false, "$or": bson.A{bson.M{"owned_visibility.conversation_id": c.ConversationID}, bson.M{"owned_visibility.notification.conversation_id": c.ConversationID}}}
	cursor, err := s.db.Collection(Effects).Find(ctx, filter, options.Find().SetLimit(1001).SetProjection(bson.M{"branch_id": 1}))
	if err != nil {
		return err
	}
	var ids []struct {
		Branch string `bson:"branch_id"`
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
		d, err := s.readDutyRecord(ctx, id.Branch)
		if err != nil {
			return err
		}
		if d.ActionCut == nil || d.Revision >= math.MaxInt64-1 {
			return ErrConflict
		}
		denied := map[string]bool{}
		for _, h := range d.VisibilityDenied {
			denied[h] = true
		}
		for start := 0; start < len(d.Recipients); start += 64 {
			page := d.Recipients[start:min(start+64, len(d.Recipients))]
			result, err := s.compareOwnedDutyVisibility(ctx, d, page, false)
			if err != nil {
				return err
			}
			for uid, hidden := range result {
				if hidden {
					denied[hash([]byte(uid))] = true
				}
			}
		}
		all := len(d.Recipients) > 0
		for _, uid := range d.Recipients {
			all = all && denied[hash([]byte(uid))]
		}
		exact := bson.M{"_id": d.ID, "revision": d.Revision, "payload_sha256": d.PayloadSHA}
		if all {
			if err := s.retirePayload(ctx, exact); err != nil {
				return err
			}
			continue
		}
		if len(denied) == len(d.VisibilityDenied) {
			continue
		}
		values := make([]string, 0, len(denied))
		for h := range denied {
			values = append(values, h)
		}
		updated, err := s.db.Collection(Effects).UpdateOne(ctx, exact, bson.M{"$set": bson.M{"visibility_denied": values}, "$unset": onlineClaimFields(), "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if updated.MatchedCount != 1 || updated.ModifiedCount != 1 {
			return ErrConflict
		}
	}
	return nil
}
