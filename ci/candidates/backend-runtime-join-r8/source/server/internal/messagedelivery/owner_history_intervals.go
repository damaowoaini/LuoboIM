package messagedelivery

import (
	"context"
	"math"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s *Store) readHistoryInterval(ctx context.Context, d NamespaceOriginDescriptor, owner string, raw bson.Raw) (ownerHistoryInterval, error) {
	var r ownerHistoryInterval
	if !historyIntervalShape(raw) || bson.Unmarshal(raw, &r) != nil || r.Epoch != s.epoch || r.CID != d.CID || r.Owner != owner || r.OriginSHA != d.SHA256 || !identity(r.ID) || !identity(r.ProofID) || !validSHA(r.ProofSHA) || r.Lower < 1 || r.Upper < 0 || r.Upper >= math.MaxInt64 || r.Upper != 0 && r.Upper < r.Lower || r.Revision < 1 || r.Revision >= math.MaxInt64 {
		return r, ErrConflict
	}
	if r.Kind == "namespace-owner-imported-history-v1" {
		return s.readImportedHistoryInterval(ctx, d, owner, r)
	}
	g, e := s.readHistoryGeneration(ctx, d, owner, r.ProofID, r.ProofSHA)
	if e != nil {
		return r, e
	}
	if r.ID != historyIntervalID(s.epoch, r.Kind, d.CID, owner, g.Command) {
		return r, ErrConflict
	}
	switch r.Kind {
	case "namespace-owner-history-admission-v1":
		if g.Forbid || r.Lower != max(int64(1), g.Lower) || r.Upper != g.Head || r.Revision != 1 || r.SourceID != "" || r.BranchID != "" || r.InputSHA != "" || r.ReceiptSHA != "" {
			return r, ErrConflict
		}
	case "namespace-owner-live-history-v1":
		if r.Lower != max(g.Head+1, g.Lower) {
			return r, ErrConflict
		}
		if r.Upper == 0 {
			if r.SourceID != "" || r.BranchID != "" || r.InputSHA != "" || r.ReceiptSHA != "" || r.Revision != 1 {
				return r, ErrConflict
			}
		} else if e = s.checkProspectiveReceipt(ctx, d, r.SourceID, r.BranchID, r.InputSHA, r.ReceiptSHA, r.Lower, r.Upper); e != nil {
			return r, e
		}
		head, e := s.directHistoryCounter(ctx, d.CID)
		if e != nil {
			return r, e
		}
		r.Upper, e = groupHistoryBound(s.epoch, d, owner, g.Command, r.Lower, head.Maximum, false, r.Upper, s.historyRead(ctx))
		if e != nil {
			return r, e
		}
	default:
		return r, ErrConflict
	}
	return r, nil
}
func (s *Store) readAdditionalHistory(ctx context.Context, d NamespaceOriginDescriptor, owner string, seqs []int64, floor int64) (map[int64]bool, error) {
	allowed := map[int64]bool{}
	ranges := make([]bson.M, 0, len(seqs))
	for _, seq := range seqs {
		if seq >= floor {
			ranges = append(ranges, bson.M{"lower": bson.M{"$lte": seq}, "$or": bson.A{bson.M{"upper": bson.M{"$gte": seq}}, bson.M{"kind": "namespace-owner-live-history-v1", "upper": int64(0)}}})
		}
	}
	if len(ranges) == 0 {
		return allowed, nil
	}
	filter := bson.M{"epoch": s.epoch, "conversation_id": d.CID, "user_id": owner, "kind": bson.M{"$in": []string{"namespace-owner-live-history-v1", "namespace-owner-history-admission-v1", "namespace-owner-imported-history-v1"}}, "$or": ranges}
	cursor, e := s.db.Collection(Sequences).Find(ctx, filter, options.Find().SetBatchSize(64))
	if e != nil {
		return nil, e
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		r, e := s.readHistoryInterval(ctx, d, owner, cursor.Current)
		if e != nil {
			return nil, e
		}
		for _, seq := range seqs {
			if seq >= floor && seq >= r.Lower && seq <= r.Upper {
				allowed[seq] = true
			}
		}
	}
	if e = cursor.Err(); e != nil {
		return nil, e
	}
	return allowed, nil
}
func (s *Store) additionalHistoryUpper(ctx context.Context, d NamespaceOriginDescriptor, owner string) (int64, error) {
	filter := bson.M{"epoch": s.epoch, "conversation_id": d.CID, "user_id": owner, "kind": bson.M{"$in": []string{"namespace-owner-live-history-v1", "namespace-owner-history-admission-v1", "namespace-owner-imported-history-v1"}}}
	cursor, e := s.db.Collection(Sequences).Find(ctx, filter, options.Find().SetBatchSize(64))
	if e != nil {
		return 0, e
	}
	defer cursor.Close(ctx)
	upper := int64(0)
	for cursor.Next(ctx) {
		var raw bson.Raw
		if e = cursor.Decode(&raw); e != nil {
			return 0, e
		}
		row, e := s.readHistoryInterval(ctx, d, owner, raw)
		if e != nil {
			return 0, e
		}
		upper = max(upper, row.Upper)
	}
	return upper, cursor.Err()
}
