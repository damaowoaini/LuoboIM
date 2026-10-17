package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

type memberNoticeHeader struct {
	ID                  string    `bson:"_id"`
	Kind                string    `bson:"kind"`
	Epoch               string    `bson:"epoch"`
	EffectID            string    `bson:"effect_id"`
	CommandID           string    `bson:"command_id"`
	GroupID             string    `bson:"group_id"`
	Actor               string    `bson:"actor"`
	Action              string    `bson:"action"`
	RemovalInputSHA256  string    `bson:"removal_input_sha256"`
	SourcePayloadSHA256 string    `bson:"source_payload_sha256"`
	ProfileSHA256       string    `bson:"profile_sha256"`
	TargetsSHA256       string    `bson:"targets_sha256"`
	GroupRevision       int64     `bson:"group_revision"`
	Owners              []string  `bson:"owners"`
	CreatedAt           time.Time `bson:"created_at"`
}

type memberNoticePending struct {
	ID             string `bson:"_id"`
	Kind           string `bson:"kind"`
	Epoch          string `bson:"epoch"`
	HeaderID       string `bson:"header_id"`
	HeaderSHA256   string `bson:"header_sha256"`
	UserID         string `bson:"user_id"`
	ConversationID string `bson:"conversation_id"`
	BranchID       string `bson:"branch_id"`
	PayloadSHA256  string `bson:"payload_sha256"`
	Persist        bool   `bson:"persist"`
	Lower          int64  `bson:"lower"`
	Upper          int64  `bson:"upper"`
}

type memberNoticeEntitlement struct {
	ID             string `bson:"_id"`
	Kind           string `bson:"kind"`
	Epoch          string `bson:"epoch"`
	UserID         string `bson:"user_id"`
	ConversationID string `bson:"conversation_id"`
	PendingID      string `bson:"pending_id"`
	PendingSHA256  string `bson:"pending_sha256"`
	ReceiptID      string `bson:"receipt_id"`
	ReceiptSHA256  string `bson:"receipt_sha256"`
	Seq            int64  `bson:"seq"`
}

// A query bound only. It never grants the interval between Upper and Latest.
type memberNoticeHead struct {
	ID                string `bson:"_id"`
	Kind              string `bson:"kind"`
	Epoch             string `bson:"epoch"`
	UserID            string `bson:"user_id"`
	ConversationID    string `bson:"conversation_id"`
	EntitlementID     string `bson:"entitlement_id"`
	EntitlementSHA256 string `bson:"entitlement_sha256"`
	Latest            int64  `bson:"latest"`
	Revision          int64  `bson:"revision"`
}

func noticePendingID(epoch, effect, branch, uid string) string {
	return key(epoch, "member-notice-pending", effect+"\x00"+branch+"\x00"+uid)
}
func noticeEntitlementID(epoch, cid, uid string, seq int64) string {
	return key(epoch, "member-notice-entitlement", cid+"\x00"+uid+"\x00"+strconv.FormatInt(seq, 10))
}
func noticeHeadID(epoch, cid, uid string) string {
	return key(epoch, "member-notice-entitlement-head", cid+"\x00"+uid)
}
func canonicalRecordSHA(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return hash(body)
}

func exactStringListRecord(raw bson.Raw, field string, strings, longs []string, date string) ([]string, bool) {
	elements, err := raw.Elements()
	if err != nil {
		return nil, false
	}
	var rest bson.D
	var values []string
	found := false
	seen := map[string]bool{}
	for _, e := range elements {
		if seen[e.Key()] {
			return nil, false
		}
		seen[e.Key()] = true
		if e.Key() != field {
			rest = append(rest, bson.E{Key: e.Key(), Value: e.Value()})
			continue
		}
		found = true
		a, ok := e.Value().ArrayOK()
		if !ok {
			return nil, false
		}
		items, err := a.Values()
		if err != nil {
			return nil, false
		}
		for _, item := range items {
			s, ok := item.StringValueOK()
			if !ok {
				return nil, false
			}
			values = append(values, s)
		}
	}
	base, err := bson.Marshal(rest)
	return values, found && err == nil && exactFields(base, strings, longs, nil, date)
}

func (s *Store) decodeNoticeHeader(raw bson.Raw) (memberNoticeHeader, error) {
	owners, ok := exactStringListRecord(raw, "owners", []string{"_id", "kind", "epoch", "effect_id", "command_id", "group_id", "actor", "action", "removal_input_sha256", "source_payload_sha256", "profile_sha256", "targets_sha256"}, []string{"group_revision"}, "created_at")
	var h memberNoticeHeader
	if !ok || bson.Unmarshal(raw, &h) != nil || h.ID != key(s.epoch, "member-notice-header", h.EffectID) || h.Kind != "member-notice-header-v1" || h.Epoch != s.epoch || !validUUID(h.EffectID) || !validUUID(h.CommandID) || !identity(h.GroupID) || !identity(h.Actor) || h.GroupRevision < 1 || h.GroupRevision >= math.MaxInt64 || h.CreatedAt.IsZero() || len(owners) < 1 {
		return h, ErrConflict
	}
	for _, v := range []string{h.RemovalInputSHA256, h.SourcePayloadSHA256, h.ProfileSHA256, h.TargetsSHA256} {
		if !validSHA(v) {
			return h, ErrConflict
		}
	}
	if h.Action != string(groupauthority.NoticeKick) && h.Action != string(groupauthority.NoticeQuit) {
		return h, ErrConflict
	}
	for i, uid := range owners {
		if !identity(uid) || i > 0 && uid <= owners[i-1] {
			return h, ErrConflict
		}
	}
	return h, nil
}

func (s *Store) decodeNoticePending(raw bson.Raw) (memberNoticePending, error) {
	var p memberNoticePending
	if !exactFields(raw, []string{"_id", "kind", "epoch", "header_id", "header_sha256", "user_id", "conversation_id", "branch_id", "payload_sha256"}, []string{"lower", "upper"}, []string{"persist"}, "") || bson.Unmarshal(raw, &p) != nil || p.Kind != "member-notice-pending-v1" || p.Epoch != s.epoch || !validSHA(p.ID) || !validSHA(p.HeaderID) || !validSHA(p.HeaderSHA256) || !identity(p.UserID) || !identity(p.ConversationID) || !validUUID(p.BranchID) || !validSHA(p.PayloadSHA256) || p.Lower < 0 || p.Upper < 0 || p.Upper >= math.MaxInt64 {
		return p, ErrConflict
	}
	return p, nil
}

func (s *Store) decodeNoticeEntitlement(raw bson.Raw) (memberNoticeEntitlement, error) {
	var e memberNoticeEntitlement
	if !exactFields(raw, []string{"_id", "kind", "epoch", "user_id", "conversation_id", "pending_id", "pending_sha256", "receipt_id", "receipt_sha256"}, []string{"seq"}, nil, "") || bson.Unmarshal(raw, &e) != nil || e.Kind != "member-notice-entitlement-v1" || e.Epoch != s.epoch || !identity(e.UserID) || !identity(e.ConversationID) || e.Seq < 1 || e.Seq >= math.MaxInt64 || e.ID != noticeEntitlementID(s.epoch, e.ConversationID, e.UserID, e.Seq) || !validSHA(e.PendingID) || !validSHA(e.PendingSHA256) || !validSHA(e.ReceiptID) || !validSHA(e.ReceiptSHA256) {
		return e, ErrConflict
	}
	return e, nil
}

func (s *Store) decodeNoticeHead(raw bson.Raw) (memberNoticeHead, error) {
	var h memberNoticeHead
	if !exactFields(raw, []string{"_id", "kind", "epoch", "user_id", "conversation_id", "entitlement_id", "entitlement_sha256"}, []string{"latest", "revision"}, nil, "") || bson.Unmarshal(raw, &h) != nil || h.Kind != "member-notice-entitlement-head-v1" || h.Epoch != s.epoch || !identity(h.UserID) || !identity(h.ConversationID) || h.ID != noticeHeadID(s.epoch, h.ConversationID, h.UserID) || h.Latest < 1 || h.Latest >= math.MaxInt64 || h.Revision < 1 || h.Revision >= math.MaxInt64 || h.EntitlementID != noticeEntitlementID(s.epoch, h.ConversationID, h.UserID, h.Latest) || !validSHA(h.EntitlementSHA256) {
		return h, ErrConflict
	}
	return h, nil
}

// Idempotency compares an exact strict typed record; a miss alone never
// authorizes overwriting a malformed, foreign or previously committed value.
func (s *Store) insertNoticeRecord(ctx context.Context, id string, value any, decode func(bson.Raw) (any, error)) error {
	var raw bson.Raw
	err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": id}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_, err = s.db.Collection(Receipts).InsertOne(ctx, value)
		return err
	}
	if err != nil {
		return err
	}
	prior, err := decode(raw)
	if err != nil {
		return err
	}
	if canonicalRecordSHA(prior) != canonicalRecordSHA(value) {
		return ErrConflict
	}
	return nil
}

func removalMessage(f groupeffects.Frozen) (bool, error) {
	var m sdkws.MsgData
	if proto.Unmarshal(f.Payload, &m) != nil {
		return false, ErrConflict
	}
	return m.ContentType == constant.MemberKickedNotification || m.ContentType == constant.MemberQuitNotification, nil
}

func (s *Store) validateNoticeHeader(ctx context.Context, h memberNoticeHeader) (MemberRemovalReceipt, error) {
	var raw bson.Raw
	if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "membership-removal", h.CommandID)}).Decode(&raw); err != nil {
		return MemberRemovalReceipt{}, errors.Join(ErrUnprepared, err)
	}
	r, err := decodeRemovalReceipt(raw)
	if err != nil {
		return r, err
	}
	return r, s.noticeHeaderMatchesRemoval(h, r)
}

func (s *Store) noticeHeaderMatchesRemoval(h memberNoticeHeader, r MemberRemovalReceipt) error {
	if !s.validRemovalReceipt(r, h.CommandID) || r.GroupID != h.GroupID || r.GroupRevision != h.GroupRevision || r.InputSHA256 != h.RemovalInputSHA256 || r.MemberCount != int64(len(h.Owners)) || !r.CreatedAt.Equal(h.CreatedAt) {
		return ErrConflict
	}
	info := groupauthority.MemberRemovalInfo{Command: groupauthority.NoticeCommandInfo{ID: h.CommandID, Epoch: s.epoch, Actor: h.Actor, GroupID: h.GroupID, Action: groupauthority.NoticeAction(h.Action), CreatedAt: h.CreatedAt}, GroupRevision: h.GroupRevision, Members: h.Owners}
	if canonicalRecordSHA(info) != r.InputSHA256 {
		return ErrConflict
	}
	return nil
}

// Called after closure and the real typed group effect Queue, in the ORIGINAL
// business TX. Only the private removal capability supplies the owner set.
func (s *Store) RecordMemberNotificationPending(ctx context.Context, cap *groupauthority.MemberRemovalScope, effectID string) error {
	if s == nil || s.db == nil || ctx == nil || cap == nil || !validUUID(effectID) {
		return ErrInvalid
	}
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	info, err := cap.Inspect(ctx, s.db)
	if err != nil {
		return err
	}
	if info.Command.Epoch != s.epoch {
		return ErrConflict
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return err
	}
	owned, err := effects.ReadOwnedNotificationForRedaction(ctx, effectID)
	if err != nil {
		return err
	}
	f, targets, _, profileSHA, _ := owned.Snapshot()
	if f.ID != effectID || f.OperationID != info.Command.ID || f.Actor != info.Command.Actor || f.GroupID != info.Command.GroupID || !f.CreatedAt.Equal(info.Command.CreatedAt) {
		return ErrConflict
	}
	isRemoval, err := removalMessage(f)
	if err != nil || !isRemoval {
		return errors.Join(ErrConflict, err)
	}
	var m sdkws.MsgData
	if proto.Unmarshal(f.Payload, &m) != nil || info.Command.Action == groupauthority.NoticeKick && m.ContentType != constant.MemberKickedNotification || info.Command.Action == groupauthority.NoticeQuit && m.ContentType != constant.MemberQuitNotification {
		return ErrConflict
	}
	owners := append([]string(nil), info.Members...)
	sort.Strings(owners)
	h := memberNoticeHeader{ID: key(s.epoch, "member-notice-header", effectID), Kind: "member-notice-header-v1", Epoch: s.epoch, EffectID: effectID, CommandID: info.Command.ID, GroupID: info.Command.GroupID, Actor: info.Command.Actor, Action: string(info.Command.Action), RemovalInputSHA256: canonicalRecordSHA(info), SourcePayloadSHA256: f.PayloadSHA256, ProfileSHA256: profileSHA, TargetsSHA256: canonicalRecordSHA(targets), GroupRevision: info.GroupRevision, Owners: owners, CreatedAt: info.Command.CreatedAt}
	r, err := s.validateNoticeHeader(ctx, h)
	if err != nil {
		return err
	}
	plans, err := groupeffects.PlanNotificationBranches(f)
	if err != nil {
		return err
	}
	rosters := map[string]map[string]bool{}
	for _, target := range targets {
		ids := map[string]bool{}
		for _, uid := range target.UserIDs {
			ids[uid] = true
		}
		rosters[target.BranchID] = ids
	}
	for _, uid := range owners {
		for _, plan := range plans {
			if !rosters[plan.ID][uid] {
				continue
			}
			w, err := s.ReadMembershipWindow(ctx, plan.ConversationID, uid)
			if err != nil {
				return err
			}
			upper := r.Upper
			if plan.ConversationID == r.NotificationCID {
				upper = r.NotificationUpper
			} else if plan.ConversationID != r.ConversationID {
				return ErrConflict
			}
			if !w.Closed || w.LastCommand != info.Command.ID || w.GroupRevision != info.GroupRevision || w.Upper != upper {
				return ErrConflict
			}
			p := memberNoticePending{ID: noticePendingID(s.epoch, effectID, plan.ID, uid), Kind: "member-notice-pending-v1", Epoch: s.epoch, HeaderID: h.ID, HeaderSHA256: canonicalRecordSHA(h), UserID: uid, ConversationID: plan.ConversationID, BranchID: plan.ID, PayloadSHA256: plan.PayloadSHA256, Persist: plan.Persist, Lower: w.Lower, Upper: w.Upper}
			if err := s.insertNoticeRecord(ctx, p.ID, p, func(raw bson.Raw) (any, error) { return s.decodeNoticePending(raw) }); err != nil {
				return err
			}
		}
	}
	return s.insertNoticeRecord(ctx, h.ID, h, func(raw bson.Raw) (any, error) { return s.decodeNoticeHeader(raw) })
}

func (s *Store) bindMemberNotificationEntitlements(ctx context.Context, in ownedActionInput, r Receipt, denied map[string][]string) error {
	isRemoval, err := removalMessage(in.frozen)
	if err != nil || !isRemoval {
		return err
	}
	var raw bson.Raw
	if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-notice-header", in.frozen.ID)}).Decode(&raw); err != nil {
		return errors.Join(ErrUnprepared, err)
	}
	h, err := s.decodeNoticeHeader(raw)
	if err != nil {
		return err
	}
	if h.EffectID != in.frozen.ID || h.CommandID != in.frozen.OperationID || h.GroupID != in.frozen.GroupID || h.Actor != in.frozen.Actor || h.SourcePayloadSHA256 != in.frozen.PayloadSHA256 || h.ProfileSHA256 != in.profileSHA || h.TargetsSHA256 != canonicalRecordSHA(in.targets) {
		return ErrConflict
	}
	if _, err := s.validateNoticeHeader(ctx, h); err != nil {
		return err
	}
	owners := map[string]bool{}
	for _, uid := range h.Owners {
		owners[uid] = true
	}
	plans, err := groupeffects.PlanNotificationBranches(in.frozen)
	if err != nil {
		return err
	}
	byID := map[string]groupeffects.NotificationBranch{}
	for _, p := range plans {
		byID[p.ID] = p
	}
	targets := map[string][]string{}
	for _, t := range in.targets {
		targets[t.BranchID] = t.UserIDs
	}
	for _, b := range r.Branches {
		plan, ok := byID[b.ID]
		if !ok || b.ConversationID != plan.ConversationID || b.Persist != plan.Persist {
			return ErrConflict
		}
		blocked := map[string]bool{}
		for _, uid := range denied[b.ID] {
			blocked[uid] = true
		}
		for _, uid := range targets[b.ID] {
			if !owners[uid] {
				continue
			}
			id := noticePendingID(s.epoch, in.frozen.ID, b.ID, uid)
			if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": id}).Decode(&raw); err != nil {
				return errors.Join(ErrUnprepared, err)
			}
			p, err := s.decodeNoticePending(raw)
			if err != nil {
				return err
			}
			if p.ID != id || p.HeaderID != h.ID || p.HeaderSHA256 != canonicalRecordSHA(h) || p.UserID != uid || p.ConversationID != b.ConversationID || p.BranchID != b.ID || p.PayloadSHA256 != plan.PayloadSHA256 || p.Persist != b.Persist {
				return ErrConflict
			}
			if !b.Persist || b.Hidden || blocked[uid] {
				continue
			} // Seq0 never creates a history grant.
			if b.Seq <= p.Upper || b.Seq < 1 || b.Seq >= math.MaxInt64 {
				return ErrConflict
			}
			e := memberNoticeEntitlement{ID: noticeEntitlementID(s.epoch, b.ConversationID, uid, b.Seq), Kind: "member-notice-entitlement-v1", Epoch: s.epoch, UserID: uid, ConversationID: b.ConversationID, PendingID: p.ID, PendingSHA256: canonicalRecordSHA(p), ReceiptID: r.ID, ReceiptSHA256: canonicalRecordSHA(r), Seq: b.Seq}
			if err := s.insertNoticeRecord(ctx, e.ID, e, func(raw bson.Raw) (any, error) { return s.decodeNoticeEntitlement(raw) }); err != nil {
				return err
			}
			if err := s.advanceNoticeHead(ctx, e); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) advanceNoticeHead(ctx context.Context, e memberNoticeEntitlement) error {
	id := noticeHeadID(s.epoch, e.ConversationID, e.UserID)
	var raw bson.Raw
	err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": id}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		_, err = s.db.Collection(Receipts).InsertOne(ctx, memberNoticeHead{ID: id, Kind: "member-notice-entitlement-head-v1", Epoch: s.epoch, UserID: e.UserID, ConversationID: e.ConversationID, EntitlementID: e.ID, EntitlementSHA256: canonicalRecordSHA(e), Latest: e.Seq, Revision: 1})
		return err
	}
	if err != nil {
		return err
	}
	head, err := s.decodeNoticeHead(raw)
	if err != nil {
		return err
	}
	if head.UserID != e.UserID || head.ConversationID != e.ConversationID {
		return ErrConflict
	}
	if head.Latest > e.Seq {
		return nil
	}
	if head.Latest == e.Seq {
		if head.EntitlementID != e.ID || head.EntitlementSHA256 != canonicalRecordSHA(e) {
			return ErrConflict
		}
		return nil
	}
	if head.Revision >= math.MaxInt64-1 {
		return ErrConflict
	}
	result, err := s.db.Collection(Receipts).UpdateOne(ctx, bson.M{"_id": id, "revision": head.Revision, "latest": head.Latest}, bson.M{"$set": bson.M{"entitlement_id": e.ID, "entitlement_sha256": canonicalRecordSHA(e), "latest": e.Seq}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
