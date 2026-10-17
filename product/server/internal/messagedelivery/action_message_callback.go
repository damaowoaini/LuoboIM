package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

type ownedMessageRow struct {
	ID                  string `bson:"_id"`
	Epoch               string `bson:"epoch"`
	Kind                string `bson:"kind"`
	OwnedMessageSummary `bson:",inline"`
}
type OwnedMessageDispatch struct {
	store *Store
	row   ownedMessageRow
}

func (s *Store) validateOwnedMessageCall(ctx context.Context, call OwnedGroupMessageCall) error {
	in, err := s.ownedActionInput(ctx, call.EffectID)
	if err != nil {
		return err
	}
	proof, err := ownedMessageProof(call)
	if err != nil || !validSHA(proof) {
		return ErrInvalid
	}
	want := in.profile.BeforeMessage
	if call.Stage == "after-message" {
		want = in.profile.AfterMessage
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(call.Message)
	if err != nil {
		return ErrInvalid
	}
	// Payload is a deterministic protobuf; use its actual canonical marshaler
	// rather than a textual DTO to bind source identity/content.
	if in.profileSHA != call.ProfileSHA256 || in.frozen.OperationID != call.OperationID || want != call.Callback || hash(body) != in.frozen.PayloadSHA256 {
		return ErrConflict
	}
	return nil
}
func (s *Store) runOwnedMessageCallback(ctx context.Context, call OwnedGroupMessageCall, engine OwnedGroupMessageCallbacks) error {
	if call.Callback.Binding == "" {
		return nil
	}
	body, err := engine.RequestBody(ctx, call)
	if err != nil {
		return err
	}
	if len(body) == 0 || len(body) > 40<<20 {
		return ErrInvalid
	}
	dispatch, err := s.beginOwnedMessageCallback(ctx, call, hash(body))
	if err != nil {
		return err
	}
	if dispatch == nil {
		return s.requireOwnedMessageCallback(ctx, call)
	}
	if err = engine.Execute(ctx, call, dispatch, body); err != nil {
		if errors.Is(err, ErrOwnedMessageUnknown) || errors.Is(err, ErrOwnedMessageRejected) {
			return s.requireOwnedMessageCallback(ctx, call)
		}
		return err
	}
	return s.requireOwnedMessageCallback(ctx, call)
}
func (s *Store) beginOwnedMessageCallback(ctx context.Context, call OwnedGroupMessageCall, bodySHA string) (*OwnedMessageDispatch, error) {
	if ctx == nil || !validSHA(bodySHA) || mongo.SessionFromContext(ctx) != nil {
		return nil, ErrInvalid
	}
	attempt, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrUnprepared
	}
	proof, err := ownedMessageProof(call)
	if err != nil {
		return nil, err
	}
	var claim ownedMessageRow
	granted := false
	err = s.transaction(ctx, func(tx context.Context) error {
		granted = false
		if err := s.validateOwnedMessageCall(tx, call); err != nil {
			return err
		}
		id := key(s.epoch, "owned-message-callback", ownedMessageKey(call))
		var old ownedMessageRow
		err := s.db.Collection(Receipts).FindOne(tx, bson.M{"_id": id}).Decode(&old)
		if err == nil {
			if old.ID != id || old.Epoch != s.epoch || old.Kind != "owned-message-callback-v1" || old.RequestSHA256 != proof || old.BodySHA256 != bodySHA || old.ProfileSHA256 != call.ProfileSHA256 || old.EffectID != call.EffectID || old.Stage != call.Stage || old.Revision < 1 || old.Revision >= math.MaxInt64-2 {
				return ErrConflict
			}
			switch old.State {
			case "acknowledged", "unconfirmed", "rejected":
				return s.requireOwnedMessageCallback(tx, call)
			case "pending-not-dispatched", "pending-confirmed":
			default:
				return ErrOwnedMessageUnknown
			}
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		claim = ownedMessageRow{ID: id, Epoch: s.epoch, Kind: "owned-message-callback-v1", OwnedMessageSummary: OwnedMessageSummary{EffectID: call.EffectID, Stage: call.Stage, ProfileSHA256: call.ProfileSHA256, RequestSHA256: proof, BodySHA256: bodySHA, State: "prepared", Revision: 1, AttemptID: attempt.String(), InstanceID: s.instanceID}}
		if old.ID != "" {
			claim.Revision = old.Revision + 1
			changed, err := s.db.Collection(Receipts).ReplaceOne(tx, bson.M{"_id": id, "revision": old.Revision, "state": old.State, "request_sha256": proof, "body_sha256": bodySHA}, claim)
			if err != nil {
				return err
			}
			if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
				return ErrConflict
			}
		} else {
			if _, err = s.db.Collection(Receipts).InsertOne(tx, claim); err != nil {
				return err
			}
		}
		granted = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !granted {
		return nil, nil
	}
	return &OwnedMessageDispatch{store: s, row: claim}, nil
}

// Physical permission is a KNOWN strong commit. Clear/redaction must write
// this same source row before it can acknowledge privacy changes. No unknown
// transaction result may grant HTTP. Commit marks an already-started attempt;
// socket/remote effects after it cannot be retracted by a subsequent Clear.
func (d *OwnedMessageDispatch) BeforeHTTP(ctx context.Context, call OwnedGroupMessageCall, body []byte) error {
	if d == nil || d.store == nil || ctx == nil || hash(body) != d.row.BodySHA256 {
		return ErrInvalid
	}
	err := d.store.transaction(ctx, func(tx context.Context) error {
		if err := d.store.validateOwnedMessageCall(tx, call); err != nil {
			return err
		}
		effects, err := d.store.ownedActionInput(tx, call.EffectID)
		if err != nil {
			return err
		}
		_, redacted, err := effects.denied(tx, true)
		if err != nil {
			return err
		}
		if redacted {
			return ErrOwnedActionRedacted
		}
		if err = effects.effects.FenceOwnedNotification(tx, effects.cap); err != nil {
			return err
		}
		result, err := d.store.db.Collection(Receipts).UpdateOne(tx, bson.M{"_id": d.row.ID, "epoch": d.row.Epoch, "revision": d.row.Revision, "state": "prepared", "attempt_id": d.row.AttemptID, "instance_id": d.row.InstanceID}, bson.M{"$set": bson.M{"state": "dispatching"}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if result.MatchedCount != 1 || result.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return err
	}
	d.row.State = "dispatching"
	d.row.Revision++
	return nil
}
func (d *OwnedMessageDispatch) NotDispatched(ctx context.Context) error {
	return d.finish(ctx, "pending-not-dispatched", hash([]byte("owned adapter did not invoke HTTP.Do")))
}
func (d *OwnedMessageDispatch) Finish(ctx context.Context, outcome, outcomeSHA string) error {
	state := ""
	switch outcome {
	case "acknowledged", "rejected":
		state = outcome
	case "unknown":
		state = "unconfirmed"
	default:
		return ErrInvalid
	}
	err := d.finish(ctx, state, outcomeSHA)
	if err != nil {
		return err
	}
	if state == "unconfirmed" {
		return ErrOwnedMessageUnknown
	}
	if state == "rejected" {
		return ErrOwnedMessageRejected
	}
	return nil
}
func (d *OwnedMessageDispatch) finish(ctx context.Context, state, proof string) error {
	if d == nil || d.store == nil || ctx == nil || !validSHA(proof) || d.row.State != "prepared" && d.row.State != "dispatching" {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	result, err := d.store.db.Collection(Receipts).UpdateOne(current, bson.M{"_id": d.row.ID, "epoch": d.row.Epoch, "revision": d.row.Revision, "state": d.row.State, "attempt_id": d.row.AttemptID, "instance_id": d.row.InstanceID}, bson.M{"$set": bson.M{"state": state, "outcome_sha256": proof}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil || result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrOwnedMessageUnknown
	}
	return nil
}
func (s *Store) InspectOwnedMessageCallback(ctx context.Context, effect, stage string) (OwnedMessageSummary, error) {
	if s == nil || ctx == nil || !validUUID(effect) || stage != "before-message" && stage != "after-message" {
		return OwnedMessageSummary{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if mongo.SessionFromContext(current) == nil {
		if err := s.CheckReady(current); err != nil {
			return OwnedMessageSummary{}, err
		}
	} else if err := s.requireTX(current); err != nil {
		return OwnedMessageSummary{}, err
	}
	var row ownedMessageRow
	id := key(s.epoch, "owned-message-callback", effect+"\x00"+stage)
	if err := s.db.Collection(Receipts).FindOne(current, bson.M{"_id": id}).Decode(&row); err != nil {
		return OwnedMessageSummary{}, err
	}
	if row.ID != id || row.Epoch != s.epoch || row.Kind != "owned-message-callback-v1" || row.EffectID != effect || row.Stage != stage || !validSHA(row.ProfileSHA256) || !validSHA(row.RequestSHA256) || !validSHA(row.BodySHA256) || !validUUID(row.AttemptID) || !validUUID(row.InstanceID) || row.Revision < 1 || row.Revision >= math.MaxInt64 {
		return OwnedMessageSummary{}, ErrConflict
	}
	return row.OwnedMessageSummary, nil
}

// Operator-only, not registered on HTTP. Complete expected receipt plus an
// independently verified drained instance proof is mandatory; unknown CAS is
// inspected rather than retried. Never reruns group RPC/before callback.
func (s *Store) ConfirmOwnedMessageCallback(ctx context.Context, expected OwnedMessageSummary, decision, evidence string, drain KafkaAttemptDrainProof) error {
	if !validSHA(evidence) || drain.InstanceID != expected.InstanceID || !validSHA(drain.EvidenceSHA256) {
		return ErrInvalid
	}
	state := ""
	switch decision {
	case "applied":
		state = "acknowledged"
	case "not-applied":
		state = "pending-confirmed"
	case "rejected":
		state = "rejected"
	default:
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx context.Context) error {
		actual, err := s.InspectOwnedMessageCallback(tx, expected.EffectID, expected.Stage)
		if err != nil {
			return err
		}
		if actual != expected || actual.State != "prepared" && actual.State != "dispatching" && actual.State != "unconfirmed" || actual.Revision >= math.MaxInt64-1 {
			return ErrConflict
		}
		proof, _ := json.Marshal([]any{decision, evidence, drain})
		changed, err := s.db.Collection(Receipts).UpdateOne(tx, bson.M{"_id": key(s.epoch, "owned-message-callback", actual.EffectID+"\x00"+actual.Stage), "revision": actual.Revision, "state": actual.State, "attempt_id": actual.AttemptID, "body_sha256": actual.BodySHA256, "request_sha256": actual.RequestSHA256, "profile_sha256": actual.ProfileSHA256}, bson.M{"$set": bson.M{"state": state, "outcome_sha256": hash(proof)}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
}
