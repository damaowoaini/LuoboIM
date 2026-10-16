package credentialmigration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"crypto/md5"
	"encoding/json"
	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/credentialmigration"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	adminmodel "github.com/OpenIMSDK/chat/pkg/common/db/model/admin"
	admin "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	chat "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/passwordstore"
	"github.com/OpenIMSDK/chat/pkg/common/registration"
	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func isolatedMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LUOBO_INTEGRATION_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires an explicitly configured isolated MySQL Unix socket")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" {
		t.Fatal("integration tests require a private Unix socket")
	}
	adminDB, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to isolated MySQL")
	}
	var suffix [8]byte
	rand.Read(suffix[:])
	name := "luobo_credentials_test_" + hex.EncodeToString(suffix[:])
	if err := adminDB.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal(err)
	}
	cfg.DBName = name
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.Close()
		}
		adminDB.Exec("DROP DATABASE `" + name + "`")
		if sqlDB, err := adminDB.DB(); err == nil {
			sqlDB.Close()
		}
	})
	return db
}

type columnMetadata struct {
	IsNullable       string         `gorm:"column:IS_NULLABLE"`
	ColumnDefault    sql.NullString `gorm:"column:COLUMN_DEFAULT"`
	CharacterSetName string         `gorm:"column:CHARACTER_SET_NAME"`
	CollationName    string         `gorm:"column:COLLATION_NAME"`
	ColumnComment    string         `gorm:"column:COLUMN_COMMENT"`
	Extra            string         `gorm:"column:EXTRA"`
}

func passwordColumnMetadata(t *testing.T, db *gorm.DB, source string) columnMetadata {
	t.Helper()
	var column columnMetadata
	if err := db.Raw(`SELECT IS_NULLABLE,COLUMN_DEFAULT,CHARACTER_SET_NAME,COLLATION_NAME,COLUMN_COMMENT,EXTRA FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='password'`, source).Scan(&column).Error; err != nil {
		t.Fatal(err)
	}
	return column
}

func TestMySQLCredentialMigrationBackupUpgradeAndSafeRollback(t *testing.T) {
	db := isolatedMySQL(t)
	ctx := context.Background()
	const oldWire = "0123456789abcdef0123456789abcdef"
	const newWire = "abcdef0123456789abcdef0123456789"
	if err := db.Exec("CREATE TABLE accounts (user_id VARCHAR(64) PRIMARY KEY, password VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '0123456789abcdef0123456789abcdef' COMMENT 'wire credential')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE admins (account VARCHAR(64) PRIMARY KEY, user_id VARCHAR(64), password VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_bin NULL DEFAULT NULL COMMENT 'admin credential')").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO accounts(user_id,password) VALUES (?,?),(?,?)", "u1", oldWire, "u2", oldWire).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO admins(account,user_id,password) VALUES (?,?,?)", "admin-account", "admin-owner", oldWire).Error; err != nil {
		t.Fatal(err)
	}
	if err := credentialmigration.CheckSchema(ctx, db, "accounts"); err == nil {
		t.Fatal("narrow credential column accepted before encrypted backup")
	}
	accountMetadata := passwordColumnMetadata(t, db, "accounts")
	adminMetadata := passwordColumnMetadata(t, db, "admins")
	key := make([]byte, 32)
	rand.Read(key)
	path := filepath.Join(t.TempDir(), "credentials.enc")
	result, err := credentialmigration.Apply(ctx, db, key, path)
	if err != nil || result.BackedUp != 3 {
		t.Fatalf("backup/expand failed: %v (%+v)", err, result)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(before, []byte(oldWire)) {
		t.Fatal("backup contains unencrypted password digest")
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatal("backup is not private")
	}
	if err := credentialmigration.CheckSchema(ctx, db, "accounts"); err != nil {
		t.Fatal(err)
	}
	if _, err := credentialmigration.Apply(ctx, db, key, path); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("repeated migration overwrote original backup")
	}
	if err := db.AutoMigrate(&chat.AccountMetadataMigration{}, &chat.Register{}, &chat.Attribute{}, &chat.UserLoginRecord{}, &admin.AdminMetadataMigration{}, &accountstate.CredentialRevocation{}, &accountstate.Cancellation{}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(accountMetadata, passwordColumnMetadata(t, db, "accounts")) || !reflect.DeepEqual(adminMetadata, passwordColumnMetadata(t, db, "admins")) {
		t.Fatal("expansion or startup migration changed password column metadata")
	}
	chatDB := database.NewChatDatabase(db)
	adminDB := database.NewAdminDatabase(db, nil)
	for _, id := range []string{"u1", "u2"} {
		hashed, err := passwordstore.Hash(ctx, oldWire)
		if err != nil {
			t.Fatal(err)
		}
		if changed, err := chatDB.UpgradePasswordHash(ctx, id, oldWire, hashed); err != nil || !changed {
			t.Fatalf("legacy format upgrade failed: %v", err)
		}
		if changed, err := chatDB.UpgradePasswordHash(ctx, id, oldWire, hashed); err != nil || changed {
			t.Fatal("legacy CAS is not idempotent")
		}
	}
	adminHash, _ := passwordstore.Hash(ctx, oldWire)
	if changed, err := adminDB.UpgradeAdminPasswordHash(ctx, "admin-account", "admin-owner", oldWire, adminHash); err != nil || !changed {
		t.Fatal("admin credential owner upgrade failed")
	}
	if changed, err := adminDB.UpgradeAdminPasswordHash(ctx, "admin-account", "other-owner", adminHash, adminHash); err != nil || changed {
		t.Fatal("credential upgrade ignored account ownership")
	}
	if err := chatDB.UpdatePassword(ctx, "u2", newWire); err != nil {
		t.Fatal(err)
	}
	if pending, err := accountstate.HasPendingCredentials(ctx, db, "u2"); err != nil || !pending {
		t.Fatal("password update did not durably queue session revocation")
	}
	if err := chatDB.RegisterUser(ctx, &chat.Register{UserID: "new-user", CreateTime: time.Now()}, &chat.Account{UserID: "new-user", Password: newWire}, &chat.Attribute{UserID: "new-user", CreateTime: time.Now(), ChangeTime: time.Now(), BirthTime: time.Unix(0, 0)}); err != nil {
		t.Fatal(err)
	}
	result, err = credentialmigration.Rollback(ctx, db, key, path)
	if err != nil || result.Restored != 2 || result.Skipped != 1 {
		t.Fatalf("safe rollback failed: %v (%+v)", err, result)
	}
	one, _ := chatDB.GetAccount(ctx, "u1")
	if one.Password != oldWire {
		t.Fatal("unchanged legacy credential not restored")
	}
	for _, id := range []string{"u2", "new-user"} {
		account, err := chatDB.GetAccount(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if valid, legacy, err := passwordstore.Verify(ctx, account.Password, newWire); err != nil || !valid || legacy {
			t.Fatal("rollback overwrote changed/new user password")
		}
	}
	if _, err := credentialmigration.Rollback(ctx, db, key, path); err != nil {
		t.Fatal("repeated rollback failed")
	}
	wrongKey := make([]byte, 32)
	if _, err := credentialmigration.Apply(ctx, db, wrongKey, path); err == nil {
		t.Fatal("backup with wrong key was accepted")
	}
	otherTenant := isolatedMySQL(t)
	if _, err := credentialmigration.Apply(ctx, otherTenant, key, path); err == nil {
		t.Fatal("cross-tenant backup accepted")
	}
}

func TestMySQLPasswordResetConsumesCodeAndOutboxAtomically(t *testing.T) {
	db := isolatedMySQL(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&chat.Account{}, &chat.VerifyCode{}, &accountstate.CredentialRevocation{}); err != nil {
		t.Fatal(err)
	}
	const oldWire = "0123456789abcdef0123456789abcdef"
	const newWire = "abcdef0123456789abcdef0123456789"
	if err := db.Create(&chat.Account{UserID: "u1", Password: oldWire}).Error; err != nil {
		t.Fatal(err)
	}
	code := &chat.VerifyCode{Account: "u1", Code: "123456", Duration: 120, UsedFor: 2, CreateTime: time.Now()}
	if err := db.Create(code).Error; err != nil {
		t.Fatal(err)
	}
	store := database.NewChatDatabase(db)
	if err := store.UpdatePasswordAndDeleteVerifyCode(ctx, "u1", newWire, code.ID); err != nil {
		t.Fatal(err)
	}
	var used chat.VerifyCode
	if err := db.First(&used, code.ID).Error; err != nil || !used.Used {
		t.Fatal("successful OTP not retained as consumed")
	}
	if err := store.UpdatePasswordAndDeleteVerifyCode(ctx, "u1", oldWire, code.ID); err == nil {
		t.Fatal("concurrent/replayed reset accepted")
	}
	account, _ := store.GetAccount(ctx, "u1")
	if valid, _, _ := passwordstore.Verify(ctx, account.Password, newWire); !valid {
		t.Fatal("replayed reset changed current password")
	}
	var job accountstate.CredentialRevocation
	if err := db.First(&job, "user_id = ?", "u1").Error; err != nil || job.Generation != 1 {
		t.Fatal("replayed reset changed outbox generation")
	}
	second := &chat.VerifyCode{Account: "u1", Code: "654321", Duration: 120, UsedFor: 2, CreateTime: time.Now()}
	if err := db.Create(second).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TRIGGER fail_credential_outbox BEFORE INSERT ON credential_revocations FOR EACH ROW SIGNAL SQLSTATE '45000' SET MESSAGE_TEXT = 'simulated outbox failure'").Error; err != nil {
		t.Fatal(err)
	}
	if err := store.UpdatePasswordAndDeleteVerifyCode(ctx, "u1", oldWire, second.ID); err == nil {
		t.Fatal("outbox failure did not abort password reset")
	}
	var retained chat.VerifyCode
	if err := db.First(&retained, second.ID).Error; err != nil || retained.Used {
		t.Fatal("failed transaction consumed OTP")
	}
	account, _ = store.GetAccount(ctx, "u1")
	if valid, _, _ := passwordstore.Verify(ctx, account.Password, newWire); !valid {
		t.Fatal("failed outbox transaction committed new password")
	}
}

func TestMySQLRegistrationReceiptCommitsWithAccountAndOTP(t *testing.T) {
	db := isolatedMySQL(t)
	ctx := context.Background()
	if err := db.AutoMigrate(&chat.Account{}, &chat.Attribute{}, &chat.Register{}, &chat.VerifyCode{}, &registration.Receipt{}); err != nil {
		t.Fatal(err)
	}
	store := database.NewChatDatabase(db)
	register := func(id, userID string, codeID uint) error {
		now := time.Now()
		return store.RegisterUser(registration.WithID(ctx, id), &chat.Register{UserID: userID, CreateTime: now}, &chat.Account{UserID: userID, Password: "0123456789abcdef0123456789abcdef"}, &chat.Attribute{UserID: userID, Account: userID, Email: "Alice@example.com", BirthTime: time.Unix(0, 0), CreateTime: now, ChangeTime: now}, codeID)
	}
	code := &chat.VerifyCode{Account: "Alice@example.com", Code: "123456", UsedFor: 1, CreateTime: time.Now()}
	if err := db.Create(code).Error; err != nil {
		t.Fatal(err)
	}
	const id = "f27b0e7c-63df-4b94-b463-6c9644a8403b"
	if err := register(id, "receipt-owner", code.ID); err != nil {
		t.Fatal(err)
	}
	var receipt registration.Receipt
	if err := db.Where("registration_id = ?", id).Take(&receipt).Error; err != nil || receipt.UserID != "receipt-owner" {
		t.Fatal("receipt not atomic with account", err)
	}
	identity, _ := registration.Identity(" alice@EXAMPLE.COM ", "", "")
	if receipt.NormalizedIdentity != identity {
		t.Fatal("route canonical identity mismatch")
	}
	next := &chat.VerifyCode{Account: "Alice@example.com", Code: "654321", UsedFor: 1, CreateTime: time.Now()}
	if err := db.Create(next).Error; err != nil {
		t.Fatal(err)
	}
	if err := register("027b0e7c-63df-4b94-b463-6c9644a8403b", "wrong-owner", next.ID); err == nil {
		t.Fatal("second intent claimed existing identity")
	}
	var count int64
	db.Model(&chat.Account{}).Where("user_id = ?", "wrong-owner").Count(&count)
	if count != 0 {
		t.Fatal("receipt failure committed orphan credential")
	}
	var retained chat.VerifyCode
	if err := db.First(&retained, next.ID).Error; err != nil || retained.Used {
		t.Fatal("receipt failure consumed OTP")
	}
}

func TestMySQLAdminBootstrapNeverUsesPredictableDefaultOrResetsExisting(t *testing.T) {
	db := isolatedMySQL(t)
	if err := db.AutoMigrate(&admin.Admin{}); err != nil {
		t.Fatal(err)
	}
	old := config.Config.AdminList
	config.Config.AdminList = []config.Admin{{AdminID: "initial-admin", ImAdminID: "im-service-admin"}}
	defer func() { config.Config.AdminList = old }()
	t.Setenv("LUOBO_ADMIN_BOOTSTRAP_PASSWORDS_FILE", "")
	model := adminmodel.NewAdmin(db)
	if err := model.InitAdmin(context.Background()); err == nil {
		t.Fatal("empty database bootstrapped predictable credentials")
	}
	var count int64
	db.Model(&admin.Admin{}).Count(&count)
	if count != 0 {
		t.Fatal("failed bootstrap partially inserted admins")
	}
	password := "fixture-initial-password-with-enough-entropy"
	data, _ := json.Marshal(map[string]string{"initial-admin": password})
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LUOBO_ADMIN_BOOTSTRAP_PASSWORDS_FILE", path)
	var starts sync.WaitGroup
	for i := 0; i < 12; i++ {
		starts.Add(1)
		go func() {
			defer starts.Done()
			if err := model.InitAdmin(context.Background()); err != nil {
				t.Error("parallel bootstrap failed", err)
			}
		}()
	}
	starts.Wait()
	if err := db.Model(&admin.Admin{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatal("parallel bootstrap must create exactly one account", count, err)
	}
	row, err := model.Take(context.Background(), "initial-admin")
	if err != nil {
		t.Fatal(err)
	}
	digest := md5.Sum([]byte(password))
	valid, legacy, err := passwordstore.Verify(context.Background(), row.Password, hex.EncodeToString(digest[:]))
	if err != nil || !valid || legacy {
		t.Fatal("initial password not compatible slow hash")
	}
	original := row.Password
	t.Setenv("LUOBO_ADMIN_BOOTSTRAP_PASSWORDS_FILE", "")
	if err := model.InitAdmin(context.Background()); err != nil {
		t.Fatal("existing admins unexpectedly require bootstrap secret", err)
	}
	row, _ = model.Take(context.Background(), "initial-admin")
	if row.Password != original {
		t.Fatal("repeat bootstrap changed existing password")
	}
}
