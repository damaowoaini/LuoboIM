package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

var (
	ErrKafkaPushCallbackUnconfirmed = errors.New("frozen push callback outcome unconfirmed; inspect original attempt")
	ErrKafkaPushCallbackRejected    = errors.New("frozen push callback rejected this dispatch")
)

// A second exact claim/visibility check is made after callback/discovery/auth
// awaits and immediately before each gateway RPC. This does not claim that a
// Mongo read can retract a frame already handed to another service.
func (s *Store) ValidateKafkaOnlineCall(ctx context.Context, call OnlineCall) error {
	if s == nil || ctx == nil || call.Message == nil || !validUUID(call.ClaimID) || call.ClaimRevision < 1 || len(call.Recipients) == 0 || len(call.Recipients) > 64 {
		return ErrInvalid
	}
	d, err := s.readDutyRecord(ctx, call.BranchID)
	if err != nil {
		return err
	}
	if !s.kafkaRecoveryAuthorized(ctx, d) || d.PolicySHA != call.PolicySHA256 || d.CID != call.ConversationID || d.Revision != call.ClaimRevision || d.OnlineClaim != call.ClaimID || d.OnlineInstance != s.instanceID || d.Hidden || !d.OnlinePending {
		return ErrConflict
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(call.Message)
	if err != nil || hash(body) != d.PayloadSHA {
		return ErrConflict
	}
	end := min(d.OnlineCursor+64, d.RecipientCount)
	if d.OnlineCursor >= end {
		return ErrConflict
	}
	if !d.Persist {
		return s.validateSilentOnlineCall(ctx, d, call)
	}
	a := onlineAttempt{store: s, duty: d, claim: call.ClaimID, targets: append([]string(nil), d.Recipients[d.OnlineCursor:end]...)}
	return a.checkOnlineClaim(ctx, call.Recipients)
}

type kafkaPushCallbackReceipt struct {
	ID         string `bson:"_id"`
	Kind       string `bson:"kind"`
	Epoch      string `bson:"epoch"`
	Branch     string `bson:"branch_id"`
	PolicySHA  string `bson:"policy_sha256"`
	Binding    string `bson:"binding"`
	RequestSHA string `bson:"request_sha256"`
	OutcomeSHA string `bson:"outcome_sha256"`
	BodySHA    string `bson:"body_sha256"`
	Mode       string `bson:"failure_mode"`
	AttemptID  string `bson:"attempt_id"`
	InstanceID string `bson:"instance_id"`
	State      string `bson:"state"`
	Revision   int64  `bson:"revision"`
}

func validKafkaPushCallbackReceipt(row kafkaPushCallbackReceipt) bool {
	if row.Kind != "before-group-push" && row.Kind != "online-push" || !validUUID(row.Epoch) || !validUUID(row.Branch) || !validSHA(row.PolicySHA) || !validSHA(row.Binding) || !validSHA(row.RequestSHA) || !validSHA(row.BodySHA) || !validUUID(row.AttemptID) || !validUUID(row.InstanceID) || row.Revision < 1 || row.Revision >= math.MaxInt64 {
		return false
	}
	if row.Mode != "default-stop" && row.Mode != "explicit-stop" && row.Mode != "continue" {
		return false
	}
	switch row.State {
	case "dispatching":
		return row.OutcomeSHA == ""
	case "continued-unknown", "continued-not-applied":
		return row.Mode == "continue" && validSHA(row.OutcomeSHA)
	case "acknowledged", "rejected", "pending-confirmed", "pending-not-dispatched", "unconfirmed":
		return validSHA(row.OutcomeSHA)
	default:
		return false
	}
}

func kafkaPushCallbackProof(d deliveryDuty, kind, binding string) (string, error) {
	body, err := json.Marshal([]any{d.Epoch, d.SourceID, d.BranchID, d.CID, d.InputSHA, d.PayloadSHA, d.PolicySHA, kind, binding})
	if err != nil {
		return "", ErrInvalid
	}
	return hash(body), nil
}

// This gate only consumes a durable known callback decision; it never invokes
// HTTP, treats a missing row as permission, or retries a prior unknown call.
// The corresponding once-only callback engine is a separate required part of
// this WIP. Enabled configurations stay closed until that engine records an
// exact decision; no callback is silently removed to admit online delivery.
func (s *Store) RequireKafkaPushCallbacks(ctx context.Context, call OnlineCall) error {
	if err := s.ValidateKafkaOnlineCall(ctx, call); err != nil {
		return err
	}
	d, err := s.readDutyRecord(ctx, call.BranchID)
	if err != nil {
		return err
	}
	policy, err := s.readKafkaPolicy(ctx, d.PolicySHA)
	if err != nil {
		return err
	}
	kind, binding, mode, err := kafkaRequiredOnlineCallback(d, policy, call.Message)
	if err != nil {
		return err
	}
	if binding == "" {
		return nil
	}
	expected, err := kafkaPushCallbackProof(d, kind, binding)
	if err != nil {
		return err
	}
	id := key(s.epoch, "kafka-push-callback", d.BranchID+"\x00"+kind)
	var receipt kafkaPushCallbackReceipt
	if err := s.db.Collection(Effects).FindOne(ctx, bson.M{"_id": id}).Decode(&receipt); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrUnprepared
		}
		return err
	}
	if !validKafkaPushCallbackReceipt(receipt) || receipt.ID != id || receipt.Kind != kind || receipt.Epoch != s.epoch || receipt.Branch != d.BranchID || receipt.PolicySHA != d.PolicySHA || receipt.Binding != binding || receipt.RequestSHA != expected || receipt.Mode != mode {
		return ErrConflict
	}
	switch receipt.State {
	case "acknowledged":
		if !validSHA(receipt.OutcomeSHA) {
			return ErrConflict
		}
		return nil
	case "continued-unknown", "continued-not-applied":
		if mode != "continue" || !validSHA(receipt.OutcomeSHA) {
			return ErrConflict
		}
		return nil
	case "rejected":
		return ErrKafkaPushCallbackRejected
	case "dispatching", "unconfirmed":
		return ErrKafkaPushCallbackUnconfirmed
	default:
		return ErrUnprepared
	}
}

func kafkaRequiredOnlineCallback(d deliveryDuty, policy KafkaDeliveryPolicy, message *sdkws.MsgData) (string, string, string, error) {
	if message == nil {
		return "", "", "", ErrInvalid
	}
	switch message.SessionType {
	case constant.SuperGroupChatType, constant.GroupChatType:
		return "before-group-push", policy.BeforeGroupPushBinding, policy.BeforeGroupFailureMode, nil
	case constant.SingleChatType, constant.NotificationChatType:
		for _, uid := range d.Recipients {
			if uid == d.Sender {
				return "", "", "", nil
			}
		}
		return "online-push", policy.OnlinePushBinding, policy.OnlineFailureMode, nil
	default:
		return "", "", "", ErrInvalid
	}
}
