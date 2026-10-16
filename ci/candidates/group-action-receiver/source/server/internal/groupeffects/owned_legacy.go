package groupeffects

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"go.mongodb.org/mongo-driver/bson"
)

var ErrLegacyOwnedQuarantined = errors.New("legacy group notification quarantined; explicit retained binding or redaction required")

type LegacyNotificationSummary struct {
	ID                string `json:"id"`
	OperationID       string `json:"operationID"`
	CommandID         string `json:"commandID"`
	Epoch             string `json:"epoch"`
	Producer          string `json:"producer"`
	State             State  `json:"state"`
	Decision          string `json:"decision"`
	Revision          int64  `json:"revision"`
	SourceSHA256      string `json:"sourceSHA256"`
	PayloadSHA256     string `json:"payloadSHA256"`
	PolicySHA256      string `json:"policySHA256"`
	RosterSHA256      string `json:"rosterSHA256"`
	OriginalCutSHA256 string `json:"originalCutSHA256"`
	Quarantined       bool   `json:"quarantined"`
}
type ownedLegacyRow struct {
	notificationDeliveryRow `bson:",inline"`
	Policy                  ActionDeliveryPolicy   `bson:"action_policy"`
	PolicySHA               string                 `bson:"action_policy_sha256"`
	CommandID               string                 `bson:"command_id"`
	CommandEpoch            string                 `bson:"command_epoch"`
	Cut                     bson.Raw               `bson:"owned_visibility"`
	Retained                *deliveryprofile.Group `bson:"receiver_owned_profile"`
	RetainedSHA             string                 `bson:"receiver_owned_profile_sha256"`
	Quarantine              string                 `bson:"receiver_quarantine"`
}

func (s *Store) legacyNotification(ctx context.Context, id string) (ownedLegacyRow, LegacyNotificationSummary, error) {
	var raw bson.Raw
	var row ownedLegacyRow
	if s == nil || ctx == nil || !validID(id) {
		return row, LegacyNotificationSummary{}, ErrInvalid
	}
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&raw); err != nil {
		return row, LegacyNotificationSummary{}, unavailable(err)
	}
	if bson.Unmarshal(raw, &row) != nil || row.Receipt.validate() != nil || row.ID != id || row.Kind != Notification || row.Policy.Owned != nil || row.Retained != nil || !row.Policy.valid(row.CommandEpoch) || row.Policy.Epoch != row.CommandEpoch || !validID(row.CommandID) || row.ProducerVersion != "group-role-v1" && row.ProducerVersion != "group-action-v1" || !validSHA(row.PolicySHA) || !validSHA(row.RosterSHA) {
		return row, LegacyNotificationSummary{}, ErrConflict
	}
	body, err := json.Marshal(row.Policy)
	if err != nil || digest(body) != row.PolicySHA {
		return row, LegacyNotificationSummary{}, ErrConflict
	}
	cutSHA := ""
	var cut deliveryprofile.VisibilityCut
	if len(row.Cut) > 0 && bson.Unmarshal(row.Cut, &cut) == nil && cut.Valid() {
		cutSHA = deliveryprofile.Digest(cut)
	}
	summary := LegacyNotificationSummary{ID: id, OperationID: row.OperationID, CommandID: row.CommandID, Epoch: row.CommandEpoch, Producer: row.ProducerVersion, State: row.State, Decision: row.Decision, Revision: row.Revision, SourceSHA256: digest(raw), PayloadSHA256: row.PayloadSHA256, PolicySHA256: row.PolicySHA, RosterSHA256: row.RosterSHA, OriginalCutSHA256: cutSHA, Quarantined: row.Quarantine == "legacy-incomplete-v1"}
	return row, summary, nil
}
func (s *Store) InspectLegacyOwnedNotification(ctx context.Context, id string) (LegacyNotificationSummary, error) {
	_, summary, err := s.legacyNotification(ctx, id)
	return summary, err
}

// Isolate only the exact old minimal producer shape. No profile is guessed and
// no effect/payload is cancelled. A quarantined row keeps its dependency order;
// independent commands/groups remain schedulable. This commits independently
// of the later receiver rejection, so the quarantine survives a restart.
func (s *Store) QuarantineLegacyOwnedNotification(ctx context.Context, id string) (bool, error) {
	if err := receiverSession(ctx, s.db); err != nil {
		return false, err
	}
	if err := s.checkSchema(ctx); err != nil {
		return false, err
	}
	var shape struct {
		Policy   ActionDeliveryPolicy   `bson:"action_policy"`
		Retained *deliveryprofile.Group `bson:"receiver_owned_profile"`
	}
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&shape); err != nil {
		return false, unavailable(err)
	}
	if shape.Policy.Owned != nil || shape.Retained != nil {
		return false, nil
	}
	row, summary, err := s.legacyNotification(ctx, id)
	if err != nil {
		return false, err
	}
	if row.State == Done && row.Decision == "operator-redacted-incomplete" {
		return true, nil
	}
	if row.State != Pending || row.Claim != "" || row.DeliveryEpoch != "" || row.InputSHA != "" || row.ReceiptSHA != "" || row.Revision >= math.MaxInt64-1 {
		return false, ErrConflict
	}
	if summary.Quarantined {
		return true, nil
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "state": Pending, "revision": row.Revision, "payload_sha256": row.PayloadSHA256, "action_policy_sha256": row.PolicySHA, "receiver_quarantine": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"receiver_quarantine": "legacy-incomplete-v1", "receiver_quarantine_source_sha256": summary.SourceSHA256}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return false, unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return false, ErrConflict
	}
	return true, nil
}

// Called only inside the explicit operator's strong transaction. The supplied
// profile is an attested retained source snapshot; the original cut MUST already
// exist in this exact DB row. Neither a supplied new cut nor Capture is allowed.
// The immutable old action_policy/hash remains byte-for-byte unchanged.
func (s *Store) RestoreLegacyOwnedNotification(ctx context.Context, expected LegacyNotificationSummary, profile deliveryprofile.Group, evidence, drainSHA string) error {
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	if !validSHA(evidence) || !validSHA(drainSHA) {
		return ErrInvalid
	}
	row, actual, err := s.legacyNotification(ctx, expected.ID)
	if err != nil {
		return err
	}
	fingerprint, err := profile.Fingerprint()
	if err != nil || actual != expected || !actual.Quarantined || actual.State != Pending || actual.OriginalCutSHA256 == "" || len(row.Cut) == 0 || profile.Transport.Epoch != actual.Epoch || row.Claim != "" || row.DeliveryEpoch != "" || row.Revision >= math.MaxInt64-1 {
		return ErrConflict
	}
	proof, _ := json.Marshal([]any{"restore-original-cut", actual, evidence, drainSHA, fingerprint})
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": actual.ID, "state": Pending, "revision": actual.Revision, "action_policy_sha256": actual.PolicySHA256, "payload_sha256": actual.PayloadSHA256, "receiver_quarantine": "legacy-incomplete-v1", "receiver_owned_profile": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"receiver_owned_profile": profile, "receiver_owned_profile_sha256": fingerprint, "receiver_migration_sha256": digest(proof), "receiver_migration_original_source_sha256": actual.SourceSHA256}, "$unset": bson.M{"receiver_quarantine": ""}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}

// This explicit privacy reconciliation is NOT delivered, ACK or after success.
// Retain all source/policy/roster/order hashes, erase plaintext/recipient IDs,
// and settle only this notification dependency. Any business After stays its
// own pending/unknown obligation under the original order and configuration.
func (s *Store) RedactLegacyOwnedNotification(ctx context.Context, expected LegacyNotificationSummary, evidence, drainSHA string) error {
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	if !validSHA(evidence) || !validSHA(drainSHA) {
		return ErrInvalid
	}
	row, actual, err := s.legacyNotification(ctx, expected.ID)
	if err != nil {
		return err
	}
	if actual != expected || !actual.Quarantined || actual.State != Pending || row.Claim != "" || row.DeliveryEpoch != "" || row.InputSHA != "" || row.ReceiptSHA != "" || row.Revision >= math.MaxInt64-1 {
		return ErrConflict
	}
	proof, _ := json.Marshal([]any{"redact-incomplete-not-delivered", actual, evidence, drainSHA})
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": actual.ID, "state": Pending, "revision": actual.Revision, "payload_sha256": actual.PayloadSHA256, "action_policy_sha256": actual.PolicySHA256, "receiver_quarantine": "legacy-incomplete-v1"}, bson.M{"$set": bson.M{"state": Done, "decision": "operator-redacted-incomplete", "outcome_sha256": digest(proof), "evidence_sha256": evidence, "updated_at": time.Now().UTC(), "receiver_migration_sha256": digest(proof)}, "$unset": bson.M{"payload": "", "delivery_targets": ""}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
