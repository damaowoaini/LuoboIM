package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// A generation is opened only by the original creation/admission/import proof.
// Messages never enumerate owners or mutate these rows. Removal closes ONLY
// the affected original generations at the independently fenced sg/n heads.
type ownerHistoryClosure struct {
	ID          string `bson:"_id"`
	Kind        string `bson:"kind"`
	Epoch       string `bson:"epoch"`
	CID         string `bson:"conversation_id"`
	Owner       string `bson:"user_id"`
	OriginSHA   string `bson:"origin_sha256"`
	Generation  string `bson:"generation_command"`
	WindowJSON  string `bson:"window_json"`
	WindowSHA   string `bson:"window_sha256"`
	RemovalJSON string `bson:"removal_json"`
	RemovalSHA  string `bson:"removal_sha256"`
	ReceiptID   string `bson:"receipt_id"`
	ReceiptSHA  string `bson:"receipt_sha256"`
	Upper       int64  `bson:"upper"`
}
type historyOriginalRead func(string, string) (bson.Raw, error)

func historyAbsent(e error) bool {
	return errors.Is(e, mongo.ErrNoDocuments) || errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent)
}
func historyClosureID(epoch, cid, owner, generation string) string {
	return key(epoch, "owner-history-closure", cid+"\x00"+owner+"\x00"+generation)
}
func (s *Store) historyRead(ctx context.Context) historyOriginalRead {
	return func(collection, id string) (bson.Raw, error) {
		var raw bson.Raw
		e := s.db.Collection(collection).FindOne(ctx, bson.M{"_id": id}).Decode(&raw)
		return raw, e
	}
}

// This is a QUERY bound, not a grant for every sequence. The final reader also
// requires each physical slot's positive history scope. Neither a counter nor
// the current roster can create an owner generation or fill a leave gap.
// finite is accepted ONLY after the caller verified the original admission/
// generation AND its exact positive source receipt. Old per-message progress
// rows have this finite endpoint; new static rows retain zero. A changed valid
// current generation cannot erase that old finite range or extend it to head.
func groupHistoryBound(epoch string, d NamespaceOriginDescriptor, owner, command string, lower, head int64, held bool, finite int64, read historyOriginalRead) (int64, error) {
	if d.Kind != "group" || !validUUID(command) || lower < 1 || head < 0 || head >= math.MaxInt64 || finite < 0 || finite != 0 && (finite < lower || finite > head) {
		return 0, ErrConflict
	}
	raw, e := read(Receipts, historyClosureID(epoch, d.CID, owner, command))
	if e == nil {
		var c ownerHistoryClosure
		if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "generation_command", "window_json", "window_sha256", "removal_json", "removal_sha256", "receipt_id", "receipt_sha256"}, []string{"upper"}, nil, "") || bson.Unmarshal(raw, &c) != nil || c.ID != historyClosureID(epoch, d.CID, owner, command) || c.Kind != "namespace-owner-history-closure-v1" || c.Epoch != epoch || c.CID != d.CID || c.Owner != owner || c.OriginSHA != d.SHA256 || c.Generation != command || c.Upper < 0 || c.Upper > head || hash([]byte(c.WindowJSON)) != c.WindowSHA || hash([]byte(c.RemovalJSON)) != c.RemovalSHA {
			return 0, ErrConflict
		}
		decoder := &Store{epoch: epoch}
		var w MembershipWindow
		var info groupauthority.MemberRemovalInfo
		if decodeImportJSON(c.WindowJSON, &w) != nil || !decoder.validMembershipWindow(w, d.CID, owner) || w.GroupID != d.GroupID || w.LastCommand != command || w.Closed && !held || decodeImportJSON(c.RemovalJSON, &info) != nil || info.Command.Epoch != epoch || info.Command.GroupID != d.GroupID || !validUUID(info.Command.ID) || info.GroupRevision <= w.GroupRevision {
			return 0, ErrConflict
		}
		found := false
		seen := map[string]bool{}
		for _, uid := range info.Members {
			if !identity(uid) || seen[uid] {
				return 0, ErrConflict
			}
			seen[uid] = true
			found = found || uid == owner
		}
		if !found {
			return 0, ErrConflict
		}
		receiptRaw, e := read(Receipts, c.ReceiptID)
		if e != nil {
			return 0, e
		}
		r, e := decodeRemovalReceipt(receiptRaw)
		if e != nil || !decoder.validRemovalReceipt(r, info.Command.ID) || c.ReceiptID != r.ID || directHistoryDigest(r) != c.ReceiptSHA || r.InputSHA256 != c.RemovalSHA || r.GroupRevision != info.GroupRevision || r.MemberCount != int64(len(info.Members)) {
			return 0, ErrConflict
		}
		upper := r.Upper
		if d.CID == r.NotificationCID {
			upper = r.NotificationUpper
		} else if d.CID != r.ConversationID {
			return 0, ErrConflict
		}
		if upper != c.Upper || finite > upper {
			return 0, ErrConflict
		}
		if upper < lower {
			return 0, nil
		}
		return upper, nil
	}
	if !historyAbsent(e) {
		return 0, e
	}
	raw, e = read(Sequences, key(epoch, "membership-window", d.CID+"\x00"+owner))
	if e != nil {
		return 0, e
	}
	var w MembershipWindow
	decoder := &Store{epoch: epoch}
	if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"lower", "upper", "revision", "group_revision"}, []string{"closed"}, "") || bson.Unmarshal(raw, &w) != nil || !decoder.validMembershipWindow(w, d.CID, owner) || w.GroupID != d.GroupID {
		return 0, ErrConflict
	}
	if w.LastCommand != command || w.Closed && !held {
		if finite > 0 {
			if w.Closed && finite > w.Upper {
				return 0, ErrConflict
			}
			return finite, nil
		}
		return 0, ErrUnprepared
	}
	if head < lower {
		return 0, nil
	}
	return head, nil
}

// Called after the real removal receipt is inserted, while cap is still live
// in that SAME business TX. 'before' was read before its window was closed.
func (s *Store) recordHistoryClosure(ctx context.Context, cap *groupauthority.MemberRemovalScope, r MemberRemovalReceipt, before MembershipWindow) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	info, e := cap.Inspect(ctx, s.db)
	if e != nil {
		return e
	}
	found := false
	for _, uid := range info.Members {
		found = found || uid == before.UserID
	}
	if !found || info.Command.ID != r.CommandID || info.Command.Epoch != s.epoch || info.Command.GroupID != before.GroupID || directHistoryDigest(info) != r.InputSHA256 {
		return ErrConflict
	}
	d, e := s.readNamespacePrincipals(ctx, before.ConversationID)
	if e != nil {
		return e
	}
	upper := r.Upper
	if d.CID == r.NotificationCID {
		upper = r.NotificationUpper
	} else if d.CID != r.ConversationID {
		return ErrConflict
	}
	w, e := json.Marshal(before)
	if e != nil {
		return e
	}
	body, e := json.Marshal(info)
	if e != nil {
		return e
	}
	c := ownerHistoryClosure{ID: historyClosureID(s.epoch, d.CID, before.UserID, before.LastCommand), Kind: "namespace-owner-history-closure-v1", Epoch: s.epoch, CID: d.CID, Owner: before.UserID, OriginSHA: d.SHA256, Generation: before.LastCommand, WindowJSON: string(w), WindowSHA: hash(w), RemovalJSON: string(body), RemovalSHA: hash(body), ReceiptID: r.ID, ReceiptSHA: directHistoryDigest(r), Upper: upper}
	return immutableImportDocument(ctx, s.db.Collection(Receipts), c.ID, c)
}

type createdHistorySource struct {
	ID         string `bson:"_id"`
	Kind       string `bson:"kind"`
	Epoch      string `bson:"epoch"`
	Command    string `bson:"command_id"`
	InfoJSON   string `bson:"info_json"`
	InfoSHA    string `bson:"info_sha256"`
	ReceiptSHA string `bson:"receipt_sha256"`
}

func createdHistoryID(epoch, command string) string {
	return key(epoch, "created-history-source", command)
}
func createdHistoryMember(g ownerHistoryGeneration, d NamespaceOriginDescriptor, owner, activation string, read historyOriginalRead) error {
	raw, e := read(Receipts, createdHistoryID(g.Epoch, g.Command))
	if e != nil {
		return e
	}
	var source createdHistorySource
	var info groupauthority.NewGroupCreationInfo
	if !exactFields(raw, []string{"_id", "kind", "epoch", "command_id", "info_json", "info_sha256", "receipt_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &source) != nil || source.ID != createdHistoryID(g.Epoch, g.Command) || source.Kind != "namespace-created-history-source-v1" || source.Epoch != g.Epoch || source.Command != g.Command || hash([]byte(source.InfoJSON)) != source.InfoSHA || decodeImportJSON(source.InfoJSON, &info) != nil || info.ID != g.Command || info.Epoch != g.Epoch || info.GroupID != d.GroupID || info.GroupRevision != 1 || info.ActivationSHA256 != activation {
		return ErrConflict
	}
	unsigned := info
	unsigned.InputSHA256 = ""
	if directHistoryDigest(unsigned) != info.InputSHA256 {
		return ErrConflict
	}
	original, e := read(Receipts, key(g.Epoch, "new-group-seed", g.Command))
	if e != nil {
		return e
	}
	decoder := &Store{epoch: g.Epoch, activationSHA: activation}
	r, e := decoder.decodeNewGroupSeed(original)
	if e != nil || directHistoryDigest(r) != g.ReceiptSHA || source.ReceiptSHA != g.ReceiptSHA || r.InputSHA256 != info.InputSHA256 || r.MembersSHA256 != info.MembersSHA256 || r.GroupID != d.GroupID || r.MemberCount != int64(len(info.Members)) || g.Head != 0 || g.Lower != 0 || g.Forbid {
		return ErrConflict
	}
	var originalCounter counter
	if decodeImportJSON(g.CounterJSON, &originalCounter) != nil || originalCounter.SeedMaximum != 0 || originalCounter.Maximum != 0 || originalCounter.SeedEvidenceSHA != r.EvidenceSHA256 {
		return ErrConflict
	}
	evidence := directHistoryDigest([]any{info, r.CutoverSHA256, r.NormalCID, r.NotificationCID})
	expected := seedReceipt(info, r.NormalCID, r.NotificationCID, r.CutoverSHA256, evidence)
	if evidence != r.EvidenceSHA256 || directHistoryDigest(expected) != directHistoryDigest(r) {
		return ErrConflict
	}
	found := false
	seen := map[string]bool{}
	for _, m := range info.Members {
		if !identity(m.UserID) || seen[m.UserID] {
			return ErrConflict
		}
		seen[m.UserID] = true
		if m.UserID == owner {
			if directHistoryDigest(m) != g.MarkerSHA {
				return ErrConflict
			}
			found = true
		}
	}
	if !found {
		return ErrConflict
	}
	return nil
}
func (s *Store) recordCreatedHistoryAdmission(ctx context.Context, info groupauthority.NewGroupCreationInfo, r NewGroupSeedReceipt) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	if info.Epoch != s.epoch || info.ID != r.CommandID || info.InputSHA256 != r.InputSHA256 || r.MemberCount != int64(len(info.Members)) {
		return ErrConflict
	}
	body, e := json.Marshal(info)
	if e != nil {
		return e
	}
	source := createdHistorySource{ID: createdHistoryID(s.epoch, info.ID), Kind: "namespace-created-history-source-v1", Epoch: s.epoch, Command: info.ID, InfoJSON: string(body), InfoSHA: hash(body), ReceiptSHA: directHistoryDigest(r)}
	if e = immutableImportDocument(ctx, s.db.Collection(Receipts), source.ID, source); e != nil {
		return e
	}
	for _, cid := range []string{r.NormalCID, r.NotificationCID} {
		d, e := s.readNamespacePrincipals(ctx, cid)
		if e != nil {
			return e
		}
		head, e := s.directHistoryCounter(ctx, cid)
		if e != nil {
			return e
		}
		if head.Maximum != 0 || head.SeedMaximum != 0 {
			return ErrConflict
		}
		counterJSON, e := json.Marshal(head)
		if e != nil {
			return e
		}
		for _, m := range info.Members {
			g := ownerHistoryGeneration{ID: historyGenerationID(s.epoch, cid, m.UserID, info.ID), Kind: "namespace-owner-created-history-generation-v1", Epoch: s.epoch, CID: cid, Owner: m.UserID, GroupID: d.GroupID, OriginSHA: d.SHA256, Command: info.ID, ReceiptSHA: directHistoryDigest(r), MarkerSHA: directHistoryDigest(m), CounterJSON: string(counterJSON), CounterSHA: hash(counterJSON)}
			sum := directHistoryDigest(g)
			if e = immutableImportDocument(ctx, s.db.Collection(Receipts), g.ID, g); e != nil {
				return e
			}
			live := ownerHistoryInterval{ID: historyIntervalID(s.epoch, "namespace-owner-live-history-v1", cid, m.UserID, info.ID), Kind: "namespace-owner-live-history-v1", Epoch: s.epoch, CID: cid, Owner: m.UserID, OriginSHA: d.SHA256, ProofID: g.ID, ProofSHA: sum, Lower: 1, Revision: 1}
			if e = immutableImportDocument(ctx, s.db.Collection(Sequences), live.ID, live); e != nil {
				return e
			}
		}
	}
	return nil
}
