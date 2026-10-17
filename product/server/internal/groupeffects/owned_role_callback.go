package groupeffects

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Nonsecret complete signing-channel binding. The old RoleAfterPolicy remains
// unchanged; this additive sidecar disambiguates retained key rotations whose
// old endpoint/configuration hash did not include signing material.
type OwnedRoleAfterProfile struct {
	Epoch               string       `bson:"epoch" json:"epoch"`
	Name                CallbackName `bson:"name" json:"name"`
	ConfigurationSHA256 string       `bson:"configuration_sha256" json:"configurationSHA256"`
	EndpointBinding     string       `bson:"endpoint_binding" json:"endpointBinding"`
	TransportSHA256     string       `bson:"transport_sha256" json:"transportSHA256"`
	TimeoutSeconds      int          `bson:"timeout_seconds" json:"timeoutSeconds"`
	FailedContinue      bool         `bson:"failed_continue" json:"failedContinue"`
}

func FreezeOwnedRoleAfter(database, epoch string, name CallbackName, base, secret string, conf config.CallBackConfig) (OwnedRoleAfterProfile, error) {
	if !conf.Enable || !validID(epoch) {
		return OwnedRoleAfterProfile{}, ErrInvalid
	}
	endpoint, err := NewEndpoint(base, database, name, []byte(secret), roleCallbackTimeout(conf.CallbackTimeOut))
	if err != nil {
		return OwnedRoleAfterProfile{}, err
	}
	binding := endpoint.Binding()
	endpoint.CloseOwned()
	configuration := []any{epoch, name, binding, conf}
	body, _ := json.Marshal(configuration)
	p := OwnedRoleAfterProfile{Epoch: epoch, Name: name, ConfigurationSHA256: digest(body), EndpointBinding: binding, TimeoutSeconds: conf.CallbackTimeOut, FailedContinue: conf.CallbackFailedContinue != nil && *conf.CallbackFailedContinue}
	proof, _ := json.Marshal([]any{database, p, base, secret, conf})
	p.TransportSHA256 = digest(proof)
	return p, nil
}

type OwnedRoleCallbackRef struct {
	ID, Cursor, Epoch, InstanceID, PayloadSHA256, ConfigurationSHA256, TransportSHA256, SourceSHA256, MigrationSourceSHA256 string
	State                                                                                                                   State
	Revision                                                                                                                int64
	Legacy                                                                                                                  bool
}
type ownedRoleCallbackRow struct {
	Receipt            `bson:",inline"`
	Order              RoleEffectOrder        `bson:"role_order"`
	Owned              *OwnedRoleAfterProfile `bson:"role_after_owned"`
	MigrationSourceSHA string                 `bson:"role_after_migration_source_sha256"`
	Quarantine         string                 `bson:"receiver_quarantine"`
}

func (s *Store) ownedRoleCallback(ctx context.Context, id string) (ownedRoleCallbackRow, OwnedRoleCallbackRef, error) {
	var raw bson.Raw
	var r ownedRoleCallbackRow
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&raw); err != nil {
		return r, OwnedRoleCallbackRef{}, unavailable(err)
	}
	if bson.Unmarshal(raw, &r) != nil || r.Receipt.validate() != nil || r.Kind != AfterCallback || r.Order.After == nil || !validSHA(r.Order.After.ConfigurationSHA256) || !validID(r.Order.Epoch) {
		return r, OwnedRoleCallbackRef{}, ErrConflict
	}
	ref := OwnedRoleCallbackRef{ID: id, Cursor: id, Epoch: r.Order.Epoch, State: r.State, InstanceID: r.SenderInstanceID, Revision: r.Revision, PayloadSHA256: r.PayloadSHA256, ConfigurationSHA256: r.Order.After.ConfigurationSHA256, SourceSHA256: digest(raw), MigrationSourceSHA256: r.MigrationSourceSHA, Legacy: r.Owned == nil}
	if r.Owned != nil {
		if r.Owned.Epoch != r.Order.Epoch || r.Owned.ConfigurationSHA256 != ref.ConfigurationSHA256 || r.Owned.TimeoutSeconds != r.Order.After.TimeoutSeconds || r.Owned.FailedContinue != r.Order.After.FailedContinue || !validSHA(r.Owned.TransportSHA256) {
			return r, ref, ErrConflict
		}
		ref.TransportSHA256 = r.Owned.TransportSHA256
	}
	return r, ref, nil
}
func (s *Store) ListOwnedRoleCallbacks(ctx context.Context, after string, limit int64) ([]OwnedRoleCallbackRef, error) {
	if s == nil || ctx == nil || limit < 1 || limit > 32 || after != "" && !validID(after) {
		return nil, ErrInvalid
	}
	filter := bson.M{"kind": AfterCallback, "state": Pending, "role_order.after": bson.M{"$exists": true}}
	if after != "" {
		filter["_id"] = bson.M{"$gt": after}
	}
	cursor, err := s.collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit).SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	var out []OwnedRoleCallbackRef
	for cursor.Next(ctx) {
		var r struct {
			ID string `bson:"_id"`
		}
		if cursor.Decode(&r) != nil {
			err = ErrConflict
			break
		}
		_, ref, e := s.ownedRoleCallback(ctx, r.ID)
		if e != nil {
			err = e
			break
		}
		out = append(out, ref)
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
func (s *Store) RequiredOwnedRoleCallbackTransports(ctx context.Context) ([]string, error) {
	cursor, err := s.collection.Find(ctx, bson.M{"kind": AfterCallback, "state": bson.M{"$ne": Done}, "role_order.after": bson.M{"$exists": true}}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for cursor.Next(ctx) {
		var r struct {
			ID string `bson:"_id"`
		}
		if cursor.Decode(&r) != nil {
			err = ErrConflict
			break
		}
		_, ref, e := s.ownedRoleCallback(ctx, r.ID)
		if e != nil {
			err = e
			break
		}
		if !ref.Legacy {
			found[ref.TransportSHA256] = true
		}
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(ctx)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	out := make([]string, 0, len(found))
	for sha := range found {
		out = append(out, sha)
	}
	sort.Strings(out)
	return out, nil
}

// Old pending signing channels are restored only from an explicitly attested,
// source-ID+whole-row-SHA pinned private entry plus old-process drain evidence.
// No current-key/config fallback. Unknown migration ACK grants no HTTP.
func (s *Store) BindOwnedRoleCallback(ctx context.Context, expected OwnedRoleCallbackRef, profile OwnedRoleAfterProfile, evidence, drain string) error {
	if s == nil || ctx == nil || !validSHA(evidence) || !validSHA(drain) || !validSHA(profile.TransportSHA256) {
		return ErrInvalid
	}
	r, actual, err := s.ownedRoleCallback(ctx, expected.ID)
	if err != nil {
		return err
	}
	if actual != expected || !actual.Legacy || r.State != Pending || r.Attempts != 0 || r.Claim != "" || r.Revision >= math.MaxInt64-1 || profile.Epoch != r.Order.Epoch || profile.ConfigurationSHA256 != r.Order.After.ConfigurationSHA256 || profile.TimeoutSeconds != r.Order.After.TimeoutSeconds || profile.FailedContinue != r.Order.After.FailedContinue {
		return ErrConflict
	}
	var payload CallbackPayload
	if json.Unmarshal(r.Payload, &payload) != nil || payload.Name != profile.Name || payload.EndpointBinding != profile.EndpointBinding {
		return ErrConflict
	}
	proof, _ := json.Marshal([]any{expected, profile, evidence, drain})
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": r.ID, "state": Pending, "revision": r.Revision, "attempts": int32(0), "payload_sha256": r.PayloadSHA256, "role_after_owned": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"role_after_owned": profile, "role_after_migration_source_sha256": expected.SourceSHA256, "role_after_migration_sha256": digest(proof)}, "$unset": bson.M{"receiver_quarantine": ""}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return ErrUnconfirmed
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) QuarantineOwnedRoleCallback(ctx context.Context, expected OwnedRoleCallbackRef) error {
	r, actual, err := s.ownedRoleCallback(ctx, expected.ID)
	if err != nil {
		return err
	}
	if actual != expected || !actual.Legacy || r.State != Pending || r.Attempts != 0 {
		return ErrConflict
	}
	if r.Quarantine == "legacy-after-transport-v1" {
		return nil
	}
	if r.Revision >= math.MaxInt64-1 {
		return ErrConflict
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": r.ID, "state": Pending, "revision": r.Revision, "payload_sha256": r.PayloadSHA256, "receiver_quarantine": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"receiver_quarantine": "legacy-after-transport-v1"}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
func (s *Store) DeliverOwnedRoleCallback(ctx context.Context, ref OwnedRoleCallbackRef, profile OwnedRoleAfterProfile, endpoint *Endpoint) error {
	r, current, err := s.ownedRoleCallback(ctx, ref.ID)
	if err != nil {
		return err
	}
	if current != ref || r.Owned == nil || *r.Owned != profile || endpoint == nil || endpoint.Binding() != profile.EndpointBinding {
		return ErrConflict
	}
	return s.DeliverCallback(ctx, ref.ID, ref.Revision, ref.PayloadSHA256, endpoint)
}

// Source-side full channel binding commits with business/notice/order.
func (s *Store) QueueOwnedRoleCallback(ctx context.Context, f Frozen, order RoleEffectOrder, profile OwnedRoleAfterProfile) error {
	if order.After == nil || profile.Epoch != order.Epoch || profile.ConfigurationSHA256 != order.After.ConfigurationSHA256 || profile.TimeoutSeconds != order.After.TimeoutSeconds || profile.FailedContinue != order.After.FailedContinue || !validSHA(profile.TransportSHA256) {
		return ErrInvalid
	}
	var p CallbackPayload
	if json.Unmarshal(f.Payload, &p) != nil || p.Name != profile.Name || p.EndpointBinding != profile.EndpointBinding {
		return ErrInvalid
	}
	if err := s.QueueRoleCallback(ctx, f, order); err != nil {
		return err
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": f.ID, "state": Pending, "revision": int64(1), "role_after_owned": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"role_after_owned": profile}})
	if err != nil {
		return unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) InspectOwnedRoleCallback(ctx context.Context, id string) (OwnedRoleCallbackRef, error) {
	_, ref, err := s.ownedRoleCallback(ctx, id)
	return ref, err
}
