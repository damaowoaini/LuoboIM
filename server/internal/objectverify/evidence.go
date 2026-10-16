// Package objectverify validates completed, owner-bound complaint evidence.
// It performs metadata lookups only; it never fetches a supplied URL.
package objectverify

import (
	"context"
	"errors"
	"mime"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify/pb"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/mongo"
)

const HTTPPath = "/internal/object/verify_evidence"
const MaxImages = 9
const MaxImageBytes = 20 * 1024 * 1024

type ObjectReader interface {
	Take(context.Context, string, string) (*relation.ObjectModel, error)
}

type Verifier struct {
	Objects ObjectReader
	Engine  string
	// APIBase is the exact stable URL prefix issued by Third's apiAddress,
	// including its trailing /object/. Provider presigned URLs are not aliases.
	APIBase string
}

func invalidEvidence() error { return errs.ErrArgs.Wrap("投诉图片不可用或不属于提交人") }

func CheckRequest(req *pb.VerifyRequest) error {
	if req == nil || len(req.OwnerUserId) == 0 || len(req.OwnerUserId) > 64 || !utf8.ValidString(req.OwnerUserId) || strings.TrimSpace(req.OwnerUserId) != req.OwnerUserId || strings.ContainsAny(req.OwnerUserId, "\x00\r\n\t") || len(req.Urls) < 1 || len(req.Urls) > MaxImages {
		return invalidEvidence()
	}
	seen := make(map[string]bool, len(req.Urls))
	for _, raw := range req.Urls {
		if len(raw) == 0 || len(raw) > 4096 || !utf8.ValidString(raw) || seen[raw] || strings.TrimSpace(raw) != raw {
			return invalidEvidence()
		}
		seen[raw] = true
	}
	return nil
}

// CanonicalObjectURL is also used by Third when it returns completed aliases.
// Escape exactly once as a URL path: names remain metadata, never URL syntax.
func CanonicalObjectURL(base, name string) string {
	return base + (&url.URL{Path: name}).EscapedPath()
}

func objectName(base, raw string) (string, error) {
	b, err := url.Parse(base)
	if err != nil || b.Scheme != "https" || b.Host == "" || b.User != nil || b.RawPath != "" || b.RawQuery != "" || b.ForceQuery || b.Fragment != "" || !strings.HasSuffix(b.Path, "/object/") || path.Clean(b.Path)+"/" != b.Path {
		return "", errs.ErrInternalServer.Wrap("投诉图片校验配置无效")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != b.Scheme || u.Host != b.Host || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || !strings.HasPrefix(u.Path, b.Path) {
		return "", invalidEvidence()
	}
	// url.Parse performs the one and only path decode. Compare the exact path
	// against its canonical escaped form; never PathUnescape a second time.
	name := strings.TrimPrefix(u.Path, b.Path)
	if len(name) == 0 || len(name) > 1024 || !utf8.ValidString(name) || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(name, "/") || strings.ContainsAny(name, "\\") || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return "", invalidEvidence()
	}
	// Preserve already-issued, unescaped Unicode/space aliases when they are
	// unambiguous. %, ? and # names must use the new canonical escaped spelling.
	legacy := base + name
	legacyAllowed := !strings.ContainsAny(name, "%?#") && raw == legacy
	if raw != CanonicalObjectURL(base, name) && !legacyAllowed {
		return "", invalidEvidence()
	}
	return name, nil
}

func imageContentType(raw string) bool {
	typ, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return false
	}
	switch strings.ToLower(typ) {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/heic", "image/heif", "image/avif":
		return true
	}
	return false
}

func (v *Verifier) Verify(ctx context.Context, req *pb.VerifyRequest) (*pb.VerifyResponse, error) {
	if err := CheckRequest(req); err != nil {
		return nil, err
	}
	if v.Objects == nil || v.Engine == "" {
		return nil, errs.ErrInternalServer.Wrap("投诉图片校验暂不可用")
	}
	// Validate the whole URL batch before the first storage lookup.
	names := make([]string, len(req.Urls))
	seenNames := make(map[string]bool, len(req.Urls))
	for i, raw := range req.Urls {
		name, err := objectName(v.APIBase, raw)
		if err != nil {
			return nil, err
		}
		if seenNames[name] {
			return nil, invalidEvidence()
		}
		seenNames[name] = true
		names[i] = name
	}
	for _, name := range names {
		obj, err := v.Objects.Take(ctx, v.Engine, name)
		if err != nil {
			if errs.ErrRecordNotFound.Is(err) || errors.Is(errs.Unwrap(err), mongo.ErrNoDocuments) {
				return nil, invalidEvidence()
			}
			return nil, errs.ErrInternalServer.Wrap("投诉图片校验暂不可用")
		}
		// Names and path prefixes are never evidence of ownership. Only a completed
		// persisted alias with an explicit owner and current storage engine counts.
		if obj == nil || obj.UserID != req.OwnerUserId || obj.Engine != v.Engine || obj.Name != name || obj.Key == "" || obj.Size <= 0 || obj.Size > MaxImageBytes || obj.CreateTime.IsZero() || !imageContentType(obj.ContentType) {
			return nil, invalidEvidence()
		}
	}
	return &pb.VerifyResponse{Valid: true}, nil
}
