package groupeffects

import (
	"context"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
)

// Same business TX and original owned receiver. The predecessor link lets the
// existing after-callback worker wait for the exact notification receipt; an
// unknown notification is not success and does not invite automatic replay.
func (s *Store) QueueOrderedActionNotification(ctx context.Context, snapshot *groupauthority.NoticeSnapshot, message *sdkws.MsgData, policy ActionDeliveryPolicy) (Frozen, error) {
	if snapshot == nil {
		return Frozen{}, ErrInvalid
	}
	return s.queueScopedActionNotificationOrder(ctx, snapshot, message, policy, true)
}

func (s *Store) QueueMemberAdmissionNotification(ctx context.Context, cap *groupauthority.MemberAdmissionScope, message *sdkws.MsgData, policy ActionDeliveryPolicy) (Frozen, error) {
	if s == nil || cap == nil {
		return Frozen{}, ErrInvalid
	}
	if _, err := cap.Inspect(ctx, s.db); err != nil {
		return Frozen{}, err
	}
	return s.queueScopedActionNotificationOrder(ctx, cap, message, policy, true)
}
