package third

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
)

func TestOwnedThirdActualProviderSDKsUsePrivateTransportAndRetainUnacknowledgedMutation(t *testing.T) {
	for _, engine := range []string{"minio", "cos", "oss"} {
		t.Run(engine, func(t *testing.T) {
			previous := config.Config
			defer func() { config.Config = previous }()
			var deletes, initiates atomic.Int32
			readEntered, readDone := make(chan struct{}), make(chan struct{})
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead && strings.HasSuffix(r.URL.Path, "/synthetic-block") {
					close(readEntered)
					<-r.Context().Done()
					close(readDone)
					return
				}
				switch r.Method {
				case http.MethodHead:
					w.WriteHeader(http.StatusOK)
				case http.MethodGet:
					w.Header().Set("Content-Type", "application/xml")
					_, _ = w.Write([]byte(`<LocationConstraint xmlns="http://s3.amazonaws.com/doc/2006-03-01/">us-east-1</LocationConstraint>`))
				case http.MethodDelete:
					deletes.Add(1)
					w.WriteHeader(http.StatusNoContent)
				case http.MethodPost:
					initiates.Add(1)
					w.Header().Set("Content-Type", "application/xml")
					_, _ = w.Write([]byte(`this is not a valid multipart initialization response`))
				default:
					t.Error("unexpected object-store method")
					w.WriteHeader(http.StatusBadRequest)
				}
			}))
			defer endpoint.Close()
			config.Config.Object.Minio.Endpoint = endpoint.URL
			config.Config.Object.Minio.SignEndpoint = ""
			config.Config.Object.Minio.Bucket = "synthetic-owned"
			config.Config.Object.Minio.PublicRead = false
			config.Config.Object.Minio.AccessKeyID = "synthetic-access"
			config.Config.Object.Minio.SecretAccessKey = "synthetic-secret"
			config.Config.Object.Minio.SessionToken = ""
			config.Config.Object.Cos.BucketURL = endpoint.URL
			config.Config.Object.Cos.SecretID = "synthetic-access"
			config.Config.Object.Cos.SecretKey = "synthetic-secret"
			config.Config.Object.Cos.SessionToken = ""
			config.Config.Object.Oss.Endpoint = endpoint.URL
			config.Config.Object.Oss.BucketURL = endpoint.URL
			config.Config.Object.Oss.Bucket = "synthetic-owned"
			config.Config.Object.Oss.AccessKeyID = "synthetic-access"
			config.Config.Object.Oss.AccessKeySecret = "synthetic-secret"
			lifetime, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			source := &ownedsource.Observer{}
			parent, err := ownedsource.Bind(lifetime, source)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := newThirdObjectTransport(parent, engine)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := transport.Close(ctx); err != nil {
					t.Error(err)
				}
			}()
			foreignLifetime, stopForeign := context.WithCancel(context.Background())
			defer stopForeign()
			foreign, err := ownedsource.Bind(foreignLifetime, &ownedsource.Observer{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = newThirdOwnedProvider(foreign, engine, nil, transport); err == nil || deletes.Load() != 0 || initiates.Load() != 0 {
				t.Fatal("real provider accepted another owner before construction")
			}
			provider, err := newThirdOwnedProvider(parent, engine, nil, transport)
			if err != nil {
				t.Fatal("real SDK constructor", err)
			}
			readCtx, stopRead := context.WithCancel(parent)
			readResult := make(chan error, 1)
			go func() { _, err := provider.StatObject(readCtx, "synthetic-block"); readResult <- err }()
			select {
			case <-readEntered:
			case <-lifetime.Done():
				t.Fatal("real SDK read did not enter")
			}
			stopRead()
			select {
			case err := <-readResult:
				if err == nil {
					t.Fatal("cancelled real SDK read succeeded")
				}
			case <-lifetime.Done():
				t.Fatal("real SDK read ignored original RPC cancellation")
			}
			select {
			case <-readDone:
			case <-lifetime.Done():
				t.Fatal("real SDK read connection did not close")
			}
			if source.Snapshot().Unconfirmed() {
				t.Fatal("read cancellation invented a source mutation")
			}
			if err = provider.DeleteObject(parent, "synthetic-only"); err != nil || deletes.Load() != 1 || source.Snapshot().Unconfirmed() {
				t.Fatal("actual known SDK result differs", err, deletes.Load())
			}
			// The real SDK must reject this malformed HTTP200 response. The provider
			// result observer must retain uncertainty, not just a closed socket.
			if _, err = provider.InitiateMultipartUpload(parent, "synthetic-only"); err == nil || initiates.Load() != 1 || !source.Snapshot().Unconfirmed() {
				t.Fatal("real SDK failure was replayed or treated as ACK", err, initiates.Load())
			}
			if _, err = provider.InitiateMultipartUpload(parent, "synthetic-only"); err == nil || initiates.Load() != 1 {
				t.Fatal("same failed provider automatically issued another physical mutation")
			}
		})
	}
}
