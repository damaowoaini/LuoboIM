package groupeffects

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
)

func (s *Store) freezeMemberRemovalVisibility(ctx context.Context, snapshot *groupauthority.NoticeSnapshot, cap *groupauthority.MemberRemovalScope, frozen Frozen, plans []NotificationBranch, bindings map[string]string, policy ActionDeliveryPolicy) (*deliveryprofile.MemberRemovalVisibilityCut, error) {
	if s == nil || policy.Owned == nil || !policy.valid(policy.Epoch) {
		return nil, ErrInvalid
	}
	profileSHA, err := policy.Owned.Fingerprint()
	if err != nil {
		return nil, err
	}
	inputs := make([]deliveryprofile.RemovalNoticeBranchInput, 0, len(plans))
	for _, plan := range plans {
		inputs = append(inputs, deliveryprofile.RemovalNoticeBranchInput{ID: plan.ID, CID: plan.ConversationID, PayloadSHA256: plan.PayloadSHA256, ReadBindingSHA256: bindings[plan.ID], Payload: plan.Payload, Persist: plan.Persist})
	}
	result, err := deliveryprofile.CaptureMemberRemovalVisibilityCut(ctx, s.db, policy.Epoch, policy.Owned.Transport.ActivationSHA256, snapshot, cap, frozen.ID, profileSHA, inputs)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
func (c *OwnedNotificationDelivery) MemberRemovalVisibilitySnapshot() *deliveryprofile.MemberRemovalVisibilityCut {
	if c == nil || c.removal == nil {
		return nil
	}
	copy := c.removal.Clone()
	return &copy
}
