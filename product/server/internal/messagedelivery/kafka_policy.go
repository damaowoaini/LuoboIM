package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// KafkaDeliveryPolicy is an immutable, non-secret configuration snapshot. A
// callback/vendor binding identifies an exact owned runtime adapter/config;
// it is never a caller URL or a credential. Recovery must possess the matching
// adapter rather than substituting whatever config is current after restart.
// This snapshot alone does NOT provide those still-required executable adapters.
type KafkaDeliveryPolicy deliveryprofile.Transport

func (p KafkaDeliveryPolicy) Fingerprint() (string, error) {
	digest, err := deliveryprofile.Transport(p).Fingerprint()
	if err != nil {
		return "", ErrInvalid
	}
	return digest, nil
}

type kafkaPolicyRow struct {
	ID     string              `bson:"_id"`
	Kind   string              `bson:"kind"`
	Epoch  string              `bson:"epoch"`
	SHA256 string              `bson:"sha256"`
	Policy KafkaDeliveryPolicy `bson:"policy"`
}

// Non-secret tenant/database identity for the dedicated badge migration.
// The epoch/control binding remains explicit; this is not a readiness proof.
func (s *Store) KafkaBadgeScope() string {
	if s == nil || s.db == nil || !s.membershipRequired {
		return ""
	}
	body, _ := json.Marshal([]string{"ordinary-kafka-badge-v1", s.db.Name()})
	return hash(body)
}

// Called in the FIRST source transaction only. A failed insert/upsert cannot
// leave a committed message without its required frozen configuration.
func (s *Store) freezeKafkaPolicy(ctx context.Context, p KafkaDeliveryPolicy, expected string) error {
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	digest, err := p.Fingerprint()
	if err != nil || digest != expected || p.Epoch != s.epoch || p.ActivationSHA256 != s.activationSHA {
		return ErrInvalid
	}
	id := key(s.epoch, "kafka-policy", digest)
	var existing kafkaPolicyRow
	err = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": id}).Decode(&existing)
	if err == nil {
		if existing.ID != id || existing.Kind != "kafka-policy-v1" || existing.Epoch != s.epoch || existing.SHA256 != digest || existing.Policy != p {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	_, err = s.db.Collection(Receipts).InsertOne(ctx, kafkaPolicyRow{ID: id, Kind: "kafka-policy-v1", Epoch: s.epoch, SHA256: digest, Policy: p})
	return err
}

func (s *Store) readKafkaPolicy(ctx context.Context, digest string) (KafkaDeliveryPolicy, error) {
	if !validSHA(digest) {
		return KafkaDeliveryPolicy{}, ErrConflict
	}
	var row kafkaPolicyRow
	id := key(s.epoch, "kafka-policy", digest)
	if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": id}).Decode(&row); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return KafkaDeliveryPolicy{}, ErrUnprepared
		}
		return KafkaDeliveryPolicy{}, err
	}
	actual, err := row.Policy.Fingerprint()
	if err != nil || actual != digest || row.ID != id || row.Kind != "kafka-policy-v1" || row.Epoch != s.epoch || row.SHA256 != digest || row.Policy.Epoch != s.epoch || row.Policy.ActivationSHA256 != s.activationSHA {
		return KafkaDeliveryPolicy{}, ErrConflict
	}
	return row.Policy, nil
}
