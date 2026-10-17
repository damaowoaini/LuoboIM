package groupeffects

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Actual network access in this file is only the task-owned in-process
// httptest loopback callback and explicitly selected temporary task Mongo DB.
func verifyFixtureCallback(t *testing.T, r *http.Request, key []byte) bool {
	t.Helper()
	body, err := io.ReadAll(io.LimitReader(r.Body, MaxPayload+1))
	if err != nil || len(body) > MaxPayload {
		return false
	}
	command := strings.TrimPrefix(r.URL.Path, "/")
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(r.Header.Get("X-OpenIM-Callback-Timestamp") + "\n" + r.Header.Get("X-OpenIM-Callback-Nonce") + "\n" + command + "\n"))
	mac.Write(body)
	sig, err := hex.DecodeString(r.Header.Get("X-OpenIM-Callback-Signature"))
	return err == nil && hmac.Equal(sig, mac.Sum(nil)) && validID(r.Header.Get("X-OpenIM-Effect-ID")) && command == callbackCommand(AfterQuit)
}
func TestGroupEffectsMongoCallbackLostACKRequiresInspectionAndExplicitConfirmation(t *testing.T) {
	ctx, authority, s, db := effectsMongo(t, nil)
	key := []byte("synthetic callback signer")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verifyFixtureCallback(t, r, key) {
			http.Error(w, "fixture rejected", 401)
			return
		}
		requests.Add(1) // Definitively applied by this synthetic remote endpoint.
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error("fixture hijack failed", err)
			return
		}
		if err := connection.Close(); err != nil {
			t.Error("fixture callback connection cleanup failed", err)
		} // No response ACK reaches the sender.
	}))
	t.Cleanup(server.Close)
	endpoint, err := NewEndpoint(server.URL, db.Name(), AfterQuit, key, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f := prepareQueuedCallback(t, ctx, authority, s, db, endpoint)
	if err := s.DeliverCallback(ctx, f.ID, 1, f.PayloadSHA256, endpoint); !errors.Is(err, ErrUnconfirmed) {
		t.Fatal("lost callback ACK was called success/no-write", err)
	}
	summary, err := s.Inspect(ctx, f.ID)
	if err != nil || summary.State != Unconfirmed || summary.Attempts != 1 || requests.Load() != 1 {
		t.Fatal("lost ACK did not retain exact durable obligation", err)
	}
	if err := s.DeliverCallback(ctx, f.ID, summary.Revision, f.PayloadSHA256, endpoint); !errors.Is(err, ErrConflict) || requests.Load() != 1 {
		t.Fatal("unknown callback automatically replayed", err)
	}
	// The in-process receiver counter and joined/stopped sending call provide
	// this fixture's definitive outcome/drain evidence. Production operators
	// must obtain independent evidence, not infer it from an error/status code.
	summary, err = s.Confirm(ctx, Confirmation{ExpectedSenderInstanceID: s.InstanceID(), ID: f.ID, ExpectedRevision: summary.Revision, ExpectedPayloadSHA256: f.PayloadSHA256, Decision: "applied", EvidenceSHA256: digest([]byte("synthetic remote recorded exactly one effect")), DrainedEvidenceSHA256: digest([]byte("synthetic DeliverCallback returned; no worker owns claim"))})
	if err != nil || summary.State != Done || requests.Load() != 1 {
		t.Fatal("verified applied outcome could not close the exact obligation", err)
	}
}
func TestGroupEffectsMongoCallbackPermissionAndRedirectRemainUnknownWithoutReplay(t *testing.T) {
	for _, mode := range []string{"401-after-application", "307-do-not-follow"} {
		t.Run(mode, func(t *testing.T) {
			ctx, authority, s, db := effectsMongo(t, nil)
			key := []byte("synthetic callback signer")
			var requests, redirected atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Add(1) }))
			t.Cleanup(target.Close)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !verifyFixtureCallback(t, r, key) {
					http.Error(w, "fixture rejected", 401)
					return
				}
				requests.Add(1)
				if mode == "401-after-application" {
					http.Error(w, "synthetic after-effect auth rejection", 401)
				} else {
					w.Header().Set("Location", target.URL)
					w.WriteHeader(307)
				}
			}))
			t.Cleanup(server.Close)
			endpoint, err := NewEndpoint(server.URL, db.Name(), AfterQuit, key, time.Second)
			if err != nil {
				t.Fatal(err)
			}
			f := prepareQueuedCallback(t, ctx, authority, s, db, endpoint)
			if err := s.DeliverCallback(ctx, f.ID, 1, f.PayloadSHA256, endpoint); !errors.Is(err, ErrUnconfirmed) {
				t.Fatal("post-dispatch response was treated as definite no-write", err)
			}
			state, err := s.Inspect(ctx, f.ID)
			if err != nil || state.State != Unconfirmed || requests.Load() != 1 || redirected.Load() != 0 {
				t.Fatal("permission/redirect caused replay or false completion", err)
			}
		})
	}
}
func TestGroupEffectsMongoCallbackACKCompletesAndConfigMismatchNeverClaims(t *testing.T) {
	ctx, authority, s, db := effectsMongo(t, nil)
	key := []byte("synthetic callback signer")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !verifyFixtureCallback(t, r, key) {
			http.Error(w, "fixture rejected", 401)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(server.Close)
	endpoint, err := NewEndpoint(server.URL, db.Name(), AfterQuit, key, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	f := prepareQueuedCallback(t, ctx, authority, s, db, endpoint)
	wrong, err := NewEndpoint(server.URL, "another-tenant", AfterQuit, key, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeliverCallback(ctx, f.ID, 1, f.PayloadSHA256, wrong); !errors.Is(err, ErrInvalid) {
		t.Fatal("foreign endpoint namespace accepted", err)
	}
	before, err := s.Inspect(ctx, f.ID)
	if err != nil || before.State != Pending || before.Revision != 1 || before.Attempts != 0 || requests.Load() != 0 {
		t.Fatal("endpoint mismatch consumed pending intent", err)
	}
	if err := s.DeliverCallback(ctx, f.ID, 1, f.PayloadSHA256, endpoint); err != nil {
		t.Fatal(err)
	}
	after, err := s.Inspect(ctx, f.ID)
	if err != nil || after.State != Done || after.Attempts != 1 || requests.Load() != 1 {
		t.Fatal("actual validated callback ACK failed to finish", err)
	}
	if err := s.DeliverCallback(context.Background(), f.ID, after.Revision, f.PayloadSHA256, endpoint); !errors.Is(err, ErrConflict) || requests.Load() != 1 {
		t.Fatal("completed callback replayed", err)
	}
}
