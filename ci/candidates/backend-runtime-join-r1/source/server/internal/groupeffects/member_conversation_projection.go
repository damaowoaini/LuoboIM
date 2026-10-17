package groupeffects

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const memberConversationProjectionKind = "member-conversation-projection-v1"

type memberConversationSource struct {
	Command       groupauthority.NoticeCommandInfo `bson:"command"`
	GroupRevision int64                            `bson:"group_revision"`
	Owner         string                           `bson:"owner"`
	CID           string                           `bson:"cid"`
	Activation    string                           `bson:"activation"`
	Cut           deliveryprofile.VisibilityCut    `bson:"cut"`
}
type memberConversationProjection struct {
	ID           string                   `bson:"_id"`
	Kind         string                   `bson:"kind"`
	Source       memberConversationSource `bson:"source"`
	SourceSHA    string                   `bson:"source_sha256"`
	State        string                   `bson:"state"`
	Revision     int64                    `bson:"revision"`
	CachePending bool                     `bson:"cache_pending"`
	Outcome      string                   `bson:"outcome"`
}
type MemberConversationPending struct{ ID, Cursor string }

func memberConversationID(command, uid string) string {
	n, err := uuid.Parse(command)
	if err != nil {
		return ""
	}
	return uuid.NewSHA1(n, []byte("member-conversation-projection\x00"+uid)).String()
}
func (p memberConversationProjection) valid() bool {
	s := p.Source
	c := s.Command
	if p.Kind != memberConversationProjectionKind || !validID(c.ID) || !validID(c.Epoch) || !validIdentity(c.Actor) || !validIdentity(c.GroupID) || c.CreatedAt.IsZero() || !validIdentity(s.Owner) || s.GroupRevision < 1 || s.GroupRevision >= math.MaxInt64 || !validSHA(s.Activation) || p.ID != memberConversationID(c.ID, s.Owner) || s.CID != msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, c.GroupID) || !s.Cut.Valid() || s.Cut.ConversationID != s.CID || p.Revision < 1 || p.Revision >= math.MaxInt64 {
		return false
	}
	if c.Action != groupauthority.NoticeAction(groupauthority.MemberAdmissionJoin) && c.Action != groupauthority.NoticeAction(groupauthority.MemberAdmissionInvite) && c.Action != groupauthority.NoticeAction(groupauthority.MemberAdmissionApprove) {
		return false
	}
	owner := deliveryprofile.SHA([]byte(s.Owner))
	if len(s.Cut.Owners) != 1 || len(s.Cut.Members) != 1 || s.Cut.Notification == nil || len(s.Cut.Notification.Owners) != 1 {
		return false
	}
	if _, ok := s.Cut.Owners[owner]; !ok {
		return false
	}
	if _, ok := s.Cut.Notification.Owners[owner]; !ok {
		return false
	}
	if !validSHA(s.Cut.Members[owner]) {
		return false
	}
	body, err := json.Marshal(s)
	if err != nil || digest(body) != p.SourceSHA {
		return false
	}
	switch p.State {
	case "pending":
		return p.Revision == 1 && !p.CachePending && p.Outcome == ""
	case "projected":
		return p.Revision >= 2 && p.CachePending && p.Outcome == "applied"
	case "done":
		return p.Revision >= 2 && !p.CachePending && (p.Outcome == "stale" || p.Outcome == "applied")
	default:
		return false
	}
}

// Capability comes from the actual absent -> present write. Epoch/revision,
// privacy and owner identity are frozen before the business transaction ACK.
// One row per actual new member avoids an ever-growing shared group array.
func (s *Store) QueueMemberConversationProjections(ctx context.Context, cap *groupauthority.MemberAdmissionScope, activation string) error {
	if s == nil || cap == nil || !validSHA(activation) {
		return ErrInvalid
	}
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	if err := s.checkSchema(ctx); err != nil {
		return err
	}
	info, err := cap.Inspect(ctx, s.db)
	if err != nil {
		return err
	}
	cid := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, info.Command.GroupID)
	for _, uid := range info.NewMembers {
		cut, err := deliveryprofile.CaptureVisibilityCut(ctx, s.db, info.Command.Epoch, activation, cid, []string{uid})
		if err != nil {
			return err
		}
		source := memberConversationSource{Command: info.Command, GroupRevision: info.GroupRevision, Owner: uid, CID: cid, Activation: activation, Cut: cut}
		body, err := json.Marshal(source)
		if err != nil {
			return ErrInvalid
		}
		row := memberConversationProjection{ID: memberConversationID(info.Command.ID, uid), Kind: memberConversationProjectionKind, Source: source, SourceSHA: digest(body), State: "pending", Revision: 1}
		if !row.valid() {
			return ErrInvalid
		}
		if _, err := s.collection.InsertOne(ctx, row); err != nil {
			return err
		}
	}
	return nil
}

// Implemented by the original membership-bound Store. This interface creates
// no tuple-based capability and avoids a groupeffects/messagedelivery cycle.
type MemberAdmissionProjectionValidator interface {
	ValidateMemberAdmissionProjection(context.Context, groupauthority.NoticeCommandInfo, int64, string) (bool, error)
}

func (s *Store) readMemberProjection(ctx context.Context, id string) (memberConversationProjection, error) {
	var raw bson.Raw
	var row memberConversationProjection
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&raw); err != nil {
		return row, err
	}
	fields, err := raw.Elements()
	if err != nil || len(fields) != 8 {
		return row, ErrConflict
	}
	seen := map[string]bool{}
	for _, field := range fields {
		if seen[field.Key()] {
			return row, ErrConflict
		}
		seen[field.Key()] = true
		switch field.Key() {
		case "_id", "kind", "source_sha256", "state", "outcome":
			if _, ok := field.Value().StringValueOK(); !ok {
				return row, ErrConflict
			}
		case "revision":
			if _, ok := field.Value().Int64OK(); !ok {
				return row, ErrConflict
			}
		case "cache_pending":
			if _, ok := field.Value().BooleanOK(); !ok {
				return row, ErrConflict
			}
		case "source":
			if _, ok := field.Value().DocumentOK(); !ok {
				return row, ErrConflict
			}
		default:
			return row, ErrConflict
		}
	}
	if bson.Unmarshal(raw, &row) != nil || row.ID != id || !row.valid() {
		return row, ErrConflict
	}
	return row, nil
}

func (s *Store) memberProjectionTransaction(ctx context.Context, work func(context.Context) error) error {
	if s == nil || ctx == nil || work == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	session, err := s.db.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (any, error) {
		if err := receiverSession(tx, s.db); err != nil {
			return nil, err
		}
		if err := s.checkSchema(tx); err != nil {
			return nil, err
		}
		return nil, work(tx)
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetReadPreference(readpref.Primary()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	return err // Unknown commit stays unknown; only this original ID is inspected again.
}

func (s *Store) ListMemberConversationProjections(ctx context.Context, after string, limit int64) ([]MemberConversationPending, error) {
	if s == nil || ctx == nil || after != "" && !validID(after) || limit < 1 || limit > 64 {
		return nil, ErrInvalid
	}
	filter := bson.M{"kind": memberConversationProjectionKind, "state": bson.M{"$in": []string{"pending", "projected"}}}
	if after != "" {
		filter["_id"] = bson.M{"$gt": after}
	}
	cur, err := s.collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit).SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID string `bson:"_id"`
	}
	err = cur.All(ctx, &rows)
	closed := cur.Close(ctx)
	if err != nil || closed != nil {
		return nil, errors.Join(err, closed)
	}
	result := make([]MemberConversationPending, 0, len(rows))
	for _, row := range rows {
		if !validID(row.ID) {
			return nil, ErrConflict
		}
		result = append(result, MemberConversationPending{ID: row.ID, Cursor: row.ID})
	}
	return result, nil
}

// ACKed projection state is read BEFORE admission validation. Its successful
// commit consumes the original admission once; a retry only drains its cache
// duty, never resets max_seq again or creates a new admission/source identity.
func (s *Store) RecoverMemberConversationProjection(ctx context.Context, id string, authority MemberAdmissionProjectionValidator, invalidate func(context.Context, string, string) error) error {
	if s == nil || !validID(id) || authority == nil || invalidate == nil {
		return ErrInvalid
	}
	var result memberConversationProjection
	err := s.memberProjectionTransaction(ctx, func(tx context.Context) error {
		row, err := s.readMemberProjection(tx, id)
		if err != nil {
			return err
		}
		result = row
		if row.State != "pending" {
			return nil
		}
		source := row.Source
		denied, err := deliveryprofile.CompareBranchVisibilityCut(tx, s.db, source.Command.Epoch, source.Activation, source.Cut, source.CID, []string{source.Owner}, 0, 0, true)
		if err != nil {
			return err
		}
		allowed := false
		if !denied[source.Owner] {
			allowed, err = authority.ValidateMemberAdmissionProjection(tx, source.Command, source.GroupRevision, source.Owner)
			if err != nil {
				return err
			}
		}
		outcome, state, pending := "stale", "done", false
		if allowed {
			// Preserve existing min_seq, receive/mute/pin/privacy/retention values.
			// There is no invented server tombstone: actual clear/delete protection
			// is the original cut plus the same-TX owner admission validation.
			filter := bson.M{"owner_user_id": source.Owner, "conversation_id": source.CID}
			cur, err := s.db.Collection("conversation").Find(tx, filter, options.Find().SetLimit(2))
			if err != nil {
				return err
			}
			var rows []relation.ConversationModel
			err = cur.All(tx, &rows)
			closed := cur.Close(tx)
			if err != nil || closed != nil {
				return errors.Join(err, closed)
			}
			if len(rows) > 1 {
				return ErrConflict
			}
			if len(rows) == 0 {
				doc, err := retention.PrepareDocument(&relation.ConversationModel{OwnerUserID: source.Owner, ConversationID: source.CID, ConversationType: constant.SuperGroupChatType, GroupID: source.Command.GroupID})
				if err != nil {
					return err
				}
				if err = retention.InsertPrepared(tx, s.db.Collection("conversation"), []any{doc}); err != nil {
					return err
				}
			} else {
				old := rows[0]
				if old.OwnerUserID != source.Owner || old.ConversationID != source.CID || old.ConversationType != constant.SuperGroupChatType || old.GroupID != source.Command.GroupID || old.UserID != "" || old.MaxSeq < 0 || old.MinSeq < 0 {
					return ErrConflict
				}
				update, err := s.db.Collection("conversation").UpdateOne(tx, filter, bson.M{"$set": bson.M{"max_seq": int64(0)}})
				if err != nil {
					return err
				}
				if update.MatchedCount != 1 {
					return ErrConflict
				}
			}
			outcome, state, pending = "applied", "projected", true
		}
		update, err := s.collection.UpdateOne(tx, bson.M{"_id": id, "source_sha256": row.SourceSHA, "state": "pending", "revision": row.Revision}, bson.M{"$set": bson.M{"state": state, "outcome": outcome, "cache_pending": pending}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if update.MatchedCount != 1 || update.ModifiedCount != 1 {
			return ErrConflict
		}
		row.State, row.Outcome, row.CachePending, row.Revision = state, outcome, pending, row.Revision+1
		result = row
		return nil
	})
	if err != nil {
		return err
	}
	if !result.CachePending {
		return nil
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = invalidate(bounded, result.Source.Owner, result.Source.CID)
	cancel()
	if err != nil {
		return err
	}
	return s.memberProjectionTransaction(ctx, func(tx context.Context) error {
		row, err := s.readMemberProjection(tx, id)
		if err != nil {
			return err
		}
		if row.SourceSHA != result.SourceSHA {
			return ErrConflict
		}
		if row.State == "done" {
			return nil
		}
		if row.State != "projected" || !row.CachePending {
			return ErrConflict
		}
		update, err := s.collection.UpdateOne(tx, bson.M{"_id": id, "source_sha256": row.SourceSHA, "state": "projected", "revision": row.Revision}, bson.M{"$set": bson.M{"state": "done", "cache_pending": false}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if update.MatchedCount != 1 || update.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
}
