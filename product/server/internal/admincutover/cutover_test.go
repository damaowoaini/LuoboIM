package admincutover

import (
	"context"
	"encoding/json"
	"github.com/OpenIMSDK/tools/tokenverify"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/redis/go-redis/v9"
	"strings"
	"testing"
	"time"
)

func TestLegacyRevocationIsOwnerScopedCASAndNeverRestoresJWT(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", DB: 15})
	defer rdb.Close()
	ctx := context.Background()
	if rdb.Ping(ctx).Err() != nil {
		t.Skip("private Redis unavailable")
	}
	id := "cutover-" + uuid.NewString()
	key := "UID_PID_TOKEN_STATUS:" + id + ":Admin"
	other := "UID_PID_TOKEN_STATUS:" + id + "-other:Admin"
	defer rdb.Del(ctx, key, other)
	secret := strings.Repeat("synthetic-key", 4)
	mint := func(p string) string {
		raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, authverify.ClassifiedClaims{Claims: tokenverify.Claims{UserID: id, PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{ID: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}, Purpose: p}).SignedString([]byte(secret))
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	legacy, changed, service := mint(""), mint(""), mint(authverify.ServicePurpose)
	rdb.HSet(ctx, key, legacy, 0, changed, 0, service, 0, "unresolved", 0)
	rdb.HSet(ctx, other, legacy, 0)
	p, e := Inspect(ctx, rdb, secret, "tenant-A", []string{id, id})
	if e != nil || len(p.Legacy) != 2 || p.Unresolved != 1 {
		t.Fatal("owner inspection failed", e)
	}
	data, _ := json.Marshal(p)
	if strings.Contains(string(data), legacy) || strings.Contains(string(data), service) {
		t.Fatal("audit serialized bearer credentials")
	}
	if Revoke(ctx, rdb, p) == nil {
		t.Fatal("unprepared mutation accepted")
	}
	rdb.HSet(ctx, key, changed, 1)
	p.State = "prepared"
	if e = Revoke(ctx, rdb, p); e != nil || p.Removed != 1 {
		t.Fatal("CAS revocation failed", e)
	}
	if rdb.HExists(ctx, key, legacy).Val() || !rdb.HExists(ctx, key, service).Val() || !rdb.HExists(ctx, key, "unresolved").Val() || rdb.HGet(ctx, key, changed).Val() != "1" || !rdb.HExists(ctx, other, legacy).Val() {
		t.Fatal("revocation crossed ownership/status boundary")
	}
	again, e := Inspect(ctx, rdb, secret, "tenant-A", []string{id})
	if e != nil || len(again.Legacy) != 1 {
		t.Fatal("repeat inspection failed")
	}
	again.State = "prepared"
	if Revoke(ctx, rdb, again) != nil {
		t.Fatal("repeatable revoke failed")
	}
	var metadata Plan
	json.Unmarshal(data, &metadata)
	metadata.State = "prepared"
	if Revoke(ctx, rdb, &metadata) == nil {
		t.Fatal("metadata report became executable credential restoration plan")
	}
}
