package messagedelivery

import (
	"context"
	"errors"
	"math"
	"sort"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type memberNoticeKey struct {
	Owner string
	Seq   int64
}

type noticeBranchMetadata struct {
	ID            string `bson:"_id"`
	Epoch         string `bson:"epoch"`
	SourceID      string `bson:"source_id"`
	BranchID      string `bson:"branch_id"`
	CID           string `bson:"conversation_id"`
	InputSHA256   string `bson:"input_sha256"`
	PayloadSHA256 string `bson:"action_input_payload_sha256"`
	ProfileSHA256 string `bson:"action_profile_sha256"`
	Producer      string `bson:"producer_version"`
	Seq           int64  `bson:"seq"`
	Persist       bool   `bson:"persist"`
}

func (s *Store) noticeBranchBatch(ctx context.Context, ids []string) (map[string]noticeBranchMetadata, error) {
	if err := s.requireTX(ctx); err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	unique := []string{}
	for _, id := range ids {
		if !validSHA(id) {
			return nil, ErrInvalid
		}
		if !wanted[id] {
			wanted[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) > 64 {
		return nil, ErrInvalid
	}
	out := map[string]noticeBranchMetadata{}
	if len(unique) == 0 {
		return out, nil
	}
	fields := []string{"_id", "epoch", "source_id", "branch_id", "conversation_id", "input_sha256", "action_input_payload_sha256", "action_profile_sha256", "producer_version"}
	projection := bson.M{"seq": 1, "persist": 1}
	for _, field := range fields {
		projection[field] = 1
	}
	cur, err := s.db.Collection(Effects).Find(ctx, bson.M{"_id": bson.M{"$in": unique}}, options.Find().SetProjection(projection).SetLimit(int64(len(unique)+1)))
	if err != nil {
		return nil, err
	}
	for cur.Next(ctx) {
		var raw bson.Raw
		var b noticeBranchMetadata
		if err = cur.Decode(&raw); err != nil {
			break
		}
		if !exactFields(raw, fields, []string{"seq"}, []string{"persist"}, "") || bson.Unmarshal(raw, &b) != nil || !wanted[b.ID] || out[b.ID].ID != "" || b.Epoch != s.epoch || !validUUID(b.SourceID) || !validUUID(b.BranchID) || b.ID != key(s.epoch, "branch", b.BranchID) || !identity(b.CID) || !validSHA(b.InputSHA256) || !validSHA(b.PayloadSHA256) || !validSHA(b.ProfileSHA256) || b.Producer != ownedGroupProducer || !b.Persist || b.Seq < 1 || b.Seq >= math.MaxInt64 {
			err = ErrConflict
			break
		}
		out[b.ID] = b
	}
	if err == nil {
		err = cur.Err()
	}
	closeErr := cur.Close(ctx)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	return out, nil
}

func exactArrayRecord(raw bson.Raw, field string, strings, longs, bools []string, date string) ([]bson.RawValue, bool) {
	elements, err := raw.Elements()
	if err != nil {
		return nil, false
	}
	var rest bson.D
	var values []bson.RawValue
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
		values, err = a.Values()
		if err != nil {
			return nil, false
		}
	}
	base, err := bson.Marshal(rest)
	return values, found && err == nil && exactFields(base, strings, longs, bools, date)
}

func (s *Store) decodeNoticeSourceReceipt(raw bson.Raw) (Receipt, error) {
	items, ok := exactArrayRecord(raw, "branches", []string{"_id", "epoch", "source_id", "input_sha256"}, nil, nil, "accepted_at")
	var r Receipt
	if !ok || len(items) < 1 || len(items) > 2 || bson.Unmarshal(raw, &r) != nil || r.Epoch != s.epoch || !validUUID(r.SourceID) || r.ID != key(s.epoch, "source", r.SourceID) || !validSHA(r.InputSHA256) || r.AcceptedAt.IsZero() {
		return r, ErrConflict
	}
	seen := map[string]bool{}
	for _, item := range items {
		d, ok := item.DocumentOK()
		if !ok || !exactFields(d, []string{"id", "conversation_id"}, []string{"seq"}, []string{"persist", "hidden"}, "") {
			return r, ErrConflict
		}
		var b AcceptedBranch
		if bson.Unmarshal(d, &b) != nil || !validUUID(b.ID) || !identity(b.ConversationID) || seen[b.ID] || b.Persist && (b.Seq < 1 || b.Seq >= math.MaxInt64) || !b.Persist && b.Seq != 0 {
			return r, ErrConflict
		}
		seen[b.ID] = true
	}
	return r, nil
}

// An exact _id page uses existing primary keys only. These are bounded I/O
// batches; a large group's owners are handled by as many pages as required.
func (s *Store) noticeReceiptBatch(ctx context.Context, ids []string) (map[string]bson.Raw, error) {
	if err := s.requireTX(ctx); err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if !validSHA(id) {
			return nil, ErrInvalid
		}
		if !wanted[id] {
			wanted[id] = true
			unique = append(unique, id)
		}
	}
	if len(unique) > 128 {
		return nil, ErrInvalid
	}
	result := map[string]bson.Raw{}
	if len(unique) == 0 {
		return result, nil
	}
	sort.Strings(unique)
	cur, err := s.db.Collection(Receipts).Find(ctx, bson.M{"_id": bson.M{"$in": unique}}, options.Find().SetLimit(int64(len(unique)+1)))
	if err != nil {
		return nil, err
	}
	for cur.Next(ctx) {
		var raw bson.Raw
		if err = cur.Decode(&raw); err != nil {
			break
		}
		id, ok := raw.Lookup("_id").StringValueOK()
		if !ok || !wanted[id] || result[id] != nil {
			err = ErrConflict
			break
		}
		result[id] = append(bson.Raw(nil), raw...)
	}
	if err == nil {
		err = cur.Err()
	}
	closeErr := cur.Close(ctx)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	return result, nil
}

// Receipts -> pending source -> private business command -> actual accepted
// branch must all agree. The summary head is deliberately not a grant. No
// legacy message body or current roster is used to reconstruct an old owner.
func (s *Store) readNoticeEntitlements(ctx context.Context, cid string, keys []memberNoticeKey) (map[string]memberNoticeEntitlement, error) {
	if len(keys) < 1 || len(keys) > 64 {
		return nil, ErrInvalid
	}
	if err := s.requireTX(ctx); err != nil {
		return nil, err
	}
	origin, err := s.readNamespacePrincipals(ctx, cid)
	if err != nil {
		return nil, err
	}
	if origin.Kind != "group" {
		return nil, ErrConflict
	}
	ids := make([]string, 0, len(keys))
	wanted := map[string]memberNoticeKey{}
	for _, k := range keys {
		if !identity(k.Owner) || k.Seq < 1 || k.Seq >= math.MaxInt64 {
			return nil, ErrInvalid
		}
		id := noticeEntitlementID(s.epoch, cid, k.Owner, k.Seq)
		if _, ok := wanted[id]; !ok {
			ids = append(ids, id)
			wanted[id] = k
		}
	}
	rows, err := s.noticeReceiptBatch(ctx, ids)
	if err != nil {
		return nil, err
	}
	result := map[string]memberNoticeEntitlement{}
	pendingIDs := []string{}
	for id, raw := range rows {
		e, err := s.decodeNoticeEntitlement(raw)
		if err != nil {
			return nil, err
		}
		k := wanted[id]
		if e.ID != id || e.ConversationID != cid || e.UserID != k.Owner || e.Seq != k.Seq {
			return nil, ErrConflict
		}
		result[id] = e
		pendingIDs = append(pendingIDs, e.PendingID)
	}
	if len(result) == 0 {
		return result, nil
	}
	pendingRows, err := s.noticeReceiptBatch(ctx, pendingIDs)
	if err != nil {
		return nil, err
	}
	pendings := map[string]memberNoticePending{}
	headerIDs := []string{}
	for _, e := range result {
		raw, ok := pendingRows[e.PendingID]
		if !ok {
			return nil, ErrUnprepared
		}
		p, err := s.decodeNoticePending(raw)
		if err != nil {
			return nil, err
		}
		if p.ID != e.PendingID || canonicalRecordSHA(p) != e.PendingSHA256 || p.UserID != e.UserID || p.ConversationID != cid || !p.Persist || e.Seq <= p.Upper || e.Seq < p.Lower {
			return nil, ErrConflict
		}
		pendings[p.ID] = p
		headerIDs = append(headerIDs, p.HeaderID)
	}
	headerRows, err := s.noticeReceiptBatch(ctx, headerIDs)
	if err != nil {
		return nil, err
	}
	headers := map[string]memberNoticeHeader{}
	sourceIDs := []string{}
	for _, e := range result {
		p := pendings[e.PendingID]
		raw, ok := headerRows[p.HeaderID]
		if !ok {
			return nil, ErrUnprepared
		}
		h, err := s.decodeNoticeHeader(raw)
		if err != nil {
			return nil, err
		}
		index := sort.SearchStrings(h.Owners, e.UserID)
		if h.ID != p.HeaderID || canonicalRecordSHA(h) != p.HeaderSHA256 || h.GroupID != origin.GroupID || p.ID != noticePendingID(s.epoch, h.EffectID, p.BranchID, e.UserID) || index == len(h.Owners) || h.Owners[index] != e.UserID || e.ReceiptID != key(s.epoch, "source", h.EffectID) {
			return nil, ErrConflict
		}
		headers[h.ID] = h
		sourceIDs = append(sourceIDs, e.ReceiptID, key(s.epoch, "membership-removal", h.CommandID))
	}
	sources, err := s.noticeReceiptBatch(ctx, sourceIDs)
	if err != nil {
		return nil, err
	}
	branchIDs := []string{}
	for _, p := range pendings {
		branchIDs = append(branchIDs, key(s.epoch, "branch", p.BranchID))
	}
	branches, err := s.noticeBranchBatch(ctx, branchIDs)
	if err != nil {
		return nil, err
	}
	for _, e := range result {
		p := pendings[e.PendingID]
		h := headers[p.HeaderID]
		raw, ok := sources[key(s.epoch, "membership-removal", h.CommandID)]
		if !ok {
			return nil, ErrUnprepared
		}
		removal, err := decodeRemovalReceipt(raw)
		if err != nil {
			return nil, err
		}
		if err := s.noticeHeaderMatchesRemoval(h, removal); err != nil {
			return nil, err
		}
		upper := removal.Upper
		if cid == removal.NotificationCID {
			upper = removal.NotificationUpper
		} else if cid != removal.ConversationID {
			return nil, ErrConflict
		}
		if p.Upper != upper {
			return nil, ErrConflict
		}
		raw, ok = sources[e.ReceiptID]
		if !ok {
			return nil, ErrUnprepared
		}
		r, err := s.decodeNoticeSourceReceipt(raw)
		if err != nil {
			return nil, err
		}
		if r.ID != e.ReceiptID || r.SourceID != h.EffectID || canonicalRecordSHA(r) != e.ReceiptSHA256 {
			return nil, ErrConflict
		}
		b, ok := branches[key(s.epoch, "branch", p.BranchID)]
		if !ok || b.SourceID != h.EffectID || b.BranchID != p.BranchID || b.CID != cid || b.InputSHA256 != r.InputSHA256 || b.PayloadSHA256 != p.PayloadSHA256 || b.ProfileSHA256 != h.ProfileSHA256 || b.Seq != e.Seq {
			return nil, ErrConflict
		}
		matched := false
		for _, b := range r.Branches {
			if b.ID == p.BranchID {
				matched = b.Persist && !b.Hidden && b.ConversationID == cid && b.Seq == e.Seq
			}
		}
		if !matched {
			return nil, ErrConflict
		}
	}
	return result, nil
}

func (s *Store) noticeQueryUpper(ctx context.Context, cid, owner string) (int64, error) {
	var raw bson.Raw
	err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": noticeHeadID(s.epoch, cid, owner)}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	h, err := s.decodeNoticeHead(raw)
	if err != nil {
		return 0, err
	}
	if h.ConversationID != cid || h.UserID != owner {
		return 0, ErrConflict
	}
	rows, err := s.readNoticeEntitlements(ctx, cid, []memberNoticeKey{{Owner: owner, Seq: h.Latest}})
	if err != nil {
		return 0, err
	}
	e, ok := rows[h.EntitlementID]
	if !ok || canonicalRecordSHA(e) != h.EntitlementSHA256 {
		return 0, ErrConflict
	}
	return h.Latest, nil
}
