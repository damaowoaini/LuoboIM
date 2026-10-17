package meetingbooking

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

func bookingMongo(t *testing.T) (*MongoStore, *mongo.Database) {
	return bookingMongoWithPreparation(t, true)
}
func bookingMongoWithPreparation(t *testing.T, prepare bool) (*MongoStore, *mongo.Database) {
	t.Helper()
	raw := os.Getenv("LUOBO_BOOKING_TEST_MONGO_URI")
	if raw == "" {
		t.Skip("explicit task booking Mongo fixture not selected")
	}
	if raw != "mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration" {
		t.Fatal("exact task replica-set URI required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(raw).SetDirect(true).SetServerSelectionTimeout(2*time.Second))
	if err != nil {
		t.Fatal("task Mongo connection failed")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := client.Disconnect(end); err != nil {
			t.Errorf("task client disconnect unconfirmed: %v", err)
		}
	})
	var hello bson.M
	if err = client.Database("admin").RunCommand(ctx, bson.M{"hello": 1}).Decode(&hello); err != nil || hello["setName"] != "luoboIntegration" || hello["isWritablePrimary"] != true {
		t.Fatal("exact task replica set unavailable")
	}
	// Direct mode prevents discovery outside the task endpoint; Ping does not
	// prove node role. The hello isWritablePrimary gate above precedes all writes.
	if client.Ping(ctx, readpref.Primary()) != nil {
		t.Fatal("task Mongo primary unavailable")
	}
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		t.Fatal("task nonce unavailable")
	}
	name := "luobo_booking_test_" + hex.EncodeToString(nonce[:12])
	claim := hex.EncodeToString(nonce[12:])
	names, err := client.ListDatabaseNames(ctx, bson.M{"name": name})
	if err != nil || len(names) != 0 {
		t.Fatal("task database absence unconfirmed; no write authorized")
	}
	db := client.Database(name)
	if err = db.CreateCollection(ctx, "booking_fixture_claim"); err != nil {
		t.Fatal("exclusive task claim failed; no drop authorized")
	}
	if _, err = db.Collection("booking_fixture_claim").InsertOne(ctx, bson.M{"_id": "task-claim", "nonce": claim}); err != nil {
		t.Fatal("task claim ACK unconfirmed; retain database")
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var record bson.M
		if db.Name() != name {
			t.Error("task database binding changed")
			return
		}
		if err := db.Collection("booking_fixture_claim").FindOne(end, bson.M{"_id": "task-claim", "nonce": claim}).Decode(&record); err != nil {
			t.Errorf("task claim unavailable; retain database: %v", err)
			return
		}
		if err := db.Drop(end); err != nil {
			t.Errorf("task database cleanup unconfirmed: %v", err)
			return
		}
		names, err := client.ListDatabaseNames(end, bson.M{"name": name})
		if err != nil || len(names) != 0 {
			t.Errorf("task database removal unconfirmed: count=%d error=%v", len(names), err)
		}
	})
	store, err := NewMongoStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	// DDL is an explicit fixture operator action in this newly claimed DB.
	// Constructor, runtime Run/List and restarted DAO reconstruction never prepare.
	if prepare {
		if err = store.PrepareIndexes(ctx); err != nil {
			t.Fatal("explicit fixture index preparation failed", err)
		}
	}
	return store, db
}

func mongoService(t *testing.T) (*Service, *mongo.Database, Principal, CreateRequest) {
	store, db := bookingMongo(t)
	s, _, p, req := setup()
	s.Store = store
	s.Access = concurrentPolicy{}
	return s, db, p, req
}
func count(t *testing.T, db *mongo.Database, name string, filter any) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	n, err := db.Collection(name).CountDocuments(ctx, filter)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBookingMongoConcurrentIntentAndLostACKRecovery(t *testing.T) {
	s, db, p, req := mongoService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Create(ctx, p, req)
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if count(t, db, BookingsCollection, bson.M{}) != 1 || count(t, db, IntentsCollection, bson.M{}) != 1 {
		t.Fatal("duplicate/partial durable create")
	}
	restarted, err := NewMongoStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	copy := &Service{Store: restarted, Access: concurrentPolicy{}, Now: s.Now}
	receipt, err := copy.Receipt(ctx, p, req.ClientRequestID)
	if err != nil || !receipt.Applied || receipt.Booking == nil {
		t.Fatal("lost ACK not recoverable after DAO reconstruction")
	}
	changed := req
	changed.Ex = "different"
	if _, err := copy.Create(ctx, p, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("same UUID changed payload allowed")
	}
}
func TestBookingMongoResolveAgainstDelayedCreate(t *testing.T) {
	s, db, p, base := mongoService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	for i := 0; i < 8; i++ {
		req := base
		req.ClientRequestID = uuid.NewString()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := s.Resolve(ctx, p, req.ClientRequestID)
			if err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			_, err := s.Create(ctx, p, req)
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
		wg.Wait()
		r, err := s.Receipt(ctx, p, req.ClientRequestID)
		if err != nil || !r.Found {
			t.Fatal("resolve/create outcome missing")
		}
		if r.DefinitiveNotApplied {
			if _, err := s.Create(ctx, p, req); !errors.Is(err, ErrConflict) {
				t.Fatal("resolved UUID resurrected")
			}
		}
	}
	if count(t, db, IntentsCollection, bson.M{}) != 8 {
		t.Fatal("resolve/write intent count")
	}
}
func TestBookingMongoTenantAndOwnerQueriesNeverCross(t *testing.T) {
	s, db, p, req := mongoService(t)
	ctx := context.Background()
	created, err := s.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	for _, foreign := range []Principal{{Tenant: p.Tenant, Owner: "other"}, {Tenant: "tenant-B", Owner: p.Owner}} {
		if _, err := s.Detail(ctx, foreign, created.Booking.BookingID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign booking visible")
		}
		q, err := s.Receipt(ctx, foreign, req.ClientRequestID)
		if err != nil || q.Found {
			t.Fatal("foreign receipt visible")
		}
		if _, err := s.Cancel(ctx, foreign, CancelRequest{ClientRequestID: uuid.NewString(), BookingID: created.Booking.BookingID, ExpectedVersion: 1}); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign cancellation allowed")
		}
		if _, err := s.Create(ctx, foreign, req); err != nil {
			t.Fatal("independent owner+tenant UUID incorrectly conflicts")
		}
	}
	if count(t, db, BookingsCollection, bson.M{}) != 3 {
		t.Fatal("scope uniqueness")
	}
}
func TestBookingMongoTransactionRollbackUsesActualSession(t *testing.T) {
	store, db := bookingMongo(t)
	p := Principal{Tenant: "A", Owner: "rollback-owner"}
	ctx := context.Background()
	_, err := store.Run(ctx, p, func(tx Tx) (*Result, error) {
		err := tx.InsertBooking(ctx, &Booking{ID: uuid.NewString(), Tenant: p.Tenant, Owner: p.Owner, State: "scheduled", Version: 1, Payload: &Payload{MeetingName: "synthetic", StartTime: time.Now().Unix(), MeetingDuration: 3600, InviteeUserIDs: []string{}}, CreatedAt: time.Unix(1000, 0), UpdatedAt: time.Unix(1000, 0)})
		if err != nil {
			return nil, err
		}
		return nil, errors.New("synthetic abort after actual insert")
	})
	if err == nil {
		t.Fatal("callback failure ignored")
	}
	if count(t, db, BookingsCollection, bson.M{}) != 0 {
		t.Fatal("insert escaped transaction session")
	}
	_, err = store.Run(ctx, p, func(tx Tx) (*Result, error) {
		return nil, tx.InsertBooking(ctx, &Booking{ID: uuid.NewString(), Tenant: "foreign", Owner: p.Owner, State: "scheduled", Version: 1, Payload: &Payload{}})
	})
	if !errors.Is(err, ErrDenied) || count(t, db, BookingsCollection, bson.M{}) != 0 {
		t.Fatal("storage scope guard bypass")
	}
}
func TestBookingMongoUpdateCancelCASAndMinimalReceipt(t *testing.T) {
	s, db, p, req := mongoService(t)
	ctx := context.Background()
	created, err := s.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	id := created.Booking.BookingID
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := s.Update(ctx, p, UpdateRequest{ClientRequestID: uuid.NewString(), BookingID: id, ExpectedVersion: 1, Payload: req.Payload})
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		_, err := s.Cancel(ctx, p, CancelRequest{ClientRequestID: uuid.NewString(), BookingID: id, ExpectedVersion: 1})
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Error(err)
		}
	}()
	wg.Wait()
	current, err := s.Detail(ctx, p, id)
	if err != nil || current.Version != 2 {
		t.Fatal("CAS failed")
	}
	if current.State != "cancelled" {
		if _, err := s.Cancel(ctx, p, CancelRequest{ClientRequestID: uuid.NewString(), BookingID: id, ExpectedVersion: 2}); err != nil {
			t.Fatal(err)
		}
	}
	var raw bson.M
	if err := db.Collection(BookingsCollection).FindOne(ctx, bson.M{"_id": id}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, exists := raw["payload"]; exists {
		t.Fatal("cancel retained payload")
	}
	late, err := s.Create(ctx, p, req)
	if err != nil || late.Booking.State != "cancelled" || late.Booking.Payload != nil {
		t.Fatal("late create restored cancelled booking")
	}
}
func TestBookingMongoBookingNeverCreatesRTCOrMediaState(t *testing.T) {
	s, db, p, req := mongoService(t)
	r, err := s.Create(context.Background(), p, req)
	if err != nil || r.Booking.MediaReady {
		t.Fatal("booking failed or authorized media")
	}
	for _, name := range []string{"rtc_rooms", "rtc_active_users", "rtc_outbox", "rtc_meeting_admissions"} {
		if count(t, db, name, bson.M{}) != 0 {
			t.Fatal("booking touched legacy RTC/media collections")
		}
	}
	var row bson.M
	if err := db.Collection(BookingsCollection).FindOne(context.Background(), bson.M{"_id": r.Booking.BookingID}).Decode(&row); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"token", "platform_id", "session", "media_sid", "accepted", "lease"} {
		if _, ok := row[key]; ok {
			t.Fatal("media authority in booking document")
		}
	}
}

func TestBookingMongoMissingIndexesCannotCreateStateUntilExplicitPreparation(t *testing.T) {
	store, db := bookingMongoWithPreparation(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	s, _, p, req := setup()
	s.Store = store
	s.Access = concurrentPolicy{}
	if _, err := s.Create(ctx, p, req); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing indexes did not block write")
	}
	if _, err := s.Receipt(ctx, p, req.ClientRequestID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("receipt skipped readiness")
	}
	if _, err := s.Resolve(ctx, p, req.ClientRequestID); !errors.Is(err, ErrUnavailable) {
		t.Fatal("resolve skipped readiness")
	}
	if _, err := s.List(ctx, p, ListRequest{PageSize: 10}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("list skipped readiness")
	}
	names, err := db.ListCollectionNames(ctx, bson.M{"name": bson.M{"$in": bson.A{BookingsCollection, IntentsCollection}}})
	if err != nil || len(names) != 0 {
		t.Fatal("runtime silently created booking collections")
	}
	if err = store.PrepareIndexes(ctx); err != nil {
		t.Fatal("explicit operator preparation failed", err)
	}
	if _, err = s.Create(ctx, p, req); err != nil {
		t.Fatal("prepared store could not create booking", err)
	}
}
func TestBookingMongoUnsafePartialIndexIsNotRepairedByRuntimeOrOperator(t *testing.T) {
	store, db := bookingMongo(t)
	ctx := context.Background()
	if _, err := db.Collection(IntentsCollection).Indexes().DropOne(ctx, "booking_intent_owner_request_unique"); err != nil {
		t.Fatal(err)
	}
	_, err := db.Collection(IntentsCollection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bookingIndexRequirements[0].key, Options: options.Index().SetName("booking_intent_owner_request_unique").SetUnique(true).SetPartialFilterExpression(bson.M{"owner": "synthetic_subset"})})
	if err != nil {
		t.Fatal("task-only partial index setup failed", err)
	}
	if !errors.Is(store.CheckReady(ctx), ErrUnavailable) || !errors.Is(store.PrepareIndexes(ctx), ErrUnavailable) {
		t.Fatal("unsafe partial index was accepted or implicitly repaired")
	}
}
