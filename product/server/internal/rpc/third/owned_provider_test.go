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
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3"
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

// Exercise the actual locked COS decoder and provider, not a replacement
// RoundTripper or a fake SDK result. Each original owner gets one response.
func TestOwnedCOSMultipartResponseRequiresOriginalTargetAndActualUploadIdentifier(t *testing.T) {
	previous := config.Config
	defer func() { config.Config = previous }()
	const key = "synthetic-folder/object 空格 "
	const upload = "synthetic-original-upload"
	valid := `<InitiateMultipartUploadResult><Bucket>synthetic-owned</Bucket><Key>` + key + `</Key><UploadId>` + upload + `</UploadId></InitiateMultipartUploadResult>`
	cases := []struct {
		name, body string
		valid      bool
	}{
		{"non-xml-http200", "this is not a valid multipart initialization response", false},
		{"empty-http200", "", false},
		{"missing-upload-id", strings.Replace(valid, "<UploadId>"+upload+"</UploadId>", "", 1), false},
		{"blank-upload-id", strings.Replace(valid, upload, " ", 1), false},
		{"missing-bucket", strings.Replace(valid, "<Bucket>synthetic-owned</Bucket>", "", 1), false},
		{"missing-key", strings.Replace(valid, "<Key>"+key+"</Key>", "", 1), false},
		{"foreign-key", strings.Replace(valid, key, "synthetic-foreign-target", 1), false},
		{"complete-original-response", valid, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != "/"+key || !r.URL.Query().Has("uploads") {
					t.Error("actual SDK changed the original multipart target")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer endpoint.Close()
			config.Config.Object.Cos.BucketURL = endpoint.URL
			config.Config.Object.Cos.SecretID = "synthetic-access"
			config.Config.Object.Cos.SecretKey = "synthetic-secret"
			config.Config.Object.Cos.SessionToken = ""
			lifetime, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			source := &ownedsource.Observer{}
			parent, err := ownedsource.Bind(lifetime, source)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := newThirdObjectTransport(parent, "cos")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				if err := transport.Close(ctx); err != nil {
					t.Error(err)
				}
			}()
			provider, err := newThirdOwnedProvider(parent, "cos", nil, transport)
			if err != nil {
				t.Fatal(err)
			}
			result, err := provider.InitiateMultipartUpload(parent, key)
			if calls.Load() != 1 {
				t.Fatal("actual SDK replayed a multipart mutation", calls.Load())
			}
			if tc.valid {
				if err != nil || result == nil || result.UploadID != upload || result.Key != key || result.Bucket != "synthetic-owned" || source.Snapshot().Unconfirmed() {
					t.Fatal("complete original acknowledgement was rejected", err)
				}
			} else {
				if err == nil || result != nil || !source.Snapshot().Unconfirmed() {
					t.Fatal("invalid HTTP200 became an acknowledged source mutation", err)
				}
				if _, err := provider.InitiateMultipartUpload(parent, key); err == nil || calls.Load() != 1 || !source.Snapshot().Unconfirmed() {
					t.Fatal("original uncertain operation was repeated or cleared")
				}
			}
		})
	}
}

func TestOwnedCOSCopyAndCompletionRequireActualAcknowledgementFields(t *testing.T) {
	previous := config.Config
	defer func() { config.Config = previous }()
	const key = "synthetic-folder/original object "
	const sourceKey = "synthetic-source"
	const upload = "synthetic-original-upload"
	const tag = `"synthetic-etag"`
	complete := `<CompleteMultipartUploadResult><Location>https://synthetic.invalid/object</Location><Bucket>synthetic-owned</Bucket><Key>` + key + `</Key><ETag>` + tag + `</ETag></CompleteMultipartUploadResult>`
	copyBody := `<CopyObjectResult><ETag>` + tag + `</ETag><LastModified>2026-10-17T00:00:00Z</LastModified></CopyObjectResult>`
	cases := []struct {
		name, operation, body string
		valid                 bool
	}{
		{"completion-empty", "complete", "", false},
		{"completion-missing-location", "complete", strings.Replace(complete, "<Location>https://synthetic.invalid/object</Location>", "", 1), false},
		{"completion-missing-bucket", "complete", strings.Replace(complete, "<Bucket>synthetic-owned</Bucket>", "", 1), false},
		{"completion-missing-key", "complete", strings.Replace(complete, "<Key>"+key+"</Key>", "", 1), false},
		{"completion-foreign-key", "complete", strings.Replace(complete, key, "synthetic-other", 1), false},
		{"completion-missing-etag", "complete", strings.Replace(complete, "<ETag>"+tag+"</ETag>", "", 1), false},
		{"completion-blank-etag", "complete", strings.Replace(complete, tag, `" "`, 1), false},
		{"completion-original-response", "complete", complete, true},
		{"copy-non-xml", "copy", "not an XML acknowledgement", false},
		{"copy-empty-result", "copy", "<CopyObjectResult/>", false},
		{"copy-missing-etag", "copy", strings.Replace(copyBody, "<ETag>"+tag+"</ETag>", "", 1), false},
		{"copy-blank-etag", "copy", strings.Replace(copyBody, tag, `" "`, 1), false},
		{"copy-original-response", "copy", copyBody, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.URL.Path != "/"+key ||
					(tc.operation == "complete" && (r.Method != http.MethodPost || r.URL.Query().Get("uploadId") != upload)) ||
					(tc.operation == "copy" && (r.Method != http.MethodPut || r.Header.Get("X-Cos-Copy-Source") != r.Host+"/"+sourceKey)) {
					t.Error("actual SDK changed original object operation target")
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer endpoint.Close()
			config.Config.Object.Cos.BucketURL = endpoint.URL
			config.Config.Object.Cos.SecretID = "synthetic-access"
			config.Config.Object.Cos.SecretKey = "synthetic-secret"
			config.Config.Object.Cos.SessionToken = ""
			lifetime, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			observer := &ownedsource.Observer{}
			parent, err := ownedsource.Bind(lifetime, observer)
			if err != nil {
				t.Fatal(err)
			}
			transport, err := newThirdObjectTransport(parent, "cos")
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				ctx, stop := context.WithTimeout(context.Background(), time.Second)
				defer stop()
				if err := transport.Close(ctx); err != nil {
					t.Error(err)
				}
			}()
			provider, err := newThirdOwnedProvider(parent, "cos", nil, transport)
			if err != nil {
				t.Fatal(err)
			}
			invoke := func() (exists, exact bool, err error) {
				if tc.operation == "complete" {
					result, e := provider.CompleteMultipartUpload(parent, upload, key, []s3.Part{{PartNumber: 1, ETag: tag}})
					return result != nil, result != nil && result.Key == key && result.Bucket == "synthetic-owned" && result.Location == "https://synthetic.invalid/object" && result.ETag == tag, e
				}
				result, e := provider.CopyObject(parent, sourceKey, key)
				return result != nil, result != nil && result.Key == key && result.ETag == "synthetic-etag", e
			}
			exists, exact, err := invoke()
			if calls.Load() != 1 {
				t.Fatal("SDK replayed original object mutation", calls.Load())
			}
			if tc.valid {
				if err != nil || !exists || !exact || observer.Snapshot().Unconfirmed() {
					t.Fatal("original successful object response was rejected", err)
				}
			} else {
				if err == nil || exists || !observer.Snapshot().Unconfirmed() {
					t.Fatal("missing acknowledgement fields confirmed an object mutation", err)
				}
				if exists, _, err := invoke(); err == nil || exists || calls.Load() != 1 || !observer.Snapshot().Unconfirmed() {
					t.Fatal("unconfirmed object operation was replayed or cleared")
				}
			}
		})
	}
}
