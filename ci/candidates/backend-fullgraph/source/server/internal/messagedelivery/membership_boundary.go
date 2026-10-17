package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const MembershipSchemaID = "schema:membership-boundary-v1"

// Stored in the already catalog-verified sequence and receipt collections.
// This new marker is never minted by a runtime constructor or a miss. Its
// operator producer/complete migration and all readers remain a REQUIRED next
// slice. Nothing here guesses a legacy owner's bound or retires old protection.
func (s *Store) checkMembershipSchema(ctx context.Context) error {
	for _, name := range []string{Sequences, Receipts} {
		var raw bson.Raw
		if err := s.db.Collection(name).FindOne(ctx, bson.M{"_id": MembershipSchemaID}).Decode(&raw); err != nil {
			return errors.Join(ErrUnprepared, err)
		}
		fields, err := raw.Elements()
		if err != nil || len(fields) != 8 {
			return ErrUnprepared
		}
		seen := make(map[string]bool, len(fields))
		for _, field := range fields {
			if seen[field.Key()] {
				return ErrUnprepared
			}
			seen[field.Key()] = true
		}
		id, a := raw.Lookup("_id").StringValueOK()
		version, b := raw.Lookup("version").Int32OK()
		epoch, c := raw.Lookup("epoch").StringValueOK()
		activation, d := raw.Lookup("activation_sha256").StringValueOK()
		if !a || id != MembershipSchemaID || !b || version != 1 || !c || epoch != s.epoch || !d || activation != s.activationSHA {
			return ErrUnprepared
		}
		for _, field := range []string{"legacy_message_writers_retired", "legacy_group_writers_retired", "owner_bounds_seeded", "readers_bound"} {
			value, ok := raw.Lookup(field).BooleanOK()
			if !ok || !value {
				return ErrUnprepared
			}
		}
	}
	return nil
}

// Closed plus Upper=0 is intentional: legacy max_seq=0 means unbounded and
// cannot represent an empty conversation from which an owner has left.
type MembershipWindow struct {
	ID                 string `bson:"_id"`
	Kind               string `bson:"kind"`
	Epoch              string `bson:"epoch"`
	ConversationID     string `bson:"conversation_id"`
	GroupID            string `bson:"group_id"`
	UserID             string `bson:"user_id"`
	Closed             bool   `bson:"closed"`
	Lower              int64  `bson:"lower"`
	Upper              int64  `bson:"upper"`
	Revision           int64  `bson:"revision"`
	GroupRevision      int64  `bson:"group_revision"`
	LastCommand        string `bson:"last_command"`
	SeedEvidenceSHA256 string `bson:"seed_evidence_sha256"`
}

func (s *Store) validMembershipWindow(w MembershipWindow, cid, uid string) bool {
	return membershipWindowShape(w) && w.Epoch == s.epoch && w.ConversationID == cid && w.UserID == uid
}

func membershipWindowShape(w MembershipWindow) bool {
	normal, notice, err := groupNamespacePair(w.GroupID)
	legacy := msgprocessor.GetConversationIDBySessionType(constant.GroupChatType, w.GroupID)
	if err != nil || !validUUID(w.Epoch) || !identity(w.UserID) || w.ID != key(w.Epoch, "membership-window", w.ConversationID+"\x00"+w.UserID) || w.Kind != "membership-window-v1" || !identity(w.GroupID) || w.ConversationID != normal && w.ConversationID != notice && w.ConversationID != legacy || w.Revision < 1 || w.Revision == math.MaxInt64 || w.GroupRevision < 1 || w.GroupRevision == math.MaxInt64 || w.Lower < 0 || w.Upper < 0 || !validSHA(w.SeedEvidenceSHA256) {
		return false
	}
	if w.Closed {
		return validUUID(w.LastCommand)
	}
	return w.Upper == 0 && (w.LastCommand == "" || validUUID(w.LastCommand))
}

func exactFields(raw bson.Raw, strings, longs []string, bools []string, date string) bool {
	fields, err := raw.Elements()
	count := len(strings) + len(longs) + len(bools)
	if date != "" {
		count++
	}
	if err != nil || len(fields) != count {
		return false
	}
	seen := make(map[string]bool, len(fields))
	for _, field := range fields {
		if seen[field.Key()] {
			return false
		}
		seen[field.Key()] = true
	}
	for _, name := range strings {
		if _, ok := raw.Lookup(name).StringValueOK(); !ok {
			return false
		}
	}
	for _, name := range longs {
		if _, ok := raw.Lookup(name).Int64OK(); !ok {
			return false
		}
	}
	for _, name := range bools {
		if _, ok := raw.Lookup(name).BooleanOK(); !ok {
			return false
		}
	}
	if date != "" {
		if _, ok := raw.Lookup(date).DateTimeOK(); !ok {
			return false
		}
	}
	return true
}

// Callers must already enforce the authenticated owner/CID contract. This
// internal primary/majority read is not a public permission bypass. A missing
// owner record is unprepared, never implicitly open. All legacy readers need
// to merge this with their authoritative lower floor before activation.
func (s *Store) ReadMembershipWindow(ctx context.Context, cid, uid string) (MembershipWindow, error) {
	if s == nil || ctx == nil {
		return MembershipWindow{}, ErrInvalid
	}
	var result MembershipWindow
	read := func(current context.Context) error {
		w, e := s.readStoredMembershipWindow(current, cid, uid)
		if e != nil {
			return e
		}
		d, e := s.readNamespacePrincipals(current, cid)
		if e != nil {
			return e
		}
		result, e = s.prospectiveMemberWindow(current, d, w)
		return e
	}
	if mongo.SessionFromContext(ctx) != nil {
		e := read(ctx)
		return result, e
	}
	if e := s.CheckReady(ctx); e != nil {
		return result, e
	}
	e := s.transaction(ctx, read)
	return result, e
}
func (s *Store) readStoredMembershipWindow(ctx context.Context, cid, uid string) (MembershipWindow, error) {
	if s == nil || s.db == nil || ctx == nil || !identity(cid) || !identity(uid) {
		return MembershipWindow{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if mongo.SessionFromContext(ctx) == nil {
		if err := s.CheckReady(ctx); err != nil {
			return MembershipWindow{}, err
		}
	} else if err := s.requireTX(ctx); err != nil {
		return MembershipWindow{}, err
	}
	if err := s.checkMembershipSchema(ctx); err != nil {
		return MembershipWindow{}, err
	}
	origin, err := s.readNamespacePrincipals(ctx, cid)
	if err != nil {
		return MembershipWindow{}, err
	}
	if origin.Kind != "group" {
		return MembershipWindow{}, ErrConflict
	}
	var raw bson.Raw
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "membership-window", cid+"\x00"+uid)}).Decode(&raw); err != nil {
		return MembershipWindow{}, errors.Join(ErrUnprepared, err)
	}
	if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"lower", "upper", "revision", "group_revision"}, []string{"closed"}, "") {
		return MembershipWindow{}, ErrConflict
	}
	var row MembershipWindow
	if bson.Unmarshal(raw, &row) != nil {
		return MembershipWindow{}, ErrConflict
	}
	if !s.validMembershipWindow(row, cid, uid) || row.GroupID != origin.GroupID {
		return MembershipWindow{}, ErrConflict
	}
	return row, nil
}

// This is a window test, not full message visibility. Tombstones/revokes,
// current source identity and authoritative global/owner lower floors remain
// mandatory. In particular n_ tips have a different CID from the sg_ bound.
func (w MembershipWindow) AllowsSequence(seq, minimum int64) bool {
	return membershipWindowShape(w) && seq > 0 && minimum >= 0 && seq >= max(minimum, w.Lower) && (!w.Closed || seq <= w.Upper)
}

type MemberRemovalReceipt struct {
	ID                          string    `bson:"_id"`
	Kind                        string    `bson:"kind"`
	Epoch                       string    `bson:"epoch"`
	CommandID                   string    `bson:"command_id"`
	InputSHA256                 string    `bson:"input_sha256"`
	GroupID                     string    `bson:"group_id"`
	ConversationID              string    `bson:"conversation_id"`
	NotificationCID             string    `bson:"notification_cid"`
	GroupRevision               int64     `bson:"group_revision"`
	Upper                       int64     `bson:"upper"`
	CounterRevision             int64     `bson:"counter_revision"`
	NotificationUpper           int64     `bson:"notification_upper"`
	NotificationCounterRevision int64     `bson:"notification_counter_revision"`
	MemberCount                 int64     `bson:"member_count"`
	CreatedAt                   time.Time `bson:"created_at"`
}

// RecordMemberRemoval must follow the actual same-TX model deletion and
// completed action checks, before business commit. No recipient list is an
// argument. It increments the SAME counter revision used by ordinary Accept;
// it does not allocate a second sequence namespace or call an external RPC.
func (s *Store) RecordMemberRemoval(ctx context.Context, cap *groupauthority.MemberRemovalScope) (MemberRemovalReceipt, error) {
	if s == nil || s.db == nil || ctx == nil || cap == nil {
		return MemberRemovalReceipt{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.requireTX(ctx); err != nil {
		return MemberRemovalReceipt{}, err
	}
	if err := s.checkMembershipSchema(ctx); err != nil {
		return MemberRemovalReceipt{}, err
	}
	info, err := cap.Inspect(ctx, s.db)
	if err != nil {
		return MemberRemovalReceipt{}, err
	}
	if info.Command.Epoch != s.epoch || len(info.Members) == 0 {
		return MemberRemovalReceipt{}, ErrInvalid
	}
	cid, notice, err := groupNamespacePair(info.Command.GroupID)
	if err != nil {
		return MemberRemovalReceipt{}, err
	}
	body, err := json.Marshal(info)
	if err != nil {
		return MemberRemovalReceipt{}, ErrInvalid
	}
	digest := hash(body)
	rid := key(s.epoch, "membership-removal", info.Command.ID)
	var raw bson.Raw
	err = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": rid}).Decode(&raw)
	if err == nil {
		old, err := decodeRemovalReceipt(raw)
		if err != nil {
			return MemberRemovalReceipt{}, err
		}
		if !s.validRemovalReceipt(old, info.Command.ID) || old.InputSHA256 != digest || old.GroupID != info.Command.GroupID || old.GroupRevision != info.GroupRevision || old.MemberCount != int64(len(info.Members)) {
			return MemberRemovalReceipt{}, ErrConflict
		}
		return old, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return MemberRemovalReceipt{}, err
	}
	normalCounter, err := s.membershipCounter(ctx, cid, info.Command.GroupID, true)
	if err != nil {
		return MemberRemovalReceipt{}, err
	}
	noticeCounter, err := s.membershipCounter(ctx, notice, info.Command.GroupID, true)
	if err != nil {
		return MemberRemovalReceipt{}, err
	}
	beforeHistoryClose := []MembershipWindow{}
	for _, current := range []counter{normalCounter, noticeCounter} {
		for _, uid := range info.Members {
			window, err := s.readStoredMembershipWindow(ctx, current.CID, uid)
			if err != nil {
				return MemberRemovalReceipt{}, err
			}
			d, err := s.readNamespacePrincipals(ctx, current.CID)
			if err != nil {
				return MemberRemovalReceipt{}, err
			}
			live, err := s.prospectiveMemberWindow(ctx, d, window)
			if err != nil {
				return MemberRemovalReceipt{}, err
			}
			if window.GroupID != info.Command.GroupID || live.Closed || window.GroupRevision >= info.GroupRevision || window.Revision >= math.MaxInt64-1 {
				return MemberRemovalReceipt{}, ErrUnprepared
			}
			beforeHistoryClose = append(beforeHistoryClose, window)
			result, err := s.db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": window.ID, "epoch": s.epoch, "revision": window.Revision, "group_revision": window.GroupRevision, "closed": window.Closed, "upper": window.Upper, "lower": window.Lower}, bson.M{"$set": bson.M{"closed": true, "upper": current.Maximum, "group_revision": info.GroupRevision, "last_command": info.Command.ID}, "$inc": bson.M{"revision": int64(1)}})
			if err != nil {
				return MemberRemovalReceipt{}, err
			}
			if result.MatchedCount != 1 || result.ModifiedCount != 1 {
				return MemberRemovalReceipt{}, ErrConflict
			}
		}
	}
	receipt := MemberRemovalReceipt{ID: rid, Kind: "membership-removal-pair-v2", Epoch: s.epoch, CommandID: info.Command.ID, InputSHA256: digest, GroupID: info.Command.GroupID, ConversationID: cid, NotificationCID: notice, GroupRevision: info.GroupRevision, Upper: normalCounter.Maximum, CounterRevision: normalCounter.Revision, NotificationUpper: noticeCounter.Maximum, NotificationCounterRevision: noticeCounter.Revision, MemberCount: int64(len(info.Members)), CreatedAt: info.Command.CreatedAt}
	if !s.validRemovalReceipt(receipt, info.Command.ID) {
		return MemberRemovalReceipt{}, ErrConflict
	}
	if _, err := s.db.Collection(Receipts).InsertOne(ctx, receipt); err != nil {
		return MemberRemovalReceipt{}, err
	}
	for _, before := range beforeHistoryClose {
		if err := s.recordHistoryClosure(ctx, cap, receipt, before); err != nil {
			return MemberRemovalReceipt{}, err
		}
	}
	return receipt, nil
}

func (s *Store) validRemovalReceipt(r MemberRemovalReceipt, command string) bool {
	normal, notice, err := groupNamespacePair(r.GroupID)
	return err == nil && validUUID(command) && r.ID == key(s.epoch, "membership-removal", command) && r.Kind == "membership-removal-pair-v2" && r.Epoch == s.epoch && r.CommandID == command && validSHA(r.InputSHA256) && identity(r.GroupID) && r.ConversationID == normal && r.NotificationCID == notice && r.GroupRevision >= 1 && r.GroupRevision < math.MaxInt64 && r.Upper >= 0 && r.Upper < math.MaxInt64 && r.NotificationUpper >= 0 && r.NotificationUpper < math.MaxInt64 && r.CounterRevision >= 2 && r.CounterRevision < math.MaxInt64 && r.NotificationCounterRevision >= 2 && r.NotificationCounterRevision < math.MaxInt64 && r.MemberCount > 0 && !r.CreatedAt.IsZero()
}

func decodeRemovalReceipt(raw bson.Raw) (MemberRemovalReceipt, error) {
	if !exactFields(raw, []string{"_id", "kind", "epoch", "command_id", "input_sha256", "group_id", "conversation_id", "notification_cid"}, []string{"group_revision", "upper", "counter_revision", "notification_upper", "notification_counter_revision", "member_count"}, nil, "created_at") {
		return MemberRemovalReceipt{}, ErrConflict
	}
	var row MemberRemovalReceipt
	if bson.Unmarshal(raw, &row) != nil {
		return MemberRemovalReceipt{}, ErrConflict
	}
	return row, nil
}

// Inspect an unknown ACK by its ORIGINAL server command, without redoing a
// member mutation or inventing a new boundary. Absence is not rollback proof.
// A future operator/managed recovery caller must retain that exact command.
func (s *Store) ResolveMemberRemoval(ctx context.Context, command string) (MemberRemovalReceipt, error) {
	if s == nil || s.db == nil || ctx == nil || !validUUID(command) || mongo.SessionFromContext(ctx) != nil {
		return MemberRemovalReceipt{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(ctx); err != nil {
		return MemberRemovalReceipt{}, err
	}
	if err := s.checkMembershipSchema(ctx); err != nil {
		return MemberRemovalReceipt{}, err
	}
	var raw bson.Raw
	if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "membership-removal", command)}).Decode(&raw); err != nil {
		return MemberRemovalReceipt{}, errors.Join(ErrCommitUnconfirmed, err)
	}
	row, err := decodeRemovalReceipt(raw)
	if err != nil {
		return MemberRemovalReceipt{}, err
	}
	if !s.validRemovalReceipt(row, command) {
		return MemberRemovalReceipt{}, ErrConflict
	}
	return row, nil
}
