package messagedelivery

import (
	"context"
	"errors"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// The future admission starts AFTER the captured head, not after an imported
// owner's old Upper. Neither an import Lower nor max_seq=0 fills the gap.
type ownerHistorySegments struct {
	BaseLower, BaseUpper, SeedMaximum, FutureLower, FutureUpper, Floor, HistoricalFloor int64
	BaseClosed                                                                          bool
}

func (p ownerHistorySegments) valid() bool {
	return p.BaseLower >= 0 && p.BaseUpper >= 0 && (p.BaseClosed || p.BaseUpper == 0) && p.SeedMaximum >= 0 && p.SeedMaximum < math.MaxInt64-1 && p.FutureLower == p.SeedMaximum+1 && p.FutureUpper >= 0 && p.FutureUpper < math.MaxInt64 && (p.FutureUpper == 0 || p.FutureUpper >= p.FutureLower) && p.Floor >= 0 && p.HistoricalFloor >= 0
}
func (p ownerHistorySegments) upper() int64 {
	past := p.SeedMaximum
	if p.BaseClosed {
		past = min(past, p.BaseUpper)
	}
	return max(past, p.FutureUpper)
}
func (p ownerHistorySegments) allows(seq int64) bool {
	if !p.valid() || seq < 1 || seq >= math.MaxInt64 || seq < max(p.Floor, p.HistoricalFloor) {
		return false
	}
	past := p.SeedMaximum
	if p.BaseClosed {
		past = min(past, p.BaseUpper)
	}
	return seq >= p.BaseLower && seq <= past || seq >= p.FutureLower && seq <= p.FutureUpper
}
func (s *Store) rawProspective(ctx context.Context, d NamespaceOriginDescriptor, owner string) (namespaceOwnerProspective, error) {
	var raw bson.Raw
	var row namespaceOwnerProspective
	e := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceOwnerProspectiveID(s.epoch, d.CID, owner)}).Decode(&raw)
	if e != nil {
		return row, e
	}
	if !ownerProspectiveShape(raw) || bson.Unmarshal(raw, &row) != nil || !validSHA(row.BaseSHA) {
		return row, ErrConflict
	}
	result, e := s.readOwnerProspective(ctx, d, owner, row.BaseSHA)
	if e != nil {
		return result, errors.Join(ErrConflict, e)
	}
	return result, nil
}

// This only projects CURRENT delivery eligibility. It never changes the stored
// import window or the immutable historical [L,U] used by the history reader.
func (s *Store) prospectiveMemberWindow(ctx context.Context, d NamespaceOriginDescriptor, w MembershipWindow) (MembershipWindow, error) {
	p, e := s.rawProspective(ctx, d, w.UserID)
	cut, _, cutErr := s.namespaceCutover(ctx)
	if cutErr != nil {
		return w, cutErr
	}
	if e == mongo.ErrNoDocuments {
		if w.LastCommand == cut.MigrationID {
			head, e := s.directHistoryCounter(ctx, d.CID)
			if e != nil {
				return w, e
			}
			if !w.Closed {
				w.Closed = true
				w.Upper = head.SeedMaximum
			}
		}
		return w, nil
	}
	if e != nil {
		return w, e
	}
	if w.LastCommand == cut.MigrationID {
		var a namespaceimport.ProspectiveOwnerAdmission
		if decodeImportJSON(p.AdmissionJSON, &a) != nil || a.Kind != "held-cut-roster-v1" {
			return w, ErrConflict
		}
		w.Closed = false
		w.Upper = 0
		w.Lower = max(w.Lower, p.Lower, p.HistoricalFloor)
	}
	if !s.validMembershipWindow(w, d.CID, w.UserID) {
		return w, ErrConflict
	}
	return w, nil
}
func (s *Store) groupOwnerHistory(ctx context.Context, d NamespaceOriginDescriptor, w MembershipWindow) (*ownerHistorySegments, error) {
	f, e := s.rawProspective(ctx, d, w.UserID)
	missing := e == mongo.ErrNoDocuments
	if e != nil && !missing {
		return nil, e
	}
	head, e := s.directHistoryCounter(ctx, d.CID)
	if e != nil {
		return nil, e
	}
	if !missing && (f.SeedMaximum != head.SeedMaximum || f.Upper > head.Maximum) {
		return nil, ErrConflict
	}
	cut, _, e := s.namespaceCutover(ctx)
	if e != nil {
		return nil, e
	}
	var raw bson.Raw
	var boundary namespaceImportBoundary
	var original namespaceimport.EffectiveOwnerBoundary
	boundaryErr := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "namespace-import-boundary", cut.MigrationID+"\x00"+namespaceimport.EffectiveOwnerKey(d.CID, w.UserID))}).Decode(&raw)
	p := ownerHistorySegments{BaseClosed: true, SeedMaximum: head.SeedMaximum, FutureLower: head.SeedMaximum + 1}
	if !missing {
		var admission namespaceimport.ProspectiveOwnerAdmission
		if decodeImportJSON(f.AdmissionJSON, &admission) != nil {
			return nil, ErrConflict
		}
		command := cut.MigrationID
		held := admission.Kind == "held-cut-roster-v1"
		if !held {
			proof, _, e := s.readOwnerAdmission(ctx, d, w.UserID, f.BaseSHA)
			if e != nil {
				return nil, e
			}
			var sourceWindow MembershipWindow
			if decodeImportJSON(proof.SourceWindowJSON, &sourceWindow) != nil {
				return nil, ErrConflict
			}
			command = sourceWindow.LastCommand
		}
		p.FutureUpper, e = groupHistoryBound(s.epoch, d, w.UserID, command, f.Lower, head.Maximum, held, f.Upper, s.historyRead(ctx))
		if e != nil {
			return nil, e
		}
		p.HistoricalFloor = f.HistoricalFloor
	}
	if boundaryErr == mongo.ErrNoDocuments {
		if !missing && f.BaseSHA != directHistoryDigest([]string{"namespace-no-historical-base-v1", s.epoch, d.CID, w.UserID, d.SHA256}) {
			return nil, ErrConflict
		}
	} else {
		if boundaryErr != nil {
			return nil, boundaryErr
		}
		if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "conversation_id", "user_id", "owner_key_sha256", "boundary_sha256", "coverage_sha256", "boundary_json", "window_id", "window_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &boundary) != nil || boundary.Kind != "namespace-import-boundary-v1" || !missing && boundary.WindowSHA256 != f.BaseSHA || boundary.ConversationID != d.CID || boundary.UserID != w.UserID || decodeImportJSON(boundary.BoundaryJSON, &original) != nil {
			return nil, ErrConflict
		}
		digest, e := namespaceimport.EffectiveBoundarySHA256(original)
		if e != nil || digest != boundary.BoundarySHA256 || boundary.ID != key(s.epoch, "namespace-import-boundary", cut.MigrationID+"\x00"+namespaceimport.EffectiveOwnerKey(d.CID, w.UserID)) || boundary.OwnerKeySHA256 != namespaceimport.EffectiveOwnerKey(d.CID, w.UserID) || original.Window.CID != d.CID || original.Window.OwnerID != w.UserID || original.Window.OriginSHA256 != d.SHA256 || original.Window.GroupID != d.GroupID {
			return nil, ErrConflict
		}
		var published namespaceImportCutover
		if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "namespace-import-cutover", cut.MigrationID)}).Decode(&published); e != nil {
			return nil, errors.Join(ErrUnprepared, e)
		}
		if published.Kind != "namespace-import-cutover-v1" || published.HeaderSHA256 != boundary.HeaderSHA256 || published.InventorySHA256 != boundary.InventorySHA256 || published.ManifestSHA256 != cut.ManifestSHA256 {
			return nil, ErrConflict
		}
		p.BaseLower, p.BaseUpper, p.BaseClosed = original.Window.Lower, original.Window.Upper, original.Window.Closed
	}
	// An actual post-cut removal caps only the future branch at its real head.
	// Import closed Upper describes the historical base, not live roster state.
	if w.LastCommand != cut.MigrationID && w.Closed {
		p.FutureUpper = min(p.FutureUpper, w.Upper)
	}
	floor, e := s.admissionFloor(ctx, d.CID, w.UserID)
	if e != nil {
		return nil, e
	}
	p.Floor = floor
	revocation, e := s.readOwnerHistoricalFloor(ctx, d, w.UserID)
	if e == nil {
		p.HistoricalFloor = max(p.HistoricalFloor, revocation.Floor)
	} else if e != mongo.ErrNoDocuments {
		return nil, e
	}
	if !p.valid() {
		return nil, ErrConflict
	}
	return &p, nil
}
