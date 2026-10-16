package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/cont"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/intent"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
)

type aliasProofFixture struct {
	*intent.Store
	proof *intent.CompletionProof
	fail  bool
}

func (f *aliasProofFixture) FindCompleted(ctx context.Context, engine, hash string, size int64) (*intent.CompletionProof, error) {
	if f.fail {
		return nil, errors.New("synthetic proof storage failure")
	}
	if mcontext.GetOpUserID(ctx) != f.proof.Owner || engine != f.proof.Engine || hash != f.proof.Hash || size != f.proof.Size {
		return nil, errs.ErrNoPermission
	}
	p := *f.proof
	return &p, nil
}

type aliasProviderFixture struct {
	s3.Interface
	object      s3.ObjectInfo
	accessedKey string
}

func (f *aliasProviderFixture) Engine() string { return "fixture" }
func (f *aliasProviderFixture) StatObject(_ context.Context, key string) (*s3.ObjectInfo, error) {
	if key != f.object.Key {
		return nil, errs.ErrRecordNotFound
	}
	o := f.object
	return &o, nil
}
func (f *aliasProviderFixture) AccessURL(_ context.Context, key string, _ time.Duration, _ *s3.AccessURLOption) (string, error) {
	f.accessedKey = key
	return "https://fixture.invalid/provider/" + key, nil
}

type aliasMetadataFixture struct {
	relation.ObjectInfoModelInterface
	writes int
	value  relation.ObjectModel
}

func (f *aliasMetadataFixture) SetObject(_ context.Context, value *relation.ObjectModel) error {
	f.writes++
	f.value = *value
	return nil
}

type aliasCacheFixture struct {
	cache.ObjectCache
	value *relation.ObjectModel
}

func (f *aliasCacheFixture) DelObjectName(string, ...string) cache.ObjectCache { return f }
func (f *aliasCacheFixture) ExecDel(context.Context, ...bool) error            { return nil }
func (f *aliasCacheFixture) GetName(_ context.Context, engine, name string) (*relation.ObjectModel, error) {
	if f.value == nil || engine != f.value.Engine || name != f.value.Name {
		return nil, errs.ErrRecordNotFound
	}
	o := *f.value
	return &o, nil
}

func TestS3AliasBindsAuthenticatedOwnerAndVerifiedCompletion(t *testing.T) {
	proof := &intent.CompletionProof{ID: "synthetic-completion-proof", Owner: "synthetic-owner", Engine: "fixture", Hash: strings.Repeat("a", 32), Size: 12, Key: "openim/data/owner-v2/scoped/synthetic-key", ETag: "synthetic-provider-etag"}
	receipts := &aliasProofFixture{proof: proof}
	provider := &aliasProviderFixture{object: s3.ObjectInfo{Key: proof.Key, Size: proof.Size, ETag: proof.ETag}}
	db := &aliasMetadataFixture{}
	c := &aliasCacheFixture{}
	store := &s3Database{s3: cont.New(nil, provider, receipts), db: db, cache: c}
	ctx := context.WithValue(context.Background(), constant.OpUserID, proof.Owner)
	value := relation.ObjectModel{Name: "synthetic-owner/file", UserID: proof.Owner, Hash: proof.Hash, Key: proof.Key, Size: 12, CompletionProofID: "client-supplied-fake"}
	if err := store.SetObject(ctx, &value); err != nil {
		t.Fatal(err)
	}
	if db.writes != 1 || db.value.Engine != "fixture" || db.value.CompletionProofID != proof.ID {
		t.Fatal("metadata was not bound to trusted completion")
	}
	for _, change := range []func(*relation.ObjectModel){func(o *relation.ObjectModel) { o.UserID = "" }, func(o *relation.ObjectModel) { o.UserID = "untrusted-owner" }, func(o *relation.ObjectModel) { o.Key = "different-unproven-location" }, func(o *relation.ObjectModel) { o.Size++ }, func(o *relation.ObjectModel) { o.Hash = "invalid-completion-identity" }} {
		invalid := value
		change(&invalid)
		if err := store.SetObject(ctx, &invalid); err == nil {
			t.Fatal("unproven alias accepted")
		}
		if db.writes != 1 {
			t.Fatal("unproven alias reached metadata writer")
		}
	}
	receipts.fail = true
	if err := store.SetObject(ctx, &value); err == nil || db.writes != 1 {
		t.Fatal("proof storage failure failed open")
	}
}

func TestS3LegacyAccessURLUsesRetainedMetadataKey(t *testing.T) {
	legacy := &relation.ObjectModel{Name: "legacy-file", Engine: "fixture", Key: "openim/data/hash/historical-key", Size: 12, ContentType: "image/png"}
	provider := &aliasProviderFixture{}
	store := &s3Database{s3: cont.New(nil, provider, nil), cache: &aliasCacheFixture{value: legacy}}
	_, url, err := store.AccessURL(context.Background(), legacy.Name, time.Hour, nil)
	if err != nil || provider.accessedKey != legacy.Key || url != "https://fixture.invalid/provider/"+legacy.Key {
		t.Fatal("historical key/url was rewritten or required invented ownership", err)
	}
	if legacy.UserID != "" || legacy.CompletionProofID != "" {
		t.Fatal("legacy ownership was backfilled")
	}
}
