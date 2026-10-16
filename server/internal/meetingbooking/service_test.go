package meetingbooking

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

type memoryStore struct {
	sync.Mutex
	bookings   map[string]*Booking
	intents    map[string]*Intent
	failIntent bool
}
type memoryTx struct {
	scope      Principal
	bookings   map[string]*Booking
	intents    map[string]*Intent
	failIntent bool
}

func key(p Principal, id string) string { return p.Tenant + "\x00" + p.Owner + "\x00" + id }
func clone[T any](v T) T {
	data, _ := json.Marshal(v)
	var out T
	_ = json.Unmarshal(data, &out)
	return out
}
func memory() *memoryStore {
	return &memoryStore{bookings: map[string]*Booking{}, intents: map[string]*Intent{}}
}
func (m *memoryStore) Run(ctx context.Context, p Principal, fn func(Tx) (*Result, error)) (*Result, error) {
	m.Lock()
	defer m.Unlock()
	tx := &memoryTx{scope: p, bookings: clone(m.bookings), intents: clone(m.intents), failIntent: m.failIntent}
	r, err := fn(tx)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		m.bookings = tx.bookings
		m.intents = tx.intents
	}
	return r, err
}
func (t *memoryTx) GetIntent(_ context.Context, id string) (*Intent, error) {
	v := t.intents[key(t.scope, id)]
	if v == nil {
		return nil, ErrNotFound
	}
	return clone(v), nil
}
func (t *memoryTx) InsertIntent(_ context.Context, v *Intent) error {
	if t.failIntent {
		return errors.New("synthetic commit failure")
	}
	k := key(t.scope, v.RequestID)
	if t.intents[k] != nil {
		return ErrConflict
	}
	t.intents[k] = clone(v)
	return nil
}
func (t *memoryTx) GetBooking(_ context.Context, id string) (*Booking, error) {
	v := t.bookings[key(t.scope, id)]
	if v == nil {
		return nil, ErrNotFound
	}
	return clone(v), nil
}
func (t *memoryTx) InsertBooking(_ context.Context, v *Booking) error {
	t.bookings[key(t.scope, v.ID)] = clone(v)
	return nil
}
func (t *memoryTx) ReplaceBooking(_ context.Context, v *Booking, version int64) error {
	k := key(t.scope, v.ID)
	if t.bookings[k] == nil || t.bookings[k].Version != version {
		return ErrConflict
	}
	t.bookings[k] = clone(v)
	return nil
}
func (m *memoryStore) List(_ context.Context, p Principal, q ListQuery) ([]Booking, bool, error) {
	m.Lock()
	defer m.Unlock()
	rows := []Booking{}
	for _, v := range m.bookings {
		if v.Tenant != p.Tenant || v.Owner != p.Owner || (!q.IncludeCancelled && v.State != "scheduled") {
			continue
		}
		if q.BeforeID != "" && (v.CreatedAt.After(q.BeforeTime) || (v.CreatedAt.Equal(q.BeforeTime) && v.ID >= q.BeforeID)) {
			continue
		}
		rows = append(rows, *clone(v))
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	more := len(rows) > q.Limit
	if more {
		rows = rows[:q.Limit]
	}
	return rows, more, nil
}

type allowPolicy struct {
	calls int
	err   error
}

func (a *allowPolicy) Check(context.Context, Principal, []string) error { a.calls++; return a.err }
func setup() (*Service, *memoryStore, Principal, CreateRequest) {
	m := memory()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	s := &Service{Store: m, Access: &allowPolicy{}, Now: func() time.Time { return now }}
	return s, m, Principal{Tenant: "tenant-A", Owner: "booking-owner"}, CreateRequest{ClientRequestID: uuid.NewString(), Payload: Payload{MeetingName: "  synthetic booking  ", StartTime: now.Add(time.Hour).Unix(), MeetingDuration: 3600, InviteeUserIDs: []string{"peer-b", "peer-a"}, Ex: "opaque-not-a-mode"}}
}

func TestBookingCanonicalIdempotencyAndNoMediaAuthority(t *testing.T) {
	s, m, p, req := setup()
	ctx := context.Background()
	first, err := s.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	req.InviteeUserIDs = []string{"peer-a", "peer-b"}
	second, err := s.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Booking.BookingID != second.Booking.BookingID || len(m.bookings) != 1 || len(m.intents) != 1 {
		t.Fatal("duplicate booking intent")
	}
	data, _ := json.Marshal(second)
	for _, forbidden := range []string{`"token"`, `"roomID"`, `"accepted"`, `"platformID"`, `"session"`, `"lease"`} {
		if strings.Contains(string(data), forbidden) {
			t.Fatal("booking leaked media authority field")
		}
	}
	if second.Booking.MediaReady || second.Booking.State != "scheduled" || second.Booking.HostUserID != p.Owner {
		t.Fatal("unexpected booking authority")
	}
	req.MeetingName = "changed"
	if _, err := s.Create(ctx, p, req); !errors.Is(err, ErrConflict) {
		t.Fatal("same intent changed payload accepted")
	}
}
func TestBookingConcurrentSameIntentCommitsOnce(t *testing.T) {
	s, m, p, req := setup()
	s.Access = concurrentPolicy{}
	var wg sync.WaitGroup
	ids := make(chan string, 16)
	fail := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s.Create(context.Background(), p, req)
			if err != nil {
				fail <- err
			} else {
				ids <- r.Booking.BookingID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(fail)
	for err := range fail {
		t.Fatal(err)
	}
	one := ""
	for id := range ids {
		if one != "" && one != id {
			t.Fatal("multiple IDs")
		}
		one = id
	}
	if len(m.bookings) != 1 || len(m.intents) != 1 {
		t.Fatal("concurrent duplicates")
	}
}

type concurrentPolicy struct{}

func (concurrentPolicy) Check(context.Context, Principal, []string) error { return nil }
func TestBookingResolveVersusLateCreateCannotResurrect(t *testing.T) {
	for i := 0; i < 20; i++ {
		s, m, p, req := setup()
		s.Access = concurrentPolicy{}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, err := s.Resolve(context.Background(), p, req.ClientRequestID)
			if err != nil {
				t.Error(err)
			}
		}()
		go func() {
			defer wg.Done()
			_, err := s.Create(context.Background(), p, req)
			if err != nil && !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		}()
		wg.Wait()
		r, err := s.Receipt(context.Background(), p, req.ClientRequestID)
		if err != nil || !r.Found {
			t.Fatal("missing durable outcome")
		}
		if r.DefinitiveNotApplied {
			if len(m.bookings) != 0 {
				t.Fatal("resolved tombstone has booking")
			}
			if _, err := s.Create(context.Background(), p, req); !errors.Is(err, ErrConflict) {
				t.Fatal("late create resurrected")
			}
		} else if len(m.bookings) != 1 {
			t.Fatal("committed create lost")
		}
	}
}
func TestBookingCancelClearsPayloadAndLateReplayReturnsCurrentTombstone(t *testing.T) {
	s, m, p, req := setup()
	ctx := context.Background()
	created, err := s.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	request := CancelRequest{ClientRequestID: uuid.NewString(), BookingID: created.Booking.BookingID, ExpectedVersion: 1}
	cancelled, err := s.Cancel(ctx, p, request)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Booking.Payload != nil || cancelled.Booking.State != "cancelled" || cancelled.Booking.Version != 2 {
		t.Fatal("not minimal cancelled receipt")
	}
	late, err := s.Create(ctx, p, req)
	if err != nil || late.Booking.State != "cancelled" || late.Booking.Payload != nil {
		t.Fatal("late original create replay restored schedule")
	}
	if _, err := s.Update(ctx, p, UpdateRequest{ClientRequestID: uuid.NewString(), BookingID: request.BookingID, ExpectedVersion: 1, Payload: req.Payload}); !errors.Is(err, ErrConflict) {
		t.Fatal("late update resurrected cancel")
	}
	if _, err := s.Cancel(ctx, p, request); err != nil {
		t.Fatal("cancel same-intent retry failed")
	}
	raw, _ := json.Marshal(m.intents)
	if strings.Contains(string(raw), req.Ex) || strings.Contains(string(raw), "synthetic booking") {
		t.Fatal("private payload retained in intent")
	}
}
func TestBookingConcurrentUpdateAndCancelUseCAS(t *testing.T) {
	s, _, p, req := setup()
	s.Access = concurrentPolicy{}
	ctx := context.Background()
	created, err := s.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, err := s.Update(ctx, p, UpdateRequest{ClientRequestID: uuid.NewString(), BookingID: created.Booking.BookingID, ExpectedVersion: 1, Payload: req.Payload})
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		_, err := s.Cancel(ctx, p, CancelRequest{ClientRequestID: uuid.NewString(), BookingID: created.Booking.BookingID, ExpectedVersion: 1})
		if err != nil && !errors.Is(err, ErrConflict) {
			t.Error(err)
		}
	}()
	wg.Wait()
	final, err := s.Detail(ctx, p, created.Booking.BookingID)
	if err != nil || final.Version != 2 {
		t.Fatal("CAS applied twice or lost update")
	}
}
func TestBookingOwnerTenantBoundaryAndNoForeignInviteOracle(t *testing.T) {
	s, _, p, req := setup()
	ctx := context.Background()
	r, err := s.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	calls := s.Access.(*allowPolicy).calls
	for _, foreign := range []Principal{{Tenant: p.Tenant, Owner: "other"}, {Tenant: "tenant-B", Owner: p.Owner}} {
		if _, err := s.Detail(ctx, foreign, r.Booking.BookingID); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign detail visible")
		}
		q, err := s.Receipt(ctx, foreign, req.ClientRequestID)
		if err != nil || q.Found {
			t.Fatal("foreign receipt visible")
		}
		if _, err := s.Update(ctx, foreign, UpdateRequest{ClientRequestID: uuid.NewString(), BookingID: r.Booking.BookingID, ExpectedVersion: 1, Payload: req.Payload}); !errors.Is(err, ErrNotFound) {
			t.Fatal("foreign update allowed")
		}
	}
	if s.Access.(*allowPolicy).calls != calls {
		t.Fatal("foreign target triggered invite lookups")
	}
}
func TestBookingUnknownACKAndProcessReconstructionResolveOriginalIntent(t *testing.T) {
	s, m, p, req := setup()
	if _, err := s.Create(context.Background(), p, req); err != nil {
		t.Fatal(err)
	} // deliberately discard response
	restarted := &Service{Store: m, Access: &allowPolicy{err: ErrDenied}, Now: func() time.Time { return s.now().AddDate(2, 0, 0) }}
	r, err := restarted.Create(context.Background(), p, req)
	if err != nil || r.Booking == nil {
		t.Fatal("original receipt retry depends on now/changed invite policy")
	}
	resolved, err := restarted.Resolve(context.Background(), p, req.ClientRequestID)
	if err != nil || !resolved.Applied || resolved.Booking.BookingID != r.Booking.BookingID {
		t.Fatal("resolve implicitly removed existing booking")
	}
}
func TestBookingUpdateIntentReplayAfterCancelNeverRestoresPayload(t *testing.T) {
	s, _, p, req := setup()
	ctx := context.Background()
	first, err := s.Create(ctx, p, req)
	if err != nil {
		t.Fatal(err)
	}
	request := UpdateRequest{ClientRequestID: uuid.NewString(), BookingID: first.Booking.BookingID, ExpectedVersion: 1, Payload: req.Payload}
	request.MeetingName = "renamed booking"
	updated, err := s.Update(ctx, p, request)
	if err != nil || updated.Booking.Version != 2 || updated.Booking.Payload.MeetingName != "renamed booking" {
		t.Fatal("update did not persist actual fields")
	}
	if _, err := s.Cancel(ctx, p, CancelRequest{ClientRequestID: uuid.NewString(), BookingID: first.Booking.BookingID, ExpectedVersion: 2}); err != nil {
		t.Fatal(err)
	}
	late, err := s.Update(ctx, p, request)
	if err != nil || late.Booking.State != "cancelled" || late.Booking.Payload != nil || late.AppliedVersion != 2 {
		t.Fatal("late acknowledged update resurrected cancelled payload")
	}
	request.MeetingName = "different retry"
	if _, err := s.Update(ctx, p, request); !errors.Is(err, ErrConflict) {
		t.Fatal("changed mutation payload reused same UUID")
	}
}
func TestBookingWriteFailureAndCancelledContextRollBackBothRecords(t *testing.T) {
	s, m, p, req := setup()
	m.failIntent = true
	if _, err := s.Create(context.Background(), p, req); err == nil {
		t.Fatal("write failure ignored")
	}
	if len(m.bookings) != 0 || len(m.intents) != 0 {
		t.Fatal("partial transaction")
	}
	m.failIntent = false
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Create(ctx, p, req); err == nil {
		t.Fatal("cancelled request committed")
	}
	if len(m.bookings) != 0 {
		t.Fatal("cancelled request left booking")
	}
}
func TestBookingListUsesOwnerScopeAndStableCursor(t *testing.T) {
	s, _, p, req := setup()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		req.ClientRequestID = uuid.NewString()
		if _, err := s.Create(ctx, p, req); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	cursor := ""
	for {
		r, err := s.List(ctx, p, ListRequest{PageSize: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range r.Bookings {
			if seen[row.BookingID] {
				t.Fatal("duplicate page")
			}
			seen[row.BookingID] = true
		}
		if !r.HasMore {
			break
		}
		if r.NextCursor == "" {
			t.Fatal("no cursor")
		}
		cursor = r.NextCursor
	}
	if len(seen) != 3 {
		t.Fatal("lost list entries")
	}
	r, err := s.List(ctx, Principal{Tenant: "B", Owner: p.Owner}, ListRequest{PageSize: 10})
	if err != nil || len(r.Bookings) != 0 {
		t.Fatal("tenant list crossed")
	}
}
func TestBookingValidationAndAuthoritativePolicyFailClosed(t *testing.T) {
	s, m, p, request := setup()
	for name, alter := range map[string]func(*CreateRequest){"bad-uuid": func(r *CreateRequest) { r.ClientRequestID = "not-uuid" }, "bad-duration": func(r *CreateRequest) { r.MeetingDuration = 59 }, "far-start": func(r *CreateRequest) { r.StartTime = s.now().AddDate(2, 0, 0).Unix() }, "name-bytes": func(r *CreateRequest) { r.MeetingName = strings.Repeat("中", 86) }, "duplicate-invite": func(r *CreateRequest) { r.InviteeUserIDs = []string{"peer", "peer"} }, "host-invite": func(r *CreateRequest) { r.InviteeUserIDs = []string{p.Owner} }} {
		t.Run(name, func(t *testing.T) {
			req := clone(request)
			alter(&req)
			if _, err := s.Create(context.Background(), p, req); !errors.Is(err, ErrInvalid) {
				t.Fatal("invalid request accepted")
			}
		})
	}
	s.Access = &allowPolicy{err: ErrDenied}
	if _, err := s.Create(context.Background(), p, request); !errors.Is(err, ErrDenied) {
		t.Fatal("policy failure ignored")
	}
	if len(m.bookings) != 0 || len(m.intents) != 0 {
		t.Fatal("invalid/policy denied persisted")
	}
}
