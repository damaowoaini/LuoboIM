package messagedelivery

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
)

func (s *Store) compareOwnedDutyVisibility(ctx context.Context, d deliveryDuty, users []string, fence bool) (map[string]bool, error) {
	if d.ActionCut == nil || d.ProducerVersion != ownedGroupProducer {
		return nil, ErrInvalid
	}
	if d.RemovalCut == nil {
		return deliveryprofile.CompareBranchVisibilityCut(ctx, s.db, s.epoch, s.activationSHA, *d.ActionCut, d.CID, users, d.Seq, d.StoredPeerSeq, fence)
	}
	call := deliveryprofile.RemovalVisibilityIdentity{EffectID: d.SourceID, BranchID: d.BranchID, CID: d.CID, ProfileSHA256: d.ActionProfileSHA, Payload: d.Payload}
	return deliveryprofile.CompareMemberRemovalVisibilityCut(ctx, s.db, s.epoch, s.activationSHA, *d.RemovalCut, call, users, d.Seq, d.StoredPeerSeq, fence)
}
