package cont

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/s3/intent"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"strings"
	"sync"
	"testing"
	"time"
)

var absent = errors.New("synthetic not found")

type storeFixture struct {
	s3.Interface
	mu             sync.Mutex
	objects        map[string]s3.ObjectInfo
	signed         []string
	copies         []string
	deleted        []string
	multipartSigns int
}

func (f *storeFixture) Engine() string                                 { return "fixture" }
func (f *storeFixture) PartSize(context.Context, int64) (int64, error) { return 64, nil }
func (f *storeFixture) StatObject(ctx context.Context, key string) (*s3.ObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.objects[key]
	if !ok {
		return nil, absent
	}
	return &v, nil
}
func (f *storeFixture) IsNotFound(err error) bool { return errors.Is(err, absent) }
func (f *storeFixture) PresignedPutObject(ctx context.Context, key string, d time.Duration) (string, error) {
	if d <= 0 || d > intent.MaxLifetime {
		return "", errors.New("invalid signing expiry")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signed = append(f.signed, key)
	return "https://fixture.invalid/" + key, nil
}
func (f *storeFixture) CopyObject(ctx context.Context, src, dst string) (*s3.CopyObjectInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.objects[src]
	if !ok {
		return nil, absent
	}
	v.Key = dst
	f.objects[dst] = v
	f.copies = append(f.copies, src)
	return &s3.CopyObjectInfo{Key: dst, ETag: v.ETag}, nil
}
func (f *storeFixture) DeleteObject(ctx context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, key)
	delete(f.objects, key)
	return nil
}
func (f *storeFixture) InitiateMultipartUpload(ctx context.Context, key string) (*s3.InitiateMultipartUploadResult, error) {
	return &s3.InitiateMultipartUploadResult{UploadID: uuid.NewString(), Key: key}, nil
}
func (f *storeFixture) AuthSign(ctx context.Context, id, key string, expiry time.Duration, parts []int) (*s3.AuthSignResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.multipartSigns++
	f.signed = append(f.signed, key)
	return &s3.AuthSignResult{}, nil
}
func (f *storeFixture) AbortMultipartUpload(context.Context, string, string) error { return nil }

type cacheFixture struct {
	cache.S3Cache
	impl *storeFixture
}

func (c *cacheFixture) GetKey(ctx context.Context, engine, key string) (*s3.ObjectInfo, error) {
	return c.impl.StatObject(ctx, key)
}
func (c *cacheFixture) DelS3Key(string, ...string) cache.S3Cache { return c }
func (c *cacheFixture) ExecDel(context.Context, ...bool) error   { return nil }
func fixture(t *testing.T) (*Controller, *intent.Store, *mongo.Database, *storeFixture, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	mc, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"))
	if err != nil {
		t.Fatal(err)
	}
	if err = mc.Ping(ctx, nil); err != nil {
		t.Skip("isolated Mongo fixture unavailable")
	}
	db := mc.Database("luobo_upload_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() { _ = db.Drop(context.Background()); _ = mc.Disconnect(context.Background()) })
	receipts, err := intent.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	f := &storeFixture{objects: map[string]s3.ObjectInfo{}}
	owner := context.WithValue(ctx, constant.OpUserID, "synthetic-owner")
	return New(&cacheFixture{impl: f}, f, receipts), receipts, db, f, owner
}
func md5hex(data string) string { v := md5.Sum([]byte(data)); return hex.EncodeToString(v[:]) }
func TestUploadIntentOwnerAndLegacyRebuild(t *testing.T) {
	c, _, db, f, owner := fixture(t)
	part := md5hex("fixture-file")
	hash := md5hex(part)
	legacy := newMultipartUploadID(multipartUploadID{Type: UploadTypePresigned, Key: "victim/existing-object", Size: 12, Hash: hash})
	f.objects["victim/existing-object"] = s3.ObjectInfo{Key: "victim/existing-object", Size: 12, ETag: part}
	if _, err := c.CompleteUpload(owner, legacy, []string{part}); err == nil {
		t.Fatal("unproven old key completion accepted")
	}
	var wg sync.WaitGroup
	results := make(chan string, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sign, err := c.AuthSign(owner, legacy, []int{1})
			if err != nil {
				t.Error(err)
				return
			}
			results <- sign.Parts[0].URL
		}()
	}
	wg.Wait()
	close(results)
	target := ""
	for v := range results {
		if target != "" && v != target {
			t.Fatal("concurrent legacy rebuild created different targets")
		}
		target = v
	}
	if target == "" || strings.Contains(target, "victim") {
		t.Fatal("legacy target trusted")
	}
	key := strings.TrimPrefix(target, "https://fixture.invalid/")
	f.objects[key] = s3.ObjectInfo{Key: key, Size: 12, ETag: part}
	restart, err := intent.New(owner, db)
	if err != nil {
		t.Fatal(err)
	}
	c.intents = restart
	if _, err = c.CompleteUpload(owner, legacy, []string{part}); err != nil {
		t.Fatal(err)
	}
	for _, v := range append(f.copies, f.deleted...) {
		if v == "victim/existing-object" {
			t.Fatal("victim object touched")
		}
	}
	if _, ok := f.objects["victim/existing-object"]; !ok {
		t.Fatal("victim removed")
	}
	peer := context.WithValue(owner, constant.OpUserID, "synthetic-peer")
	peerSign, err := c.AuthSign(peer, legacy, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	if peerSign.Parts[0].URL == target {
		t.Fatal("cross-owner legacy target reused")
	}
	t.Log("LUOBO_UPLOAD_LEGACY_REBUILD_OWNER_RESTART_PASS")
}
func TestUploadNewIntentRejectsForeignTamperExpiryAndParts(t *testing.T) {
	c, receipts, db, f, owner := fixture(t)
	hash := md5hex(md5hex("new-file"))
	res, err := c.InitiateUpload(owner, hash, 12, time.Hour, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.UploadID, intent.Prefix) {
		t.Fatal("new ID not opaque")
	}
	peer := context.WithValue(owner, constant.OpUserID, "synthetic-peer")
	before := len(f.signed)
	if _, err = c.AuthSign(peer, res.UploadID, []int{1}); err == nil {
		t.Fatal("foreign signed")
	}
	if _, err = c.CompleteUpload(peer, res.UploadID, []string{md5hex("new-file")}); err == nil {
		t.Fatal("foreign completed")
	}
	if _, err = c.AuthSign(owner, res.UploadID+"tamper", []int{1}); err == nil {
		t.Fatal("tamper signed")
	}
	for _, parts := range [][]int{nil, {0}, {2}, {1, 1}, {-1}} {
		if _, err = c.AuthSign(owner, res.UploadID, parts); err == nil {
			t.Fatalf("invalid parts accepted: %v", parts)
		}
	}
	if len(f.signed) != before {
		t.Fatal("invalid request reached signer")
	}
	if _, err = c.AuthSign(owner, res.UploadID, []int{1}); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Collection("object_upload_intents").UpdateOne(owner, bson.M{"_id": res.UploadID}, bson.M{"$set": bson.M{"expires_at": time.Now().Add(-time.Minute)}}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.AuthSign(owner, res.UploadID, []int{1}); err == nil {
		t.Fatal("expired intent signed")
	}
	var v multipartUploadID
	if _, err = receipts.Load(owner, res.UploadID, "form", c.Engine(), &v); err == nil {
		t.Fatal("purpose mismatch accepted")
	}
	if _, err = receipts.Load(owner, res.UploadID, "multipart", "other-engine", &v); err == nil {
		t.Fatal("engine mismatch accepted")
	}
	forged := newMultipartUploadID(multipartUploadID{Type: UploadTypeMultipart, ID: "forged-provider-id", Key: "victim/key", Size: 90, Hash: hash})
	if _, err = c.AuthSign(owner, forged, []int{1}); err == nil {
		t.Fatal("forged multipart signed")
	}
	if f.multipartSigns != 0 {
		t.Fatal("forgery reached provider")
	}
	t.Log("LUOBO_UPLOAD_OPAQUE_OWNER_EXPIRY_PARTS_PASS")
}
func TestUploadFormReceiptOwnerNamespaceRestartAndFailure(t *testing.T) {
	_, s, db, _, owner := fixture(t)
	payload := map[string]any{"key": "private-generated-key", "size": 12, "name": "synthetic-owner/file"}
	id, err := s.Create(owner, "form", "fixture", payload, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	s, err = intent.New(owner, db)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if _, err = s.Load(owner, id, "form", "fixture", &got); err != nil {
		t.Fatal(err)
	}
	if got["key"] != payload["key"] {
		t.Fatal("payload changed")
	}
	peer := context.WithValue(owner, constant.OpUserID, "peer")
	if _, err = s.Load(peer, id, "form", "fixture", &got); err == nil {
		t.Fatal("foreign form receipt accepted")
	}
	if _, err = s.Load(owner, "eyJrZXkiOiJ2aWN0aW0ifQ==", "form", "fixture", &got); err == nil {
		t.Fatal("unsigned form receipt accepted")
	}
	other, err := intent.New(owner, db.Client().Database(db.Name()+"_other"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Client().Database(db.Name() + "_other").Drop(context.Background()) })
	if _, err = other.Load(owner, id, "form", "fixture", &got); err == nil {
		t.Fatal("different database accepted receipt")
	}
	cancelled, cancel := context.WithCancel(owner)
	cancel()
	if _, err = s.Create(cancelled, "form", "fixture", payload, time.Hour); err == nil {
		t.Fatal("failed persistence accepted")
	}
	t.Log("LUOBO_FORM_OWNER_NAMESPACE_PERSISTENCE_PASS")
}
func TestUploadPartValidation(t *testing.T) {
	u := &multipartUploadID{Type: UploadTypeMultipart, Size: 129, PartSize: 64}
	if err := validatePartNumbers(u, []int{3, 1, 2}); err != nil {
		t.Fatal(err)
	}
	for _, parts := range [][]int{{4}, {1, 1}, nil} {
		if validatePartNumbers(u, parts) == nil {
			t.Fatal(fmt.Sprint(parts))
		}
	}
}
