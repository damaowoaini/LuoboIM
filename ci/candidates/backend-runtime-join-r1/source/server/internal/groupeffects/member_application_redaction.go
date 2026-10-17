package groupeffects

import (
	"context"
	"errors"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var ErrMemberApplicationRedactionNeedsBatch = errors.New("pending member application source redaction needs controlled drain")

// Same authenticated visibility TX after its original policy/cut mutation.
// Owner-specific Clear does not erase another still-authorized recipient's
// source. Once every planned branch denies THIS exact recipient, erase raw
// payload while retaining metadata/hash/cut/claim for a late broker receipt.
func (s *Store) RedactMemberApplicationSources(ctx context.Context, change table.PendingVisibilityChange) error {
	if s == nil || ctx == nil || !validIdentity(change.ConversationID) {
		return ErrInvalid
	}
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	if err := s.checkSchema(ctx); err != nil {
		return err
	}
	filter := bson.M{"kind": memberApplicationDirectKind, "redacted": false, "source.cids": change.ConversationID}
	cur, err := s.collection.Find(ctx, filter, options.Find().SetLimit(1001).SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	var ids []struct {
		ID string `bson:"_id"`
	}
	err = cur.All(ctx, &ids)
	closed := cur.Close(ctx)
	if err != nil || closed != nil {
		return errors.Join(err, closed)
	}
	if len(ids) > 1000 {
		return ErrMemberApplicationRedactionNeedsBatch
	}
	for _, id := range ids {
		row, err := s.readMemberApplicationDirect(ctx, id.ID)
		if err != nil {
			return err
		}
		if row.Redacted {
			continue
		}
		message, err := row.message()
		if err != nil {
			return err
		}
		allDenied := true
		for _, branch := range memberApplicationBranches(message) {
			cut, ok := row.Source.Cuts[msgprocessor.GetConversationIDByMsg(branch)]
			if !ok {
				return ErrConflict
			}
			denied, err := deliveryprofile.CompareOrdinaryVisibilityCut(ctx, s.db, row.Source.Proof.Command.Epoch, row.Source.Activation, cut, branch, []string{row.Source.Proof.Recipient}, 0, true)
			if err != nil {
				return err
			}
			if !denied[row.Source.Proof.Recipient] {
				allDenied = false
			}
		}
		if !allDenied {
			continue
		}
		state := row.State
		if state == "pending" {
			state = "suppressed"
		}
		if state == "dispatching" {
			state = "unknown"
		}
		update, err := s.collection.UpdateOne(ctx, bson.M{"_id": row.ID, "source_sha256": row.SourceSHA, "revision": row.Revision, "redacted": false}, bson.M{"$set": bson.M{"state": state, "redacted": true, "payload": []byte{}}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if update.MatchedCount != 1 || update.ModifiedCount != 1 {
			return ErrConflict
		}
	}
	return nil
}
