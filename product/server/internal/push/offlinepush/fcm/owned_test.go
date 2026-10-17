package fcm

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	http2 "github.com/openimsdk/open-im-server/v3/pkg/common/http"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type ownedFCMCacheFixture struct{ cache.MsgModel }

func (ownedFCMCacheFixture) GetFcmToken(_ context.Context, _ string, platform int) (string, error) {
	return fmt.Sprintf("recipient-%d", platform), nil
}
func (ownedFCMCacheFixture) GetUserBadgeUnreadCountSum(context.Context, string) (int, error) {
	return 0, nil
}
func TestOwnedFCMActualOAuthBatchClientPreservesACKAndStopsSDKRetryAfterUnknown(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"ack", "bad-batch", "server-error"} {
		t.Run(kind, func(t *testing.T) {
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			original, later := &ownedsource.Observer{}, &ownedsource.Observer{}
			ctx, err := ownedsource.Bind(parent, original)
			if err != nil {
				t.Fatal(err)
			}
			var tokens, batches atomic.Int32
			var observedParts atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/token":
					tokens.Add(1)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"access_token":"owned-test-token","token_type":"Bearer","expires_in":3600}`)
				case "/batch":
					batches.Add(1)
					_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
					if err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					reader := multipart.NewReader(r.Body, params["boundary"])
					count := 0
					for {
						part, err := reader.NextPart()
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Error(err)
							w.WriteHeader(400)
							return
						}
						if _, err := io.Copy(io.Discard, part); err != nil {
							t.Error(err)
							return
						}
						count++
					}
					observedParts.Store(int32(count))
					if kind == "server-error" {
						w.WriteHeader(503)
						fmt.Fprint(w, "unconfirmed")
						return
					}
					if kind == "bad-batch" {
						w.Header().Set("Content-Type", "multipart/mixed; boundary=owned")
						fmt.Fprint(w, "malformed")
						return
					}
					w.Header().Set("Content-Type", "multipart/mixed; boundary=owned")
					for i := 0; i < count; i++ {
						fmt.Fprintf(w, "--owned\r\nContent-Type: application/http\r\n\r\nHTTP/1.1 200 OK\r\nContent-Type: application/json\r\n\r\n{\"name\":\"projects/owned/messages/%d\"}\r\n", i)
					}
					fmt.Fprint(w, "--owned--\r\n")
				default:
					t.Error("unexpected SDK request")
					w.WriteHeader(400)
				}
			}))
			template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "owned-loopback-only"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"oauth2.googleapis.com", "fcm.googleapis.com"}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
			der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
			if err != nil {
				t.Fatal(err)
			}
			server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
			server.StartTLS()
			defer server.Close()
			certificate, err := x509.ParseCertificate(der)
			if err != nil {
				t.Fatal(err)
			}
			roots := x509.NewCertPool()
			roots.AddCert(certificate)
			previous := http.DefaultTransport
			transport := previous.(*http.Transport).Clone()
			transport.Proxy = nil
			transport.DialTLSContext = func(current context.Context, network, addr string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(addr)
				if err != nil || (host != "oauth2.googleapis.com" && host != "fcm.googleapis.com") {
					return nil, fmt.Errorf("unexpected fixture host")
				}
				dialer := &tls.Dialer{NetDialer: &net.Dialer{}, Config: &tls.Config{RootCAs: roots, ServerName: host, MinVersion: tls.VersionTLS12}}
				return dialer.DialContext(current, "tcp", server.Listener.Addr().String())
			}
			http.DefaultTransport = transport
			defer func() { http.DefaultTransport = previous; transport.CloseIdleConnections() }()
			httpOwner, err := http2.NewOwnedPushHTTP(ctx, cancel)
			if err != nil {
				t.Fatal(err)
			}
			defer httpOwner.Close()
			directory := t.TempDir()
			if err := os.Chmod(directory, 0700); err != nil {
				t.Fatal(err)
			}
			private, err := x509.MarshalPKCS8PrivateKey(key)
			if err != nil {
				t.Fatal(err)
			}
			credentials := map[string]string{"type": "service_account", "project_id": "owned", "private_key_id": "fixture", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})), "client_email": "owned@owned.iam.gserviceaccount.com", "client_id": "123", "token_uri": "https://oauth2.googleapis.com/token"}
			raw, err := json.Marshal(credentials)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "credentials.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			client, err := newOwnedClient(ctx, ownedFCMCacheFixture{}, httpOwner, cancel, path)
			if err != nil {
				t.Fatal(err)
			}
			laterCtx, laterCancel := context.WithCancel(context.Background())
			defer laterCancel()
			laterCtx, err = ownedsource.Bind(laterCtx, later)
			if err != nil {
				t.Fatal(err)
			}
			opts := &offlinepush.Opts{Signal: &offlinepush.Signal{ClientMsgID: "original"}}
			err = client.Push(laterCtx, []string{"actual-recipient"}, "title", "content", opts)
			if tokens.Load() != 1 || batches.Load() != 1 || observedParts.Load() != int32(len(Terminal)) || (err == nil) != (kind == "ack") || original.Snapshot().Unconfirmed() != (kind != "ack") || later.Snapshot().PushDelivery.Started != 0 {
				t.Fatal("actual SDK/OAuth/multipart/original owner outcome differs")
			}
			if kind != "ack" {
				_ = client.Push(laterCtx, []string{"actual-recipient"}, "title", "content", opts)
				if batches.Load() != 1 {
					t.Fatal("SDK or explicit caller replayed unknown batch")
				}
			}
		})
	}
}
