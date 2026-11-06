package admindelegation

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	admintable "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	driver "github.com/go-sql-driver/mysql"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Only the private fixture DSN enters this bounded file reader. Neither the
// DSN nor an underlying parser/connection error is printed on failure.
func fixtureDSNFile(t *testing.T, path string) string {
	t.Helper()
	actual, e := filepath.EvalSymlinks(path)
	if e != nil || !filepath.IsAbs(path) || filepath.Clean(path) != path || actual != path {
		t.Fatal("private MySQL DSN file requires a canonical absolute path")
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Mode().Perm() != 0600 || before.Size() < 1 || before.Size() > 16<<10 {
		t.Fatal("private MySQL DSN file must be bounded regular 0600")
	}
	identity, ok := before.Sys().(*syscall.Stat_t)
	parent, parentErr := os.Lstat(filepath.Dir(path))
	if !ok || identity.Uid != uint32(os.Getuid()) || identity.Nlink != 1 || parentErr != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		t.Fatal("private MySQL DSN file requires current owner, one link and a 0700 parent")
	}
	parentIdentity, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || parentIdentity.Uid != uint32(os.Getuid()) {
		t.Fatal("private MySQL DSN parent must belong to the current user")
	}
	file, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		t.Fatal("private MySQL DSN file could not be opened")
	}
	defer file.Close()
	opened, e := file.Stat()
	if e != nil || !os.SameFile(before, opened) {
		t.Fatal("private MySQL DSN file identity changed")
	}
	body, readErr := io.ReadAll(io.LimitReader(file, (16<<10)+1))
	after, statErr := file.Stat()
	current, pathErr := os.Lstat(path)
	currentParent, directoryErr := os.Lstat(filepath.Dir(path))
	if readErr != nil || statErr != nil || pathErr != nil || directoryErr != nil || len(body) != int(before.Size()) || !os.SameFile(before, after) || !os.SameFile(after, current) || !os.SameFile(parent, currentParent) || after.Size() != before.Size() || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) || current.Mode() != before.Mode() {
		t.Fatal("private MySQL DSN file changed while reading")
	}
	dsn := strings.TrimSuffix(string(body), "\n")
	if dsn == "" || strings.ContainsAny(dsn, "\r\n\x00") {
		t.Fatal("private MySQL DSN file must contain one bounded configuration line")
	}
	return dsn
}

func fixture(t *testing.T) (*Service, string, string) {
	t.Helper()
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	dsn := ""
	if path := os.Getenv("LUOBO_INTEGRATION_MYSQL_DSN_FILE"); path != "" {
		dsn = fixtureDSNFile(t, path)
	} else {
		dsn = os.Getenv("LUOBO_INTEGRATION_MYSQL_DSN")
		if dsn == "" {
			dsn = os.Getenv("LUOBO_TEST_MYSQL_DSN")
		}
	}
	configuredSQL := dsn != ""
	if !configuredSQL {
		dsn = "root@unix(/Users/chuzu/Documents/projects/LuoboIM/references/integration/mysql8.4/mysql.sock)/luobo_integration?parseTime=true"
	}
	connection, e := driver.ParseDSN(dsn)
	if e != nil {
		t.Fatal("invalid isolated MySQL fixture configuration")
	}
	if connection.Net == "tcp" {
		host, _, e := net.SplitHostPort(connection.Addr)
		ip := net.ParseIP(host)
		if e != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			t.Fatal("MySQL fixture requires loopback TCP or a private Unix socket")
		}
	} else if connection.Net != "unix" || !filepath.IsAbs(connection.Addr) {
		t.Fatal("MySQL fixture requires loopback TCP or a private Unix socket")
	}
	connection.ParseTime = true
	connection.Timeout, connection.ReadTimeout, connection.WriteTimeout = time.Second, 3*time.Second, 3*time.Second
	root, e := gorm.Open(mysql.Open(connection.FormatDSN()), cfg)
	if e != nil {
		if configuredSQL {
			t.Fatal("configured isolated MySQL unavailable")
		}
		t.Skip("isolated MySQL unavailable; integration control not executed")
	}
	t.Cleanup(func() {
		if sql, _ := root.DB(); sql != nil {
			_ = sql.Close()
		}
	})
	name := "luobo_admin_delegate_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if e = root.Exec("CREATE DATABASE `" + name + "`").Error; e != nil {
		t.Fatal("isolated fixture schema creation failed")
	}
	t.Cleanup(func() { root.Exec("DROP DATABASE `" + name + "`") })
	connection.DBName = name
	db, e := gorm.Open(mysql.Open(connection.FormatDSN()), cfg)
	if e != nil {
		t.Fatal("isolated fixture connection failed")
	}
	t.Cleanup(func() {
		if sql, _ := db.DB(); sql != nil {
			_ = sql.Close()
		}
	})
	address, socket := os.Getenv("LUOBO_TEST_REDIS_ADDRESS"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	options := &redis.Options{Addr: "127.0.0.1:36379", DB: 15, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second}
	if address != "" {
		host, _, e := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		if e != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			t.Fatal("Redis fixture requires loopback TCP")
		}
		options.Addr = address
	} else if socket != "" {
		if !filepath.IsAbs(socket) {
			t.Fatal("Redis fixture requires a private absolute Unix socket")
		}
		options.Network, options.Addr = "unix", socket
	}
	rdb := redis.NewClient(options)
	id := "delegate-" + uuid.NewString()
	t.Cleanup(func() {
		_ = rdb.Del(context.Background(), "CHAT_UID_TOKEN_STATUS:"+id, accountstate.DisabledKeyPrefix+id).Err()
		_ = rdb.Close()
	})
	if rdb.Ping(context.Background()).Err() != nil {
		if configuredSQL || address != "" || socket != "" {
			t.Fatal("configured isolated Redis unavailable")
		}
		t.Skip("isolated Redis unavailable; integration control not executed")
	}
	if e = db.AutoMigrate(&admintable.Admin{}, &Record{}, &RequestAudit{}, &accountstate.CredentialRevocation{}); e != nil {
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
