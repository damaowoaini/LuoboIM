package messagedelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

// A local operator must first drain the named managed instance and preserve
// its shutdown evidence. A nonce alone is not proof that an in-flight remote
// request has stopped. The CLI verifies this exact owned proof file; it never
// turns a timeout/absence into a claim of non-application.
type KafkaAttemptDrainProof struct {
	InstanceID     string `json:"instanceID"`
	EvidenceSHA256 string `json:"evidenceSHA256"`
}

type KafkaVendorSummary struct {
	BranchID, RecipientSHA256, PolicySHA256, Vendor, Binding, State string
	RequestSHA256, OutcomeSHA256, RecordSHA256                      string
	AttemptID, InstanceID                                           string
	Revision                                                        int64
	DeviceSHA256                                                    []string
}

func vendorSummary(row kafkaVendorReceipt) KafkaVendorSummary {
	body, _ := json.Marshal(row)
	return KafkaVendorSummary{BranchID: row.Branch, RecipientSHA256: row.UserSHA, PolicySHA256: row.PolicySHA, Vendor: row.Vendor, Binding: row.Binding, State: row.State, RequestSHA256: row.RequestSHA, OutcomeSHA256: row.OutcomeSHA, RecordSHA256: hash(body), AttemptID: row.AttemptID, InstanceID: row.InstanceID, Revision: row.Revision, DeviceSHA256: append([]string(nil), row.Input.DeviceSHA256...)}
}

func (s *Store) InspectKafkaVendor(ctx context.Context, branch, user string) (KafkaVendorSummary, error) {
	if s == nil || ctx == nil || !validUUID(branch) || !identity(user) {
		return KafkaVendorSummary{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return KafkaVendorSummary{}, err
	}
	d, err := s.readDutyRecord(current, branch)
	if err != nil {
		return KafkaVendorSummary{}, err
	}
	if d.ProducerVersion != "ordinary-kafka-v1" && d.ProducerVersion != ownedGroupProducer {
		return KafkaVendorSummary{}, ErrUnprepared
	}
	row, err := s.readKafkaVendorReceipt(current, d, user)
	if err != nil {
		return KafkaVendorSummary{}, err
	}
	if row == nil {
		return KafkaVendorSummary{}, ErrCommitUnconfirmed
	}
	return vendorSummary(*row), nil
}

type KafkaVendorConfirmation struct {
	Decision       string
	EvidenceSHA256 string
	Drain          KafkaAttemptDrainProof
	Devices        []KafkaVendorDeviceResult
}

// This operator-only CAS is not registered on HTTP/gRPC and performs no
// vendor call. An applied batch requires independent evidence for EVERY
// frozen device hash; a single aggregate nil or one successful token is not
// enough. Not-applied permits a new checked attempt only after exact drain+
// independent endpoint evidence, retaining the old request's hash chain.
func (s *Store) ConfirmKafkaVendor(ctx context.Context, user string, expected KafkaVendorSummary, confirmation KafkaVendorConfirmation) error {
	if s == nil || ctx == nil || !identity(user) || !validSHA(confirmation.EvidenceSHA256) || confirmation.Drain.InstanceID != expected.InstanceID || !validSHA(confirmation.Drain.EvidenceSHA256) {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	actual, err := s.InspectKafkaVendor(current, expected.BranchID, user)
	if err != nil {
		return err
	}
	a, b := []byte(nil), []byte(nil)
	a, err = json.Marshal(actual)
	if err != nil {
		return ErrConflict
	}
	b, err = json.Marshal(expected)
	if err != nil || !bytes.Equal(a, b) || actual.State != "dispatching" && actual.State != "unconfirmed" {
		return ErrConflict
	}
	d, err := s.readDutyRecord(current, expected.BranchID)
	if err != nil {
		return err
	}
	row, err := s.readKafkaVendorReceipt(current, d, user)
	if err != nil || row == nil || row.Kind != "kafka-vendor-v1" {
		return ErrConflict
	} // Device manifests require exact per-device inspect/confirmation.
	latest, err := json.Marshal(vendorSummary(*row))
	if err != nil || !bytes.Equal(latest, b) {
		return ErrConflict
	}
	state := ""
	switch confirmation.Decision {
	case "not-applied":
		if len(confirmation.Devices) != 0 {
			return ErrInvalid
		}
		state = "pending-confirmed"
	case "rejected":
		if len(confirmation.Devices) != 0 {
			return ErrInvalid
		}
		state = "operator-rejected"
	case "applied":
		wanted := map[string]bool{}
		for _, device := range row.Input.DeviceSHA256 {
			wanted[device] = true
		}
		seen := map[string]bool{}
		for _, result := range confirmation.Devices {
			if !wanted[result.DeviceSHA256] || seen[result.DeviceSHA256] || result.KnownRejection && result.ProviderReceiptSHA256 != "" || !result.KnownRejection && !validSHA(result.ProviderReceiptSHA256) {
				return ErrInvalid
			}
			seen[result.DeviceSHA256] = true
		}
		if len(seen) != len(wanted) {
			return ErrInvalid
		}
		state = "completed"
		if len(wanted) == 0 {
			state = "no-registered-token"
		}
	default:
		return ErrInvalid
	}
	proof, err := json.Marshal([]any{"operator-independent-evidence", actual.RecordSHA256, confirmation})
	if err != nil {
		return ErrInvalid
	}
	changed, err := s.db.Collection(Effects).UpdateOne(current, bson.M{"_id": row.ID, "epoch": s.epoch, "revision": row.Revision, "state": row.State, "attempt_id": row.AttemptID, "instance_id": row.InstanceID, "request_sha256": row.RequestSHA, "accounted": false}, bson.M{"$set": bson.M{"state": state, "outcome_sha256": hash(proof), "device_results": confirmation.Devices}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil || changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
		return ErrKafkaOfflineUnconfirmed
	}
	return nil
}
