package groupeffects

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// RequiredOwnedProfiles is a whole pending-catalog startup scan, not a page of
// convenient current config. Known complete retained profiles require their exact adapters. A legacy
// incomplete row is isolated durably by the receiver, not a global startup
// outage or an implicit substitution of the current profile.
func (s *Store) RequiredOwnedProfiles(ctx context.Context) ([]deliveryprofile.Group, error) {
	if s == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return nil, err
	}
	cursor, err := s.collection.Find(current, bson.M{"kind": Notification, "state": Pending, "producer_version": bson.M{"$in": bson.A{"group-action-v1", "group-role-v1"}}})
	if err != nil {
		return nil, err
	}
	found := map[string]deliveryprofile.Group{}
	for cursor.Next(current) {
		var r struct {
			Receipt     `bson:",inline"`
			Policy      ActionDeliveryPolicy   `bson:"action_policy"`
			PolicySHA   string                 `bson:"action_policy_sha256"`
			Cut         bson.Raw               `bson:"owned_visibility"`
			Retained    *deliveryprofile.Group `bson:"receiver_owned_profile"`
			RetainedSHA string                 `bson:"receiver_owned_profile_sha256"`
		}
		if cursor.Decode(&r) != nil || r.Receipt.validate() != nil {
			err = ErrUnavailable
			break
		}
		if deliveryprofile.Digest(r.Policy) != r.PolicySHA {
			err = ErrConflict
			break
		}
		profile := r.Policy.Owned
		if profile == nil {
			profile = r.Retained
			if profile == nil {
				continue
			}
			if deliveryprofile.Digest(*profile) != r.RetainedSHA {
				err = ErrConflict
				break
			}
		}
		var cut deliveryprofile.VisibilityCut
		if len(r.Cut) == 0 || bson.Unmarshal(r.Cut, &cut) != nil || !cut.Valid() {
			err = ErrConflict
			break
		}
		sha, e := profile.Fingerprint()
		if e != nil {
			err = ErrConflict
			break
		}
		found[sha] = *profile
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(current)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]deliveryprofile.Group, 0, len(keys))
	for _, k := range keys {
		out = append(out, found[k])
	}
	return out, nil
}
