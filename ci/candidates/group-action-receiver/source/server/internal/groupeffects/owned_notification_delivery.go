package groupeffects

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// OwnedNotificationDelivery can only be obtained by reading this Store's exact
// durable effect. It is never constructible from an external MsgData/opuser.
type OwnedNotificationDelivery struct {
	inner                *NotificationDelivery
	profile              deliveryprofile.Group
	cut                  *deliveryprofile.VisibilityCut
	profileSHA, producer string
}

func (c *OwnedNotificationDelivery) Snapshot() (Frozen, []NotificationTargets, deliveryprofile.Group, string, string) {
	if c == nil || c.inner == nil {
		return Frozen{}, nil, deliveryprofile.Group{}, "", ""
	}
	f, t := c.inner.Snapshot()
	return f, t, c.profile, c.profileSHA, c.producer
}
func (c *OwnedNotificationDelivery) Accepted() (bool, string, string, string) {
	if c == nil || c.inner == nil {
		return false, "", "", ""
	}
	return c.inner.Accepted()
}
func (s *Store) ReadOwnedNotificationDelivery(ctx context.Context, id string) (*OwnedNotificationDelivery, error) {
	return s.readOwnedNotificationDelivery(ctx, id, true)
}
func (s *Store) ReadOwnedNotificationForRedaction(ctx context.Context, id string) (*OwnedNotificationDelivery, error) {
	return s.readOwnedNotificationDelivery(ctx, id, false)
}
func (s *Store) readOwnedNotificationDelivery(ctx context.Context, id string, ordered bool) (*OwnedNotificationDelivery, error) {
	if s == nil || ctx == nil || !validID(id) {
		return nil, ErrInvalid
	}
	if err := receiverSession(ctx, s.db); err != nil {
		return nil, err
	}
	if err := s.checkSchema(ctx); err != nil {
		return nil, err
	}
	if ordered {
		if err := s.RoleOrderReady(ctx, id); err != nil {
			return nil, err
		}
	}
	var row struct {
		notificationDeliveryRow `bson:",inline"`
		Policy                  ActionDeliveryPolicy           `bson:"action_policy"`
		PolicySHA               string                         `bson:"action_policy_sha256"`
		Retained                *deliveryprofile.Group         `bson:"receiver_owned_profile"`
		RetainedSHA             string                         `bson:"receiver_owned_profile_sha256"`
		Quarantine              string                         `bson:"receiver_quarantine"`
		CommandID               string                         `bson:"command_id"`
		CommandEpoch            string                         `bson:"command_epoch"`
		Bindings                map[string]string              `bson:"action_read_bindings"`
		Cut                     *deliveryprofile.VisibilityCut `bson:"owned_visibility"`
	}
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&row); err != nil {
		return nil, unavailable(err)
	}
	if row.Receipt.validate() != nil || row.Kind != Notification || row.Claim != "" || !validSHA(row.RosterSHA) || !validID(row.CommandID) || row.CommandEpoch != row.Policy.Epoch || !row.Policy.valid(row.CommandEpoch) || row.ProducerVersion != "group-action-v1" && row.ProducerVersion != "group-role-v1" {
		return nil, ErrConflict
	}
	body, err := json.Marshal(row.Policy)
	if err != nil || digest(body) != row.PolicySHA {
		return nil, ErrConflict
	}
	// Historical incomplete profiles remain inspectable. An operator must bind an
	// exact retained source profile; a later current config is never substituted.
	profilePointer := row.Policy.Owned
	if profilePointer == nil {
		profilePointer = row.Retained
		if profilePointer == nil {
			return nil, ErrLegacyOwnedQuarantined
		}
		if deliveryprofile.Digest(*profilePointer) != row.RetainedSHA {
			return nil, ErrConflict
		}
	}
	if row.Quarantine != "" {
		return nil, ErrLegacyOwnedQuarantined
	}
	if row.Cut == nil || !row.Cut.Valid() {
		return nil, ErrConflict
	}
	profile := *profilePointer
	profileSHA, err := profile.Fingerprint()
	if err != nil || profile.Transport.Epoch != row.CommandEpoch {
		return nil, ErrConflict
	}
	if row.State == Done {
		proof, _ := json.Marshal([]string{row.DeliveryEpoch, row.InputSHA, row.ReceiptSHA})
		if !validID(row.DeliveryEpoch) || !validSHA(row.InputSHA) || !validSHA(row.ReceiptSHA) || digest(proof) != row.OutcomeSHA256 || len(row.Targets) != 0 || len(row.Payload) != 0 {
			return nil, ErrConflict
		}
	} else {
		if row.State != Pending || row.DeliveryEpoch != "" || row.InputSHA != "" || row.ReceiptSHA != "" || row.Revision >= math.MaxInt64-1 {
			return nil, ErrConflict
		}
		plans, err := PlanNotificationBranches(row.Frozen)
		if err != nil {
			return nil, err
		}
		if len(plans) != len(row.Targets) || len(plans) != len(row.Bindings) {
			return nil, ErrConflict
		}
		expected := map[string]bool{}
		for _, p := range plans {
			expected[p.ID] = true
		}
		for _, target := range row.Targets {
			if !expected[target.BranchID] || !validSHA(row.Bindings[target.BranchID]) {
				return nil, ErrConflict
			}
			delete(expected, target.BranchID)
			for i, uid := range target.UserIDs {
				if !validIdentity(uid) || i > 0 && uid <= target.UserIDs[i-1] {
					return nil, ErrConflict
				}
			}
		}
		// Preserve producer's exact ordering/hash; do not impose the legacy50k cap.
		encoded, err := json.Marshal(row.Targets)
		if err != nil || digest(encoded) != row.RosterSHA {
			return nil, ErrConflict
		}
	}
	return &OwnedNotificationDelivery{inner: &NotificationDelivery{store: s, row: row.notificationDeliveryRow}, profile: profile, cut: row.Cut, profileSHA: profileSHA, producer: row.ProducerVersion}, nil
}
func (s *Store) AcknowledgeOwnedNotificationDelivery(ctx context.Context, cap *OwnedNotificationDelivery, epoch, inputSHA, receiptSHA string) error {
	if cap == nil || cap.inner == nil || cap.inner.store != s {
		return ErrInvalid
	}
	return s.AcknowledgeNotificationDelivery(ctx, cap.inner, epoch, inputSHA, receiptSHA)
}

type OwnedNotificationPending struct{ ID, Cursor string }

func (s *Store) ListOwnedNotifications(ctx context.Context, after string, limit int64) ([]OwnedNotificationPending, error) {
	if s == nil || ctx == nil || limit < 1 || limit > 32 || after != "" && !validID(after) {
		return nil, ErrInvalid
	}
	filter := bson.M{"kind": Notification, "state": Pending, "producer_version": bson.M{"$in": bson.A{"group-action-v1", "group-role-v1"}}}
	if after != "" {
		filter["_id"] = bson.M{"$gt": after}
	}
	cursor, err := s.collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit).SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	var out []OwnedNotificationPending
	for cursor.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
		}
		if cursor.Decode(&row) != nil || !validID(row.ID) {
			err = ErrConflict
			break
		}
		out = append(out, OwnedNotificationPending{ID: row.ID, Cursor: row.ID})
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(ctx)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	return out, nil
}

// This same-document write is mandatory in a receiver physical-permit TX.
// Privacy source redaction competes on the same row, avoiding snapshot skew.
func (s *Store) FenceOwnedNotification(ctx context.Context, cap *OwnedNotificationDelivery) error {
	if cap == nil || cap.inner == nil || cap.inner.store != s {
		return ErrInvalid
	}
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	row := cap.inner.row
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": row.ID, "state": Pending, "revision": row.Revision, "payload_sha256": row.PayloadSHA256, "delivery_roster_sha256": row.RosterSHA}, bson.M{"$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
