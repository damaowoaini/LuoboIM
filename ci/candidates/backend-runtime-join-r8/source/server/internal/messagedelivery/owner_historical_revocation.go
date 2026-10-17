package messagedelivery

import (
	"context"
	"errors"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// Import prospective Lower is NOT this floor. Only a real authenticated
// absent->present member admission with captured forbidHistory=true can revoke
// an existing historical base. Clear already writes authoritative current
// owner/global floors in the same visibility TX; no fresh cut is captured here.
func (s *Store) revokeOwnerHistoricalBase(ctx context.Context, cap *groupauthority.MemberAdmissionScope, forbidHistory bool) error {
	if !forbidHistory {
		return nil
	}
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
	if !receipt.ForbidHistory || receipt.GroupID != info.Command.GroupID || receipt.GroupRevision != info.GroupRevision || receipt.NewMemberCount != int64(len(info.NewMembers)) {
		return ErrConflict
	}
	normal, notice, e := groupNamespacePair(info.Command.GroupID)
	if e != nil {
		return e
	}
	for _, owner := range info.NewMembers {
		for _, cid := range []string{normal, notice} {
			var raw bson.Raw
			e = s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceOwnerProspectiveID(s.epoch, cid, owner)}).Decode(&raw)
			if errors.Is(e, mongo.ErrNoDocuments) {
				continue
			}
			if e != nil {
				return e
			}
			d, e := s.readNamespacePrincipals(ctx, cid)
			if e != nil {
				return e
			}
			if d.Kind != "group" || d.GroupID != info.Command.GroupID {
				return ErrConflict
			}
			baseSHA, ok := raw.Lookup("base_sha256").StringValueOK()
			if !ok || !validSHA(baseSHA) {
				return ErrConflict
			}
			old, e := s.readOwnerProspective(ctx, d, owner, baseSHA)
			if e != nil {
				return e
			}
			window, e := s.ReadMembershipWindow(ctx, cid, owner)
			if e != nil {
				return e
			}
			if window.LastCommand != info.Command.ID || window.GroupRevision != info.GroupRevision || window.Closed {
				return ErrConflict
			}
			floor := max(old.HistoricalFloor, window.Lower)
			if floor <= old.HistoricalFloor {
				continue
			}
			if old.Revision >= math.MaxInt64-1 {
				return ErrConflict
			}
			next := old
			next.Revision++
			next.HistoricalFloor = floor
			next.RevocationCommand = info.Command.ID
			next.RevocationSHA = directHistoryDigest(receipt)
			if e = s.checkOwnerHistoricalRevocation(ctx, d, owner, next); e != nil {
				return e
			}
			result, e := s.db.Collection(Sequences).ReplaceOne(ctx, bson.M{"_id": old.ID, "revision": old.Revision, "historical_floor": old.HistoricalFloor, "admission_sha256": old.AdmissionSHA}, next)
			if e != nil {
				return e
			}
			if result.MatchedCount != 1 || result.ModifiedCount != 1 {
				return ErrConflict
			}
		}
	}
	return nil
}
func (s *Store) checkOwnerHistoricalRevocation(ctx context.Context, d NamespaceOriginDescriptor, owner string, f namespaceOwnerProspective) error {
	if d.Kind != "group" || !validUUID(f.RevocationCommand) || !validSHA(f.RevocationSHA) || f.HistoricalFloor < 1 {
		return ErrConflict
	}
	var raw bson.Raw
	if e := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-admission", f.RevocationCommand)}).Decode(&raw); e != nil {
		return errors.Join(ErrUnprepared, e)
	}
	receipt, e := s.decodeAdmissionReceipt(raw)
	if e != nil {
		return e
	}
	if !receipt.ForbidHistory || receipt.Epoch != s.epoch || receipt.GroupID != d.GroupID || directHistoryDigest(receipt) != f.RevocationSHA {
		return ErrConflict
	}
	var markerRaw bson.Raw
	var m memberAdmissionOwner
	if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "member-admission-owner", f.RevocationCommand+"\x00"+owner)}).Decode(&markerRaw); e != nil {
		return errors.Join(ErrUnprepared, e)
	}
	if !exactFields(markerRaw, []string{"_id", "kind", "epoch", "command_id", "input_sha256", "group_id", "user_id", "normal_cid", "notification_cid"}, []string{"group_revision", "normal_revision", "notification_revision", "normal_lower", "notification_lower"}, nil, "") || bson.Unmarshal(markerRaw, &m) != nil || m.ID != key(s.epoch, "member-admission-owner", f.RevocationCommand+"\x00"+owner) || m.Kind != "member-admission-owner-v1" || m.Epoch != s.epoch || m.CommandID != f.RevocationCommand || m.UserID != owner || m.InputSHA256 != receipt.InputSHA256 || m.GroupID != d.GroupID || m.GroupRevision != receipt.GroupRevision {
		return ErrConflict
	}
	normal, notice, e := groupNamespacePair(d.GroupID)
	if e != nil {
		return e
	}
	if m.NormalCID != normal || m.NotificationCID != notice {
		return ErrConflict
	}
	lower := m.NormalLower
	if d.CID == notice {
		lower = m.NotificationLower
	} else if d.CID != normal {
		return ErrConflict
	}
	if lower != f.HistoricalFloor {
		return ErrConflict
	}
	return nil
}
