package groupcleanup

import (
	"context"
	"errors"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type Store struct {
	db                   *mongo.Database
	authority            *groupauthority.Store
	delivery             *messagedelivery.Store
	effects              *groupeffects.Store
	epoch, activationSHA string
}

func NewStore(db *mongo.Database, epoch, activationSHA string) (*Store, error) {
	if db == nil || db.Client() == nil || db.Name() == "" || !validID(epoch) || !validSHA(activationSHA) {
		return nil, ErrInvalid
	}
	primary := db.Client().Database(db.Name(), options.Database().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	authority, err := groupauthority.NewStore(primary)
	if err != nil {
		return nil, err
	}
	delivery, err := messagedelivery.NewStore(primary, epoch, activationSHA)
	if err != nil {
		return nil, err
	}
	effects, err := groupeffects.NewStore(primary)
	if err != nil {
		return nil, err
	}
	return &Store{db: primary, authority: authority, delivery: delivery, effects: effects, epoch: epoch, activationSHA: activationSHA}, nil
}
func (s *Store) read(ctx context.Context, id string) (obligation, error) {
	var row obligation
	if s == nil || ctx == nil || !validID(id) {
		return row, ErrInvalid
	}
	if err := s.db.Collection(Collection).FindOne(ctx, bson.M{"_id": id}).Decode(&row); err != nil {
		return row, err
	}
	if !row.valid(s.epoch) {
		return obligation{}, ErrConflict
	}
	return row, nil
}
func (s *Store) members(ctx context.Context, gid string) ([]string, error) {
	cursor, err := s.db.Collection("group_member").Find(ctx, bson.M{"group_id": gid}, options.Find().SetProjection(bson.M{"user_id": 1}).SetLimit(50001))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		UserID string `bson:"user_id"`
	}
	err = cursor.All(ctx, &rows)
	closeErr := cursor.Close(ctx)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	var result []string
	for _, r := range rows {
		result = append(result, r.UserID)
	}
	result, _, err = canonicalMembers(result)
	return result, err
}
func (s *Store) dismissed(ctx context.Context, gid string) error {
	var row struct {
		GroupID string `bson:"group_id"`
		Status  *int32 `bson:"status"`
	}
	if err := s.db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Decode(&row); err != nil {
		return err
	}
	if row.GroupID != gid || row.Status == nil || *row.Status != constant.GroupStatusDismissed {
		return ErrConflict
	}
	return nil
}

// Queue executes after soft dismissal and frozen notification targets inside
// the SAME private AllMembers group transaction. It snapshots the authoritative
// roster, never asks a later cache which former users should receive a notice.
func (s *Store) Queue(ctx context.Context, intent Intent) error {
	if s == nil || ctx == nil || !intent.valid() || mcontext.GetOpUserID(ctx) != intent.Actor {
		return ErrInvalid
	}
	if err := groupauthority.RequireAllMembers(ctx, s.db, intent.GroupID); err != nil {
		return err
	}
	if err := s.checkSchema(ctx); err != nil {
		return err
	}
	old, err := s.read(ctx, intent.ID)
	if err == nil {
		if old.Intent != intent {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	if err := s.dismissed(ctx, intent.GroupID); err != nil {
		return err
	}
	cap, err := s.effects.ReadNotificationDelivery(ctx, intent.SourceEffectID)
	if err != nil {
		return err
	}
	done, _, _, _ := cap.Accepted()
	if done {
		return ErrConflict
	}
	frozen, targets := cap.Snapshot()
	expected, err := IntentFromDismissNotification(frozen)
	if err != nil || expected != intent {
		return ErrConflict
	}
	envelope, inputSHA, err := messagedelivery.GroupEnvelopeProof(frozen, targets)
	if err != nil {
		return err
	}
	members, err := s.members(ctx, intent.GroupID)
	if err != nil {
		return err
	}
	_, memberSHA, err := canonicalMembers(members)
	if err != nil {
		return err
	}
	row := obligation{ID: intent.ID, Epoch: s.epoch, Intent: intent, InputSHA: inputSHA, Members: members, MemberSHA: memberSHA, MemberCount: int64(len(members)), State: "pending", Revision: 1}
	for _, b := range envelope.Branches {
		row.Branches = append(row.Branches, branchProof{ID: b.ID, CID: b.ConversationID})
	}
	row.FrozenSHA = row.proof()
	if !row.valid(s.epoch) {
		return ErrInvalid
	}
	_, err = s.db.Collection(Collection).InsertOne(ctx, row)
	return err
}

var errAlreadyDone = errors.New("exact cleanup already committed")

// RecoverDismiss performs only the frozen cleanup. It never calls DismissGroup
// RPC, GetConversationMaxSeq, business callbacks, gateway or offline vendors.
// Known receiver acceptance is enough: online/offline delivery uses its frozen
// recipients independently of a later member-cache removal.
//
// The returned cache capability exists ONLY on known commit. Redis failure
// afterwards leaves the foundation's persistent exact cache job. Re-reading a
// completed cleanup does not repeat membership-history writes or notifications.
func (s *Store) RecoverDismiss(ctx context.Context, id string, model relation.GroupMemberModelInterface) (*groupauthority.Commit, error) {
	if s == nil || ctx == nil || model == nil || mongo.SessionFromContext(ctx) != nil {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return nil, err
	}
	row, err := s.read(current, id)
	if err != nil {
		return nil, err
	}
	if row.State == "done" {
		return nil, nil
	}
	completedAt := time.Now().UTC().Truncate(time.Millisecond)
	commit, err := s.authority.Mutation(current, []groupauthority.Target{{GroupID: row.Intent.GroupID, AllMembers: true, Members: append([]string(nil), row.Members...)}}, func(tx context.Context) error {
		if err := s.checkSchema(tx); err != nil {
			return err
		}
		latest, err := s.read(tx, id)
		if err != nil {
			return err
		}
		if latest.State == "done" {
			return errAlreadyDone
		}
		if latest.FrozenSHA != row.FrozenSHA {
			return ErrConflict
		}
		if err := s.dismissed(tx, row.Intent.GroupID); err != nil {
			return err
		}
		cap, err := s.effects.ReadNotificationDelivery(tx, row.Intent.SourceEffectID)
		if err != nil {
			return err
		}
		done, epoch, inputSHA, receiptSHA := cap.Accepted()
		if !done {
			return ErrDependency
		}
		frozen, _ := cap.Snapshot()
		if epoch != s.epoch || inputSHA != row.InputSHA || frozen.GroupID != row.Intent.GroupID || frozen.Actor != row.Intent.Actor || frozen.PayloadSHA256 != row.Intent.SourcePayloadSHA {
			return ErrConflict
		}
		receipt, err := s.delivery.ReadSourceReceipt(tx, row.Intent.SourceEffectID, inputSHA, receiptSHA)
		if err != nil {
			return err
		}
		if len(receipt.Branches) != len(row.Branches) {
			return ErrConflict
		}
		for i, b := range receipt.Branches {
			if b.ID != row.Branches[i].ID || b.ConversationID != row.Branches[i].CID || !b.Persist || b.Seq < 1 {
				return ErrConflict
			}
		}
		actual, err := s.members(tx, row.Intent.GroupID)
		if err != nil {
			return err
		}
		allowed := map[string]bool{}
		for _, uid := range row.Members {
			allowed[uid] = true
		}
		// A legitimate quit after soft dismissal may have removed a member. It
		// must not strand cleanup; a new/foreign member is never silently removed.
		for _, uid := range actual {
			if !allowed[uid] {
				return ErrConflict
			}
		}
		return nil
	}, func(tx context.Context) error {
		if err := groupauthority.RequireAllMembers(tx, s.db, row.Intent.GroupID); err != nil {
			return err
		}
		// The actual guarded Mgo model also records authoritative membership Left
		// history using this exact session, preserving existing history semantics.
		if err := model.Delete(tx, row.Intent.GroupID, nil); err != nil {
			return err
		}
		result, err := s.db.Collection(Collection).UpdateOne(tx, bson.M{"_id": id, "epoch": s.epoch, "state": "pending", "revision": int64(1), "frozen_sha256": row.FrozenSHA}, bson.M{"$set": bson.M{"state": "done", "revision": int64(2), "completed_at": completedAt}, "$unset": bson.M{"members": ""}})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
	if errors.Is(err, errAlreadyDone) {
		return nil, nil
	}
	return commit, err
}
