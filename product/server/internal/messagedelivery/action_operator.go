package messagedelivery

import (
	"context"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type OwnedLegacyDrainProof struct {
	Epoch                  string `json:"epoch"`
	ActivationSHA256       string `json:"activationSHA256"`
	LegacyProducersStopped bool   `json:"legacyProducersStopped"`
	ReceiverWorkersJoined  bool   `json:"receiverWorkersJoined"`
	EvidenceSHA256         string `json:"evidenceSHA256"`
}

func (s *Store) InspectLegacyGroupNotification(ctx context.Context, id string) (groupeffects.LegacyNotificationSummary, error) {
	if s == nil || ctx == nil {
		return groupeffects.LegacyNotificationSummary{}, ErrInvalid
	}
	if err := s.CheckKafkaReady(ctx); err != nil {
		return groupeffects.LegacyNotificationSummary{}, err
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return groupeffects.LegacyNotificationSummary{}, err
	}
	return effects.InspectLegacyOwnedNotification(ctx, id)
}

// This operator mutation cannot replay a group RPC, callback or provider call.
// Before restoration/redaction, prove that this source has NEVER reached this
// receiver's accepted receipt boundary; unknown old custody is left untouched.
func (s *Store) ConfirmLegacyGroupNotification(ctx context.Context, expected groupeffects.LegacyNotificationSummary, decision string, profile *deliveryprofile.Group, evidence string, drain OwnedLegacyDrainProof) error {
	if s == nil || ctx == nil || !validSHA(evidence) || drain.Epoch != s.epoch || drain.ActivationSHA256 != s.activationSHA || !drain.LegacyProducersStopped || !drain.ReceiverWorkersJoined || !validSHA(drain.EvidenceSHA256) || expected.Epoch != s.epoch {
		return ErrInvalid
	}
	if decision != "restore-original-cut" && decision != "redact-incomplete" {
		return ErrInvalid
	}
	if decision == "restore-original-cut" && profile == nil || decision == "redact-incomplete" && profile != nil {
		return ErrInvalid
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return err
	}
	if err = s.CheckKafkaReady(ctx); err != nil {
		return err
	}
	if err = effects.CheckReady(ctx); err != nil {
		return err
	}
	return s.transaction(ctx, func(tx context.Context) error {
		if err := s.db.Collection(Receipts).FindOne(tx, bson.M{"_id": key(s.epoch, "source", expected.ID)}).Err(); err != mongo.ErrNoDocuments {
			if err == nil {
				return ErrConflict
			}
			return err
		}
		proof, _ := json.Marshal(drain)
		if decision == "redact-incomplete" {
			return effects.RedactLegacyOwnedNotification(tx, expected, evidence, hash(proof))
		}
		if profile.Transport.ActivationSHA256 != s.activationSHA {
			return ErrConflict
		}
		return effects.RestoreLegacyOwnedNotification(tx, expected, *profile, evidence, hash(proof))
	})
}

func (s *Store) InspectOwnedBusinessAfter(ctx context.Context, id string) (groupeffects.OwnedRoleCallbackRef, error) {
	if s == nil || ctx == nil {
		return groupeffects.OwnedRoleCallbackRef{}, ErrInvalid
	}
	if err := s.CheckKafkaReady(ctx); err != nil {
		return groupeffects.OwnedRoleCallbackRef{}, err
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return groupeffects.OwnedRoleCallbackRef{}, err
	}
	return effects.InspectOwnedRoleCallback(ctx, id)
}
func (s *Store) ConfirmOwnedBusinessAfter(ctx context.Context, expected groupeffects.OwnedRoleCallbackRef, decision, evidence string, drain KafkaAttemptDrainProof) error {
	if s == nil || ctx == nil || !validSHA(evidence) || !validSHA(drain.EvidenceSHA256) || drain.InstanceID != expected.InstanceID || expected.Epoch != s.epoch {
		return ErrInvalid
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return err
	}
	if err = effects.CheckReady(ctx); err != nil {
		return err
	}
	actual, err := effects.InspectOwnedRoleCallback(ctx, expected.ID)
	if err != nil {
		return err
	}
	if actual != expected {
		return ErrConflict
	}
	_, err = effects.Confirm(ctx, groupeffects.Confirmation{ExpectedSenderInstanceID: expected.InstanceID, ID: expected.ID, ExpectedRevision: expected.Revision, ExpectedPayloadSHA256: expected.PayloadSHA256, Decision: decision, EvidenceSHA256: evidence, DrainedEvidenceSHA256: drain.EvidenceSHA256})
	return err
}
