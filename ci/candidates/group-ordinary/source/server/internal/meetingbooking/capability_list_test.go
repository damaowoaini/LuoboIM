package meetingbooking

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

type capabilityListStore struct {
	Store
	reads, writes int
	listError error
}
func (s *capabilityListStore) Run(ctx context.Context, p Principal, fn func(Tx) (*Result, error)) (*Result, error) {
	s.writes++
	return s.Store.Run(ctx, p, fn)
}
func (s *capabilityListStore) List(ctx context.Context, p Principal, query ListQuery) ([]Booking, bool, error) {
	s.reads++
	if s.listError != nil { return nil, false, s.listError }
	return s.Store.List(ctx, p, query)
}

func TestBookingCapabilityNormalListDescribesOnlyAuthenticatedOwnerProtocol(t *testing.T) {
	store := &capabilityListStore{Store: memory()}
	service := &Service{Store: store}
	principal := Principal{Tenant: "synthetic-tenant", Owner: "synthetic-owner"}
	page, err := service.List(context.Background(), principal, ListRequest{PageSize: 40})
	if err != nil || page == nil || page.Contract != "luobo-owner-booking" || page.ContractVersion != 1 || page.OwnerUserID != principal.Owner || !page.ReplaySafe || page.MediaReady || page.Bookings == nil || len(page.Bookings) != 0 || page.HasMore {
		t.Fatal("normal owner list did not describe the exact tokenless contract", err)
	}
	if store.reads != 1 || store.writes != 0 { t.Fatal("feature discovery performed a transaction/write") }
	raw, err := json.Marshal(page)
	if err != nil { t.Fatal(err) }
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil { t.Fatal("capability list did not use ordinary JSON") }
	for _, key := range []string{"tenant", "token", "joinToken", "roomID", "certificate", "liveURL"} {
		if _, found := fields[key]; found { t.Fatal("descriptive feature metadata became private authority or media material") }
	}
}

func TestBookingCapabilityFailedListCannotProduceReadinessMetadata(t *testing.T) {
	store := &capabilityListStore{Store: memory(), listError: ErrUnavailable}
	service := &Service{Store: store}
	page, err := service.List(context.Background(), Principal{Tenant: "tenant", Owner: "owner"}, ListRequest{PageSize: 40})
	if !errors.Is(err, ErrUnavailable) || page != nil || store.writes != 0 || store.reads != 1 {
		t.Fatal("failed readiness/list was presented as supported empty success", err)
	}
}

func TestBookingCapabilityMongoMissingIndexesCannotAdvertiseOrRepairReadiness(t *testing.T) {
	store, db := bookingMongoWithPreparation(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	before, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil { t.Fatal(err) }
	service := &Service{Store: store}
	principal := Principal{Tenant: "synthetic-tenant", Owner: "synthetic-owner"}
	page, err := service.List(ctx, principal, ListRequest{PageSize: 40})
	if !errors.Is(err, ErrUnavailable) || page != nil { t.Fatal("unready Mongo emitted protocol readiness", err) }
	after, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil || !reflect.DeepEqual(before, after) { t.Fatal("normal capability read attempted schema preparation", err) }
	// Preparation is explicit and confined to the original nonce-owned fixture.
	if err := store.PrepareIndexes(ctx); err != nil { t.Fatal(err) }
	page, err = service.List(ctx, principal, ListRequest{PageSize: 40})
	if err != nil || page == nil || page.ContractVersion != 1 || page.OwnerUserID != principal.Owner || page.MediaReady || !page.ReplaySafe { t.Fatal("prepared Mongo could not describe the normal booking contract", err) }
	for _, collection := range []string{BookingsCollection, IntentsCollection} {
		count, err := db.Collection(collection).CountDocuments(ctx, bson.M{})
		if err != nil || count != 0 { t.Fatal("capability read created bookings or operation receipts", err) }
	}
}
