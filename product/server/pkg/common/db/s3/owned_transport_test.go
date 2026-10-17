package s3

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
)

func ownedObjectFixture(t *testing.T) (context.Context, *ownedsource.Observer, *OwnedTransport) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	source := &ownedsource.Observer{}
	bound, err := ownedsource.Bind(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	template, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatal("fixed HTTP transport type")
	}
	transport, err := NewOwnedTransport(bound, template)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := transport.Close(closeCtx); err != nil {
			t.Error(err)
		}
	})
	return bound, source, transport
}
func TestOwnedObjectTransportCancelsAndJoinsActualPendingHTTPWithoutConfirmingRemoteMutation(t *testing.T) {
	entered, handlerDone := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(handlerDone)
	}))
	defer server.Close()
	ctx, source, transport := ownedObjectFixture(t)
	if _, err := NewOwnedTransport(context.Background(), http.DefaultTransport.(*http.Transport)); !errors.Is(err, ErrOwnedObjectLifecycle) {
		t.Fatal("unowned transport constructor accepted an ambient context")
	}
	client := &http.Client{Transport: transport}
	returned := make(chan error, 1)
	go func() {
		returned <- transport.Mutation(ctx, func(current context.Context) error {
			req, _ := http.NewRequestWithContext(current, http.MethodPut, server.URL+"/synthetic-object", strings.NewReader("synthetic payload"))
			response, err := client.Do(req)
			if response != nil {
				_ = response.Body.Close()
			}
			return err
		})
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("actual HTTP mutation did not enter")
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := transport.Close(closeCtx); err != nil {
		t.Fatal("actual transport failed to join", err)
	}
	select {
	case err := <-returned:
		if err == nil {
			t.Fatal("cancellation invented remote ACK")
		}
	case <-closeCtx.Done():
		t.Fatal("original operation did not return")
	}
	select {
	case <-handlerDone:
	case <-closeCtx.Done():
		t.Fatal("original remote connection remained active")
	}
	active, operations, closingBodies, unknown := transport.Snapshot()
	if active != 0 || operations != 0 || closingBodies != 0 || !unknown {
		t.Fatal("local close erased original uncertainty", active, operations, closingBodies, unknown)
	}
	snapshot := source.Snapshot()
	if !snapshot.Unconfirmed() || snapshot.ObjectStorageMutation.Started != 1 || snapshot.ObjectStorageMutation.InFlight != 0 {
		t.Fatal("original source observation differs", snapshot)
	}
	// A subsequent successful local close never converts the source to known.
	if err := transport.Close(closeCtx); err != nil || !source.Snapshot().Unconfirmed() {
		t.Fatal("second close became remote confirmation", err)
	}
}
func TestOwnedObjectTransportUsesOriginalOwnerAndRejectsSDKPhysicalReplayAfterAmbiguousResult(t *testing.T) {
	for _, name := range []string{"sdk-parsed-200-error", "second-physical-write", "distinct-multipart-parts"} {
		t.Run(name, func(t *testing.T) {
			var physical atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				physical.Add(1)
				if name == "sdk-parsed-200-error" {
					_, _ = w.Write([]byte("not-json"))
				} else {
					w.WriteHeader(http.StatusNoContent)
				}
			}))
			defer server.Close()
			original, source, transport := ownedObjectFixture(t)
			other := &ownedsource.Observer{}
			callCtx, err := ownedsource.Bind(original, other)
			// A private observer is immutable: a caller cannot replace the existing one.
			if err == nil || callCtx != nil {
				t.Fatal("original observer was replaced")
			}
			independent, cancel := context.WithCancel(context.Background())
			defer cancel()
			callCtx, err = ownedsource.Bind(independent, other)
			if err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Transport: transport}
			firstTarget, secondTarget := server.URL+"/synthetic-only", server.URL+"/synthetic-only"
			if name == "distinct-multipart-parts" {
				firstTarget += "?uploadId=synthetic&partNumber=1"
				secondTarget += "?uploadId=synthetic&partNumber=2"
			}
			err = transport.Mutation(callCtx, func(current context.Context) error {
				request, _ := http.NewRequestWithContext(current, http.MethodPut, firstTarget, strings.NewReader("synthetic"))
				response, err := client.Do(request)
				if err != nil {
					return err
				}
				defer response.Body.Close()
				if name == "sdk-parsed-200-error" {
					var body map[string]string
					return json.NewDecoder(response.Body).Decode(&body)
				}
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				retry, _ := http.NewRequestWithContext(current, http.MethodPut, secondTarget, strings.NewReader("synthetic"))
				again, err := client.Do(retry)
				if again != nil {
					_ = again.Body.Close()
				}
				return err
			})
			if name == "distinct-multipart-parts" {
				observed := source.Snapshot().ObjectStorageMutation
				if err != nil || physical.Load() != 2 || source.Snapshot().Unconfirmed() || observed.Started != 1 || observed.InFlight != 0 || other.Snapshot().ObjectStorageMutation.Started != 0 {
					t.Fatal("legal distinct part writes were blocked or transferred to another owner", err, physical.Load(), observed)
				}
				return
			}
			if err == nil || physical.Load() != 1 {
				t.Fatal("ambiguous original mutation replayed or invented ACK", err, physical.Load())
			}
			if !source.Snapshot().Unconfirmed() || other.Snapshot().Unconfirmed() || other.Snapshot().ObjectStorageMutation.Started != 0 {
				t.Fatal("old operation borrowed a later caller's owner")
			}
			if active, ops, bodies, _ := transport.Snapshot(); active != 0 || ops != 0 || bodies != 0 {
				t.Fatal("actual operation not joined", active, ops, bodies)
			}
			rejected := transport.Mutation(callCtx, func(context.Context) error { return errors.New("must not enter") })
			if !errors.Is(rejected, ErrOwnedObjectLifecycle) || physical.Load() != 1 {
				t.Fatal("unconfirmed transport reopened automatically")
			}
		})
	}
}
