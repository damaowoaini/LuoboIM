package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mw"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/admincapability"
	"github.com/openimsdk/open-im-server/v3/internal/meetingbooking"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
)

// Test-only storage. Production registration always constructs MongoStore.
type bookingAPIStore struct {
	sync.Mutex
	books   map[string]*meetingbooking.Booking
	intents map[string]*meetingbooking.Intent
	calls   int
	fail    error
}
type bookingAPITx struct {
	store *bookingAPIStore
	p     meetingbooking.Principal
}

func apiBookingKey(p meetingbooking.Principal, id string) string {
	return p.Tenant + "\x00" + p.Owner + "\x00" + id
}
func newBookingAPIStore() *bookingAPIStore {
	return &bookingAPIStore{books: map[string]*meetingbooking.Booking{}, intents: map[string]*meetingbooking.Intent{}}
}
func (s *bookingAPIStore) Run(ctx context.Context, p meetingbooking.Principal, fn func(meetingbooking.Tx) (*meetingbooking.Result, error)) (*meetingbooking.Result, error) {
	s.Lock()
	defer s.Unlock()
	s.calls++
	if s.fail != nil {
		return nil, s.fail
	}
	return fn(&bookingAPITx{store: s, p: p})
}
func (s *bookingAPIStore) List(context.Context, meetingbooking.Principal, meetingbooking.ListQuery) ([]meetingbooking.Booking, bool, error) {
	return []meetingbooking.Booking{}, false, nil
}
func (t *bookingAPITx) GetIntent(_ context.Context, id string) (*meetingbooking.Intent, error) {
	r := t.store.intents[apiBookingKey(t.p, id)]
	if r == nil {
		return nil, meetingbooking.ErrNotFound
	}
	return r, nil
}
func (t *bookingAPITx) InsertIntent(_ context.Context, r *meetingbooking.Intent) error {
	t.store.intents[apiBookingKey(t.p, r.RequestID)] = r
	return nil
}
func (t *bookingAPITx) GetBooking(_ context.Context, id string) (*meetingbooking.Booking, error) {
	r := t.store.books[apiBookingKey(t.p, id)]
	if r == nil {
		return nil, meetingbooking.ErrNotFound
	}
	copy := *r
	return &copy, nil
}
func (t *bookingAPITx) InsertBooking(_ context.Context, r *meetingbooking.Booking) error {
	t.store.books[apiBookingKey(t.p, r.ID)] = r
	return nil
}
func (t *bookingAPITx) ReplaceBooking(_ context.Context, r *meetingbooking.Booking, v int64) error {
	old := t.store.books[apiBookingKey(t.p, r.ID)]
	if old == nil || old.Version != v {
		return meetingbooking.ErrConflict
	}
	t.store.books[apiBookingKey(t.p, r.ID)] = r
	return nil
}
func bookingBody(id string) string {
	data, _ := json.Marshal(meetingbooking.CreateRequest{ClientRequestID: id, Payload: meetingbooking.Payload{MeetingName: "synthetic http booking", StartTime: time.Now().Add(time.Hour).Unix(), MeetingDuration: 3600, InviteeUserIDs: []string{}, Ex: "opaque"}})
	return string(data)
}
func bookingHTTP(t *testing.T, r http.Handler, operation, token, body string) map[string]json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/signal/meeting_booking/"+operation, strings.NewReader(body))
	req.Header.Set("token", token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("operationID", "booking-"+uuid.NewString())
	req.Header.Set("X-Luobo-Tenant", "forged-tenant")
	req.Header.Set("opUserID", "forged-owner")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var response map[string]json.RawMessage
	if json.Unmarshal(w.Body.Bytes(), &response) != nil {
		t.Fatal("nonJSON booking envelope")
	}
	return response
}
func bookingCode(t *testing.T, r map[string]json.RawMessage) int {
	t.Helper()
	var code int
	if json.Unmarshal(r["errCode"], &code) != nil {
		t.Fatal("missing standard errCode")
	}
	return code
}
func TestMeetingBookingHTTPStrictFieldsDuplicateAliasAndBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newBookingAPIStore()
	a := &meetingBookingAPI{service: &meetingbooking.Service{Store: store, Access: bookingInvitePolicy{check: func(context.Context, meetingbooking.Principal, []string) error { return nil }}}, principal: func(*gin.Context) (meetingbooking.Principal, error) {
		return meetingbooking.Principal{Tenant: "trusted-A", Owner: "trusted-owner"}, nil
	}}
	r := gin.New()
	a.routes(r, func(c *gin.Context) { c.Next() })
	good := bookingBody(uuid.NewString())
	for name, body := range map[string]string{"owner": strings.TrimSuffix(good, "}") + `,"owner":"attacker"}`, "host": strings.TrimSuffix(good, "}") + `,"hostUserID":"attacker"}`, "duplicate": strings.TrimSuffix(good, "}") + `,"clientRequestID":"` + uuid.NewString() + `"}`, "case-alias": strings.Replace(good, "clientRequestID", "ClientRequestID", 1), "trailing": good + `{}`, "null": `null`, "large": `{"ex":"` + strings.Repeat("x", 32768) + `"}`} {
		t.Run(name, func(t *testing.T) {
			before := store.calls
			if bookingCode(t, bookingHTTP(t, r, "create", "", body)) == 0 || store.calls != before {
				t.Fatal("invalid body reached storage")
			}
		})
	}
	response := bookingHTTP(t, r, "create", "", good)
	if bookingCode(t, response) != 0 {
		t.Fatal("valid booking HTTP failed")
	}
	var result meetingbooking.Result
	if json.Unmarshal(response["data"], &result) != nil || result.Booking == nil || result.Booking.HostUserID != "trusted-owner" || result.Booking.MediaReady {
		t.Fatal("trusted actor/media-free envelope failed")
	}
}
func TestMeetingBookingHTTPStorageFailureDoesNotExposeInternalError(t *testing.T) {
	store := newBookingAPIStore()
	store.fail = errors.New("synthetic-sensitive-internal-detail")
	a := &meetingBookingAPI{service: &meetingbooking.Service{Store: store}, principal: func(*gin.Context) (meetingbooking.Principal, error) {
		return meetingbooking.Principal{Tenant: "A", Owner: "owner"}, nil
	}}
	r := gin.New()
	a.routes(r, func(c *gin.Context) { c.Next() })
	response := bookingHTTP(t, r, "receipt", "", `{"clientRequestID":"`+uuid.NewString()+`"}`)
	data, _ := json.Marshal(response)
	if bookingCode(t, response) == 0 || strings.Contains(string(data), store.fail.Error()) {
		t.Fatal("storage failure hidden or leaked")
	}
}
func bookingTokenFixture(t *testing.T) (func(string, string) string, string) {
	t.Helper()
	oldSecret, oldIssuer, oldManagers := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.Manager.UserID
	config.Config.Secret = strings.Repeat("synthetic-booking-key-", 3)
	config.Config.AdminDelegation.Issuer = "trusted-booking-tenant"
	manager := "booking-manager-" + uuid.NewString()
	config.Config.Manager.UserID = []string{manager}
	t.Cleanup(func() {
		config.Config.Secret = oldSecret
		config.Config.AdminDelegation.Issuer = oldIssuer
		config.Config.Manager.UserID = oldManagers
	})
	return func(user, purpose string) string {
		claims := authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: user, PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: config.Config.AdminDelegation.Issuer, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), ID: uuid.NewString()}}, Purpose: purpose}
		if purpose == authverify.DelegatedPurpose {
			claims.Subject = "admin"
			claims.Audience = jwt.ClaimStrings{authverify.AdminAudience}
			claims.Delegation = &authverify.Delegation{Version: 1, AdminID: "admin", SessionHash: strings.Repeat("b", 64)}
		}
		token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(config.Config.Secret))
		if err != nil {
			t.Fatal(err)
		}
		return token
	}, manager
}
func TestMeetingBookingPrincipalRejectsManagementPurposesAndBodyHeaders(t *testing.T) {
	mint, manager := bookingTokenFixture(t)
	for _, item := range []struct {
		user, purpose string
		allowed       bool
	}{{"ordinary", "", true}, {manager, authverify.ServicePurpose, false}, {manager, authverify.DelegatedPurpose, false}, {manager, "", false}} {
		t.Run(item.purpose+item.user, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/signal/meeting_booking/create", nil)
			c.Request.Header.Set("token", mint(item.user, item.purpose))
			c.Request.Header.Set("X-Luobo-Tenant", "forged")
			c.Set(constant.OpUserID, item.user)
			p, err := bookingPrincipal(c)
			if item.allowed {
				if err != nil || p.Tenant != "trusted-booking-tenant" || p.Owner != item.user {
					t.Fatal("trusted principal not derived")
				}
			} else if err == nil {
				t.Fatal("management purpose gained booking owner authority")
			}
		})
	}
}
func TestMeetingBookingHTTPActualGuardAndLiveTokenStatus(t *testing.T) {
	address := os.Getenv("LUOBO_BOOKING_TEST_REDIS_ADDR")
	if address == "" {
		t.Skip("explicit private Redis fixture not selected")
	}
	if address != "127.0.0.1:36379" {
		t.Fatal("only task loopback Redis allowed")
	}
	mint, manager := bookingTokenFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	rdb := redis.NewClient(&redis.Options{Addr: address, DB: 15, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() {
		if err := rdb.Close(); err != nil {
			t.Error("booking Redis client cleanup failed")
		}
	})
	if rdb.Ping(ctx).Err() != nil {
		t.Fatal("selected task Redis unavailable")
	}
	cacheStore := cache.NewMsgCacheModel(rdb)
	owner, other := uuid.NewString(), uuid.NewString()
	if owner == other {
		t.Fatal("booking fixture identities must be distinct")
	}
	raw := mint(owner, "")
	foreign := mint(other, "")
	var nonceBytes [32]byte
	if _, err := rand.Read(nonceBytes[:]); err != nil {
		t.Fatal("booking fixture claim entropy unavailable")
	}
	nonce := hex.EncodeToString(nonceBytes[:])
	keys := []string{
		"LUOBO_BOOKING_TEST_CLAIM:" + uuid.NewString(),
		"UID_PID_TOKEN_STATUS:" + owner + ":" + constant.PlatformIDToName(10),
		"UID_PID_TOKEN_STATUS:" + other + ":" + constant.PlatformIDToName(10),
	}
	const claimFixture = `
if redis.call('EXISTS', KEYS[1], KEYS[2], KEYS[3]) ~= 0 then return 0 end
redis.call('SET', KEYS[1], ARGV[1])
redis.call('HSET', KEYS[2], ARGV[2], ARGV[4])
redis.call('HSET', KEYS[3], ARGV[3], ARGV[4])
return 1`
	claimed, err := rdb.Eval(ctx, claimFixture, keys, nonce, raw, foreign, constant.NormalToken).Int64()
	if err != nil {
		// Unknown ACK can mean the Lua committed. Do not infer ownership or clean.
		t.Fatal("booking fixture claim outcome unconfirmed; cleanup not authorized")
	}
	if claimed != 1 {
		t.Fatal("booking fixture claim refused; cleanup not authorized")
	}
	ownerExpected, cleanupCertain := constant.NormalToken, true
	t.Cleanup(func() {
		if !cleanupCertain {
			t.Error("booking fixture retained after an unconfirmed token update")
			return
		}
		clean, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		const cleanFixture = `
if redis.call('TYPE', KEYS[1]).ok ~= 'string' or redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if redis.call('TYPE', KEYS[2]).ok ~= 'hash' or redis.call('TYPE', KEYS[3]).ok ~= 'hash' then return 0 end
if redis.call('HLEN', KEYS[2]) ~= 1 or redis.call('HLEN', KEYS[3]) ~= 1 then return 0 end
if redis.call('HGET', KEYS[2], ARGV[2]) ~= ARGV[4] or redis.call('HGET', KEYS[3], ARGV[3]) ~= ARGV[5] then return 0 end
return redis.call('DEL', KEYS[1], KEYS[2], KEYS[3])`
		deleted, err := rdb.Eval(clean, cleanFixture, keys, nonce, raw, foreign, ownerExpected, constant.NormalToken).Int64()
		if err != nil || deleted != 3 {
			t.Error("booking fixture cleanup unconfirmed or ownership/state mismatch; no retry")
			return
		}
		if remaining, err := rdb.Exists(clean, keys...).Result(); err != nil || remaining != 0 {
			t.Error("booking fixture cleanup absence not confirmed")
		}
	})
	// Exercise the actual cache API after the atomic fixture claim. A failed
	// update ACK never grants a guessed cleanup state, even for idempotent writes.
	for user, token := range map[string]string{owner: raw, other: foreign} {
		if err := cacheStore.AddTokenFlag(ctx, user, 10, token, constant.NormalToken); err != nil {
			cleanupCertain = false
			t.Fatal("booking fixture normal token update unconfirmed")
		}
	}
	store := newBookingAPIStore()
	a := &meetingBookingAPI{service: &meetingbooking.Service{Store: store, Access: bookingInvitePolicy{check: func(context.Context, meetingbooking.Principal, []string) error { return nil }}}, principal: bookingPrincipal}
	router := gin.New()
	router.Use(mw.GinParseOperationID(), admincapability.Guard(rdb, nil))
	a.routes(router, GinParseToken(rdb))
	response := bookingHTTP(t, router, "create", raw, bookingBody(uuid.NewString()))
	if bookingCode(t, response) != 0 {
		t.Fatal("normal live user booking failed")
	}
	var result meetingbooking.Result
	_ = json.Unmarshal(response["data"], &result)
	if bookingCode(t, bookingHTTP(t, router, "detail", foreign, `{"bookingID":"`+result.Booking.BookingID+`"}`)) == 0 {
		t.Fatal("foreign owner detail leaked")
	}
	for _, purpose := range []string{authverify.ServicePurpose, authverify.DelegatedPurpose} {
		if bookingCode(t, bookingHTTP(t, router, "create", mint(manager, purpose), bookingBody(uuid.NewString()))) == 0 {
			t.Fatal("manager class passed actual guard")
		}
	}
	if err := cacheStore.AddTokenFlag(ctx, owner, 10, raw, constant.KickedToken); err != nil {
		cleanupCertain = false
		t.Fatal("booking fixture kicked token update unconfirmed")
	}
	// Advance the expected owner status only after this exact API call ACKs.
	ownerExpected = constant.KickedToken
	before := store.calls
	if bookingCode(t, bookingHTTP(t, router, "receipt", raw, `{"clientRequestID":"`+uuid.NewString()+`"}`)) == 0 || store.calls != before {
		t.Fatal("revoked exact token reached booking store")
	}
}

func TestMeetingBookingRegistrationWithoutDatabaseKeepsUnrelatedRoutesAlive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/synthetic-existing", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	// Production registration must neither panic nor call discovery/DB repair.
	registerMeetingBookingRoutes(r, func(c *gin.Context) { c.Next() }, nil, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/synthetic-existing", nil))
	if w.Code != http.StatusNoContent {
		t.Fatal("booking unavailability removed existing route")
	}
	found := 0
	for _, route := range r.Routes() {
		if strings.HasPrefix(route.Path, "/signal/meeting_booking/") {
			found++
		}
	}
	if found != 7 {
		t.Fatal("booking endpoints vanished instead of failing closed")
	}
}
func TestMeetingBookingUnreadyStorageKeepsExistingWireErrorAndOtherRoutes(t *testing.T) {
	for _, name := range []string{"constructor_unavailable", "runtime_unready"} {
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			a := &meetingBookingAPI{principal: func(*gin.Context) (meetingbooking.Principal, error) {
				return meetingbooking.Principal{Tenant: "fixture", Owner: "owner"}, nil
			}}
			if name == "runtime_unready" {
				store := newBookingAPIStore()
				store.fail = meetingbooking.ErrUnavailable
				a.service = &meetingbooking.Service{Store: store}
			}
			a.routes(r, func(c *gin.Context) { c.Next() })
			r.GET("/synthetic-existing", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			response := bookingHTTP(t, r, "create", "synthetic-unused", bookingBody(uuid.NewString()))
			if bookingCode(t, response) != 500 {
				t.Fatal("readiness changed established wire error code")
			}
			var message, detail string
			if json.Unmarshal(response["errMsg"], &message) != nil || message != errs.ErrInternalServer.Msg() {
				t.Fatal("readiness changed established standard error message")
			}
			if json.Unmarshal(response["errDlt"], &detail) != nil || !strings.Contains(detail, "预约结果未确认，请查询同一意图回执") {
				t.Fatal("readiness changed established recovery contract")
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/synthetic-existing", nil))
			if w.Code != http.StatusNoContent {
				t.Fatal("unready booking affected unrelated route")
			}
		})
	}
}
