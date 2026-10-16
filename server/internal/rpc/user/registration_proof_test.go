package user

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	pbuser "github.com/OpenIMSDK/protocol/user"
	"github.com/OpenIMSDK/protocol/wrapperspb"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/convert"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/registrationproof"
	"google.golang.org/grpc/metadata"
)

var stopAfterProfileWrite = errors.New("test boundary after actual update dispatch")

type registrationDB struct {
	controller.UserDatabase
	created    []*relation.UserModel
	existCalls int
	ex         string
	writes     int
}

func (d *registrationDB) IsExist(context.Context, []string) (bool, error) {
	d.existCalls++
	return false, nil
}
func (d *registrationDB) Create(ctx context.Context, models []*relation.UserModel) error {
	for _, u := range models {
		if e := registrationproof.CheckCreate(ctx, u.UserID, u.Ex); e != nil {
			return e
		}
	}
	d.created = models
	return nil
}
func (d *registrationDB) UpdateByMap(_ context.Context, _ string, args map[string]any) error {
	if e := registrationproof.CheckUpdateMap(args); e != nil {
		return e
	}
	if next, ok := args["ex"].(string); ok {
		ex, e := registrationproof.ProtectUpdate(d.ex, next)
		if e != nil {
			return e
		}
		d.ex = ex
	}
	d.writes++
	return stopAfterProfileWrite
}

type registrationTokens map[string]int

func (f registrationTokens) GetTokensWithoutError(context.Context, string, int) (map[string]int, error) {
	return f, nil
}

type registrationNonces struct{}

func (registrationNonces) Consume(context.Context, string, time.Duration) (bool, error) {
	return true, nil
}
func registrationConfig(t *testing.T) {
	t.Helper()
	secret, issuer, managers, callbacks := config.Config.Secret, config.Config.AdminDelegation.Issuer, config.Config.Manager.UserID, config.Config.Callback
	config.Config.Secret = strings.Repeat("synthetic-user-registration-", 3)
	config.Config.AdminDelegation.Issuer = "user-registration-A"
	config.Config.Manager.UserID = []string{"fixture-service"}
	config.Config.Callback.CallbackBeforeUserRegister.Enable = false
	config.Config.Callback.CallbackAfterUserRegister.Enable = false
	config.Config.Callback.CallbackBeforeUpdateUserInfo.Enable = false
	config.Config.Callback.CallbackBeforeUpdateUserInfoEx.Enable = false
	t.Cleanup(func() {
		config.Config.Secret = secret
		config.Config.AdminDelegation.Issuer = issuer
		config.Config.Manager.UserID = managers
		config.Config.Callback = callbacks
	})
}
func userRegistrationProof(t *testing.T, s *userServer, req *pbuser.UserRegisterReq) context.Context {
	t.Helper()
	c := authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: "fixture-service", PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: config.Config.AdminDelegation.Issuer, ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), ID: uuid.NewString()}}, Purpose: authverify.ServicePurpose}
	raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(config.Config.Secret))
	if e != nil {
		t.Fatal(e)
	}
	s.registrationAuthorizer = &registrationproof.Authorizer{Tokens: registrationTokens{raw: constant.NormalToken}, Nonces: registrationNonces{}, Secret: []byte(config.Config.Secret)}
	ctx, e := registrationproof.WithRPCProof(context.Background(), []byte(config.Config.Secret), raw, req, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	md := metadata.MD{}
	for _, key := range ctx.Value(constant.RpcCustomHeader).([]string) {
		md.Set(key, ctx.Value(key).([]string)...)
	}
	return metadata.NewIncomingContext(context.Background(), md)
}
func TestUserRegisterEntryRequiresSignedInitialProofAndLocksCallbackIdentity(t *testing.T) {
	registrationConfig(t)
	db := &registrationDB{}
	s := &userServer{UserDatabase: db}
	marker := `{"luoboRegistrationID":"` + uuid.NewString() + `"}`
	marked := &pbuser.UserRegisterReq{Secret: config.Config.Secret, Users: []*sdkws.UserInfo{{UserID: "owner", Nickname: "synthetic", Ex: marker}}}
	if _, e := s.UserRegister(context.Background(), marked); e == nil || db.existCalls != 0 || len(db.created) != 0 {
		t.Fatal("secret/opuser-only request minted an initial proof")
	}
	response := map[string]any{}
	callbackCalls := 0
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callbackCalls++
		if r.Header.Get("X-OpenIM-Callback-Signature") == "" {
			t.Error("callback lacked production authentication")
		}
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer callback.Close()
	config.Config.Callback.CallbackUrl = callback.URL
	config.Config.Callback.CallbackBeforeUserRegister = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
	config.Config.Callback.CallbackBeforeUpdateUserInfo.CallbackTimeOut = 2
	for _, replacement := range []*sdkws.UserInfo{{UserID: "other", Nickname: "changed"}, {UserID: "owner", Nickname: "changed", Ex: `{"luoboAdminProvisioningID":"` + uuid.NewString() + `"}`}, {UserID: "owner", Nickname: "changed", Ex: `{"luoboRegistrationID":"` + uuid.NewString() + `"}`}} {
		response = map[string]any{"users": []*sdkws.UserInfo{replacement}}
		if _, e := s.UserRegister(userRegistrationProof(t, s, marked), marked); e == nil || len(db.created) != 0 {
			t.Fatal("callback replaced original identity/proof")
		}
		// Callback may replace the request slice; each replay below is a new
		// fixture request, never an attempt to retry a production account.
		marked = &pbuser.UserRegisterReq{Secret: config.Config.Secret, Users: []*sdkws.UserInfo{{UserID: "owner", Nickname: "synthetic", Ex: marker}}}
	}
	response = map[string]any{"users": []*sdkws.UserInfo{{UserID: "owner", Nickname: "safe callback", Ex: `{"ordinary":"changed"}`}}}
	if _, e := s.UserRegister(userRegistrationProof(t, s, marked), marked); e != nil || len(db.created) != 1 {
		t.Fatal("safe callback plus authenticated proof rejected", e)
	}
	m, e := registrationproof.InitialMarkers(db.created[0].Ex)
	if e != nil || m[registrationproof.PublicKey] == "" || db.created[0].Nickname != "safe callback" {
		t.Fatal("callback erased initial proof or normal edit")
	}
	db.created = nil
	legacy := &pbuser.UserRegisterReq{Secret: config.Config.Secret, Users: []*sdkws.UserInfo{{UserID: "legacy", Nickname: "old integration", Ex: "opaque"}}}
	response = map[string]any{"users": []*sdkws.UserInfo{{UserID: "legacy", Nickname: "malicious callback", Ex: marker}}}
	if _, e := s.UserRegister(context.Background(), legacy); e == nil || len(db.created) != 0 {
		t.Fatal("callback minted proof for marker-free legacy registration")
	}
	response = map[string]any{}
	legacy.Users[0].Ex = "opaque"
	if _, e := s.UserRegister(context.Background(), legacy); e != nil || len(db.created) != 1 {
		t.Fatal("legacy marker-free registration changed", e)
	}
	if callbackCalls != 6 {
		t.Fatal("actual callback path not exercised", callbackCalls)
	}
}
func TestUserUpdateExEntryAndBothBeforeCallbacksCannotForgeProof(t *testing.T) {
	registrationConfig(t)
	marker := `{"luoboAdminProvisioningID":"` + uuid.NewString() + `"}`
	db := &registrationDB{ex: marker}
	s := &userServer{UserDatabase: db}
	ctx := context.WithValue(context.Background(), constant.OpUserID, "owner")
	response := map[string]any{}
	callback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(response) }))
	defer callback.Close()
	config.Config.Callback.CallbackUrl = callback.URL
	config.Config.Callback.CallbackBeforeUpdateUserInfoEx = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
	config.Config.Callback.CallbackBeforeUpdateUserInfo = config.CallBackConfig{Enable: true, CallbackTimeOut: 2}
	for _, ex := range []string{`{"custom":2}`, "", marker} {
		response = map[string]any{"ex": ex}
		_, e := s.UpdateUserInfoEx(ctx, &pbuser.UpdateUserInfoExReq{UserInfo: &sdkws.UserInfoWithEx{UserID: "owner", Ex: &wrapperspb.StringValue{Value: marker}}})
		if !errors.Is(e, stopAfterProfileWrite) {
			t.Fatal("legitimate UpdateUserInfoEx did not reach protected writer", e)
		}
		m, e := registrationproof.InitialMarkers(db.ex)
		if e != nil || m[registrationproof.AdminKey] == "" {
			t.Fatal("actual update callback erased authoritative proof")
		}
	}
	for _, ex := range []string{`{"luoboRegistrationID":"` + uuid.NewString() + `"}`, `{"luoboAdminProvisioningID":null}`, `{"luoboAdminProvisioningId":"` + uuid.NewString() + `"}`, "opaque"} {
		writes := db.writes
		response = map[string]any{"ex": ex}
		_, e := s.UpdateUserInfoEx(ctx, &pbuser.UpdateUserInfoExReq{UserInfo: &sdkws.UserInfoWithEx{UserID: "owner", Ex: &wrapperspb.StringValue{Value: marker}}})
		if e == nil || errors.Is(e, stopAfterProfileWrite) || db.writes != writes {
			t.Fatal("Ex-wrapper callback modified proof")
		}
		// The legacy callback uses a plain string, then both real converters
		// converge on the same protected database write entry.
		response = map[string]any{"ex": ex}
		req := &pbuser.UpdateUserInfoReq{UserInfo: &sdkws.UserInfo{UserID: "owner", Ex: marker}}
		if e := CallbackBeforeUpdateUserInfo(ctx, req); e != nil {
			t.Fatal(e)
		}
		if e := db.UpdateByMap(ctx, req.UserInfo.UserID, convert.UserPb2DBMap(req.UserInfo)); e == nil || errors.Is(e, stopAfterProfileWrite) {
			t.Fatal("legacy callback/converter bypassed protected writer")
		}
	}
	foreign := context.WithValue(context.Background(), constant.OpUserID, "another")
	writes := db.writes
	if _, e := s.UpdateUserInfo(foreign, &pbuser.UpdateUserInfoReq{UserInfo: &sdkws.UserInfo{UserID: "owner", Ex: marker}}); e == nil {
		t.Fatal("legacy update accepted foreign actor")
	}
	if _, e := s.UpdateUserInfoEx(foreign, &pbuser.UpdateUserInfoExReq{UserInfo: &sdkws.UserInfoWithEx{UserID: "owner"}}); e == nil || db.writes != writes {
		t.Fatal("Ex update accepted foreign actor")
	}
	if _, e := s.UpdateUserInfo(ctx, &pbuser.UpdateUserInfoReq{}); e == nil {
		t.Fatal("nil legacy userInfo accepted")
	}
	if _, e := s.UpdateUserInfoEx(ctx, &pbuser.UpdateUserInfoExReq{}); e == nil {
		t.Fatal("nil extended userInfo accepted")
	}
}
