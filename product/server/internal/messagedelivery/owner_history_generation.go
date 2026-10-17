package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Each REAL creation/admission command has an immutable generation row. Its
// effective upper is a checked current head or original removal closure, never
// progress rewritten per message or one range across leave gaps.
// These are private same-TX producers, not public history-admission APIs.
type ownerHistoryGeneration struct {
	ID          string `bson:"_id"`
	Kind        string `bson:"kind"`
	Epoch       string `bson:"epoch"`
	CID         string `bson:"conversation_id"`
	Owner       string `bson:"user_id"`
	GroupID     string `bson:"group_id"`
	OriginSHA   string `bson:"origin_sha256"`
	Command     string `bson:"command_id"`
	ReceiptSHA  string `bson:"receipt_sha256"`
	MarkerSHA   string `bson:"marker_sha256"`
	CounterJSON string `bson:"counter_json"`
	CounterSHA  string `bson:"counter_sha256"`
	Head        int64  `bson:"source_head"`
	Lower       int64  `bson:"historical_lower"`
	Forbid      bool   `bson:"forbid_history"`
}
type ownerHistoryInterval struct {
	ID         string `bson:"_id"`
	Kind       string `bson:"kind"`
	Epoch      string `bson:"epoch"`
	CID        string `bson:"conversation_id"`
	Owner      string `bson:"user_id"`
	OriginSHA  string `bson:"origin_sha256"`
	ProofID    string `bson:"proof_id"`
	ProofSHA   string `bson:"proof_sha256"`
	Lower      int64  `bson:"lower"`
	Upper      int64  `bson:"upper"`
	Revision   int64  `bson:"revision"`
	SourceID   string `bson:"source_id"`
	BranchID   string `bson:"branch_id"`
	InputSHA   string `bson:"input_sha256"`
	ReceiptSHA string `bson:"receipt_sha256"`
}
type ownerHistoryFloor struct {
	ID        string `bson:"_id"`
	Kind      string `bson:"kind"`
	Epoch     string `bson:"epoch"`
	CID       string `bson:"conversation_id"`
	Owner     string `bson:"user_id"`
	OriginSHA string `bson:"origin_sha256"`
	ProofID   string `bson:"proof_id"`
	ProofSHA  string `bson:"proof_sha256"`
	Floor     int64  `bson:"floor"`
	Revision  int64  `bson:"revision"`
}

func historyGenerationID(epoch, cid, owner, command string) string {
	return key(epoch, "owner-history-generation", cid+"\x00"+owner+"\x00"+command)
}
func historyIntervalID(epoch, kind, cid, owner, generation string) string {
	return key(epoch, "owner-history-segment", kind+"\x00"+cid+"\x00"+owner+"\x00"+generation)
}
func historyFloorID(epoch, cid, owner string) string {
	return key(epoch, "owner-history-floor", cid+"\x00"+owner)
}
func historyGenerationShape(raw bson.Raw) bool {
	return exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "group_id", "origin_sha256", "command_id", "receipt_sha256", "marker_sha256", "counter_json", "counter_sha256"}, []string{"source_head", "historical_lower"}, []string{"forbid_history"}, "")
}
func historyIntervalShape(raw bson.Raw) bool {
	return exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "proof_id", "proof_sha256", "source_id", "branch_id", "input_sha256", "receipt_sha256"}, []string{"lower", "upper", "revision"}, nil, "")
}
func historyFloorShape(raw bson.Raw) bool {
	return exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "proof_id", "proof_sha256"}, []string{"floor", "revision"}, nil, "")
}

func (s *Store) readHistoryGeneration(ctx context.Context, d NamespaceOriginDescriptor, owner, id, sum string) (ownerHistoryGeneration, error) {
	var g ownerHistoryGeneration
	var raw bson.Raw
	if e := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": id}).Decode(&raw); e != nil {
		return g, errors.Join(ErrUnprepared, e)
	}
	if !historyGenerationShape(raw) || bson.Unmarshal(raw, &g) != nil || directHistoryDigest(g) != sum || g.ID != id || g.ID != historyGenerationID(s.epoch, d.CID, owner, g.Command) || g.Kind != "namespace-owner-history-generation-v1" && g.Kind != "namespace-owner-created-history-generation-v1" || g.Epoch != s.epoch || g.CID != d.CID || g.Owner != owner || d.Kind != "group" || g.GroupID != d.GroupID || g.OriginSHA != d.SHA256 || !validUUID(g.Command) || g.Head < 0 || g.Head >= math.MaxInt64-1 || g.Lower < 0 || g.Lower >= math.MaxInt64 || !validSHA(g.ReceiptSHA) || !validSHA(g.MarkerSHA) || hash([]byte(g.CounterJSON)) != g.CounterSHA {
		return g, ErrConflict
	}
	var original counter
	if decodeImportJSON(g.CounterJSON, &original) != nil || original.ID != key(s.epoch, "sequence", d.CID) || original.Epoch != s.epoch || original.CID != d.CID || original.Maximum != g.Head || original.SeedMaximum < 0 || original.SeedMaximum > g.Head || !validSHA(original.SeedEvidenceSHA) || original.Revision < 1 {
		return g, ErrConflict
	}
	current, e := s.directHistoryCounter(ctx, d.CID)
	if e != nil {
		return g, e
	}
	if current.SeedMaximum != original.SeedMaximum || current.SeedEvidenceSHA != original.SeedEvidenceSHA || current.Maximum < original.Maximum || current.Revision < original.Revision {
		return g, ErrConflict
	}
	if g.Kind == "namespace-owner-created-history-generation-v1" {
		return g, createdHistoryMember(g, d, owner, s.activationSHA, s.historyRead(ctx))
	}
	var receiptRaw, markerRaw bson.Raw
	if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-admission", g.Command)}).Decode(&receiptRaw); e != nil {
		return g, e
	}
	receipt, e := s.decodeAdmissionReceipt(receiptRaw)
	if e != nil {
		return g, e
	}
	if receipt.CommandID != g.Command || receipt.GroupID != d.GroupID || receipt.ForbidHistory != g.Forbid || directHistoryDigest(receipt) != g.ReceiptSHA {
		return g, ErrConflict
	}
	if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-admission-owner", g.Command+"\x00"+owner)}).Decode(&markerRaw); e != nil {
		return g, e
	}
	var marker memberAdmissionOwner
	if !exactFields(markerRaw, []string{"_id", "kind", "epoch", "command_id", "input_sha256", "group_id", "user_id", "normal_cid", "notification_cid"}, []string{"group_revision", "normal_revision", "notification_revision", "normal_lower", "notification_lower"}, nil, "") || bson.Unmarshal(markerRaw, &marker) != nil || directHistoryDigest(marker) != g.MarkerSHA || marker.ID != key(s.epoch, "member-admission-owner", g.Command+"\x00"+owner) || marker.Kind != "member-admission-owner-v1" || marker.Epoch != s.epoch || marker.CommandID != g.Command || marker.GroupID != d.GroupID || marker.UserID != owner || marker.InputSHA256 != receipt.InputSHA256 || marker.GroupRevision != receipt.GroupRevision {
		return g, ErrConflict
	}
	normal, notice, e := groupNamespacePair(d.GroupID)
	if e != nil {
		return g, e
	}
	if marker.NormalCID != normal || marker.NotificationCID != notice {
		return g, ErrConflict
	}
	lower := marker.NormalLower
	if d.CID == notice {
		lower = marker.NotificationLower
	} else if d.CID != normal {
		return g, ErrConflict
	}
	if lower != g.Lower || g.Forbid && lower < g.Head+1 {
		return g, ErrConflict
	}
	return g, nil
}

// The capability is still live here, after RecordMemberAdmission inserted its
// original receipt/owner markers and fenced BOTH independent namespace heads.
func (s *Store) recordMemberHistoryAdmission(ctx context.Context, cap *groupauthority.MemberAdmissionScope, forbid bool) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	if cap == nil {
		return ErrInvalid
	}
	info, e := cap.Inspect(ctx, s.db)
	if e != nil {
		return e
	}
	if info.Command.Epoch != s.epoch {
		return ErrConflict
	}
	var receiptRaw bson.Raw
	if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-admission", info.Command.ID)}).Decode(&receiptRaw); e != nil {
		return e
	}
	receipt, e := s.decodeAdmissionReceipt(receiptRaw)
	if e != nil {
		return e
	}
	if receipt.ForbidHistory != forbid || receipt.GroupID != info.Command.GroupID || receipt.GroupRevision != info.GroupRevision || receipt.NewMemberCount != int64(len(info.NewMembers)) {
		return ErrConflict
	}
	normal, notice, e := groupNamespacePair(info.Command.GroupID)
	if e != nil {
		return e
	}
	for _, owner := range info.NewMembers {
		for _, cid := range []string{normal, notice} {
			d, e := s.readNamespacePrincipals(ctx, cid)
			if e != nil {
				return e
			}
			if d.Kind != "group" || d.GroupID != info.Command.GroupID {
				return ErrConflict
			}
			c, e := s.membershipCounter(ctx, cid, d.GroupID, false)
			if e != nil {
				return e
			}
			w, e := s.readStoredMembershipWindow(ctx, cid, owner)
			if e != nil {
				return e
			}
			if w.Closed || w.LastCommand != info.Command.ID || w.GroupRevision != info.GroupRevision {
				return ErrConflict
			}
			var marker memberAdmissionOwner
			if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-admission-owner", info.Command.ID+"\x00"+owner)}).Decode(&marker); e != nil {
				return e
			}
			counterJSON, e := json.Marshal(c)
			if e != nil {
				return e
			}
			generation := ownerHistoryGeneration{ID: historyGenerationID(s.epoch, cid, owner, info.Command.ID), Kind: "namespace-owner-history-generation-v1", Epoch: s.epoch, CID: cid, Owner: owner, GroupID: d.GroupID, OriginSHA: d.SHA256, Command: info.Command.ID, ReceiptSHA: directHistoryDigest(receipt), MarkerSHA: directHistoryDigest(marker), CounterJSON: string(counterJSON), CounterSHA: hash(counterJSON), Head: c.Maximum, Lower: w.Lower, Forbid: forbid}
			sum := directHistoryDigest(generation)
			if e = immutableImportDocument(ctx, s.db.Collection(Receipts), generation.ID, generation); e != nil {
				return e
			}
			if _, e = s.readHistoryGeneration(ctx, d, owner, generation.ID, sum); e != nil {
				return e
			}
			live := ownerHistoryInterval{ID: historyIntervalID(s.epoch, "namespace-owner-live-history-v1", cid, owner, info.Command.ID), Kind: "namespace-owner-live-history-v1", Epoch: s.epoch, CID: cid, Owner: owner, OriginSHA: d.SHA256, ProofID: generation.ID, ProofSHA: sum, Lower: max(c.Maximum+1, w.Lower), Revision: 1}
			if e = immutableImportDocument(ctx, s.db.Collection(Sequences), live.ID, live); e != nil {
				return e
			}
			if !forbid && max(int64(1), w.Lower) <= c.Maximum {
				past := ownerHistoryInterval{ID: historyIntervalID(s.epoch, "namespace-owner-history-admission-v1", cid, owner, info.Command.ID), Kind: "namespace-owner-history-admission-v1", Epoch: s.epoch, CID: cid, Owner: owner, OriginSHA: d.SHA256, ProofID: generation.ID, ProofSHA: sum, Lower: max(int64(1), w.Lower), Upper: c.Maximum, Revision: 1}
				if e = immutableImportDocument(ctx, s.db.Collection(Sequences), past.ID, past); e != nil {
					return e
				}
			}
			if forbid {
				if e = s.advanceOwnerHistoricalFloor(ctx, d, owner, generation); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (s *Store) advanceOwnerHistoricalFloor(ctx context.Context, d NamespaceOriginDescriptor, owner string, g ownerHistoryGeneration) error {
	if !g.Forbid || g.Lower < g.Head+1 {
		return ErrConflict
	}
	next := ownerHistoryFloor{ID: historyFloorID(s.epoch, d.CID, owner), Kind: "namespace-owner-historical-floor-v1", Epoch: s.epoch, CID: d.CID, Owner: owner, OriginSHA: d.SHA256, ProofID: g.ID, ProofSHA: directHistoryDigest(g), Floor: g.Lower, Revision: 1}
	var raw bson.Raw
	e := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": next.ID}).Decode(&raw)
	if errors.Is(e, mongo.ErrNoDocuments) {
		_, e = s.db.Collection(Sequences).InsertOne(ctx, next)
		return e
	}
	if e != nil {
		return e
	}
	old, e := s.readOwnerHistoricalFloor(ctx, d, owner)
	if e != nil {
		return e
	}
	if old.Floor >= next.Floor {
		return nil
	}
	if old.Revision >= math.MaxInt64-1 {
		return ErrConflict
	}
	next.Revision = old.Revision + 1
	result, e := s.db.Collection(Sequences).ReplaceOne(ctx, bson.M{"_id": old.ID, "revision": old.Revision, "floor": old.Floor, "proof_sha256": old.ProofSHA}, next)
	if e != nil {
		return e
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) readOwnerHistoricalFloor(ctx context.Context, d NamespaceOriginDescriptor, owner string) (ownerHistoryFloor, error) {
	var f ownerHistoryFloor
	var raw bson.Raw
	e := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": historyFloorID(s.epoch, d.CID, owner)}).Decode(&raw)
	if e != nil {
		return f, e
	}
	if !historyFloorShape(raw) || bson.Unmarshal(raw, &f) != nil || f.ID != historyFloorID(s.epoch, d.CID, owner) || f.Kind != "namespace-owner-historical-floor-v1" || f.Epoch != s.epoch || f.CID != d.CID || f.Owner != owner || f.OriginSHA != d.SHA256 || f.Floor < 1 || f.Revision < 1 || f.Revision >= math.MaxInt64 {
		return f, ErrConflict
	}
	g, e := s.readHistoryGeneration(ctx, d, owner, f.ProofID, f.ProofSHA)
	if e != nil {
		return f, e
	}
	if !g.Forbid || g.Lower != f.Floor {
		return f, ErrConflict
	}
	return f, nil
}
