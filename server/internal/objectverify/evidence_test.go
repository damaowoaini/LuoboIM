package objectverify

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/objectverify/pb"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/mongo"
)

type objectFixture struct {
	objects map[string]*relation.ObjectModel
	calls   int
	fail    error
}

func (f *objectFixture) Take(_ context.Context, engine, name string) (*relation.ObjectModel, error) {
	f.calls++
	if f.fail != nil {
		return nil, f.fail
	}
	if o := f.objects[name]; o != nil && o.Engine == engine {
		copy := *o
		return &copy, nil
	}
	return nil, errs.ErrRecordNotFound.Wrap()
}
func completed(name, owner string) *relation.ObjectModel {
	return &relation.ObjectModel{Name: name, UserID: owner, Engine: "fixture", Key: "provider-private-key", Size: 12, ContentType: "image/png", CreateTime: time.Unix(1800000000, 0)}
}
func TestCompletedOwnerMetadataBatchAndImageBoundaries(t *testing.T) {
	base := "https://media.fixture.invalid/api/object/"
	for _, mime := range []string{"image/jpeg", "image/png", "image/gif", "image/webp", "image/bmp", "image/heic", "image/heif", "image/avif", "image/png; synthetic=value"} {
		t.Run(mime, func(t *testing.T) {
			db := &objectFixture{objects: map[string]*relation.ObjectModel{}}
			req := &pb.VerifyRequest{OwnerUserId: "owner"}
			for i := 0; i < MaxImages; i++ {
				name := "owner/" + strings.Repeat("x", i+1) + ".png"
				obj := completed(name, "owner")
				obj.ContentType = mime
				obj.Size = MaxImageBytes
				db.objects[name] = obj
				req.Urls = append(req.Urls, base+name)
			}
			resp, err := (&Verifier{Objects: db, Engine: "fixture", APIBase: base}).Verify(context.Background(), req)
			if err != nil || resp == nil || !resp.Valid || db.calls != MaxImages {
				t.Fatal("valid complete image batch rejected")
			}
		})
	}
}
func TestUnownedIncompleteWrongTypeAndOversizeFailClosed(t *testing.T) {
	base, name := "https://media.fixture.invalid/api/object/", "owner/evidence.png"
	for _, tc := range []struct {
		name   string
		change func(*relation.ObjectModel)
	}{
		{"foreign-owner", func(o *relation.ObjectModel) { o.UserID = "peer" }},
		{"missing-legacy-owner", func(o *relation.ObjectModel) { o.UserID = "" }},
		{"missing-key", func(o *relation.ObjectModel) { o.Key = "" }},
		{"missing-completion-time", func(o *relation.ObjectModel) { o.CreateTime = time.Time{} }},
		{"empty", func(o *relation.ObjectModel) { o.Size = 0 }},
		{"negative", func(o *relation.ObjectModel) { o.Size = -1 }},
		{"over20MiB", func(o *relation.ObjectModel) { o.Size = MaxImageBytes + 1 }},
		{"file-disguised-extension", func(o *relation.ObjectModel) { o.ContentType = "application/pdf" }},
		{"active-svg", func(o *relation.ObjectModel) { o.ContentType = "image/svg+xml" }},
		{"invalid-MIME", func(o *relation.ObjectModel) { o.ContentType = "image/png; broken" }},
		{"wrong-alias", func(o *relation.ObjectModel) { o.Name = "peer/evidence.png" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := completed(name, "owner")
			tc.change(o)
			v := &Verifier{Objects: &objectFixture{objects: map[string]*relation.ObjectModel{name: o}}, Engine: "fixture", APIBase: base}
			_, err := v.Verify(context.Background(), &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{base + name}})
			if err == nil || !errs.ErrArgs.Is(err) {
				t.Fatal("invalid evidence was accepted or leaked storage error")
			}
		})
	}
	for _, db := range []*objectFixture{{objects: map[string]*relation.ObjectModel{}}, {fail: errors.New("private Mongo DSN must not escape")}} {
		_, err := (&Verifier{Objects: db, Engine: "fixture", APIBase: base}).Verify(context.Background(), &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{base + name}})
		if err == nil || strings.Contains(err.Error(), "DSN") {
			t.Fatal("missing/storage failure accepted or leaked")
		}
	}
	_, err := (&Verifier{Objects: &objectFixture{fail: errs.Wrap(mongo.ErrNoDocuments)}, Engine: "fixture", APIBase: base}).Verify(context.Background(), &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{base + name}})
	if err == nil || !errs.ErrArgs.Is(err) {
		t.Fatal("real Mongo missing-record envelope did not remain a generic invalid batch")
	}
}
func TestCanonicalURLValidationPrecedesAllStorageLookups(t *testing.T) {
	base := "https://media.fixture.invalid/api/object/"
	for _, raw := range []string{
		"http://media.fixture.invalid/api/object/owner/a.png",
		"https://foreign.invalid/api/object/owner/a.png",
		"https://media.fixture.invalid.evil.test/api/object/owner/a.png",
		"https://x@media.fixture.invalid/api/object/owner/a.png",
		base + "owner/a.png?download=1", base + "owner/a.png?", base + "owner/a.png#fragment",
		base + "owner/../peer/a.png", base + "owner//a.png", base + "owner/./a.png", base + "/owner/a.png",
		base + "owner/%2e%2e/peer/a.png", base + "owner%2Fa.png", base + "owner/%61.png", base + "owner/a\\b.png",
		base + "owner/a\r\nb.png", base + "owner/a\x00.png", base + "owner/a.png ", base,
	} {
		t.Run(raw, func(t *testing.T) {
			db := &objectFixture{objects: map[string]*relation.ObjectModel{"owner/good.png": completed("owner/good.png", "owner")}}
			_, err := (&Verifier{Objects: db, Engine: "fixture", APIBase: base}).Verify(context.Background(), &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{base + "owner/good.png", raw}})
			if err == nil || db.calls != 0 {
				t.Fatal("noncanonical URL reached storage or was accepted")
			}
		})
	}
}

func TestThirdIssuedLiteralAndCanonicalEscapedNamesResolveExactlyOnce(t *testing.T) {
	base := "https://media.fixture.invalid/api/object/"
	for _, tc := range []struct {
		name, canonical string
		legacy          bool
	}{
		{"owner/evidence.png", "owner/evidence.png", true},
		{"owner/中文 证据.png", "owner/%E4%B8%AD%E6%96%87%20%E8%AF%81%E6%8D%AE.png", true},
		{"owner/a b.png", "owner/a%20b.png", true},
		{"owner/a+b.png", "owner/a+b.png", true},
		{"owner/emoji-🥕.png", "owner/emoji-%F0%9F%A5%95.png", true},
		{"owner/a#b?.png", "owner/a%23b%3F.png", false},
		{"owner/50%.png", "owner/50%25.png", false},
		{"owner/literal%2Fname.png", "owner/literal%252Fname.png", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanonicalObjectURL(base, tc.name); got != base+tc.canonical {
				t.Fatal("Third URL path escaping changed the logical object name")
			}
			obj := completed(tc.name, "owner")
			obj.Key = "temp/20261015/unrelated-provider-key-with-no-alias-name"
			db := &objectFixture{objects: map[string]*relation.ObjectModel{tc.name: obj}}
			v := &Verifier{Objects: db, Engine: "fixture", APIBase: base}
			forms := []string{base + tc.canonical}
			if tc.legacy {
				forms = append(forms, base+tc.name)
			}
			for _, raw := range forms {
				name, err := objectName(base, raw)
				if err != nil || name != tc.name {
					t.Fatal("single-decode canonical/literal name mismatch")
				}
				if r, e := v.Verify(context.Background(), &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{raw}}); e != nil || r == nil || !r.Valid {
					t.Fatal("issued URL did not reach its exact completed alias metadata")
				}
			}
		})
	}
}

func TestEscapedURLAliasesCannotChangeObjectIdentityOrDuplicateEvidence(t *testing.T) {
	base := "https://media.fixture.invalid/api/object/"
	name := "owner/中文 证据.png"
	db := &objectFixture{objects: map[string]*relation.ObjectModel{name: completed(name, "owner"), "owner/literal%2Fname.png": completed("owner/literal%2Fname.png", "owner")}}
	v := &Verifier{Objects: db, Engine: "fixture", APIBase: base}
	if _, err := v.Verify(context.Background(), &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{base + name, CanonicalObjectURL(base, name)}}); err == nil || db.calls != 0 {
		t.Fatal("two URL spellings of one exact alias bypassed deduplication")
	}
	for _, raw := range []string{
		base + "owner/literal%2Fname.png", // decoded slash, never literal %2F
		base + "owner/a%2fb.png", base + "owner/%2e%2e/peer/a.png", base + "owner/%00.png",
		base + "owner/a#b?.png", base + "owner/a%23b.png?download=1",
		base + "temp/20261015/unrelated-provider-key?X-Amz-Signature=synthetic",
		"https://objects.fixture.invalid/bucket/temp/key?X-Amz-Signature=synthetic",
	} {
		before := db.calls
		if _, err := v.Verify(context.Background(), &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{raw}}); err == nil || db.calls != before {
			t.Fatal("URL alias/provider/delimiter ambiguity reached metadata or was accepted")
		}
	}
	// The same logical name in another active engine is not a matching alias.
	if _, err := (&Verifier{Objects: db, Engine: "other-engine", APIBase: base}).Verify(context.Background(), &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{CanonicalObjectURL(base, name)}}); err == nil {
		t.Fatal("different storage engine metadata accepted")
	}
}
func TestBoundsAndMissingConfigurationNeverAuthorize(t *testing.T) {
	valid := &pb.VerifyRequest{OwnerUserId: "owner", Urls: []string{"https://media.fixture.invalid/api/object/owner/a.png"}}
	for _, req := range []*pb.VerifyRequest{nil, {}, {OwnerUserId: "owner"}, {OwnerUserId: "", Urls: valid.Urls}, {OwnerUserId: strings.Repeat("u", 65), Urls: valid.Urls}, {OwnerUserId: "owner", Urls: []string{valid.Urls[0], valid.Urls[0]}}, {OwnerUserId: "owner", Urls: []string{strings.Repeat("x", 4097)}}, {OwnerUserId: "owner", Urls: make([]string, 10)}} {
		if CheckRequest(req) == nil {
			t.Fatal("invalid bounds accepted")
		}
	}
	for _, base := range []string{"", "http://fixture.invalid/api/object/", "https://user@fixture.invalid/api/object/", "https://fixture.invalid/api/object/?key=x", "https://fixture.invalid/api/../object/"} {
		v := &Verifier{Objects: &objectFixture{}, Engine: "fixture", APIBase: base}
		if _, err := v.Verify(context.Background(), valid); err == nil {
			t.Fatal("invalid server base configuration accepted")
		}
	}
}
