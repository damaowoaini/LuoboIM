package meetingbooking

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const BookingsCollection = "luobo_meeting_bookings_v1"
const IntentsCollection = "luobo_meeting_booking_intents_v1"

type MongoStore struct {
	db                *mongo.Database
	bookings, intents *mongo.Collection
	indexes           indexCatalog
}
type mongoTx struct {
	scope             Principal
	session           context.Context
	bookings, intents *mongo.Collection
}

var malformed = errors.New("stored booking integrity failure")

// NewMongoStore creates local handles only: no DB command, connectivity probe,
// collection creation or index DDL. Readiness is checked on every Run/List.
func NewMongoStore(_ context.Context, db *mongo.Database) (*MongoStore, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	return &MongoStore{db: db, bookings: db.Collection(BookingsCollection), intents: db.Collection(IntentsCollection), indexes: mongoIndexCatalog{db: db}}, nil
}

func scoped(p Principal, id string) bson.M {
	return bson.M{"tenant": p.Tenant, "owner": p.Owner, "_id": id}
}
func (s *MongoStore) Run(ctx context.Context, p Principal, fn func(Tx) (*Result, error)) (*Result, error) {
	if !validPrincipal(p) {
		return nil, ErrDenied
	}
	if err := s.CheckReady(ctx); err != nil {
		return nil, err
	}
	for retry := 0; retry < 5; retry++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		session, err := s.db.Client().StartSession()
		if err != nil {
			return nil, err
		}
		value, err := session.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
			return fn(&mongoTx{scope: p, session: sc, bookings: s.bookings, intents: s.intents})
		}, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).SetReadPreference(readpref.Primary()))
		session.EndSession(ctx)
		if err == nil {
			if value == nil {
				return nil, nil
			}
			return value.(*Result), nil
		}
		// Competing owner+UUID inserts may be duplicate keys without a Mongo
		// transient label. Retry the SAME intent, never change its booking UUID.
		// Do not blindly retry UnknownTransactionCommitResult as a new intent.
		if !mongo.IsDuplicateKeyError(err) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil, ErrConflict
}
func (t *mongoTx) GetIntent(ctx context.Context, id string) (*Intent, error) {
	ctx = t.session // Every operation uses this transaction, not the outer HTTP context.
	var row Intent
	err := t.intents.FindOne(ctx, bson.M{"tenant": t.scope.Tenant, "owner": t.scope.Owner, "request_id": id}).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !storedIntentValid(&row, t.scope, id) {
		return nil, malformed
	}
	return &row, nil
}
func (t *mongoTx) InsertIntent(ctx context.Context, row *Intent) error {
	ctx = t.session // Every operation uses this transaction, not the outer HTTP context.
	if !storedIntentValid(row, t.scope, row.RequestID) {
		return ErrDenied
	}
	_, err := t.intents.InsertOne(ctx, row)
	return err
}
func (t *mongoTx) GetBooking(ctx context.Context, id string) (*Booking, error) {
	ctx = t.session // Every operation uses this transaction, not the outer HTTP context.
	var row Booking
	err := t.bookings.FindOne(ctx, scoped(t.scope, id)).Decode(&row)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if !storedBookingValid(&row, t.scope, id) {
		return nil, malformed
	}
	return &row, nil
}
func (t *mongoTx) InsertBooking(ctx context.Context, row *Booking) error {
	ctx = t.session // Every operation uses this transaction, not the outer HTTP context.
	if !storedBookingValid(row, t.scope, row.ID) || row.State != "scheduled" || row.Version != 1 {
		return ErrDenied
	}
	_, err := t.bookings.InsertOne(ctx, row)
	return err
}
func (t *mongoTx) ReplaceBooking(ctx context.Context, row *Booking, previous int64) error {
	ctx = t.session // Every operation uses this transaction, not the outer HTTP context.
	if !storedBookingValid(row, t.scope, row.ID) || row.Version != previous+1 {
		return ErrDenied
	}
	filter := scoped(t.scope, row.ID)
	filter["version"] = previous
	filter["state"] = "scheduled"
	result, err := t.bookings.ReplaceOne(ctx, filter, row)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return ErrConflict
	}
	return nil
}
func (s *MongoStore) List(ctx context.Context, p Principal, q ListQuery) ([]Booking, bool, error) {
	if !validPrincipal(p) || q.Limit < 1 || q.Limit > 100 {
		return nil, false, ErrDenied
	}
	if err := s.CheckReady(ctx); err != nil {
		return nil, false, err
	}
	filter := bson.M{"tenant": p.Tenant, "owner": p.Owner}
	if !q.IncludeCancelled {
		filter["state"] = "scheduled"
	} else {
		filter["state"] = bson.M{"$in": []string{"scheduled", "cancelled"}}
	}
	if q.BeforeID != "" {
		filter["$or"] = bson.A{bson.M{"created_at": bson.M{"$lt": q.BeforeTime}}, bson.M{"created_at": q.BeforeTime, "_id": bson.M{"$lt": q.BeforeID}}}
	}
	reader, err := s.bookings.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}, {Key: "_id", Value: -1}}).SetLimit(int64(q.Limit+1)))
	if err != nil {
		return nil, false, err
	}
	defer reader.Close(ctx)
	rows := []Booking{}
	if err := reader.All(ctx, &rows); err != nil {
		return nil, false, err
	}
	for _, row := range rows {
		if !storedBookingValid(&row, p, row.ID) {
			return nil, false, malformed
		}
	}
	more := len(rows) > q.Limit
	if more {
		rows = rows[:q.Limit]
	}
	return rows, more, nil
}
