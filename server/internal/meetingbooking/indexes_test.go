package meetingbooking

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/description"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/x/mongo/driver"
)

func rawIndex(t *testing.T, doc bson.D) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func readyIndexRows(t *testing.T, r indexRequirement) []bson.Raw {
	doc := bson.D{{Key: "name", Value: r.name}, {Key: "key", Value: r.key}}
	if r.unique {
		doc = append(doc, bson.E{Key: "unique", Value: true})
	}
	return []bson.Raw{rawIndex(t, bson.D{{Key: "name", Value: "_id_"}, {Key: "key", Value: bson.D{{Key: "_id", Value: 1}}}}), rawIndex(t, doc)}
}
func TestBookingIndexSemanticReadinessIsExactAndIDUniqueMayBeOmitted(t *testing.T) {
	for _, name := range []string{"ready", "id_unique_true", "simple_explicit", "missing_id", "missing_required", "unique_missing", "wrong_order", "wrong_direction", "sparse", "partial_even_empty", "hidden", "ttl_required", "ttl_extra", "non_simple_collation", "building", "prepare_unique", "malformed_unique", "duplicate_name", "id_false_unique", "extra_unique", "extra_prepare_unique_absent", "extra_prepare_unique_false", "nonunique_extra_allowed"} {
		t.Run(name, func(t *testing.T) {
			r := bookingIndexRequirements[0]
			rows := readyIndexRows(t, r)
			var required, id bson.D
			if bson.Unmarshal(rows[1], &required) != nil || bson.Unmarshal(rows[0], &id) != nil {
				t.Fatal("fixture decode")
			}
			allowed := false
			switch name {
			case "ready":
				allowed = true
			case "id_unique_true":
				id = append(id, bson.E{Key: "unique", Value: true})
				allowed = true
			case "simple_explicit":
				required = append(required, bson.E{Key: "collation", Value: bson.D{{Key: "locale", Value: "simple"}}})
				allowed = true
			case "missing_id":
				rows = rows[1:]
			case "missing_required":
				rows = rows[:1]
			case "unique_missing":
				required = required[:2]
			case "wrong_order":
				required[1].Value = bson.D{{Key: "owner", Value: 1}, {Key: "tenant", Value: 1}, {Key: "request_id", Value: 1}}
			case "wrong_direction":
				required[1].Value = bson.D{{Key: "tenant", Value: 1}, {Key: "owner", Value: 1}, {Key: "request_id", Value: -1}}
			case "sparse":
				required = append(required, bson.E{Key: "sparse", Value: true})
			case "partial_even_empty":
				required = append(required, bson.E{Key: "partialFilterExpression", Value: bson.D{}})
			case "hidden":
				required = append(required, bson.E{Key: "hidden", Value: true})
			case "ttl_required":
				required = append(required, bson.E{Key: "expireAfterSeconds", Value: int64(30)})
			case "ttl_extra":
				rows = append(rows, rawIndex(t, bson.D{{Key: "name", Value: "unexpected_ttl"}, {Key: "key", Value: bson.D{{Key: "created_at", Value: 1}}}, {Key: "expireAfterSeconds", Value: 0}}))
			case "non_simple_collation":
				required = append(required, bson.E{Key: "collation", Value: bson.D{{Key: "locale", Value: "en"}}})
			case "building":
				required = append(required, bson.E{Key: "buildUUID", Value: "synthetic_build"})
			case "prepare_unique":
				required = append(required, bson.E{Key: "prepareUnique", Value: true})
			case "malformed_unique":
				required[2].Value = "true"
			case "duplicate_name":
				rows = append(rows, rows[1])
			case "id_false_unique":
				id = append(id, bson.E{Key: "unique", Value: false})
			case "extra_unique":
				rows = append(rows, rawIndex(t, bson.D{{Key: "name", Value: "unexpected_unique"}, {Key: "key", Value: bson.D{{Key: "owner", Value: 1}}}, {Key: "unique", Value: true}}))
			case "extra_prepare_unique_absent", "extra_prepare_unique_false":
				extra := bson.D{{Key: "name", Value: "unexpected_preparing_unique"}, {Key: "key", Value: bson.D{{Key: "owner", Value: 1}}}, {Key: "prepareUnique", Value: true}}
				if name == "extra_prepare_unique_false" {
					extra = append(extra, bson.E{Key: "unique", Value: false})
				}
				rows = append(rows, rawIndex(t, extra))
			case "nonunique_extra_allowed":
				rows = append(rows, rawIndex(t, bson.D{{Key: "name", Value: "additional_read_index"}, {Key: "key", Value: bson.D{{Key: "created_at", Value: 1}}}}))
				allowed = true
			}
			if name != "missing_id" && name != "missing_required" {
				rows[0] = rawIndex(t, id)
				rows[1] = rawIndex(t, required)
			}
			if err := verifyIndexRows(rows, r, false); (err == nil) != allowed {
				t.Fatalf("ready=%v want=%v", err == nil, allowed)
			}
		})
	}
	// The ordinary paging index must NOT silently become unique or change sort.
	paging := bookingIndexRequirements[1]
	rows := readyIndexRows(t, paging)
	if err := verifyIndexRows(rows, paging, false); err != nil {
		t.Fatal("valid nonunique paging index rejected")
	}
	var doc bson.D
	_ = bson.Unmarshal(rows[1], &doc)
	doc = append(doc, bson.E{Key: "unique", Value: true})
	rows[1] = rawIndex(t, doc)
	if err := verifyIndexRows(rows, paging, false); err == nil {
		t.Fatal("unexpected unique paging constraint accepted")
	}
}

type indexCatalogFixture struct {
	rows           map[string][]bson.Raw
	listError      error
	createError    error
	lists, creates int
	onCreate       func(indexRequirement)
}

func (f *indexCatalogFixture) List(_ context.Context, name string) ([]bson.Raw, error) {
	f.lists++
	if f.listError != nil {
		return nil, f.listError
	}
	r, ok := f.rows[name]
	if !ok {
		return nil, mongo.CommandError{Code: 26, Message: "synthetic namespace absent"}
	}
	return r, nil
}
func (f *indexCatalogFixture) Create(_ context.Context, r indexRequirement) error {
	f.creates++
	if f.createError != nil {
		return f.createError
	}
	if f.onCreate != nil {
		f.onCreate(r)
	}
	return nil
}

func readyCatalog(t *testing.T) *indexCatalogFixture {
	f := &indexCatalogFixture{rows: map[string][]bson.Raw{}}
	for _, r := range bookingIndexRequirements {
		f.rows[r.collection] = readyIndexRows(t, r)
	}
	return f
}
func TestBookingConstructorMakesZeroDatabaseCallsEvenWithCancelledContext(t *testing.T) {
	commands := 0
	client, err := mongo.NewClient(options.Client().SetMonitor(&event.CommandMonitor{Started: func(context.Context, *event.CommandStartedEvent) { commands++ }}))
	if err != nil {
		t.Fatal(err)
	} // NewClient is local only; deliberately never Connect.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store, err := NewMongoStore(ctx, client.Database("synthetic_not_connected"))
	if err != nil || store == nil || commands != 0 {
		t.Fatal("constructor attempted DB work")
	}
	if _, err = NewMongoStore(ctx, nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal("nil store did not fail safely")
	}
}

// Catalog command-boundary simulation only: no restricted Mongo user is
// created or authenticated here. Real least-privilege IAM remains unverified.
func TestBookingReadinessNeedsOnlyIndexReadsNotCreatePrivilege(t *testing.T) {
	for _, required := range bookingIndexRequirements {
		for _, mode := range []string{"unique_absent", "unique_false"} {
			t.Run(required.collection+"_extra_prepare_"+mode, func(t *testing.T) {
				catalog := readyCatalog(t)
				extra := bson.D{{Key: "name", Value: "unexpected_preparing_unique"}, {Key: "key", Value: bson.D{{Key: "owner", Value: 1}}}, {Key: "prepareUnique", Value: true}}
				if mode == "unique_false" {
					extra = append(extra, bson.E{Key: "unique", Value: false})
				}
				catalog.rows[required.collection] = append(catalog.rows[required.collection], rawIndex(t, extra))
				store := &MongoStore{indexes: catalog}
				if !errors.Is(store.CheckReady(context.Background()), ErrUnavailable) || catalog.creates != 0 {
					t.Fatal("runtime accepted preparing uniqueness or performed DDL")
				}
				if !errors.Is(store.PrepareIndexes(context.Background()), ErrUnavailable) || catalog.creates != 0 {
					t.Fatal("operator repaired unknown preparing uniqueness")
				}
			})
		}
	}
	t.Run("ordinary_extra_read_index_stays_available", func(t *testing.T) {
		catalog := readyCatalog(t)
		catalog.rows[IntentsCollection] = append(catalog.rows[IntentsCollection], rawIndex(t, bson.D{{Key: "name", Value: "additional_read_index"}, {Key: "key", Value: bson.D{{Key: "created_at", Value: 1}}}}))
		if err := (&MongoStore{indexes: catalog}).CheckReady(context.Background()); err != nil || catalog.creates != 0 {
			t.Fatal("ordinary additional read index blocked or DDL attempted")
		}
	})
	catalog := readyCatalog(t)
	catalog.createError = mongo.CommandError{Code: 13, Message: "synthetic createIndexes denied"}
	store := &MongoStore{indexes: catalog}
	if err := store.CheckReady(context.Background()); err != nil || catalog.lists != 2 || catalog.creates != 0 {
		t.Fatal("runtime requires DDL permission")
	}
	catalog.listError = mongo.CommandError{Code: 13, Message: "synthetic listIndexes denied"}
	if err := store.CheckReady(context.Background()); !errors.Is(err, ErrUnavailable) || catalog.creates != 0 {
		t.Fatal("runtime tried repair or accepted unreadable schema")
	}
}
func TestBookingEveryStorageEntryRejectsUnreadableIndexesBeforeCallbackOrDB(t *testing.T) {
	catalog := &indexCatalogFixture{listError: mongo.CommandError{Code: 13, Message: "synthetic listIndexes denied"}}
	s, _, p, request := setup()
	s.Store = &MongoStore{indexes: catalog}
	ctx := context.Background()
	id := uuid.NewString()
	for _, name := range []string{"create", "update", "cancel", "receipt", "resolve", "detail", "list"} {
		t.Run(name, func(t *testing.T) {
			var err error
			switch name {
			case "create":
				_, err = s.Create(ctx, p, request)
			case "update":
				_, err = s.Update(ctx, p, UpdateRequest{ClientRequestID: request.ClientRequestID, BookingID: id, ExpectedVersion: 1, Payload: request.Payload})
			case "cancel":
				_, err = s.Cancel(ctx, p, CancelRequest{ClientRequestID: request.ClientRequestID, BookingID: id, ExpectedVersion: 1})
			case "receipt":
				_, err = s.Receipt(ctx, p, request.ClientRequestID)
			case "resolve":
				_, err = s.Resolve(ctx, p, request.ClientRequestID)
			case "detail":
				_, err = s.Detail(ctx, p, id)
			case "list":
				_, err = s.List(ctx, p, ListRequest{PageSize: 10})
			}
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("readiness skipped: %T", err)
			}
		})
	}
	if catalog.creates != 0 || catalog.lists != 7 || s.Access.(*allowPolicy).calls != 0 {
		t.Fatalf("unexpected reads=%d creates=%d accessCalls=%d", catalog.lists, catalog.creates, s.Access.(*allowPolicy).calls)
	}
}
func TestBookingOperatorPreflightCannotRepairUnknownSchemaOrReadDenial(t *testing.T) {
	for _, name := range []string{"list_denied", "unsafe_ttl", "create_denied"} {
		t.Run(name, func(t *testing.T) {
			c := readyCatalog(t)
			switch name {
			case "list_denied":
				c.listError = mongo.CommandError{Code: 13}
			case "unsafe_ttl":
				c.rows[IntentsCollection] = append(c.rows[IntentsCollection], rawIndex(t, bson.D{{Key: "name", Value: "surprise_ttl"}, {Key: "key", Value: bson.D{{Key: "created_at", Value: 1}}}, {Key: "expireAfterSeconds", Value: 0}}))
			case "create_denied":
				c.createError = mongo.CommandError{Code: 13}
			}
			err := (&MongoStore{indexes: c}).PrepareIndexes(context.Background())
			if !errors.Is(err, ErrUnavailable) {
				t.Fatal("operator preparation failure ACKed")
			}
			if name != "create_denied" && c.creates != 0 {
				t.Fatal("operator changed schema before preflight completed")
			}
		})
	}
}

func TestBookingOperatorPreparationIsExplicitBeforeReadyRuntimeReads(t *testing.T) {
	for _, initial := range []string{"namespace_absent", "only_intrinsic_id"} {
		t.Run(initial, func(t *testing.T) {
			catalog := &indexCatalogFixture{rows: map[string][]bson.Raw{}}
			if initial == "only_intrinsic_id" {
				for _, r := range bookingIndexRequirements {
					catalog.rows[r.collection] = readyIndexRows(t, r)[:1]
				}
			}
			catalog.onCreate = func(r indexRequirement) { catalog.rows[r.collection] = readyIndexRows(t, r) }
			store := &MongoStore{indexes: catalog}
			if !errors.Is(store.CheckReady(context.Background()), ErrUnavailable) || catalog.creates != 0 {
				t.Fatal("runtime repaired absent schema")
			}
			if err := store.PrepareIndexes(context.Background()); err != nil || catalog.creates != 2 {
				t.Fatal("explicit operator could not prepare exact indexes")
			}
			before := catalog.creates
			if err := store.CheckReady(context.Background()); err != nil || catalog.creates != before {
				t.Fatal("ready runtime attempted DDL")
			}
		})
	}
}

// Only the public Deployment interface is embedded: the original mtest client
// owns its Mock lifecycle. This wrapper never forwards Connect/Disconnect or
// changes global mtest.MockDescription.
type bookingReplicaSetMock struct {
	driver.Deployment
	enforcePrimary bool
}

var _ driver.Deployment = (*bookingReplicaSetMock)(nil)

func (*bookingReplicaSetMock) Kind() description.TopologyKind {
	return description.ReplicaSetWithPrimary
}
func (d *bookingReplicaSetMock) SelectServer(ctx context.Context, selector description.ServerSelector) (driver.Server, error) {
	if d.enforcePrimary {
		topology := description.Topology{Kind: description.ReplicaSetWithPrimary}
		secondary := description.Server{Kind: description.RSSecondary}
		selected, err := selector.SelectServer(topology, []description.Server{secondary})
		if err != nil || len(selected) != 0 {
			return nil, errors.New("fixture selector allowed secondary-only topology")
		}
		selected, err = selector.SelectServer(topology, []description.Server{secondary, {Kind: description.RSPrimary}})
		if err != nil || len(selected) != 1 || selected[0].Kind != description.RSPrimary {
			return nil, errors.New("fixture selector did not require primary")
		}
	}
	return d.Deployment.SelectServer(ctx, selector)
}

// These cases exercise the actual cached driver against its in-process Mock
// deployment. No Mongo endpoint, IAM identity, socket or external server is used.
func TestBookingRawListIndexesDriverBoundaryPreservesAbsenceAndNeverRepairsRuntime(t *testing.T) {
	mockOptions := options.Client().SetRetryReads(false)
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock).CreateCollection(false).DatabaseName("luobo_booking_command_mock").ClientOptions(mockOptions))
	useReplicaSet := func(mt *mtest.T) func() {
		if mockOptions.Deployment == nil || mockOptions.Deployment.Kind() != description.Single {
			mt.Fatal("expected original in-process Single Mock deployment")
		}
		deployment := &bookingReplicaSetMock{Deployment: mockOptions.Deployment, enforcePrimary: true}
		// A secondary default is deliberately adversarial. Production raw
		// RunCmd(primary) must override it and pass the strict selector above.
		options := options.Client().SetRetryReads(false).SetReadPreference(readpref.Secondary()).SetMonitor(mockOptions.Monitor)
		options.Deployment = deployment
		client, err := mongo.Connect(context.Background(), options)
		if err != nil || deployment.Kind() != description.ReplicaSetWithPrimary {
			mt.Fatal("local ReplicaSet Mock client initialization failed")
		}
		mt.DB = client.Database(mt.DB.Name())
		return func() {
			// endSessions uses primaryPreferred internally; the command under
			// test is already verified. Leave Mock lifecycle to mtest's client.
			deployment.enforcePrimary = false
			if client.NumberSessionsInProgress() != 0 {
				mt.Error("local Mock client leaked active sessions")
			}
			mt.AddMockResponses(mtest.CreateSuccessResponse(), mtest.CreateSuccessResponse())
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := client.Disconnect(ctx); err != nil {
				mt.Errorf("local Mock client cleanup unconfirmed: %v", err)
			}
		}
	}
	namespaceMissing := func() bson.D {
		return mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 26, Name: "NamespaceNotFound", Message: "synthetic namespace absent"})
	}
	ready := func(mt *mtest.T, required indexRequirement) bson.D {
		var docs []bson.D
		for _, raw := range readyIndexRows(mt.T, required) {
			var doc bson.D
			if err := bson.Unmarshal(raw, &doc); err != nil {
				mt.Fatal(err)
			}
			docs = append(docs, doc)
		}
		return mtest.CreateCursorResponse(0, mt.DB.Name()+"."+required.collection, mtest.FirstBatch, docs...)
	}
	checkCommands := func(mt *mtest.T, names []string, collections []string) {
		events := mt.GetAllStartedEvents()
		if len(events) != len(names) {
			mt.Fatalf("command count=%d expected=%d", len(events), len(names))
		}
		for i, event := range events {
			name, ok := event.Command.Lookup(names[i]).StringValueOK()
			if event.CommandName != names[i] || event.DatabaseName != mt.DB.Name() || !ok || name != collections[i] {
				mt.Fatal("unexpected command boundary or namespace")
			}
			// OP_MSG omits primary preference on a ReplicaSet; Single would
			// normalize it to primaryPreferred. The selector is checked too.
			if _, err := event.Command.LookupErr("$readPreference"); err == nil {
				mt.Fatal("ReplicaSet primary command unexpectedly serialized a preference")
			}
		}
	}
	mt.Run("driver_index_view_suppresses_namespace_error_but_raw_catalog_preserves_it", func(mt *mtest.T) {
		originalDB := mt.DB
		defer useReplicaSet(mt)()
		ctx := context.Background()
		mt.AddMockResponses(namespaceMissing())
		cursor, err := originalDB.Collection(IntentsCollection).Indexes().List(ctx)
		if err != nil || cursor == nil || cursor.Next(ctx) || cursor.Err() != nil {
			mt.Fatal("cached driver boundary changed; reevaluate absence handling")
		}
		if err := cursor.Close(ctx); err != nil {
			mt.Fatal("empty driver cursor cleanup unconfirmed")
		}
		mt.ClearEvents()
		mt.AddMockResponses(namespaceMissing())
		_, err = (mongoIndexCatalog{db: mt.DB}).List(ctx, IntentsCollection)
		var server mongo.ServerError
		if !errors.As(err, &server) || !server.HasErrorCode(26) {
			mt.Fatal("raw catalog lost authoritative NamespaceNotFound")
		}
		checkCommands(mt, []string{"listIndexes"}, []string{IntentsCollection})
	})
	mt.Run("runtime_missing_namespace_is_read_only", func(mt *mtest.T) {
		defer useReplicaSet(mt)()
		mt.AddMockResponses(namespaceMissing())
		store, err := NewMongoStore(context.Background(), mt.DB)
		if err != nil || len(mt.GetAllStartedEvents()) != 0 {
			mt.Fatal("constructor issued command")
		}
		if !errors.Is(store.CheckReady(context.Background()), ErrUnavailable) {
			mt.Fatal("runtime accepted missing namespace")
		}
		checkCommands(mt, []string{"listIndexes"}, []string{IntentsCollection})
	})
	mt.Run("explicit_operator_preserves_namespace_error_and_prepares_then_reads", func(mt *mtest.T) {
		defer useReplicaSet(mt)()
		mt.AddMockResponses(namespaceMissing(), namespaceMissing(), mtest.CreateSuccessResponse(), mtest.CreateSuccessResponse(), ready(mt, bookingIndexRequirements[0]), ready(mt, bookingIndexRequirements[1]))
		store, err := NewMongoStore(context.Background(), mt.DB)
		if err != nil || store.PrepareIndexes(context.Background()) != nil {
			mt.Fatal("explicit operator could not prepare missing namespaces")
		}
		checkCommands(mt, []string{"listIndexes", "listIndexes", "createIndexes", "createIndexes", "listIndexes", "listIndexes"}, []string{IntentsCollection, BookingsCollection, IntentsCollection, BookingsCollection, IntentsCollection, BookingsCollection})
		for _, event := range mt.GetAllStartedEvents() {
			if event.CommandName == "createIndexes" {
				wc := event.Command.Lookup("writeConcern").Document()
				majority, ok := wc.Lookup("w").StringValueOK()
				journal, journalOK := wc.Lookup("j").BooleanOK()
				if !ok || majority != "majority" || !journalOK || !journal {
					mt.Fatal("operator index creation lost durable ACK")
				}
			}
		}
	})
	mt.Run("successful_empty_catalog_never_means_absent_or_allows_DDL", func(mt *mtest.T) {
		defer useReplicaSet(mt)()
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+IntentsCollection, mtest.FirstBatch), mtest.CreateCursorResponse(0, mt.DB.Name()+"."+IntentsCollection, mtest.FirstBatch))
		store, err := NewMongoStore(context.Background(), mt.DB)
		if err != nil || !errors.Is(store.CheckReady(context.Background()), ErrUnavailable) || !errors.Is(store.PrepareIndexes(context.Background()), ErrUnavailable) {
			mt.Fatal("empty successful catalog mistaken for authoritative absence")
		}
		checkCommands(mt, []string{"listIndexes", "listIndexes"}, []string{IntentsCollection, IntentsCollection})
	})
	mt.Run("actual_driver_permission_error_never_repairs", func(mt *mtest.T) {
		defer useReplicaSet(mt)()
		denied := mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 13, Name: "Unauthorized", Message: "synthetic listIndexes denied"})
		mt.AddMockResponses(denied, denied)
		store, err := NewMongoStore(context.Background(), mt.DB)
		if err != nil || !errors.Is(store.CheckReady(context.Background()), ErrUnavailable) || !errors.Is(store.PrepareIndexes(context.Background()), ErrUnavailable) {
			mt.Fatal("unreadable catalog accepted or repaired")
		}
		checkCommands(mt, []string{"listIndexes", "listIndexes"}, []string{IntentsCollection, IntentsCollection})
	})
}
