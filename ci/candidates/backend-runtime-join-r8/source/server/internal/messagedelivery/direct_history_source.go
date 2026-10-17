package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/retention"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Called ONLY while the private new-source proof is active, after the actual
// receipt/positive slot/duty were inserted in this SAME transaction. It never
// captures a new privacy cut, invents a historical owner, or handles replays.
func (s *Store) recordDirectHistoryAcceptance(ctx context.Context, input Envelope, r Receipt, owned bool, denied map[string][]string) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	proof, ok := ctx.Value(namespaceInputKey{}).(*namespaceInputProof)
	if !ok || proof == nil || !proof.active.Load() || !proof.newSource.Load() || proof.store != s || proof.session != mongo.SessionFromContext(ctx) || proof.digest != r.InputSHA256 || r.SourceID != input.ID || len(r.Branches) != len(input.Branches) {
		return ErrUnprepared
	}
	for i, accepted := range r.Branches {
		branch := input.Branches[i]
		if accepted.ID != branch.ID || accepted.ConversationID != branch.ConversationID || accepted.Persist != branch.Persist {
			return ErrConflict
		}
		if !accepted.Persist || accepted.Hidden {
			continue
		}
		if accepted.Seq < 1 || accepted.Seq >= math.MaxInt64 {
			return ErrConflict
		}
		var m sdkws.MsgData
		if proto.Unmarshal(branch.Payload, &m) != nil {
			return ErrInvalid
		}
		d, e := DescribeNamespace(&m)
		if e != nil {
			return e
		}
		if d.CID != branch.ConversationID {
			return ErrConflict
		}
		if d.Kind == "group" {
			if e = s.recordGroupHistoryScope(ctx, d, branch, r, accepted, &m, owned, denied[branch.ID]); e != nil {
				return e
			}
			continue
		}
		actual, e := s.readNamespacePrincipals(ctx, d.CID)
		if e != nil {
			return e
		}
		if actual.SHA256 != d.SHA256 || actual.Kind != d.Kind {
			return ErrConflict
		}
		head, e := s.directHistoryCounter(ctx, d.CID)
		if e != nil {
			return e
		}
		if accepted.Seq <= head.SeedMaximum || accepted.Seq > head.Maximum {
			return ErrConflict
		}
		owners := append([]string(nil), d.OriginIDs...)
		if d.Kind == "server-notification" {
			owners = []string{m.RecvID}
		}
		seen := map[string]bool{}
		for _, owner := range owners {
			if seen[owner] {
				continue
			}
			seen[owner] = true
			blocked := false
			for _, uid := range denied[branch.ID] {
				blocked = blocked || owner == uid
			}
			if blocked {
				continue
			}
			b, baseSHA, e := s.readDirectHistoryBase(ctx, d, owner)
			fresh := errors.Is(e, mongo.ErrNoDocuments)
			if fresh {
				// A newly effective original participant gets NO imported history. This
				// base is closed-empty. Its separate immutable source proof can only
				// authorize future >= the pre-existing seed maximum+1.
				_, cutSHA, err := s.namespaceCutover(ctx)
				if err != nil {
					return err
				}
				b = directNamespaceWindow{ID: directNamespaceWindowID(s.epoch, d.CID, owner), Kind: directSourceOwnerKind, Epoch: s.epoch, ConversationID: d.CID, UserID: owner, OriginSHA256: d.SHA256, Closed: true, HeaderSHA256: cutSHA, BoundarySHA256: directHistoryDigest([]string{r.SourceID, branch.ID, directHistoryDigest(r), owner})}
				raw, err := bson.Marshal(b)
				if err != nil {
					return err
				}
				baseSHA = hash(raw)
				if _, err = s.db.Collection(Sequences).InsertOne(ctx, b); err != nil {
					return err
				}
			} else if e != nil {
				return e
			}
			admission := namespaceimport.ProspectiveOwnerAdmission{Kind: "actual-positive-source-v1", CID: d.CID, OwnerID: owner, OriginSHA256: d.SHA256, SeedMaximum: head.SeedMaximum, Lower: head.SeedMaximum + 1, SourceID: r.SourceID, BranchID: branch.ID, InputSHA256: r.InputSHA256, ReceiptSHA256: directHistoryDigest(r)}
			admissionSHA, e := namespaceimport.ProspectiveAdmissionSHA256(admission)
			if e != nil {
				return e
			}
			admissionJSON, e := json.Marshal(admission)
			if e != nil {
				return e
			}
			expected := namespaceOwnerProspective{ID: namespaceOwnerProspectiveID(s.epoch, d.CID, owner), Kind: "namespace-owner-prospective-v1", Epoch: s.epoch, CID: d.CID, Owner: owner, OriginSHA: d.SHA256, BaseSHA: baseSHA, AdmissionJSON: string(admissionJSON), AdmissionSHA: admissionSHA, SourceID: r.SourceID, BranchID: branch.ID, ReceiptSHA: directHistoryDigest(r), InputSHA: r.InputSHA256, SeedMaximum: head.SeedMaximum, Lower: head.SeedMaximum + 1, Upper: accepted.Seq, Revision: 1}
			// The immutable admission proof is not regenerated from future/cache state.
			// Existing held-roster admissions keep their original page proof exactly.
			var oldRaw bson.Raw
			e = s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": expected.ID}).Decode(&oldRaw)
			if errors.Is(e, mongo.ErrNoDocuments) {
				proof := namespaceOwnerAdmissionProof{ID: namespaceOwnerAdmissionID(s.epoch, d.CID, owner), Kind: "namespace-owner-admission-v1", Epoch: s.epoch, CID: d.CID, Owner: owner, OriginSHA: d.SHA256, BaseSHA: baseSHA, AdmissionJSON: expected.AdmissionJSON, AdmissionSHA: admissionSHA}
				if _, e = s.db.Collection(Receipts).InsertOne(ctx, proof); e != nil {
					return e
				}
				if _, e = s.db.Collection(Sequences).InsertOne(ctx, expected); e != nil {
					return e
				}
			} else {
				if e != nil {
					return e
				}
				old, e := s.readOwnerProspective(ctx, d, owner, baseSHA)
				if e != nil {
					return e
				}
				if old.SeedMaximum != expected.SeedMaximum || old.Lower != expected.Lower || old.Upper >= expected.Upper || old.Revision >= math.MaxInt64-1 {
					return ErrConflict
				}
				expected.AdmissionJSON, expected.AdmissionSHA = old.AdmissionJSON, old.AdmissionSHA
				expected.HistoricalFloor, expected.RevocationCommand, expected.RevocationSHA = old.HistoricalFloor, old.RevocationCommand, old.RevocationSHA
				expected.Revision = old.Revision + 1
				result, e := s.db.Collection(Sequences).ReplaceOne(ctx, bson.M{"_id": old.ID, "kind": old.Kind, "epoch": s.epoch, "base_sha256": baseSHA, "revision": old.Revision, "upper": old.Upper, "historical_floor": old.HistoricalFloor}, expected)
				if e != nil {
					return e
				}
				if result.MatchedCount != 1 || result.ModifiedCount != 1 {
					return ErrConflict
				}
			}
			// Only the ACTUAL normal stored branch creates/reopens a standard visible
			// conversation. A positive n_ branch does not manufacture a normal row or
			// borrow its head; silent Seq0 cannot reach this block.
			if d.CID == msgprocessor.GetChatConversationIDByMsg(&m) {
				if e = s.projectDirectConversation(ctx, &m, d.CID, owner); e != nil {
					return e
				}
			}
		}
	}
	return nil
}
func (s *Store) projectDirectConversation(ctx context.Context, m *sdkws.MsgData, cid, owner string) error {
	if e := s.requireTX(ctx); e != nil {
		return e
	}
	peer := m.SendID
	if owner == m.SendID {
		peer = m.RecvID
	}
	var row relation.ConversationModel
	collection := s.db.Collection("conversation")
	filter := bson.M{"owner_user_id": owner, "conversation_id": cid}
	e := collection.FindOne(ctx, filter).Decode(&row)
	if errors.Is(e, mongo.ErrNoDocuments) {
		row = relation.ConversationModel{OwnerUserID: owner, ConversationID: cid, ConversationType: m.SessionType, UserID: peer, CreateTime: time.Now().UTC().Truncate(time.Millisecond)}
		doc, e := retention.PrepareDocument(&row)
		if e != nil {
			return e
		}
		return retention.InsertPrepared(ctx, collection, []any{doc})
	}
	if e != nil {
		return e
	}
	// The original receiver creation typo wrote self as peer. Only that exact
	// original participant value or the correct typed peer can be repaired;
	// unrelated legacy identities are not silently adopted.
	if row.OwnerUserID != owner || row.ConversationID != cid || row.GroupID != "" || row.ConversationType != m.SessionType || row.UserID != peer && row.UserID != owner || row.MinSeq < 0 || row.MaxSeq < 0 || m.SessionType != constant.SingleChatType && m.SessionType != constant.NotificationChatType {
		return ErrConflict
	}
	if row.MaxSeq == 0 && row.UserID == peer {
		return nil
	}
	result, e := collection.UpdateOne(ctx, bson.M{"owner_user_id": owner, "conversation_id": cid, "conversation_type": row.ConversationType, "user_id": row.UserID, "max_seq": row.MaxSeq}, bson.M{"$set": bson.M{"max_seq": int64(0), "user_id": peer}})
	if e != nil {
		return e
	}
	if result.MatchedCount != 1 {
		return ErrConflict
	}
	return nil
}
