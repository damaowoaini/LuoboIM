package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type memberAdmissionReceipt struct {
	ID             string    `bson:"_id"`
	Kind           string    `bson:"kind"`
	Epoch          string    `bson:"epoch"`
	CommandID      string    `bson:"command_id"`
	InputSHA256    string    `bson:"input_sha256"`
	GroupID        string    `bson:"group_id"`
	Actor          string    `bson:"actor"`
	Action         string    `bson:"action"`
	GroupRevision  int64     `bson:"group_revision"`
	NewMemberCount int64     `bson:"new_member_count"`
	ForbidHistory  bool      `bson:"forbid_history"`
	CreatedAt      time.Time `bson:"created_at"`
}

// One immutable row per actual new owner avoids an ever-growing group array.
// The two revisions refer to owner windows, not the mutable whole-group head.
type memberAdmissionOwner struct {
	ID                   string `bson:"_id"`
	Kind                 string `bson:"kind"`
	Epoch                string `bson:"epoch"`
	CommandID            string `bson:"command_id"`
	InputSHA256          string `bson:"input_sha256"`
	GroupID              string `bson:"group_id"`
	UserID               string `bson:"user_id"`
	NormalCID            string `bson:"normal_cid"`
	NotificationCID      string `bson:"notification_cid"`
	GroupRevision        int64  `bson:"group_revision"`
	NormalRevision       int64  `bson:"normal_revision"`
	NotificationRevision int64  `bson:"notification_revision"`
	NormalLower          int64  `bson:"normal_lower"`
	NotificationLower    int64  `bson:"notification_lower"`
}

func (s *Store) membershipCounter(ctx context.Context, cid, gid string, fence bool) (counter, error) {
	if err := s.requireTX(ctx); err != nil {
		return counter{}, err
	}
	origin, err := s.readNamespacePrincipals(ctx, cid)
	if err != nil {
		return counter{}, err
	}
	if origin.Kind != "group" || origin.GroupID != gid {
		return counter{}, ErrConflict
	}
	if err := s.requireNamespaceCounter(ctx, cid); err != nil {
		return counter{}, err
	}
	var c counter
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", cid)}).Decode(&c); err != nil {
		return counter{}, err
	}
	if fence {
		if c.Revision >= math.MaxInt64-1 {
			return counter{}, ErrConflict
		}
		result, err := s.db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": c.ID, "epoch": s.epoch, "conversation_id": cid, "maximum": c.Maximum, "revision": c.Revision}, bson.M{"$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return counter{}, err
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 1 {
			return counter{}, ErrConflict
		}
		c.Revision++
	}
	return c, nil
}

func (s *Store) admissionFloor(ctx context.Context, cid, uid string) (int64, error) {
	floors, err := unrelation.NewMsgMongoDriver(s.db).ReadMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: cid}, {ConversationID: cid, UserID: uid}})
	if err != nil {
		return 0, err
	}
	var lower int64
	for _, floor := range floors {
		if floor.ConversationID != cid || floor.UserID != "" && floor.UserID != uid || floor.MinSeq < 0 {
			return 0, ErrConflict
		}
		lower = max(lower, floor.MinSeq)
	}
	return lower, nil
}

func (s *Store) admitOwnerWindow(ctx context.Context, info groupauthority.MemberAdmissionInfo, uid string, c counter, forbidHistory bool) (MembershipWindow, error) {
	lower, err := s.admissionFloor(ctx, c.CID, uid)
	if err != nil {
		return MembershipWindow{}, err
	}
	var raw bson.Raw
	err = s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "membership-window", c.CID+"\x00"+uid)}).Decode(&raw)
	fresh := errors.Is(err, mongo.ErrNoDocuments)
	if err != nil && !fresh {
		return MembershipWindow{}, err
	}
	w := MembershipWindow{ID: key(s.epoch, "membership-window", c.CID+"\x00"+uid), Kind: "membership-window-v1", Epoch: s.epoch, ConversationID: c.CID, GroupID: info.Command.GroupID, UserID: uid, Revision: 1, SeedEvidenceSHA256: c.SeedEvidenceSHA}
	if !fresh {
		if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"lower", "upper", "revision", "group_revision"}, []string{"closed"}, "") || bson.Unmarshal(raw, &w) != nil || !s.validMembershipWindow(w, c.CID, uid) || w.GroupID != info.Command.GroupID || !w.Closed || w.GroupRevision >= info.GroupRevision || w.Revision >= math.MaxInt64-1 {
			return MembershipWindow{}, ErrConflict
		}
		lower = max(lower, w.Lower)
	}
	if forbidHistory {
		// MinSeq is inclusive. The last pre-admission message is not new history.
		if c.Maximum >= math.MaxInt64-1 {
			return MembershipWindow{}, ErrConflict
		}
		lower = max(lower, c.Maximum+1)
	}
	oldRevision := w.Revision
	w.Closed, w.Lower, w.Upper, w.GroupRevision, w.LastCommand = false, lower, 0, info.GroupRevision, info.Command.ID
	if !fresh {
		w.Revision++
	}
	if !s.validMembershipWindow(w, c.CID, uid) {
		return MembershipWindow{}, ErrConflict
	}
	if fresh {
		_, err = s.db.Collection(Sequences).InsertOne(ctx, w)
	} else {
		result, e := s.db.Collection(Sequences).ReplaceOne(ctx, bson.M{"_id": w.ID, "epoch": s.epoch, "revision": oldRevision, "closed": true}, w)
		err = e
		if err == nil && (result.MatchedCount != 1 || result.ModifiedCount != 1) {
			err = ErrConflict
		}
	}
	return w, err
}

// The caller supplies its captured server policy, never a request field. The
// private capability proves the actual same-TX absent -> present transition.
// Existing members are not reset. Both real namespace heads compete with the
// ordinary allocator in this transaction, without allocating a notice early.
func (s *Store) RecordMemberAdmission(ctx context.Context, cap *groupauthority.MemberAdmissionScope, forbidHistory bool) error {
	if s == nil || s.db == nil || ctx == nil || cap == nil {
		return ErrInvalid
	}
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	if err := s.checkMembershipSchema(ctx); err != nil {
		return err
	}
	info, err := cap.Inspect(ctx, s.db)
	if err != nil {
		return err
	}
	if info.Command.Epoch != s.epoch || info.GroupRevision < 1 || info.GroupRevision >= math.MaxInt64 {
		return ErrInvalid
	}
	// Absence of a historical owner is usable only after the complete controlled
	// import. A missing row by itself never proves that an old owner was new.
	if _, _, err := s.namespaceCutover(ctx); err != nil {
		return err
	}
	normal, notice, err := groupNamespacePair(info.Command.GroupID)
	if err != nil {
		return err
	}
	body, err := json.Marshal([]any{info, forbidHistory})
	if err != nil {
		return err
	}
	receipt := memberAdmissionReceipt{ID: key(s.epoch, "member-admission", info.Command.ID), Kind: "member-admission-v1", Epoch: s.epoch, CommandID: info.Command.ID, InputSHA256: hash(body), GroupID: info.Command.GroupID, Actor: info.Command.Actor, Action: string(info.Command.Action), GroupRevision: info.GroupRevision, NewMemberCount: int64(len(info.NewMembers)), ForbidHistory: forbidHistory, CreatedAt: info.Command.CreatedAt}
	var prior bson.Raw
	err = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": receipt.ID}).Decode(&prior)
	if err == nil {
		row, e := s.decodeAdmissionReceipt(prior)
		if e != nil {
			return e
		}
		a, _ := json.Marshal(row)
		b, _ := json.Marshal(receipt)
		if string(a) != string(b) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	if len(info.NewMembers) > 0 {
		n, err := s.membershipCounter(ctx, normal, info.Command.GroupID, true)
		if err != nil {
			return err
		}
		p, err := s.membershipCounter(ctx, notice, info.Command.GroupID, true)
		if err != nil {
			return err
		}
		for _, uid := range info.NewMembers {
			w, err := s.admitOwnerWindow(ctx, info, uid, n, forbidHistory)
			if err != nil {
				return err
			}
			v, err := s.admitOwnerWindow(ctx, info, uid, p, forbidHistory)
			if err != nil {
				return err
			}
			marker := memberAdmissionOwner{ID: key(s.epoch, "member-admission-owner", info.Command.ID+"\x00"+uid), Kind: "member-admission-owner-v1", Epoch: s.epoch, CommandID: info.Command.ID, InputSHA256: receipt.InputSHA256, GroupID: info.Command.GroupID, UserID: uid, NormalCID: normal, NotificationCID: notice, GroupRevision: info.GroupRevision, NormalRevision: w.Revision, NotificationRevision: v.Revision, NormalLower: w.Lower, NotificationLower: v.Lower}
			if _, err := s.db.Collection(Receipts).InsertOne(ctx, marker); err != nil {
				return err
			}
		}
	}
	_, err = s.db.Collection(Receipts).InsertOne(ctx, receipt)
	return err
}

func (s *Store) decodeAdmissionReceipt(raw bson.Raw) (memberAdmissionReceipt, error) {
	if !exactFields(raw, []string{"_id", "kind", "epoch", "command_id", "input_sha256", "group_id", "actor", "action"}, []string{"group_revision", "new_member_count"}, []string{"forbid_history"}, "created_at") {
		return memberAdmissionReceipt{}, ErrConflict
	}
	var r memberAdmissionReceipt
	if bson.Unmarshal(raw, &r) != nil || r.ID != key(s.epoch, "member-admission", r.CommandID) || r.Kind != "member-admission-v1" || r.Epoch != s.epoch || !validUUID(r.CommandID) || !validSHA(r.InputSHA256) || !identity(r.GroupID) || !identity(r.Actor) || r.GroupRevision < 1 || r.GroupRevision >= math.MaxInt64 || r.NewMemberCount < 0 || r.CreatedAt.IsZero() {
		return memberAdmissionReceipt{}, ErrConflict
	}
	if r.Action != string(groupauthority.MemberAdmissionJoin) && r.Action != string(groupauthority.MemberAdmissionInvite) && r.Action != string(groupauthority.MemberAdmissionApprove) {
		return memberAdmissionReceipt{}, ErrConflict
	}
	return r, nil
}

// A valid but superseded owner admission is terminally stale, not a reason to
// recreate its conversation. Unknown or malformed authority remains an error.
// Privacy/deletion cuts and conversation mutation stay in the caller's TX.
func (s *Store) ValidateMemberAdmissionProjection(ctx context.Context, command groupauthority.NoticeCommandInfo, groupRevision int64, uid string) (bool, error) {
	if s == nil || s.db == nil || ctx == nil || command.Epoch != s.epoch || !validUUID(command.ID) || !identity(uid) || groupRevision < 1 {
		return false, ErrInvalid
	}
	if err := s.requireTX(ctx); err != nil {
		return false, err
	}
	if err := s.checkMembershipSchema(ctx); err != nil {
		return false, err
	}
	var raw bson.Raw
	if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-admission", command.ID)}).Decode(&raw); err != nil {
		return false, errors.Join(ErrUnprepared, err)
	}
	r, err := s.decodeAdmissionReceipt(raw)
	if err != nil {
		return false, err
	}
	if r.GroupID != command.GroupID || r.Actor != command.Actor || r.Action != string(command.Action) || r.GroupRevision != groupRevision || !r.CreatedAt.Equal(command.CreatedAt) {
		return false, ErrConflict
	}
	err = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-admission-owner", command.ID+"\x00"+uid)}).Decode(&raw)
	if err != nil {
		return false, errors.Join(ErrUnprepared, err)
	}
	if !exactFields(raw, []string{"_id", "kind", "epoch", "command_id", "input_sha256", "group_id", "user_id", "normal_cid", "notification_cid"}, []string{"group_revision", "normal_revision", "notification_revision", "normal_lower", "notification_lower"}, nil, "") {
		return false, ErrConflict
	}
	var marker memberAdmissionOwner
	normal, notice, err := groupNamespacePair(command.GroupID)
	if err != nil {
		return false, err
	}
	if bson.Unmarshal(raw, &marker) != nil || marker.ID != key(s.epoch, "member-admission-owner", command.ID+"\x00"+uid) || marker.Kind != "member-admission-owner-v1" || marker.Epoch != s.epoch || marker.CommandID != command.ID || marker.InputSHA256 != r.InputSHA256 || marker.GroupID != command.GroupID || marker.UserID != uid || marker.NormalCID != normal || marker.NotificationCID != notice || marker.GroupRevision != groupRevision || marker.NormalRevision < 1 || marker.NotificationRevision < 1 || marker.NormalLower < 0 || marker.NotificationLower < 0 {
		return false, ErrConflict
	}
	for _, part := range []struct {
		cid             string
		revision, lower int64
	}{{normal, marker.NormalRevision, marker.NormalLower}, {notice, marker.NotificationRevision, marker.NotificationLower}} {
		w, err := s.ReadMembershipWindow(ctx, part.cid, uid)
		if err != nil {
			return false, err
		}
		if w.LastCommand != command.ID || w.GroupRevision != groupRevision || w.Closed || w.Lower != part.lower {
			return false, nil
		}
		// Other authoritative visibility fences can advance the physical
		// revision without changing this admission's logical identity.
		if w.Revision < part.revision {
			return false, ErrConflict
		}
		if w.Revision >= math.MaxInt64-1 {
			return false, ErrConflict
		}
		// A read alone would allow snapshot write skew with a concurrent leave.
		// The projection queue must commit its terminal state/cache duty in this
		// same transaction; rollback restores these exact original revisions.
		result, err := s.db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": w.ID, "epoch": s.epoch, "revision": w.Revision, "last_command": command.ID, "closed": false}, bson.M{"$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return false, err
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 1 {
			return false, ErrConflict
		}
	}
	if err := s.db.Collection("group_member").FindOne(ctx, bson.M{"group_id": command.GroupID, "user_id": uid}).Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
