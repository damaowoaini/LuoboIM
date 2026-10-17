package dbconn_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/accountstate"
	"github.com/OpenIMSDK/chat/pkg/common/credentialmigration"
	admin "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	chat "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/dbconn"
	mysqldriver "github.com/go-sql-driver/mysql"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func isolatedMigrationMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LUOBO_INTEGRATION_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires explicit task-private MySQL socket")
	}
	cfg, err := mysqldriver.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" {
		t.Fatal("integration migration requires private Unix socket")
	}
	open := func(dsn string) *gorm.DB {
		db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal("cannot connect to task MySQL fixture")
		}
		return db
	}
	root := open(dsn)
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	name := "luobo_schema_lock_" + hex.EncodeToString(suffix[:])
	if err := root.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal(err)
	}
	cfg.DBName = name
	db := open(cfg.FormatDSN())
	pool, _ := db.DB()
	pool.SetMaxOpenConns(16)
	t.Cleanup(func() {
		pool.Close()
		if err := root.Exec("DROP DATABASE `" + name + "`").Error; err != nil {
			t.Error(err)
		}
		pool, _ := root.DB()
		pool.Close()
	})
	return db
}

func TestMySQLConcurrentServiceMigrationsShareTenantLock(t *testing.T) {
	db := isolatedMigrationMySQL(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			var err error
			switch i % 3 {
			case 0:
				err = credentialmigration.MigrateSchema(ctx, db, "admins", admin.Admin{}, admin.AdminMetadataMigration{}, credentialmigration.FormatUpgrade{}, accountstate.CredentialRevocation{})
			case 1:
				err = credentialmigration.MigrateSchema(ctx, db, "accounts", chat.Account{}, chat.AccountMetadataMigration{}, credentialmigration.FormatUpgrade{}, accountstate.Cancellation{})
			case 2:
				err = dbconn.AutoMigrate(ctx, db, &credentialmigration.FormatUpgrade{}, &accountstate.CredentialRevocation{}, &accountstate.Cancellation{})
			}
			if err != nil {
				t.Error("parallel service migration failed", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for _, name := range []string{"admins", "accounts", "credential_format_upgrades", "account_cancellations", "credential_revocations"} {
		if !db.Migrator().HasTable(name) {
			t.Error("parallel migrations lost table", name)
		}
	}
	if err := credentialmigration.CheckSchema(ctx, db, "accounts"); err != nil {
		t.Fatal(err)
	}
	if t.Failed() {
		return
	}
	t.Log("LUOBO_MYSQL_CONCURRENT_SCHEMA_MIGRATION_PASS")
}

func TestMySQLMigrationLockPinsSessionAndReleasesOnErrorCancellationAndPanic(t *testing.T) {
	db := isolatedMigrationMySQL(t)
	ctx := context.Background()
	var active atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := dbconn.WithSchemaLock(ctx, db, func(locked *gorm.DB) error {
				if active.Add(1) != 1 {
					t.Error("two schema writers entered simultaneously")
				}
				defer active.Add(-1)
				var first, second int64
				if err := locked.Raw("SELECT CONNECTION_ID()").Scan(&first).Error; err != nil {
					return err
				}
				time.Sleep(5 * time.Millisecond)
				if err := locked.Raw("SELECT CONNECTION_ID()").Scan(&second).Error; err != nil {
					return err
				}
				if first != second {
					return errors.New("migration changed physical connection")
				}
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	broken := errors.New("intentional failed migration")
	if err := dbconn.WithSchemaLock(ctx, db, func(*gorm.DB) error { return broken }); !errors.Is(err, broken) {
		t.Fatal("failed migration lost original error", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	if err := dbconn.WithSchemaLock(cancelCtx, db, func(*gorm.DB) error { cancel(); return cancelCtx.Err() }); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled migration did not return cancellation", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("expected fixture panic")
			}
		}()
		_ = dbconn.WithSchemaLock(ctx, db, func(*gorm.DB) error { panic("fixture panic") })
	}()
	verifyCtx, done := context.WithTimeout(ctx, 2*time.Second)
	defer done()
	if err := dbconn.WithSchemaLock(verifyCtx, db, func(locked *gorm.DB) error {
		var value sql.NullInt64
		return locked.Raw("SELECT 1").Scan(&value).Error
	}); err != nil {
		t.Fatal("error/cancellation/panic leaked a migration lock", err)
	}
}
