package messagedelivery

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

var ErrRedactionNeedsBatch = errors.New("pending owner redaction exceeds transaction page; controlled drain required")

// MutateMessageVisibility implements the opt-in DAO/retention hook. The
// original authenticated source writer and pending-duty redaction commit in
// one strong transaction. This hook grants no actor/group authorization.
// Activation must bind ALL legacy visibility writers; an old unbound binary
// cannot be made safe by merely installing the new effects collection.
func (s *Store) MutateMessageVisibility(ctx context.Context, db *mongo.Database, changes []table.PendingVisibilityChange, work func(context.Context) error) error {
	if s == nil || s.db == nil || db == nil || db.Client() != s.db.Client() || db.Name() != s.db.Name() || ctx == nil || work == nil || len(changes) > 10000 {
		return ErrUnprepared
	}
	prepared := make([]table.PendingVisibilityChange, len(changes))
	for i, c := range changes {
		if !identity(c.ConversationID) || len(c.Owners) > 50000 || len(c.Sequences) > 10000 || c.Below < 0 || c.Below > 0 && len(c.Sequences) > 0 || c.Below == 0 && len(c.Sequences) == 0 {
			return ErrInvalid
		}
		c.Owners = append([]string(nil), c.Owners...)
		c.Sequences = append([]int64(nil), c.Sequences...)
		seen := map[string]bool{}
		for _, uid := range c.Owners {
			if !identity(uid) || seen[uid] {
				return ErrInvalid
			}
			seen[uid] = true
		}
		for _, seq := range c.Sequences {
			if seq < 1 {
				return ErrInvalid
			}
		}
		prepared[i] = c
	}
	apply := func(tx context.Context) error {
		if err := s.requireTX(tx); err != nil {
			return err
		}
		if err := work(tx); err != nil {
			return err
		}
		for _, c := range prepared {
			filter := bson.M{"epoch": s.epoch, "conversation_id": c.ConversationID, "persist": true, "state": "ready"}
			if c.Below > 0 {
				filter["seq"] = bson.M{"$lt": c.Below}
			} else {
				filter["seq"] = bson.M{"$in": c.Sequences}
			}
			if len(c.Owners) == 0 {
				if err := s.retirePayload(tx, filter); err != nil {
					return err
				}
				continue
			}
			// Bound the owner-specific materialization. Refuse a too-large TX;
			// never silently advance the floor after retiring only a prefix.
			cursor, err := s.db.Collection(Effects).Find(tx, filter, options.Find().SetLimit(1001).SetProjection(bson.M{"branch_id": 1}))
			if err != nil {
				return err
			}
			var ids []struct {
				Branch string `bson:"branch_id"`
			}
			err = cursor.All(tx, &ids)
			closeErr := cursor.Close(tx)
			if err != nil || closeErr != nil {
				return errors.Join(err, closeErr)
			}
			if len(ids) > 1000 {
				return ErrRedactionNeedsBatch
			}
			for _, id := range ids {
				d, err := s.readDuty(tx, id.Branch)
				if err != nil {
					return err
				}
				denied := map[string]bool{}
				for _, h := range d.VisibilityDenied {
					if !validSHA(h) {
						return ErrConflict
					}
					denied[h] = true
				}
				for _, uid := range c.Owners {
					denied[hash([]byte(uid))] = true
				}
				exact := bson.M{"_id": d.ID, "epoch": s.epoch, "revision": d.Revision, "payload_sha256": d.PayloadSHA}
				all := len(d.Recipients) > 0
				for _, uid := range d.Recipients {
					if !denied[hash([]byte(uid))] {
						all = false
					}
				}
				if all {
					if err := s.retirePayload(tx, exact); err != nil {
						return err
					}
					continue
				}
				values := make([]string, 0, len(denied))
				for h := range denied {
					values = append(values, h)
				}
				result, err := s.db.Collection(Effects).UpdateOne(tx, exact, bson.M{"$set": bson.M{"visibility_denied": values}, "$unset": onlineClaimFields(), "$inc": bson.M{"revision": int64(1)}})
				if err != nil {
					return err
				}
				if result.MatchedCount != 1 || result.ModifiedCount != 1 {
					return ErrConflict
				}
			}
		}
		return nil
	}
	if mongo.SessionFromContext(ctx) != nil {
		if _, ok := ctx.Deadline(); !ok {
			return ErrUnprepared
		}
		return apply(ctx)
	}
	if err := s.CheckReady(ctx); err != nil {
		return err
	}
	return s.transaction(ctx, apply)
}

func onlineClaimFields() bson.M {
	return bson.M{"online_claim": "", "online_instance": "", "online_lease_until": ""}
}

// Only the duty is retired: history visibility belongs to the original writer.
// Minimal source/branch/payload hashes and seq remain resolvable after deletion.
// Max projection may still advance a non-sensitive counter; no body is cached.
func (s *Store) retirePayload(ctx context.Context, filter bson.M) error {
	// A malformed/overflowed record must abort the source transaction rather
	// than be silently skipped by a narrower update filter.
	bad := bson.M{"$and": bson.A{filter, bson.M{"$or": bson.A{bson.M{"revision": bson.M{"$not": bson.M{"$type": "long"}}}, bson.M{"revision": bson.M{"$lt": int64(1)}}, bson.M{"revision": bson.M{"$gte": int64(math.MaxInt64)}}}}}}
	if err := s.db.Collection(Effects).FindOne(ctx, bad, options.FindOne().SetProjection(bson.M{"_id": 1})).Err(); err == nil {
		return ErrConflict
	} else if !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	unset := onlineClaimFields()
	for _, field := range []string{"payload", "recipients", "online_acks", "visibility_denied"} {
		unset[field] = ""
	}
	// Pipeline evaluates the old max_pending value once, then erases the body
	// and claim in the same document update. No second old-revision filter.
	unsetFields := bson.A{}
	for field := range unset {
		unsetFields = append(unsetFields, field)
	}
	_, err := s.db.Collection(Effects).UpdateMany(ctx, filter, bson.A{
		bson.M{"$set": bson.M{"hidden": true, "visibility_revoked": true, "push_pending": false, "online_pending": false, "offline_pending": false, "sender_read_pending": false, "state": bson.M{"$cond": bson.A{bson.M{"$ifNull": bson.A{"$max_pending", false}}, "ready", "done"}}, "revision": bson.M{"$add": bson.A{"$revision", int64(1)}}}},
		bson.M{"$unset": unsetFields},
	})
	return err
}

// RefreshVisibility is called before claiming EVERY bounded online page,
// including after a restart/unknown ACK. It reads primary Mongo source slots
// and owner/global floors in one snapshot and erases newly hidden payloads.
// A stale Redis body cannot serve as existence or visibility proof here.
func (s *Store) RefreshVisibility(ctx context.Context, branchID string) error {
	if ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnprepared
	}
	if err := s.CheckReady(ctx); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx context.Context) error {
		d, err := s.readDuty(tx, branchID)
		if err != nil {
			return err
		}
		if !d.OnlinePending {
			return nil
		}
		if d.OnlineClaim != "" && time.Now().Before(d.OnlineLeaseUntil) {
			return ErrOnlineBusy
		}
		if !d.Persist || d.Seq < 1 || d.Seq == math.MaxInt64 {
			return ErrUnprepared
		}
		var row table.MsgDocModel
		model := table.MsgDocModel{}
		err = s.db.Collection("msg").FindOne(tx, bson.M{"doc_id": model.GetDocID(d.CID, d.Seq)}).Decode(&row)
		absent := errors.Is(err, mongo.ErrNoDocuments)
		if err != nil && !absent {
			return err
		}
		index := model.GetMsgIndex(d.Seq)
		var slot *table.MsgInfoModel
		if !absent && index >= 0 && index < int64(len(row.Msg)) {
			slot = row.Msg[index]
		}
		filter := bson.M{"_id": d.ID, "epoch": s.epoch, "revision": d.Revision, "payload_sha256": d.PayloadSHA}
		if absent || slot == nil || slot.Msg == nil || slot.GlobalDeleted || slot.Revoke != nil {
			return s.retirePayload(tx, filter)
		}
		var frozen sdkws.MsgData
		if proto.Unmarshal(d.Payload, &frozen) != nil {
			return ErrConflict
		}
		source := convert.MsgDB2Pb(slot.Msg)
		source.IsRead = false
		frozen.IsRead = false
		if !proto.Equal(source, &frozen) {
			return ErrConflict
		}
		end := min(d.OnlineCursor+64, d.RecipientCount)
		if end < d.OnlineCursor || end > int64(len(d.Recipients)) {
			return ErrConflict
		}
		page := d.Recipients[d.OnlineCursor:end]
		keys := []table.MessageVisibilityFloor{{ConversationID: d.CID}}
		for _, uid := range page {
			keys = append(keys, table.MessageVisibilityFloor{ConversationID: d.CID, UserID: uid})
		}
		floors, err := unrelation.NewMsgMongoDriver(s.db).ReadMessageVisibility(tx, keys)
		if err != nil {
			return err
		}
		denied := map[string]bool{}
		for _, v := range d.VisibilityDenied {
			if !validSHA(v) {
				return ErrConflict
			}
			denied[v] = true
		}
		for _, f := range floors {
			if f.MinSeq <= d.Seq {
				continue
			}
			if f.UserID == "" {
				return s.retirePayload(tx, filter)
			}
			denied[hash([]byte(f.UserID))] = true
		}
		for _, uid := range slot.DelList {
			denied[hash([]byte(uid))] = true
		}
		all := true
		for _, uid := range d.Recipients {
			if !denied[hash([]byte(uid))] {
				all = false
			}
		}
		if all {
			return s.retirePayload(tx, filter)
		}
		values := make([]string, 0, len(denied))
		for v := range denied {
			values = append(values, v)
		}
		// Reads alone do not serialize with a deletion. The new DAO hook modifies
		// this SAME duty; this write conflicts with an in-flight tombstone/floor TX.
		res, err := s.db.Collection(Effects).UpdateOne(tx, filter, bson.M{"$set": bson.M{"visibility_denied": values}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if res.MatchedCount != 1 || res.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
}

// checkOnlineClaim is a final admission read, not an atomic network revoke.
// A deletion committed before it invalidates the capability and sends zero.
// The remaining read->socket interval requires the planned gateway-side
// admission/drain protocol; this WIP does NOT claim linearizable remote erase.
func (a *onlineAttempt) checkOnlineClaim(ctx context.Context) error {
	current, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	d, err := a.store.readDuty(current, a.duty.BranchID)
	if err != nil {
		return err
	}
	if d.Revision != a.duty.Revision || !d.OnlinePending || d.Hidden || d.OnlineClaim != a.claim || d.OnlineInstance != a.store.instanceID {
		return ErrConflict
	}
	return nil
}
