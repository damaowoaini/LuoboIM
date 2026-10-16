package groupeffects

import (
	"context"
	"encoding/json"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

// RoleEffectOrder preserves the original after-callback/notification ordering.
// An unknown predecessor is not success and is never automatically replayed.
type RoleAfterPolicy struct {
	ConfigurationSHA256 string `bson:"configuration_sha256"`
	TimeoutSeconds      int    `bson:"timeout_seconds"`
	FailedContinue      bool   `bson:"failed_continue"`
}
type RoleEffectOrder struct {
	CommandID             string           `bson:"command_id"`
	Epoch                 string           `bson:"epoch"`
	PreviousID            string           `bson:"previous_id,omitempty"`
	PreviousPayloadSHA256 string           `bson:"previous_payload_sha256,omitempty"`
	After                 *RoleAfterPolicy `bson:"after,omitempty"`
}

func (s *Store) bindRoleOrder(ctx context.Context, f Frozen, order RoleEffectOrder) error {
	if !validID(order.CommandID) || !validID(order.Epoch) || (order.PreviousID == "") != (order.PreviousPayloadSHA256 == "") || order.PreviousID != "" && (!validID(order.PreviousID) || !validSHA(order.PreviousPayloadSHA256) || order.PreviousID == f.ID) {
		return ErrInvalid
	}
	if order.PreviousID != "" {
		var prior struct {
			Receipt `bson:",inline"`
			Order   RoleEffectOrder `bson:"role_order"`
		}
		if err := s.collection.FindOne(ctx, bson.M{"_id": order.PreviousID}).Decode(&prior); err != nil {
			return unavailable(err)
		}
		if prior.Receipt.validate() != nil || prior.State != Pending || prior.Actor != f.Actor || prior.PayloadSHA256 != order.PreviousPayloadSHA256 || prior.Order.CommandID != order.CommandID || prior.Order.Epoch != order.Epoch {
			return ErrConflict
		}
	}
	r, err := s.collection.UpdateOne(ctx, bson.M{"_id": f.ID, "state": Pending, "revision": int64(1), "payload_sha256": f.PayloadSHA256, "role_order": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"role_order": order}})
	if err != nil {
		return unavailable(err)
	}
	if r.MatchedCount != 1 || r.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) QueueRoleCallback(ctx context.Context, f Frozen, order RoleEffectOrder) error {
	if f.Kind != AfterCallback || order.After == nil || !validSHA(order.After.ConfigurationSHA256) {
		return ErrInvalid
	}
	if err := s.Queue(ctx, []Frozen{f}); err != nil {
		return err
	}
	return s.bindRoleOrder(ctx, f, order)
}
func (s *Store) QueueRoleNotification(ctx context.Context, snapshot *groupauthority.RoleNoticeSnapshot, target string, f Frozen, policy ActionDeliveryPolicy, order RoleEffectOrder) error {
	if s == nil || snapshot == nil || f.Kind != Notification {
		return ErrInvalid
	}
	command := snapshot.Command()
	if order.CommandID != command.ID || order.Epoch != command.Epoch || !policy.valid(command.Epoch) || f.Actor != command.Actor || f.GroupID != snapshot.Group().GroupID || !f.CreatedAt.Equal(command.CreatedAt) {
		return ErrInvalid
	}
	var message sdkws.MsgData
	if proto.Unmarshal(f.Payload, &message) != nil || policy.OfflineEnabled != msgprocessor.Options(message.Options).IsOfflinePush() {
		return ErrInvalid
	}
	plans, err := PlanNotificationBranches(f)
	if err != nil {
		return err
	}
	targets := make([]NotificationTargets, 0, len(plans))
	bindings := map[string]string{}
	for _, branch := range plans {
		var data sdkws.MsgData
		if proto.Unmarshal(branch.Payload, &data) != nil {
			return ErrInvalid
		}
		ids, binding, err := snapshot.NotificationRecipients(ctx, s.db, target, branch.ID, branch.PayloadSHA256, data.ContentType)
		if err != nil {
			return err
		}
		targets = append(targets, NotificationTargets{BranchID: branch.ID, UserIDs: ids})
		bindings[branch.ID] = binding
	}
	// This lane stores its complete roster without introducing the old helper's
	// 50,000 product cap. Mongo BSON size/TX errors abort BEFORE any business ACK.
	encoded, err := json.Marshal(targets)
	if err != nil {
		return ErrInvalid
	}
	roster := digest(encoded)
	if err = s.Queue(ctx, []Frozen{f}); err != nil {
		return err
	}
	if err = s.bindRoleOrder(ctx, f, order); err != nil {
		return err
	}
	policyBody, _ := json.Marshal(policy)
	r, err := s.collection.UpdateOne(ctx, bson.M{"_id": f.ID, "state": Pending, "revision": int64(1), "payload_sha256": f.PayloadSHA256}, bson.M{"$set": bson.M{"delivery_targets": targets, "delivery_roster_sha256": roster, "action_read_bindings": bindings, "action": command.Action, "action_policy": policy, "action_policy_sha256": digest(policyBody), "command_id": command.ID, "command_epoch": command.Epoch, "producer_version": "group-role-v1"}})
	if err != nil {
		return unavailable(err)
	}
	if r.MatchedCount != 1 || r.ModifiedCount != 1 {
		return ErrConflict
	}
	cut, err := s.freezeOwnedVisibility(ctx, &message, policy, targets)
	if err != nil {
		return err
	}
	if err = s.bindOwnedVisibility(ctx, f.ID, cut); err != nil {
		return err
	}
	return nil
}

// RoleOrderReady is a read-only startup/dispatch gate used by BOTH callback
// claim and notification intake. Done is monotonic. It grants no write scope.
// The action-aware receiver must additionally honor privacy/policy admission;
// the legacy minimal receiver continues to reject producer_version!=empty.
func (s *Store) RoleOrderReady(ctx context.Context, id string) error {
	if s == nil || ctx == nil || !validID(id) {
		return ErrInvalid
	}
	var row struct {
		Receipt `bson:",inline"`
		Order   *RoleEffectOrder `bson:"role_order"`
	}
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&row); err != nil {
		return unavailable(err)
	}
	if row.Order == nil {
		return nil
	} // unchanged legacy effects
	if row.Receipt.validate() != nil || !validID(row.Order.CommandID) || !validID(row.Order.Epoch) {
		return ErrConflict
	}
	o := row.Order
	if o.PreviousID == "" {
		if o.PreviousPayloadSHA256 != "" {
			return ErrConflict
		}
		return nil
	}
	if !validID(o.PreviousID) || !validSHA(o.PreviousPayloadSHA256) || o.PreviousID == id {
		return ErrConflict
	}
	var prior struct {
		Receipt         `bson:",inline"`
		Order           RoleEffectOrder `bson:"role_order"`
		ContinueAllowed bool            `bson:"role_continue_allowed"`
	}
	if err := s.collection.FindOne(ctx, bson.M{"_id": o.PreviousID}).Decode(&prior); err != nil {
		return unavailable(err)
	}
	if prior.Receipt.validate() != nil || prior.Order.CommandID != o.CommandID || prior.Order.Epoch != o.Epoch || prior.Actor != row.Actor || prior.PayloadSHA256 != o.PreviousPayloadSHA256 {
		return ErrConflict
	}
	if prior.State != Done && !(prior.State == Unconfirmed && prior.ContinueAllowed && prior.Order.After != nil && prior.Order.After.FailedContinue) {
		return ErrConflict
	}
	return nil
}

// Only code-controlled delivery may mark an uncertain transport result as
// continue-under-frozen-policy. The uncertainty itself remains inspectable.
func (s *Store) roleAfterPolicy(ctx context.Context, id string) (*RoleAfterPolicy, error) {
	var row struct {
		Order *RoleEffectOrder `bson:"role_order"`
	}
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&row); err != nil {
		return nil, unavailable(err)
	}
	if row.Order == nil {
		return nil, nil
	}
	p := row.Order.After
	if p == nil || !validSHA(p.ConfigurationSHA256) {
		return nil, ErrConflict
	}
	copy := *p
	return &copy, nil
}

// pkg/common/http.Post adds a configured deadline only for timeout>0;
// its shared http.Client ALWAYS caps requests at15s (http_client.go41-43/70-75).
// Preserve timeout0/negative as that15s cap, and cap larger settings likewise.
func roleCallbackTimeout(seconds int) time.Duration {
	if seconds <= 0 || seconds > 15 {
		return 15 * time.Second
	}
	return time.Duration(seconds) * time.Second
}
