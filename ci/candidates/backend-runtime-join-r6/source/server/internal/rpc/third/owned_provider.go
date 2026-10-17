package third

import (
	"context"
	"net/http"

	miniov7 "github.com/minio/minio-go/v7"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/cos"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/minio"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/oss"
)

func newThirdObjectTransport(parent context.Context, engine string) (*s3.OwnedTransport, error) {
	var template *http.Transport
	if engine == "minio" {
		// This fixed SDK function preserves MinIO's CA and compression semantics.
		// Secure=true keeps the same verified TLS defaults for a distinct sign host.
		var err error
		template, err = miniov7.DefaultTransport(true)
		if err != nil {
			return nil, err
		}
	} else {
		if engine != "cos" && engine != "oss" {
			return nil, s3.ErrOwnedObjectLifecycle
		}
		var ok bool
		template, ok = http.DefaultTransport.(*http.Transport)
		if !ok {
			return nil, s3.ErrOwnedObjectLifecycle
		}
	}
	return s3.NewOwnedTransport(parent, template)
}
func newThirdOwnedProvider(parent context.Context, engine string, cache cache.MinioCache, transport *s3.OwnedTransport) (s3.Interface, error) {
	switch engine {
	case "minio":
		return minio.NewOwnedMinio(parent, cache, transport)
	case "cos":
		return cos.NewOwnedCos(parent, transport)
	case "oss":
		return oss.NewOwnedOSS(parent, transport)
	default:
		return nil, s3.ErrOwnedObjectLifecycle
	}
}
