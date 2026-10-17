package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/common/riskcontrol"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

const ownedGroupProducer = "group-effect-v1"

// These interfaces are internal owned runtime adapters, never RPC arguments.
// The concrete signed implementation freezes private endpoint material once.
type OwnedGroupMessageCallbacks interface {
	ProfileFingerprint() string
	RequestBody(context.Context, OwnedGroupMessageCall) ([]byte, error)
	Execute(context.Context, OwnedGroupMessageCall, *OwnedMessageDispatch, []byte) error
	Close() error
}
type OwnedGroupMessageCall struct {
	EffectID, OperationID, Stage, ProfileSHA256 string
	Message                                     *sdkws.MsgData
	Callback                                    deliveryprofile.Callback
}
type ownedActionInput struct {
	store                *Store
	effects              *groupeffects.Store
	cap                  *groupeffects.OwnedNotificationDelivery
	frozen               groupeffects.Frozen
	targets              []groupeffects.NotificationTargets
	profile              deliveryprofile.Group
	profileSHA, producer string
}

func (s *Store) ownedActionInput(ctx context.Context, id string) (ownedActionInput, error) {
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return ownedActionInput{}, err
	}
	cap, err := effects.ReadOwnedNotificationDelivery(ctx, id)
	if err != nil {
		return ownedActionInput{}, err
	}
	f, t, p, sha, producer := cap.Snapshot()
	if p.Transport.Epoch != s.epoch || p.Transport.ActivationSHA256 != s.activationSHA {
		return ownedActionInput{}, ErrUnprepared
	}
	return ownedActionInput{store: s, effects: effects, cap: cap, frozen: f, targets: t, profile: p, profileSHA: sha, producer: producer}, nil
}
func (s *Store) ownedAccepted(ctx context.Context, id string) (Receipt, bool, error) {
	var r Receipt
	err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "source", id)}).Decode(&r)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	if r.ID != key(s.epoch, "source", id) || r.SourceID != id || r.Epoch != s.epoch || !validSHA(r.InputSHA256) || r.AcceptedAt.IsZero() || len(r.Branches) < 1 || len(r.Branches) > 2 {
		return Receipt{}, false, ErrConflict
	}
	for _, b := range r.Branches {
		if !validUUID(b.ID) || !identity(b.ConversationID) || b.Persist && b.Seq < 1 || !b.Persist && b.Seq != 0 {
			return Receipt{}, false, ErrConflict
		}
	}
	return r, true, nil
}

// RecoverOwnedGroupNotification is the actual role/action source receiver.
// It never dispatches a business RPC or synthesizes a Kafka source identity.
// Source receipt is resolved before risk/callback/seq/history reconstruction.
func (s *Store) RecoverOwnedGroupNotification(ctx context.Context, id string, callbacks OwnedGroupMessageCallbacks) (Receipt, error) {
	if s == nil || ctx == nil || !s.membershipRequired || !validUUID(id) || mongo.SessionFromContext(ctx) != nil {
		return Receipt{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(ctx); err != nil {
		return Receipt{}, err
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return Receipt{}, err
	}
	if err = effects.CheckReady(ctx); err != nil {
		return Receipt{}, err
	}
	var input ownedActionInput
	var result Receipt
	var exists, done, allRedacted, quarantined bool
	err = s.transaction(ctx, func(tx context.Context) error {
		var err error
		quarantined, err = effects.QuarantineLegacyOwnedNotification(tx, id)
		if err != nil {
			return err
		}
		if quarantined {
			return nil
		}
		input, err = s.ownedActionInput(tx, id)
		if err != nil {
			return err
		}
		accepted, epoch, inputSHA, receiptSHA := input.cap.Accepted()
		done = accepted
		result, exists, err = s.ownedAccepted(tx, id)
		if err != nil {
			return err
		}
		if !done {
			_, allRedacted, err = input.denied(tx, false)
			if err != nil {
				return err
			}
		}
		if done {
			body, _ := json.Marshal(result)
			if !exists || epoch != s.epoch || inputSHA != result.InputSHA256 || hash(body) != receiptSHA {
				return ErrConflict
			}
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	if quarantined {
		return Receipt{}, groupeffects.ErrLegacyOwnedQuarantined
	}
	if done {
		return result, nil
	}
	if callbacks == nil || callbacks.ProfileFingerprint() != input.profileSHA {
		return Receipt{}, ErrUnprepared
	}
	var message sdkws.MsgData
	if proto.Unmarshal(input.frozen.Payload, &message) != nil || message.ContentType == constant.Text {
		return Receipt{}, ErrConflict
	}
	// Existing modifier only applies to Text. A frozen management notification
	// must remain a notification, so enabled modifier is accurately a no-op.
	call := OwnedGroupMessageCall{EffectID: id, OperationID: input.frozen.OperationID, ProfileSHA256: input.profileSHA, Message: &message}
	if !exists {
		call.Stage = "before-message"
		call.Callback = input.profile.BeforeMessage
		if !allRedacted {
			blocked, err := riskcontrol.IsActionBlocked(ctx, riskcontrol.RiskDisableGroupMsgKey)
			if err != nil {
				return Receipt{}, err
			}
			if blocked {
				return Receipt{}, ErrOwnedMessageRejected
			}
			if err = s.runOwnedMessageCallback(ctx, call, callbacks); err != nil && !errors.Is(err, ErrOwnedActionRedacted) {
				return Receipt{}, err
			}
		}
		err = s.transaction(ctx, func(tx context.Context) error {
			current, err := s.ownedActionInput(tx, id)
			if err != nil {
				return err
			}
			if current.profileSHA != input.profileSHA || current.frozen.PayloadSHA256 != input.frozen.PayloadSHA256 {
				return ErrConflict
			}
			_, redacted, err := current.denied(tx, true)
			if err != nil {
				return err
			}
			if !redacted {
				if err = s.requireOwnedMessageCallback(tx, call); err != nil {
					return err
				}
			}
			result, err = s.acceptOwnedAction(tx, current)
			return err
		})
		if err != nil {
			return Receipt{}, err
		}
	}
	// Legacy SendMsg logs AfterSendGroupMsg errors after accepted MQ work. Keep
	// that write ACK/order contract, but retain the exact unknown/rejected duty.
	call.Stage = "after-message"
	call.Callback = input.profile.AfterMessage
	afterErr := error(nil)
	if !receiptAllHidden(result) && !allRedacted {
		afterErr = s.runOwnedMessageCallback(ctx, call, callbacks)
	}
	if afterErr != nil && !errors.Is(afterErr, ErrOwnedMessageUnknown) && !errors.Is(afterErr, ErrOwnedMessageRejected) && !errors.Is(afterErr, ErrOwnedActionRedacted) {
		return Receipt{}, afterErr
	}
	err = s.transaction(ctx, func(tx context.Context) error {
		current, err := s.ownedActionInput(tx, id)
		if err != nil {
			return err
		}
		if current.profileSHA != input.profileSHA {
			return ErrConflict
		}
		_, redacted, err := current.denied(tx, true)
		if err != nil {
			return err
		}
		if !redacted && !receiptAllHidden(result) {
			if err = s.requireOwnedMessageCallback(tx, call); err != nil {
				return err
			}
		}
		resolved, ok, err := s.ownedAccepted(tx, id)
		if err != nil || !ok {
			return errors.Join(ErrConflict, err)
		}
		body, _ := json.Marshal(resolved)
		return current.effects.AcknowledgeOwnedNotificationDelivery(tx, current.cap, s.epoch, resolved.InputSHA256, hash(body))
	})
	if err != nil {
		return Receipt{}, err
	}
	return result, nil
}
func (s *Store) acceptOwnedAction(ctx context.Context, in ownedActionInput) (Receipt, error) {
	if err := s.requireTX(ctx); err != nil {
		return Receipt{}, err
	}
	plans, err := groupeffects.PlanNotificationBranches(in.frozen)
	if err != nil {
		return Receipt{}, err
	}
	rosters := map[string][]string{}
	for _, t := range in.targets {
		rosters[t.BranchID] = t.UserIDs
	}
	envelope := Envelope{ID: in.frozen.ID}
	for _, p := range plans {
		ids, ok := rosters[p.ID]
		if !ok {
			return Receipt{}, ErrConflict
		}
		envelope.Branches = append(envelope.Branches, Branch{ID: p.ID, ConversationID: p.ConversationID, Payload: p.Payload, Recipients: ids, Persist: p.Persist})
	}
	p := KafkaDeliveryPolicy(in.profile.Transport)
	policySHA, err := p.Fingerprint()
	if err != nil {
		return Receipt{}, err
	}
	if err = s.freezeKafkaPolicy(ctx, p, policySHA); err != nil {
		return Receipt{}, err
	}
	denied, _, err := in.denied(ctx, true)
	if err != nil {
		return Receipt{}, err
	}
	r, err := s.acceptSourceEnvelope(ctx, envelope, true, denied)
	if err != nil {
		return Receipt{}, err
	}
	// Complete owned profile is bound in the same transaction as every branch,
	// using the exact source capability rather than arbitrary caller recipients.
	storedCID := ""
	storedSeq := int64(0)
	for _, b := range r.Branches {
		if b.Persist && b.ConversationID == in.cap.VisibilitySnapshot().ConversationID {
			storedCID = b.ConversationID
			storedSeq = b.Seq
		}
	}
	// Facts commit with the source receipt/slot/counter; they grant NO read permission.
	if err := s.freezeOwnedNotificationFacts(ctx, in, r, envelope, denied); err != nil {
		return Receipt{}, err
	}
	for _, b := range r.Branches {
		deniedHashes := make([]string, 0, len(denied[b.ID]))
		for _, uid := range denied[b.ID] {
			deniedHashes = append(deniedHashes, hash([]byte(uid)))
		}
		result, err := s.db.Collection(Effects).UpdateOne(ctx, bson.M{"_id": key(s.epoch, "branch", b.ID), "epoch": s.epoch, "source_id": in.frozen.ID, "revision": int64(1), "producer_version": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"producer_version": ownedGroupProducer, "action_producer": in.producer, "action_profile": in.profile, "action_profile_sha256": in.profileSHA, "owned_visibility": in.cap.VisibilitySnapshot(), "stored_peer_cid": storedCID, "stored_peer_seq": storedSeq, "policy_sha256": policySHA, "source_operation_id": in.frozen.OperationID, "visibility_denied": deniedHashes}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return Receipt{}, err
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 1 {
			return Receipt{}, ErrConflict
		}
	}
	return r, nil
}

var ErrOwnedMessageUnknown = errors.New("owned message callback unconfirmed; inspect original receipt")
var ErrOwnedMessageRejected = errors.New("owned message callback or risk rejected")

// Minimal immutable callback receipts live in the already prepared Receipts
// collection. They never store endpoint/secret or raw response bodies.
type OwnedMessageSummary struct {
	EffectID      string `bson:"effect_id" json:"effectID"`
	Stage         string `bson:"stage" json:"stage"`
	ProfileSHA256 string `bson:"profile_sha256" json:"profileSHA256"`
	RequestSHA256 string `bson:"request_sha256" json:"requestSHA256"`
	BodySHA256    string `bson:"body_sha256" json:"bodySHA256"`
	State         string `bson:"state" json:"state"`
	Revision      int64  `bson:"revision" json:"revision"`
	AttemptID     string `bson:"attempt_id" json:"attemptID"`
	InstanceID    string `bson:"instance_id" json:"instanceID"`
	OutcomeSHA256 string `bson:"outcome_sha256" json:"outcomeSHA256"`
}

func ownedMessageKey(call OwnedGroupMessageCall) string { return call.EffectID + "\x00" + call.Stage }
func ownedMessageProof(call OwnedGroupMessageCall) (string, error) {
	if !validUUID(call.EffectID) || !validSHA(call.ProfileSHA256) || call.Message == nil || !call.Callback.Valid() || call.Stage != "before-message" && call.Stage != "after-message" {
		return "", ErrInvalid
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(call.Message)
	if err != nil {
		return "", ErrInvalid
	}
	frozen, _ := json.Marshal([]any{call.EffectID, call.OperationID, call.Stage, call.ProfileSHA256, call.Callback, hash(body)})
	return hash(frozen), nil
}
func (s *Store) requireOwnedMessageCallback(ctx context.Context, call OwnedGroupMessageCall) error {
	if call.Callback.Binding == "" {
		return nil
	}
	proof, err := ownedMessageProof(call)
	if err != nil {
		return err
	}
	var row OwnedMessageSummary
	if err = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "owned-message-callback", ownedMessageKey(call))}).Decode(&row); err != nil {
		return err
	}
	if row.EffectID != call.EffectID || row.Stage != call.Stage || row.RequestSHA256 != proof || row.ProfileSHA256 != call.ProfileSHA256 || !validSHA(row.BodySHA256) || !validUUID(row.AttemptID) || !validUUID(row.InstanceID) || row.Revision < 1 || row.Revision >= math.MaxInt64 {
		return ErrConflict
	}
	switch row.State {
	case "acknowledged":
		return nil
	case "unconfirmed":
		if call.Stage == "after-message" || call.Callback.Mode == "continue" {
			return nil
		}
		return ErrOwnedMessageUnknown
	case "rejected":
		if call.Stage == "after-message" {
			return nil
		}
		return ErrOwnedMessageRejected
	default:
		return ErrOwnedMessageUnknown
	}
}
