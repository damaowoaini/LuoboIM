package messagedelivery

import (
	"context"
	"errors"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func (s *Store) RequiredOwnedActionPolicies(ctx context.Context) ([]string, error) {
	if s == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.checkRequiredPolicyCatalog(current); err != nil {
		return nil, err
	}
	cursor, err := s.db.Collection(Effects).Find(current, bson.M{"epoch": s.epoch, "producer_version": ownedGroupProducer, "state": "ready"})
	if err != nil {
		return nil, err
	}
	found := map[string]bool{}
	for cursor.Next(current) {
		var row struct {
			Branch string `bson:"branch_id"`
		}
		if cursor.Decode(&row) != nil {
			err = ErrConflict
			break
		}
		d, e := s.readDutyRecord(current, row.Branch)
		if e != nil {
			err = e
			break
		}
		found[d.PolicySHA] = true
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(current)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}
