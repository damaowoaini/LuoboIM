package groupeffects

import (
	"context"
	"encoding/json"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

// ActionDeliveryPolicy freezes legitimate configuration without forcing an
// unreliable notification into persistent history, disabling offline delivery,
// or dropping an enabled callback. Actual adapters/lifecycles must support this
// complete policy before the slice can be activated.
type ActionDeliveryPolicy struct {
	Owned                  *deliveryprofile.Group `bson:"owned,omitempty" json:"owned,omitempty"`
	Epoch                  string                 `bson:"epoch" json:"epoch"`
	BeforeMessageBinding   string                 `bson:"before_message_binding,omitempty" json:"beforeMessageBinding,omitempty"`
	AfterMessageBinding    string                 `bson:"after_message_binding,omitempty" json:"afterMessageBinding,omitempty"`
	ConfigurationSHA256    string                 `bson:"configuration_sha256" json:"configurationSHA256"`
	BeforeGroupPushBinding string                 `bson:"before_group_push_binding,omitempty" json:"beforeGroupPushBinding,omitempty"`
	OnlinePushBinding      string                 `bson:"online_push_binding,omitempty" json:"onlinePushBinding,omitempty"`
	OfflinePushBinding     string                 `bson:"offline_push_binding,omitempty" json:"offlinePushBinding,omitempty"`
	OfflineEnabled         bool                   `bson:"offline_enabled" json:"offlineEnabled"`
}

func (p ActionDeliveryPolicy) valid(epoch string) bool {
	if p.Owned != nil {
		if _, err := p.Owned.Fingerprint(); err != nil || p.Owned.Transport.Epoch != epoch {
			return false
		}
	}
	if p.Epoch != epoch || !validID(epoch) || !validSHA(p.ConfigurationSHA256) {
		return false
	}
	for _, binding := range []string{p.BeforeMessageBinding, p.AfterMessageBinding, p.BeforeGroupPushBinding, p.OnlinePushBinding, p.OfflinePushBinding} {
		if binding != "" && !validSHA(binding) {
			return false
		}
	}
	return true
}

// QueueActionNotification is a real producer: Queue, branch recipients and the
// frozen policy are persisted in the business Mongo transaction. Recipient IDs
// are derived by a private authority snapshot; they are not caller arguments.
func (s *Store) QueueActionNotification(ctx context.Context, snapshot *groupauthority.NoticeSnapshot, message *sdkws.MsgData, policy ActionDeliveryPolicy) (Frozen, error) {
	if s == nil || snapshot == nil || message == nil {
		return Frozen{}, ErrInvalid
	}
	command := snapshot.Command()
	if !policy.valid(command.Epoch) || policy.OfflineEnabled != msgprocessor.Options(message.Options).IsOfflinePush() {
		return Frozen{}, ErrInvalid
	}
	frozen, err := NewNotification(command.ID, 0, command.GroupID, command.Actor, message, command.CreatedAt)
	if err != nil {
		return Frozen{}, err
	}
	plans, err := PlanNotificationBranches(frozen)
	if err != nil {
		return Frozen{}, err
	}
	targets := make([]NotificationTargets, 0, len(plans))
	bindings := make(map[string]string, len(plans))
	for _, branch := range plans {
		var data sdkws.MsgData
		if proto.Unmarshal(branch.Payload, &data) != nil {
			return Frozen{}, ErrInvalid
		}
		ids, binding, err := snapshot.NotificationRecipients(ctx, s.db, branch.ID, branch.PayloadSHA256, data.ContentType, msgprocessor.IsNotification(branch.ConversationID))
		if err != nil {
			return Frozen{}, err
		}
		targets = append(targets, NotificationTargets{BranchID: branch.ID, UserIDs: ids})
		bindings[branch.ID] = binding
	}
	targets, rosterSHA, err := canonicalNotificationTargetsMode(plans, targets, policy.Owned != nil)
	if err != nil {
		return Frozen{}, err
	}
	if err := s.Queue(ctx, []Frozen{frozen}); err != nil {
		return Frozen{}, err
	}
	// A policy/recipient write failure aborts the surrounding business TX. There
	// is never a committed business change with an unrecorded required effect.
	policyBody, err := json.Marshal(policy)
	if err != nil {
		return Frozen{}, ErrInvalid
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": frozen.ID, "kind": Notification, "state": Pending, "revision": int64(1), "payload_sha256": frozen.PayloadSHA256, "delivery_targets": bson.M{"$exists": false}}, bson.M{"$set": bson.M{
		"delivery_targets": targets, "delivery_roster_sha256": rosterSHA,
		"action_read_bindings": bindings, "action": command.Action,
		"action_policy": policy, "action_policy_sha256": digest(policyBody),
		"command_id": command.ID, "command_epoch": command.Epoch, "producer_version": "group-action-v1",
	}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return Frozen{}, unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return Frozen{}, ErrConflict
	}
	cut, err := s.freezeOwnedVisibility(ctx, message, policy, targets)
	if err != nil {
		return Frozen{}, err
	}
	if err = s.bindOwnedVisibility(ctx, frozen.ID, cut); err != nil {
		return Frozen{}, err
	}
	return frozen, nil
}
