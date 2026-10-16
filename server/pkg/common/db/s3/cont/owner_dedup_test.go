package cont

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/intent"
)

// A local provider and one synthetic actor only. These tests do not connect to
// MinIO, claim another account's hash, or perform any live upload experiment.
type ownerMemoryStore struct {
	mu            sync.Mutex
	uploads       map[string][]byte
	proof         *intent.CompletionProof
	commits       int
	failCommit    bool
	lostCommitACK bool
}

func (m *ownerMemoryStore) Scope(ctx context.Context, engine string) (intent.Scope, error) {
	if mcontext.GetOpUserID(ctx) == "" || engine != "fixture" {
		return intent.Scope{}, errs.ErrNoPermission.Wrap("actor required")
	}
	return intent.Scope{ID: strings.Repeat("b", 64)}, nil
}
func (m *ownerMemoryStore) Create(_ context.Context, _, _ string, payload any, _ time.Duration) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := intent.Prefix + uuid.NewString()
	m.uploads[id] = b
	return id, nil
}
func (m *ownerMemoryStore) Load(_ context.Context, id, _, _ string, out any) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.uploads[id]
	if !ok {
		return time.Time{}, errs.ErrNoPermission
	}
	return time.Now().Add(time.Hour), json.Unmarshal(b, out)
}
func (m *ownerMemoryStore) RecoverLegacy(context.Context, string, string, any, time.Duration, any) (time.Time, error) {
	return time.Time{}, errs.ErrNoPermission
}
func (m *ownerMemoryStore) LoadLegacy(context.Context, string, string, any) (time.Time, error) {
	return time.Time{}, errs.ErrNoPermission
}
func (m *ownerMemoryStore) FindCompleted(ctx context.Context, engine, hash string, size int64) (*intent.CompletionProof, error) {
	if _, err := m.Scope(ctx, engine); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proof == nil || m.proof.Hash != hash || m.proof.Size != size {
		return nil, errs.ErrRecordNotFound
	}
	p := *m.proof
	return &p, nil
}
func (m *ownerMemoryStore) CommitCompleted(ctx context.Context, engine, hash string, size int64, key, etag string, parts []string) (*intent.CompletionProof, error) {
	scope, err := m.Scope(ctx, engine)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commits++
	if m.failCommit {
		return nil, errors.New("synthetic persistence failure")
	}
	if m.proof == nil {
		m.proof = &intent.CompletionProof{ID: strings.Repeat("a", 64), Version: 2, Scope: scope.ID, Owner: mcontext.GetOpUserID(ctx), Engine: engine, Hash: hash, Size: size, Key: key, ETag: etag, HashContract: intent.HashContract, PartHashes: append([]string(nil), parts...)}
	}
	if m.lostCommitACK {
		m.lostCommitACK = false
		return nil, errors.New("synthetic lost database ACK")
	}
	p := *m.proof
	return &p, nil
}

type ownerProvider struct {
	*storeFixture
	stats            []string
	copyETag         string
	corruptCopySize  bool
	failCopy         bool
	failFinalStat    bool
	lostMultipartACK bool
	multipartSize    int64
}

func (p *ownerProvider) StatObject(ctx context.Context, key string) (*s3.ObjectInfo, error) {
	p.stats = append(p.stats, key)
	if p.failFinalStat && strings.HasPrefix(key, "openim/data/owner-v2/") {
		return nil, errors.New("synthetic final stat failure")
	}
	return p.storeFixture.StatObject(ctx, key)
}
func (p *ownerProvider) CopyObject(ctx context.Context, src, dst string) (*s3.CopyObjectInfo, error) {
	if p.failCopy {
		return nil, errors.New("synthetic provider copy failure")
	}
	info, err := p.storeFixture.CopyObject(ctx, src, dst)
	if err != nil {
		return nil, err
	}
	v := p.objects[dst]
	if p.copyETag != "" {
		v.ETag = p.copyETag
		info.ETag = p.copyETag
	}
	if p.corruptCopySize {
		v.Size++
	}
	p.objects[dst] = v
	return info, nil
}
func (p *ownerProvider) CompleteMultipartUpload(_ context.Context, _ string, key string, parts []s3.Part) (*s3.CompleteMultipartUploadResult, error) {
	hashes := make([]string, len(parts))
	for i, part := range parts {
		hashes[i] = part.ETag
	}
	etag := multipartETag(hashes)
	p.objects[key] = s3.ObjectInfo{Key: key, Size: p.multipartSize, ETag: etag}
	if p.lostMultipartACK {
		return nil, errors.New("synthetic lost provider ACK")
	}
	return &s3.CompleteMultipartUploadResult{Key: key, ETag: etag}, nil
}

func ownerFixture() (*Controller, *ownerMemoryStore, *ownerProvider, context.Context) {
	m := &ownerMemoryStore{uploads: map[string][]byte{}}
	p := &ownerProvider{storeFixture: &storeFixture{objects: map[string]s3.ObjectInfo{}}}
	c := New(&cacheFixture{impl: p.storeFixture}, p, m)
	ctx := context.WithValue(context.Background(), constant.OpUserID, "synthetic-local-owner")
	return c, m, p, ctx
}
func uploadOwnBytes(t *testing.T, c *Controller, m *ownerMemoryStore, p *ownerProvider, ctx context.Context, part string, size int64) string {
	t.Helper()
	res, err := c.InitiateUpload(ctx, md5hex(part), size, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	var upload multipartUploadID
	if _, err = m.Load(ctx, res.UploadID, "multipart", p.Engine(), &upload); err != nil {
		t.Fatal(err)
	}
	p.objects[upload.Key] = s3.ObjectInfo{Key: upload.Key, Size: size, ETag: part}
	return res.UploadID
}
func assertNoGlobalHashAccess(t *testing.T, p *ownerProvider) {
	t.Helper()
	for _, key := range append(append([]string(nil), p.stats...), p.copies...) {
		if strings.HasPrefix(key, "openim/data/hash/") {
			t.Fatal("global legacy hash fallback used")
		}
	}
}

func TestOwnerDedupRequiresCompletedProofAndSupportsIdempotentOwnRetry(t *testing.T) {
	c, m, p, ctx := ownerFixture()
	part := md5hex("synthetic local bytes")
	wire := md5hex(part)
	if _, err := c.GetHashObject(ctx, wire, 12); !errs.ErrRecordNotFound.Is(err) {
		t.Fatal("missing proof became hash hit", err)
	}
	if len(p.stats) != 0 {
		t.Fatal("missing proof consulted provider")
	}
	id := uploadOwnBytes(t, c, m, p, ctx, part, 12)
	res, err := c.CompleteUpload(ctx, id, []string{part})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Key, "openim/data/owner-v2/") || res.Hash != wire || m.commits != 1 {
		t.Fatal("completion is not owner-scoped")
	}
	if m.proof == nil || m.proof.Key != res.Key || m.proof.ETag != part || len(m.proof.PartHashes) != 1 {
		t.Fatal("proof missing provider evidence")
	}
	copies := len(p.copies)
	retry, err := c.CompleteUpload(ctx, id, []string{part})
	if err != nil || retry.Key != res.Key || len(p.copies) != copies {
		t.Fatal("repeat completion changed object", err)
	}
	_, err = c.InitiateUpload(ctx, wire, 12, time.Hour, 1)
	var hit *HashAlreadyExistsError
	if !errors.As(err, &hit) || hit.Object.Key != res.Key {
		t.Fatal("own proven upload did not deduplicate", err)
	}
	if _, err = c.InitiateUpload(ctx, wire, 13, time.Hour, 1); err != nil {
		t.Fatal("different size incorrectly reused proof", err)
	}
	if _, err = c.InitiateUpload(context.Background(), wire, 12, time.Hour, 1); err == nil {
		t.Fatal("anonymous upload accepted")
	}
	assertNoGlobalHashAccess(t, p)
}

func TestOwnerDedupProviderFailuresCannotCreateProof(t *testing.T) {
	for _, kind := range []string{"wrong-size", "wrong-hash", "copy-error", "copy-content", "copy-size", "final-stat", "persistence"} {
		t.Run(kind, func(t *testing.T) {
			c, m, p, ctx := ownerFixture()
			part := md5hex("synthetic failure bytes")
			id := uploadOwnBytes(t, c, m, p, ctx, part, 12)
			var u multipartUploadID
			_, _ = m.Load(ctx, id, "multipart", p.Engine(), &u)
			switch kind {
			case "wrong-size":
				v := p.objects[u.Key]
				v.Size++
				p.objects[u.Key] = v
			case "wrong-hash":
				v := p.objects[u.Key]
				v.ETag = md5hex("different")
				p.objects[u.Key] = v
			case "copy-error":
				p.failCopy = true
			case "copy-content":
				p.copyETag = md5hex("different")
			case "copy-size":
				p.corruptCopySize = true
			case "final-stat":
				p.failFinalStat = true
			case "persistence":
				m.failCommit = true
			}
			if _, err := c.CompleteUpload(ctx, id, []string{part}); err == nil {
				t.Fatal("partial failure accepted")
			}
			if m.proof != nil {
				t.Fatal("trusted proof created before verification")
			}
			if _, ok := p.objects[u.Key]; !ok {
				t.Fatal("failed/unknown attempt deleted retry source")
			}
			assertNoGlobalHashAccess(t, p)
		})
	}
}

func TestOwnerDedupLostCommitACKRecoversWithoutDeletingCommittedObject(t *testing.T) {
	c, m, p, ctx := ownerFixture()
	part := md5hex("synthetic lost ACK")
	id := uploadOwnBytes(t, c, m, p, ctx, part, 12)
	m.lostCommitACK = true
	if _, err := c.CompleteUpload(ctx, id, []string{part}); err == nil {
		t.Fatal("lost ACK hidden")
	}
	if m.proof == nil {
		t.Fatal("fixture did not commit")
	}
	key := m.proof.Key
	if _, ok := p.objects[key]; !ok {
		t.Fatal("possibly committed object deleted")
	}
	res, err := c.CompleteUpload(ctx, id, []string{part})
	if err != nil || res.Key != key || m.commits != 1 {
		t.Fatal("durable proof failed to recover exact retry", err)
	}
	assertNoGlobalHashAccess(t, p)
}

func TestOwnerDedupExistingProofRequiresExactProviderStat(t *testing.T) {
	for _, kind := range []string{"missing", "wrong-size", "wrong-etag"} {
		t.Run(kind, func(t *testing.T) {
			c, m, p, ctx := ownerFixture()
			part := md5hex("synthetic proven bytes")
			id := uploadOwnBytes(t, c, m, p, ctx, part, 12)
			res, err := c.CompleteUpload(ctx, id, []string{part})
			if err != nil {
				t.Fatal(err)
			}
			object := p.objects[res.Key]
			switch kind {
			case "missing":
				delete(p.objects, res.Key)
			case "wrong-size":
				object.Size++
				p.objects[res.Key] = object
			case "wrong-etag":
				object.ETag = md5hex("different")
				p.objects[res.Key] = object
			}
			signed := len(p.signed)
			if _, err = c.GetHashObject(ctx, md5hex(part), 12); err == nil {
				t.Fatal("mismatched provider object accepted")
			}
			if _, err = c.InitiateUpload(ctx, md5hex(part), 12, time.Hour, 1); err == nil {
				t.Fatal("broken durable proof became an upload/dedup hit")
			}
			if len(p.signed) != signed || m.commits != 1 {
				t.Fatal("broken proof was silently replaced")
			}
			assertNoGlobalHashAccess(t, p)
		})
	}
}

func TestOwnerDedupMultipartUsesTempAndAllowsProviderCopyETagChange(t *testing.T) {
	for _, lostACK := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "lost-provider-ACK"}[lostACK], func(t *testing.T) {
			c, m, p, ctx := ownerFixture()
			parts := []string{md5hex("synthetic part1"), md5hex("synthetic part2")}
			wire := md5hex(strings.Join(parts, ","))
			res, err := c.InitiateUpload(ctx, wire, 90, time.Hour, 2)
			if err != nil {
				t.Fatal(err)
			}
			var u multipartUploadID
			_, _ = m.Load(ctx, res.UploadID, "multipart", p.Engine(), &u)
			if u.Type != UploadTypeMultipart || !strings.HasPrefix(u.Key, tempPath) || u.Scope == "" {
				t.Fatal("multipart destination is not scoped temporary intent")
			}
			p.multipartSize = 90
			p.lostMultipartACK = lostACK
			p.copyETag = md5hex("synthetic whole-file digest")
			got, err := c.CompleteUpload(ctx, res.UploadID, parts)
			if err != nil {
				t.Fatal(err)
			}
			if got.Hash != wire || got.Size != 90 || m.proof == nil || m.proof.ETag != p.copyETag {
				t.Fatal("multipart proof/hash contract lost")
			}
			assertNoGlobalHashAccess(t, p)
		})
	}
}

func TestOwnerDedupLegacyCapabilitiesRequireSafeTemporaryLocation(t *testing.T) {
	c, m, p, ctx := ownerFixture()
	part := md5hex("synthetic legacy bytes")
	wire := md5hex(part)
	old := multipartUploadID{Type: UploadTypePresigned, Key: tempPath + "synthetic-owned-old-presigned", Size: 12, PartSize: 64, Hash: wire}
	id, err := m.Create(ctx, "multipart", p.Engine(), old, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	p.objects[old.Key] = s3.ObjectInfo{Key: old.Key, Size: 12, ETag: part}
	if _, err = c.CompleteUpload(ctx, id, []string{part}); err != nil {
		t.Fatal("previous owner-bound presigned capability broke", err)
	}
	unsafe := multipartUploadID{Type: UploadTypeMultipart, ID: "old-provider-id", Key: "openim/data/hash/" + wire, Size: 90, PartSize: 64, Hash: wire}
	oldID, err := m.Create(ctx, "multipart", p.Engine(), unsafe, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	signs, stats := len(p.signed), len(p.stats)
	if _, err = c.AuthSign(ctx, oldID, []int{1}); err == nil {
		t.Fatal("old shared multipart target signed")
	}
	if _, err = c.CompleteUpload(ctx, oldID, []string{part, part}); err == nil {
		t.Fatal("old shared multipart target completed")
	}
	if len(p.signed) != signs || len(p.stats) != stats {
		t.Fatal("unsafe legacy intent reached provider")
	}
	assertNoGlobalHashAccess(t, p)
}
