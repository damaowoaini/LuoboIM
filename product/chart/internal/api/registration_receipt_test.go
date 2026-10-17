package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/constant"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/gin-gonic/gin"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func signedRouteRequest(body, path, id, ts, nonce string) string {
	h := sha256.Sum256([]byte(body))
	mac := hmac.New(sha256.New, []byte(strings.Repeat("s", 32)))
	mac.Write([]byte(ts + "\n" + nonce + "\nPOST\n" + path + "\n" + id + "\n" + hex.EncodeToString(h[:])))
	return hex.EncodeToString(mac.Sum(nil))
}
func TestRegistrationSignatureRejectsForgeryReplayAndOutage(t *testing.T) {
	t.Setenv("LUOBO_ROUTE_SHARED_SECRET", strings.Repeat("s", 32))
	const body = `{"user":{"phoneNumber":"12345678"}}`
	const id = "f27b0e7c-63df-4b94-b463-6c9644a8403b"
	const nonce = "0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name                                            string
		age                                             time.Duration
		tamper, badID, badPath, outage, replay, allowed bool
	}{
		{name: "valid", allowed: true}, {name: "forged body", tamper: true}, {name: "forged identity", badID: true}, {name: "wrong endpoint", badPath: true}, {name: "expired", age: 6 * time.Minute}, {name: "future", age: -6 * time.Minute}, {name: "nonce replay", replay: true}, {name: "Redis unavailable", outage: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			redis := &nonceRedisStub{seen: map[string]bool{}}
			if tc.outage {
				redis.err = errors.New("unavailable")
			}
			reached := false
			r := gin.New()
			r.POST("/account/register", routeRegistrationAuthentication(redis, true, "/account/register"), func(c *gin.Context) {
				reached = true
				if registration.ID(c) != id {
					t.Fatal("trusted ID not propagated")
				}
				c.Status(200)
			})
			ts := strconv.FormatInt(time.Now().Add(-tc.age).Unix(), 10)
			invoke := func() {
				wire := body
				if tc.tamper {
					wire = `{}`
				}
				req := httptest.NewRequest("POST", "/account/register", strings.NewReader(wire))
				req.Header.Set(routeRegistrationHeader, id)
				if tc.badID {
					req.Header.Set(routeRegistrationHeader, "127b0e7c-63df-4b94-b463-6c9644a8403b")
				}
				req.Header.Set("X-Luobo-Route-Timestamp", ts)
				req.Header.Set("X-Luobo-Route-Nonce", nonce)
				path := "/account/register"
				if tc.badPath {
					path = "/internal/registration/receipt"
				}
				req.Header.Set("X-Luobo-Route-Signature", signedRouteRequest(body, path, id, ts, nonce))
				r.ServeHTTP(httptest.NewRecorder(), req)
			}
			if tc.replay {
				invoke()
				if !reached {
					t.Fatal("first signed request rejected")
				}
				reached = false
			}
			invoke()
			if reached != tc.allowed {
				t.Fatal("signature authorization result incorrect")
			}
		})
	}
}
func TestOrdinaryRegistrationRemainsAvailableWithoutInternalHeaders(t *testing.T) {
	t.Setenv("LUOBO_ROUTE_SHARED_SECRET", "")
	r := gin.New()
	called := false
	r.POST("/account/register", routeRegistrationAuthentication(nil, false, "/account/register"), func(c *gin.Context) {
		called = true
		if registration.ID(c) != "" {
			t.Fatal("ordinary request gained internal proof")
		}
	})
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/account/register", strings.NewReader(`{}`)))
	if !called {
		t.Fatal("normal client registration rejected")
	}
}

type provisioningCaller struct {
	apicall.CallerInterface
	createErr      error
	existingMarker string
	found          int
}

func (c *provisioningCaller) ImAdminTokenWithDefaultAdmin(context.Context) (string, error) {
	return "synthetic-service", nil
}

func (c *provisioningCaller) RegisterUser(ctx context.Context, users []*sdkws.UserInfo) error {
	if ctx.Value(constant.CtxApiToken) != "synthetic-service" || len(users) != 1 || users[0].UserID != "owner" || !strings.Contains(users[0].Ex, "luoboRegistrationID") {
		return errors.New("registration ownership marker missing")
	}
	return c.createErr
}
func (c *provisioningCaller) FindRegisteredUsers(_ context.Context, ids []string) ([]*sdkws.UserInfo, error) {
	c.found++
	return []*sdkws.UserInfo{{UserID: "owner", Ex: c.existingMarker}}, nil
}
func TestSignedReceiptPendingStateRequiresMatchingPersistedRegistration(t *testing.T) {
	const id = "f27b0e7c-63df-4b94-b463-6c9644a8403b"
	identity, _ := registration.Identity("receipt@fixture.invalid", "", "")
	for _, kind := range []string{"pending", "confirmed", "missing", "different identity", "storage error"} {
		t.Run(kind, func(t *testing.T) {
			sqlDB, mock, e := sqlmock.New()
			if e != nil {
				t.Fatal(e)
			}
			defer sqlDB.Close()
			db, e := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
			if e != nil {
				t.Fatal(e)
			}
			query := mock.ExpectQuery("SELECT .* FROM `route_registration_receipts`").WithArgs(id)
			rows := sqlmock.NewRows([]string{"registration_id", "normalized_identity", "user_id", "managed_by_outbox", "im_provisioned_at"})
			switch kind {
			case "missing":
				query.WillReturnRows(rows)
			case "storage error":
				query.WillReturnError(errors.New("private SQL detail"))
			case "different identity":
				query.WillReturnRows(rows.AddRow(id, "foreign-identity", "owner", true, nil))
			case "confirmed":
				query.WillReturnRows(rows.AddRow(id, identity, "owner", true, time.Now()))
			default:
				query.WillReturnRows(rows.AddRow(id, identity, "owner", true, nil))
			}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/account/register", nil)
			c.Set(registration.MetadataKey, []string{id})
			handled, e := (&registrationAPI{db: db}).retry(c, "receipt@fixture.invalid", "", "")
			if kind == "missing" {
				if handled || e != nil || w.Body.Len() != 0 {
					t.Fatal("missing receipt falsely asserted commitment")
				}
				return
			}
			if kind == "different identity" || kind == "storage error" {
				if !handled || e == nil || w.Body.Len() != 0 {
					t.Fatal("unverified receipt exposed pending state")
				}
				return
			}
			var out struct {
				ErrCode int `json:"errCode"`
				Data    struct {
					Pending       bool   `json:"registrationPending"`
					RequiresLogin bool   `json:"requiresLogin"`
					Registered    bool   `json:"registered"`
					IM            bool   `json:"imProvisioned"`
					Chat          string `json:"chatToken"`
				} `json:"data"`
			}
			if e != nil || !handled || json.Unmarshal(w.Body.Bytes(), &out) != nil || !out.Data.RequiresLogin || out.Data.Chat != "" {
				t.Fatal("known receipt recovery response incorrect", e)
			}
			if kind == "pending" && (out.ErrCode != 1002 || !out.Data.Pending) {
				t.Fatal("durably pending receipt lacks structured flag")
			}
			if kind == "confirmed" && (out.ErrCode != 0 || out.Data.Pending || !out.Data.Registered || !out.Data.IM) {
				t.Fatal("confirmed receipt incorrectly treated as pending")
			}
			if e := mock.ExpectationsWereMet(); e != nil {
				t.Fatal(e)
			}
		})
	}
}
func TestRegistrationReceiptRecoversUncertainIMCreateWithoutClaimingOtherAccount(t *testing.T) {
	const id = "f27b0e7c-63df-4b94-b463-6c9644a8403b"
	marker := `{"luoboRegistrationID":"` + id + `"}`
	for _, tc := range []struct {
		name      string
		createErr error
		marker    string
		success   bool
	}{
		{name: "first create", success: true}, {name: "response lost after creation", createErr: errors.New("response lost"), marker: marker, success: true}, {name: "unrelated ID collision", createErr: errors.New("duplicate"), marker: `{}`, success: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sqlDB, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer sqlDB.Close()
			db, err := gorm.Open(mysql.New(mysql.Config{Conn: sqlDB, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, SkipDefaultTransaction: true, Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectQuery("SELECT .* FROM `attributes`").WithArgs("owner").WillReturnRows(sqlmock.NewRows([]string{"user_id", "nickname", "face_url"}).AddRow("owner", "Owner", ""))
			if tc.success {
				mock.ExpectExec("UPDATE `route_registration_receipts`").WillReturnResult(sqlmock.NewResult(0, 1))
			}
			caller := &provisioningCaller{createErr: tc.createErr, existingMarker: tc.marker}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/internal/registration/receipt", nil)
			receipt := &registration.Receipt{RegistrationID: id, UserID: "owner", CreatedAt: time.Now()}
			err = (&registrationAPI{db: db, caller: caller}).ensureIM(c, receipt)
			if (err == nil) != tc.success || (receipt.IMProvisionedAt != nil) != tc.success {
				t.Fatal("incorrect IM provisioning result", err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
