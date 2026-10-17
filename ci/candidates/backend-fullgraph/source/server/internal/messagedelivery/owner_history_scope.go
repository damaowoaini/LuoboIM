package messagedelivery

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// The real slot retains a positive-scope ID/SHA across epoch imports.
// Its immutable original receipt holds the frozen IDs once per branch, not
// N recipient strings in every slot of the 100-message aggregate document.
// del_list remains an additional negative privacy/deletion ACL; it cannot
// represent recipients that did not exist when an admin-only notice was sent.
// No body, URL, current roster or later membership list is retained here.
type groupHistoryScope struct {
	Kind       string
	Epoch      string
	CID        string
	GroupID    string
	OriginSHA  string
	SourceID   string
	BranchID   string
	InputSHA   string
	ReceiptSHA string
	PayloadSHA string
	Seq        int64
	Targets    []string
	Denied     []string
}

// Permanent per-CID floor, not per activation: a subsequent import seed cannot
// relabel missing modern scope metadata as an unrestricted legacy slot.
type groupHistoryScopeRecord struct {
	ID   string `bson:"_id"`
	Kind string `bson:"kind"`
	JSON string `bson:"scope_json"`
	SHA  string `bson:"scope_sha256"`
}

func groupScopeID(epoch, branch string) string { return key(epoch, "group-history-scope", branch) }

type groupHistoryScopeFloor struct {
	ID         string `bson:"_id"`
	Kind       string `bson:"kind"`
	CID        string `bson:"conversation_id"`
	OriginSHA  string `bson:"origin_sha256"`
	Epoch      string `bson:"source_epoch"`
	SourceID   string `bson:"source_id"`
	BranchID   string `bson:"branch_id"`
	InputSHA   string `bson:"input_sha256"`
	ReceiptSHA string `bson:"receipt_sha256"`
	From       int64  `bson:"required_from"`
}

func groupScopeFloorID(cid string) string { return key("permanent", "group-history-scope-floor", cid) }
func (s *Store) readGroupScopeFloor(ctx context.Context, d NamespaceOriginDescriptor) (groupHistoryScopeFloor, error) {
	var raw bson.Raw
	var f groupHistoryScopeFloor
	if e := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": groupScopeFloorID(d.CID)}).Decode(&raw); e != nil {
		return f, e
	}
	if !exactFields(raw, []string{"_id", "kind", "conversation_id", "origin_sha256", "source_epoch", "source_id", "branch_id", "input_sha256", "receipt_sha256"}, []string{"required_from"}, nil, "") || bson.Unmarshal(raw, &f) != nil || f.ID != groupScopeFloorID(d.CID) || f.Kind != "permanent-group-history-scope-floor-v1" || f.CID != d.CID || f.OriginSHA != d.SHA256 || !validUUID(f.Epoch) || f.From < 1 {
		return f, ErrConflict
	}
	original := &Store{db: s.db, epoch: f.Epoch}
	if e := original.checkProspectiveReceipt(ctx, d, f.SourceID, f.BranchID, f.InputSHA, f.ReceiptSHA, 1, f.From); e != nil {
		return f, e
	}
	return f, nil
}
func historyIDs(ids []string) ([]string, error) {
	out := append([]string{}, ids...)
	sort.Strings(out)
	for i, id := range out {
		if !identity(id) || i > 0 && out[i-1] == id {
			return nil, ErrConflict
		}
	}
	return out, nil
}
func historyHas(ids []string, id string) bool {
	i := sort.SearchStrings(ids, id)
	return i < len(ids) && ids[i] == id
}
func (h groupHistoryScope) valid(d NamespaceOriginDescriptor, seq int64) bool {
	if d.Kind != "group" || h.CID != d.CID || h.GroupID != d.GroupID || h.OriginSHA != d.SHA256 || h.Seq != seq || seq < 1 || !validUUID(h.Epoch) || !validUUID(h.SourceID) || !validUUID(h.BranchID) || !validSHA(h.InputSHA) || !validSHA(h.ReceiptSHA) || !validSHA(h.PayloadSHA) {
		return false
	}
	for _, ids := range [][]string{h.Targets, h.Denied} {
		for i, id := range ids {
			if !identity(id) || i > 0 && ids[i-1] >= id {
				return false
			}
		}
	}
	return h.Kind == "group-history-broadcast-v1" && len(h.Targets) == 0 || h.Kind == "group-history-targets-v1"
}
func (h groupHistoryScope) allows(owner string) bool {
	return !historyHas(h.Denied, owner) && (h.Kind == "group-history-broadcast-v1" || historyHas(h.Targets, owner))
}

// Constant-count metadata writes plus the actual frozen target IDs
// for a restricted notice. No per-owner history progress or roster lookup.
// The caller has the live private namespaceInputProof and a new same-TX source
// receipt; replay never reaches this writer or replaces a previous scope.
func (s *Store) recordGroupHistoryScope(ctx context.Context, d NamespaceOriginDescriptor, branch Branch, r Receipt, a AcceptedBranch, m *sdkws.MsgData, owned bool, denied []string) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	if m == nil || !a.Persist || a.Hidden || a.Seq < 1 {
		return ErrConflict
	}
	scope := groupHistoryScope{Kind: "group-history-broadcast-v1", Epoch: s.epoch, CID: d.CID, GroupID: d.GroupID, OriginSHA: d.SHA256, SourceID: r.SourceID, BranchID: branch.ID, InputSHA: r.InputSHA256, ReceiptSHA: directHistoryDigest(r), Seq: a.Seq, Targets: []string{}, Denied: []string{}}
	var e error
	scope.Denied, e = historyIDs(denied)
	if e != nil {
		return e
	}
	// These are actual server/owned notification sources. An ordinary user
	// broadcast keeps history for its original membership generation, including
	// a sender excluded only by IsSenderSync=false from physical online delivery.
	if owned || m.ContentType >= constant.NotificationBegin || msgprocessor.IsNotificationByMsg(m) {
		scope.Kind = "group-history-targets-v1"
		scope.Targets, e = historyIDs(branch.Recipients)
		if e != nil {
			return e
		}
	}
	var duty bson.Raw
	if e = s.db.Collection(Effects).FindOne(ctx, bson.M{"_id": key(s.epoch, "branch", branch.ID)}).Decode(&duty); e != nil {
		return e
	}
	scope.PayloadSHA, _ = duty.Lookup("payload_sha256").StringValueOK()
	if !scope.valid(d, a.Seq) {
		return ErrConflict
	}
	body, e := json.Marshal(scope)
	if e != nil {
		return e
	}
	sum := hash(body)
	scopeID := groupScopeID(s.epoch, branch.ID)
	if e = immutableImportDocument(ctx, s.db.Collection(Receipts), scopeID, groupHistoryScopeRecord{ID: scopeID, Kind: "group-history-scope-v1", JSON: string(body), SHA: sum}); e != nil {
		return e
	}
	floor, e := s.readGroupScopeFloor(ctx, d)
	if e == mongo.ErrNoDocuments {
		floor = groupHistoryScopeFloor{ID: groupScopeFloorID(d.CID), Kind: "permanent-group-history-scope-floor-v1", CID: d.CID, OriginSHA: d.SHA256, Epoch: s.epoch, SourceID: r.SourceID, BranchID: branch.ID, InputSHA: r.InputSHA256, ReceiptSHA: directHistoryDigest(r), From: a.Seq}
		if _, e = s.db.Collection(Sequences).InsertOne(ctx, floor); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else if floor.From > a.Seq {
		return ErrConflict
	}
	result, e := s.db.Collection(Effects).UpdateOne(ctx, bson.M{"_id": key(s.epoch, "branch", branch.ID), "source_id": r.SourceID, "input_sha256": r.InputSHA256, "seq": a.Seq, "payload_sha256": scope.PayloadSHA, "history_scope_sha256": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"history_scope_sha256": sum, "history_scope_id": scopeID}})
	if e != nil {
		return e
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	model := table.MsgDocModel{}
	field := fmt.Sprintf("msgs.%d", model.GetMsgIndex(a.Seq))
	result, e = s.db.Collection(model.TableName()).UpdateOne(ctx, bson.M{"doc_id": model.GetDocID(d.CID, a.Seq), field + ".msg.seq": a.Seq, field + ".history_scope_sha256": bson.M{"$exists": false}, field + ".global_deleted": bson.M{"$ne": true}}, bson.M{"$set": bson.M{field + ".history_scope_id": scopeID, field + ".history_scope_sha256": sum}})
	if e != nil {
		return e
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}

// At most one existing history page (64 requested seqs), never a roster scan.
// Imported ranges and later forbid=false admission are only candidate ranges:
// neither can overrule a persisted original positive target scope.
func (s *Store) filterGroupHistoryScopes(ctx context.Context, d NamespaceOriginDescriptor, owner string, allowed map[int64]bool) error {
	if d.Kind != "group" {
		return nil
	}
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	head, e := s.directHistoryCounter(ctx, d.CID)
	if e != nil {
		return e
	}
	floor, e := s.readGroupScopeFloor(ctx, d)
	missingFloor := e == mongo.ErrNoDocuments
	if e != nil && !missingFloor {
		return e
	}
	model := table.MsgDocModel{}
	wanted := make([]int64, 0, len(allowed))
	for seq := range allowed {
		wanted = append(wanted, seq)
	}
	var legacy map[int64]bool
	loadedLegacy := false
	var bodyScopes map[int64]bool
	loadedBodyScopes := false
	originalBody := func(seq int64) (bool, error) {
		if !loadedBodyScopes {
			var e error
			bodyScopes, e = s.legacyGroupBodyScopePermissions(ctx, d, owner, wanted)
			if e != nil {
				return false, e
			}
			loadedBodyScopes = true
		}
		return bodyScopes[seq], nil
	}
	originalLegacy := func(seq int64) (bool, error) {
		if !loadedLegacy {
			var e error
			legacy, e = s.legacyGroupScopePermissions(ctx, d, owner, wanted)
			if e != nil {
				return false, e
			}
			loadedLegacy = true
		}
		return legacy[seq], nil
	}
	for seq := range allowed {
		required := seq > head.SeedMaximum || !missingFloor && seq >= floor.From
		var row struct {
			Slot struct {
				Seq     int64    `bson:"seq"`
				Exists  bool     `bson:"exists"`
				Deleted bool     `bson:"global_deleted"`
				Denied  []string `bson:"del_list"`
				ScopeID string   `bson:"history_scope_id"`
				SHA     string   `bson:"history_scope_sha256"`
			} `bson:"slot"`
		}
		projection := bson.M{"_id": 0, "slot": bson.M{"$let": bson.M{"vars": bson.M{"s": bson.M{"$arrayElemAt": bson.A{"$msgs", model.GetMsgIndex(seq)}}}, "in": bson.M{
			"seq": "$$s.msg.seq", "exists": bson.M{"$ne": bson.A{bson.M{"$ifNull": bson.A{"$$s.msg", nil}}, nil}}, "global_deleted": bson.M{"$ifNull": bson.A{"$$s.global_deleted", false}}, "del_list": bson.M{"$ifNull": bson.A{"$$s.del_list", bson.A{}}}, "history_scope_id": "$$s.history_scope_id", "history_scope_sha256": "$$s.history_scope_sha256",
		}}}}
		cursor, e := s.db.Collection(model.TableName()).Aggregate(ctx, mongo.Pipeline{bson.D{{Key: "$match", Value: bson.M{"doc_id": model.GetDocID(d.CID, seq)}}}, bson.D{{Key: "$project", Value: projection}}})
		if e != nil {
			return e
		}
		exists := cursor.Next(ctx)
		if exists {
			e = cursor.Decode(&row)
		}
		if e == nil {
			e = cursor.Err()
		}
		closeErr := cursor.Close(ctx)
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
		if !exists {
			if required {
				delete(allowed, seq)
			} else {
				keep, e := originalLegacy(seq)
				if e != nil {
					return e
				}
				if !keep {
					delete(allowed, seq)
				}
			}
			continue
		}
		slot := row.Slot
		if slot.Deleted {
			delete(allowed, seq)
			continue
		}
		if !slot.Exists || slot.Seq != seq {
			if required || slot.ScopeID != "" || slot.SHA != "" {
				delete(allowed, seq)
			} else {
				keep, e := originalLegacy(seq)
				if e != nil {
					return e
				}
				if !keep {
					delete(allowed, seq)
				}
			}
			continue
		}
		hidden := false
		for _, uid := range slot.Denied {
			hidden = hidden || uid == owner
		}
		if hidden {
			delete(allowed, seq)
			continue
		}
		if slot.ScopeID == "" && slot.SHA == "" {
			if required {
				return ErrUnprepared
			}
			keep, e := originalBody(seq)
			if e != nil {
				return e
			}
			if !keep {
				delete(allowed, seq)
			}
			continue
		}
		var raw bson.Raw
		var record groupHistoryScopeRecord
		var scope groupHistoryScope
		if !identity(slot.ScopeID) || !validSHA(slot.SHA) {
			return ErrConflict
		}
		if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": slot.ScopeID}).Decode(&raw); e != nil {
			return e
		}
		if !exactFields(raw, []string{"_id", "kind", "scope_json", "scope_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &record) != nil || record.ID != slot.ScopeID || record.Kind != "group-history-scope-v1" || record.SHA != slot.SHA || hash([]byte(record.JSON)) != slot.SHA || decodeImportJSON(record.JSON, &scope) != nil || !scope.valid(d, seq) || record.ID != groupScopeID(scope.Epoch, scope.BranchID) || directHistoryDigest(scope) != slot.SHA {
			return ErrConflict
		}
		// Original source epoch, receipt and branch remain exact after a later
		// migration. Never substitute this activation's receipt or current targets.
		original := &Store{db: s.db, epoch: scope.Epoch}
		if e = original.checkProspectiveReceipt(ctx, d, scope.SourceID, scope.BranchID, scope.InputSHA, scope.ReceiptSHA, 1, seq); e != nil {
			return e
		}
		var duty bson.Raw
		if e = s.db.Collection(Effects).FindOne(ctx, bson.M{"_id": key(scope.Epoch, "branch", scope.BranchID)}).Decode(&duty); e != nil {
			return e
		}
		actual, ok := duty.Lookup("history_scope_sha256").StringValueOK()
		reference, rok := duty.Lookup("history_scope_id").StringValueOK()
		payload, pok := duty.Lookup("payload_sha256").StringValueOK()
		if !ok || actual != slot.SHA || !rok || reference != slot.ScopeID || !pok || payload != scope.PayloadSHA {
			return ErrConflict
		}
		if !scope.allows(owner) {
			delete(allowed, seq)
		}
	}
	return nil
}
