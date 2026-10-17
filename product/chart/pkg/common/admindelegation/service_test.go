package admindelegation

import (
	"context"
	admintable "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, string, string) {
	t.Helper()
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	socket := "/Users/chuzu/Documents/projects/LuoboIM/references/integration/mysql8.4/mysql.sock"
	root, e := gorm.Open(mysql.Open("root@unix("+socket+")/luobo_integration?parseTime=true"), cfg)
	if e != nil {
		t.Skip("isolated MySQL unavailable")
	}
	name := "luobo_admin_delegate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if e = root.Exec("CREATE DATABASE `" + name + "`").Error; e != nil {
		t.Fatal(e)
	}
	db, e := gorm.Open(mysql.Open("root@unix("+socket+")/"+name+"?parseTime=true"), cfg)
	if e != nil {
		t.Fatal(e)
	}
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:36379", DB: 15, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	if rdb.Ping(context.Background()).Err() != nil {
		t.Skip("isolated Redis unavailable")
	}
	id := "delegate-" + uuid.NewString()
	t.Cleanup(func() {
		rdb.Del(context.Background(), "CHAT_UID_TOKEN_STATUS:"+id)
		rdb.Close()
		raw, _ := db.DB()
		raw.Close()
		root.Exec("DROP DATABASE `" + name + "`")
		raw, _ = root.DB()
		raw.Close()
	})
	if e = db.AutoMigrate(&admintable.Admin{}, &Record{}, &RequestAudit{}); e != nil {
		t.Fatal(e)
	}
	if e = db.Create(&admintable.Admin{Account: "normal", UserID: id, Level: 80, GoogleAuthEnabled: true, GoogleAuthSecret: "synthetic-totp-fixture", CreateTime: time.Now()}).Error; e != nil {
		t.Fatal(e)
	}
	s := &Service{DB: db, Redis: rdb, Secret: []byte(strings.Repeat("synthetic-secret-", 3)), Issuer: "synthetic-tenant-A"}
	now := time.Now()
	session, e := jwt.NewWithClaims(jwt.SigningMethodHS256, sessionClaims{UserID: id, UserType: 2, RegisteredClaims: jwt.RegisteredClaims{ID: uuid.NewString(), ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))}}).SignedString(s.Secret)
	if e != nil {
		t.Fatal(e)
	}
	if rdb.HSet(context.Background(), "CHAT_UID_TOKEN_STATUS:"+id, session, 0).Err() != nil {
		t.Fatal("session write failed")
	}
	return s, id, session
}
func TestDelegationLiveSQLAndSessionRevocation(t *testing.T) {
	s, id, session := fixture(t)
	ctx := context.Background()
	raw, e := s.Issue(ctx, id, session, "configured-service")
	if e != nil {
		t.Fatal(e)
	}
	actor, level, e := s.Validate(ctx, raw)
	if e != nil || actor != id || level != 80 {
		t.Fatal("normal authorized admin lost management capability", e)
	}
	var record Record
	if s.DB.First(&record).Error != nil || record.TokenHash != Hash(raw) || record.SessionHash != Hash(session) {
		t.Fatal("issuance was not hash bound")
	}
	s.DB.Model(&admintable.Admin{}).Where("user_id=?", id).Update("level", 1)
	if _, level, e = s.Validate(ctx, raw); e != nil || level != 1 {
		t.Fatal("live role change was ignored")
	}
	for _, patch := range []map[string]any{{"disabled": true}, {"disabled": false, "google_auth_enabled": false}, {"google_auth_enabled": true, "level": 0}} {
		if e = s.DB.Model(&admintable.Admin{}).Where("user_id=?", id).Updates(patch).Error; e != nil {
			t.Fatal(e)
		}
		if _, _, e = s.Validate(ctx, raw); e == nil {
			t.Fatal("disabled/MFA/role invalidation failed")
		}
	}
	s.DB.Model(&admintable.Admin{}).Where("user_id=?", id).Update("level", 100)
	if _, level, e = s.Validate(ctx, raw); e != nil || level != 100 {
		t.Fatal("super role management unavailable")
	}
	s.Redis.HDel(ctx, "CHAT_UID_TOKEN_STATUS:"+id, session)
	if _, _, e = s.Validate(ctx, raw); e == nil {
		t.Fatal("revoked original chart session remained delegated")
	}
	if _, e = s.Issue(ctx, id, session, "configured-service"); e == nil {
		t.Fatal("revoked session minted capability")
	}
}
func TestDelegationRejectsUnissuedCrossTenantAndStorageFailure(t *testing.T) {
	s, id, session := fixture(t)
	ctx := context.Background()
	raw, e := s.Issue(ctx, id, session, "service")
	if e != nil {
		t.Fatal(e)
	}
	var claims Claims
	_, _, e = new(jwt.Parser).ParseUnverified(raw, &claims)
	if e != nil {
		t.Fatal(e)
	}
	claims.ID = uuid.NewString()
	forged, e := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.Secret)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Validate(ctx, forged); e == nil {
		t.Fatal("validly signed but unissued capability accepted")
	}
	s.Issuer = "synthetic-tenant-B"
	if _, _, e = s.Validate(ctx, raw); e == nil {
		t.Fatal("cross tenant issuer accepted")
	}
	s.Issuer = "synthetic-tenant-A"
	if e = s.DB.Migrator().DropTable(&Record{}); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Validate(ctx, raw); e == nil {
		t.Fatal("DB failure opened authorization")
	}
	if _, e = s.Issue(ctx, id, session, "service"); e == nil {
		t.Fatal("failed durable issuance returned token")
	}
}
func TestDelegationRejectsBusinessSessionAndAmbiguousAdmin(t *testing.T) {
	s, id, session := fixture(t)
	ctx := context.Background()
	if _, e := s.Issue(ctx, "other-admin", session, "service"); e == nil {
		t.Fatal("foreign chart session accepted")
	}
	var c sessionClaims
	_, _, _ = new(jwt.Parser).ParseUnverified(session, &c)
	c.UserType = 1
	raw, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.Secret)
	if _, e := s.Issue(ctx, id, raw, "service"); e == nil {
		t.Fatal("business session minted management capability")
	}
	s.DB.Create(&admintable.Admin{Account: "duplicate", UserID: id, Level: 100, GoogleAuthEnabled: true, GoogleAuthSecret: "synthetic-totp-fixture", CreateTime: time.Now()})
	if _, e := s.Issue(ctx, id, session, "service"); e == nil {
		t.Fatal("ambiguous admin identity accepted")
	}
}
func TestAdminLogoutOnlyRevokesExactBoundSession(t *testing.T) {
	s, id, first := fixture(t)
	ctx := context.Background()
	one, e := s.Issue(ctx, id, first, "service")
	if e != nil {
		t.Fatal(e)
	}
	var c sessionClaims
	_, _, _ = new(jwt.Parser).ParseUnverified(first, &c)
	c.ID = uuid.NewString()
	second, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.Secret)
	s.Redis.HSet(ctx, "CHAT_UID_TOKEN_STATUS:"+id, second, 0)
	two, e := s.Issue(ctx, id, second, "service")
	if e != nil {
		t.Fatal(e)
	}
	if s.RevokeSession(ctx, first) != nil || s.RevokeSession(ctx, first) != nil {
		t.Fatal("exact logout not idempotent")
	}
	if _, _, e = s.Validate(ctx, one); e == nil {
		t.Fatal("logged-out capability still active")
	}
	if _, _, e = s.Validate(ctx, two); e != nil {
		t.Fatal("another administrator session was revoked")
	}
	s.DB.Model(&admintable.Admin{}).Where("user_id=?", id).Update("disabled", true)
	if s.RevokeSession(ctx, second) != nil {
		t.Fatal("disabled session could not revoke itself")
	}
}
