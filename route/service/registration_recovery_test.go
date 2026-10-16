package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"openim-route-service/model"
)

type recoveryForward struct {
	result        map[string]interface{}
	err           error
	registrations int
	queries       int
	receipt       map[string]interface{}
	receiptErr    error
}

func (f *recoveryForward) Post(_ context.Context, _ *model.RouteResponse, path string, _ any, _, _ string) (map[string]interface{}, error) {
	if path == "/chat/internal/registration/receipt" {
		f.queries++
		return map[string]interface{}{"errCode": float64(0), "data": f.receipt}, f.receiptErr
	}
	f.registrations++
	return f.result, f.err
}
func recoveryFixture() (*RegisterService, *fakeRegistrationStore, *recoveryForward, *RegisterRequest) {
	routes, _ := fixture()
	store := &fakeRegistrationStore{attempt: &RegistrationAttempt{Intent: model.RegistrationIntent{RegistrationID: "82ea3843-1abf-4d39-887c-0791408b7af8", AccountKey: "phone:+86:13812345678", ServerID: "a", State: "reserved", CreatedAt: time.Now()}, Server: routes.invites[""]}}
	forward := &recoveryForward{receipt: map[string]interface{}{"registrationID": store.attempt.Intent.RegistrationID, "normalizedIdentity": store.attempt.Intent.AccountKey, "registered": true, "imProvisioned": true, "userID": "created-user"}}
	return &RegisterService{routes, forward, store}, store, forward, &RegisterRequest{Platform: 2, User: map[string]interface{}{"phoneNumber": "13812345678", "password": "digest"}}
}
func TestRecoveryBindsOnlyMatchingProvisionedReceiptAndReturnsNoTokens(t *testing.T) {
	s, store, forward, req := recoveryFixture()
	result, err := s.Register(context.Background(), req, "203.0.113.7", "")
	if !errors.Is(err, ErrRegistrationRecovered) || result != nil || store.confirmed != "created-user" || store.completed != 1 || forward.registrations != 0 || forward.queries != 1 {
		t.Fatal("receipt recovery did not complete without credentials")
	}
}
func TestRecoveryRejectsDifferentIntentIdentityOrIncompleteIM(t *testing.T) {
	for _, field := range []string{"registrationID", "normalizedIdentity", "imProvisioned", "userID"} {
		t.Run(field, func(t *testing.T) {
			s, store, forward, req := recoveryFixture()
			if field == "imProvisioned" {
				forward.receipt[field] = false
			} else {
				forward.receipt[field] = ""
			}
			_, err := s.Register(context.Background(), req, "", "")
			if !errors.Is(err, ErrRegistrationPending) || store.completed != 0 || forward.registrations != 0 {
				t.Fatal("untrusted or unprovisioned receipt accepted")
			}
		})
	}
}
func TestUnknownNetworkOutcomeRetainsCapacityAndIntent(t *testing.T) {
	s, store, forward, req := recoveryFixture()
	store.attempt.Fresh = true
	forward.err = errors.New("timeout after remote commit")
	_, err := s.Register(context.Background(), req, "", "")
	if !errors.Is(err, ErrRegistrationPending) || store.aborted != 0 || store.completed != 0 {
		t.Fatal("uncertain remote creation lost recovery metadata")
	}
}
func TestFreshDefinitiveRejectionReleasesReservation(t *testing.T) {
	s, store, forward, req := recoveryFixture()
	store.attempt.Fresh = true
	forward.result = map[string]interface{}{"errCode": float64(1001), "errDlt": "invalid OTP"}
	forward.receipt["registered"] = false
	result, err := s.Register(context.Background(), req, "", "")
	if err != nil || successful(result) || store.aborted != 1 || store.completed != 0 {
		t.Fatal("definitive rejected signup retained capacity")
	}
}
func TestPostCommitServerFailureRecoversOrRetainsOriginalIntent(t *testing.T) {
	for _, provisioned := range []bool{false, true} {
		t.Run(fmt.Sprint(provisioned), func(t *testing.T) {
			s, store, forward, req := recoveryFixture()
			store.attempt.Fresh = true
			forward.result = map[string]interface{}{"errCode": float64(500)}
			forward.receipt["imProvisioned"] = provisioned
			result, err := s.Register(context.Background(), req, "", "")
			want := ErrRegistrationPending
			if provisioned {
				want = ErrRegistrationRecovered
			}
			if !errors.Is(err, want) || result != nil || store.aborted != 0 || forward.queries != 1 {
				t.Fatal("post-commit failure discarded or bypassed its signed receipt")
			}
			if provisioned && (store.confirmed != "created-user" || store.completed != 1) {
				t.Fatal("provisioned account was not bound exactly once")
			}
		})
	}
}
func TestServerUnknownAndUnavailableReceiptsNeverReleaseFreshQuota(t *testing.T) {
	for _, code := range []float64{500, 503, 99999} {
		s, store, forward, req := recoveryFixture()
		store.attempt.Fresh = true
		forward.result = map[string]interface{}{"errCode": code}
		forward.receipt["registered"] = false
		_, err := s.Register(context.Background(), req, "", "")
		if !errors.Is(err, ErrRegistrationPending) || store.aborted != 0 {
			t.Fatal("server/unknown rejection released a potentially late commit")
		}
	}
	for _, invalid := range []string{"registered", "registrationID", "normalizedIdentity", "network"} {
		s, store, forward, req := recoveryFixture()
		store.attempt.Fresh = true
		forward.result = map[string]interface{}{"errCode": float64(1001)}
		forward.receipt["registered"] = false
		if invalid == "network" {
			forward.receiptErr = errors.New("unavailable")
		} else {
			delete(forward.receipt, invalid)
		}
		_, err := s.Register(context.Background(), req, "", "")
		if !errors.Is(err, ErrRegistrationPending) || store.aborted != 0 {
			t.Fatal("unproven negative receipt released quota")
		}
	}
}
func TestBindingFailurePreservesConfirmedRecoveryIntent(t *testing.T) {
	s, store, forward, req := recoveryFixture()
	store.attempt.Fresh = true
	store.completeErr = errors.New("database temporarily unavailable")
	forward.result = map[string]interface{}{"errCode": float64(0), "data": map[string]interface{}{"userID": "created-user", "imToken": "im", "chatToken": "chat"}}
	result, err := s.Register(context.Background(), req, "", "")
	if !errors.Is(err, ErrRegistrationPending) || result != nil || store.confirmed != "created-user" || store.aborted != 0 {
		t.Fatal("committed upstream account became unrecoverable")
	}
}
func TestRegistrationSignatureCoversLogicalPathBodyIDAndFreshNonce(t *testing.T) {
	secret := strings.Repeat("s", 32)
	id := "82ea3843-1abf-4d39-887c-0791408b7af8"
	f := NewHTTPForwarder(map[string]string{"a": secret})
	seen := map[string]bool{}
	f.Client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		digest := sha256.Sum256(body)
		nonce := req.Header.Get("X-Luobo-Route-Nonce")
		timestamp := req.Header.Get("X-Luobo-Route-Timestamp")
		if len(nonce) != 32 || seen[nonce] || req.Header.Get("X-Luobo-Route-Registration-ID") != id {
			t.Fatal("invalid ID/nonce")
		}
		seen[nonce] = true
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(timestamp + "\n" + nonce + "\nPOST\n/account/register\n" + id + "\n" + hex.EncodeToString(digest[:])))
		if req.Header.Get("X-Luobo-Route-Signature") != hex.EncodeToString(mac.Sum(nil)) {
			t.Fatal("wire signing contract differs from chart")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"errCode":0,"data":{}}`)), Header: make(http.Header)}, nil
	})
	for i := 0; i < 2; i++ {
		if _, err := f.Post(registrationContext(context.Background(), id), &model.RouteResponse{ServerID: "a", Domains: []string{"tenant.example"}}, "/chat/account/register", map[string]string{"verifyCode": "123456"}, "", ""); err != nil {
			t.Fatal(err)
		}
	}
}
func TestRegistrationRequiresConfiguredPerTenantSecret(t *testing.T) {
	f := NewHTTPForwarder(map[string]string{"a": strings.Repeat("s", 32)})
	if f.RegistrationConfigured("b") {
		t.Fatal("another tenant inherited a secret")
	}
	if _, err := ParseUpstreamSecrets(`{"a":"too-short"}`, ""); err == nil {
		t.Fatal("weak secret accepted")
	}
	id, err := registrationID()
	if err != nil || len(id) != 36 || id[14] != '4' || !strings.ContainsRune("89ab", rune(id[19])) {
		t.Fatal("registration ID is not UUIDv4")
	}
}
