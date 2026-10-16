package groupeffects

import (
	"context"
	"sort"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
)

// All recipients originate in NoticeSnapshot/RoleNoticeSnapshot. The visibility
// freeze is a READ capability only, and never grants RequireMembers scope.
func (s *Store) freezeOwnedVisibility(ctx context.Context, message *sdkws.MsgData, policy ActionDeliveryPolicy, targets []NotificationTargets) (*deliveryprofile.VisibilityCut, error) {
	if policy.Owned == nil {
		return nil, nil
	}
	ids := map[string]bool{}
	for _, target := range targets {
		for _, uid := range target.UserIDs {
			ids[uid] = true
		}
	}
	users := make([]string, 0, len(ids))
	for uid := range ids {
		users = append(users, uid)
	}
	sort.Strings(users)
	cut, err := deliveryprofile.CaptureVisibilityCut(ctx, s.db, policy.Epoch, policy.Owned.Transport.ActivationSHA256, msgprocessor.GetChatConversationIDByMsg(message), users)
	if err != nil {
		return nil, err
	}
	return &cut, nil
}
func (c *OwnedNotificationDelivery) VisibilitySnapshot() deliveryprofile.VisibilityCut {
	if c == nil || c.cut == nil {
		return deliveryprofile.VisibilityCut{}
	}
	result := *c.cut
	result.Owners = map[string]int64{}
	for k, v := range c.cut.Owners {
		result.Owners[k] = v
	}
	result.Members = map[string]string{}
	for k, v := range c.cut.Members {
		result.Members[k] = v
	}
	if c.cut.Notification != nil {
		copy := *c.cut.Notification
		copy.Owners = map[string]int64{}
		for k, v := range c.cut.Notification.Owners {
			copy.Owners[k] = v
		}
		result.Notification = &copy
	}
	return result
}

// Current candidate visibility; called in the same strong receipt/permit TX.
func (s *Store) ValidateOwnedVisibility(ctx context.Context, c *OwnedNotificationDelivery, branch string, users []string, sequence, normalSequence int64, fence bool) (map[string]bool, error) {
	if c == nil || c.inner == nil || c.inner.store != s || c.cut == nil {
		return nil, ErrInvalid
	}
	plans, err := PlanNotificationBranches(c.inner.row.Frozen)
	if err != nil {
		return nil, err
	}
	cid := ""
	for _, plan := range plans {
		if plan.ID == branch {
			cid = plan.ConversationID
			break
		}
	}
	if cid == "" {
		return nil, ErrInvalid
	}
	return deliveryprofile.CompareBranchVisibilityCut(ctx, s.db, c.profile.Transport.Epoch, c.profile.Transport.ActivationSHA256, *c.cut, cid, users, sequence, normalSequence, fence)
}

// Binding in the already queued effect is part of its producing TX, so failure
// aborts the original member/status write instead of committing an incomplete
// plaintext obligation that a later config could reinterpret.
func (s *Store) bindOwnedVisibility(ctx context.Context, id string, cut *deliveryprofile.VisibilityCut) error {
	if cut == nil {
		return nil
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "state": Pending, "owned_visibility": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"owned_visibility": cut}})
	if err != nil {
		return unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
