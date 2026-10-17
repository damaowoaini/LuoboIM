package api

import (
	"context"
	driver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"strings"
	"testing"
)

func TestMySQLLegacyCredentialAuditIsDurableAndCannotBeRewritten(t *testing.T) {
	dsn := os.Getenv("LUOBO_INTEGRATION_MYSQL_DSN")
	if dsn == "" {
		t.Skip("requires task-private MySQL Unix socket")
	}
	cfg, err := driver.ParseDSN(dsn)
	if err != nil || cfg.Net != "unix" || !strings.Contains(cfg.Addr, "/references/integration/mysql8.4/") {
		t.Fatal("only private MySQL fixture permitted")
	}
	root, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("cannot connect to private MySQL")
	}
	name := "luobo_preview_audit_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE DATABASE `" + name + "`").Error; err != nil {
		t.Fatal(err)
	}
	rootSQL, _ := root.DB()
	defer func() { root.Exec("DROP DATABASE `" + name + "`"); rootSQL.Close() }()
	cfg.DBName = name
	db, err := gorm.Open(mysql.Open(cfg.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	if db.AutoMigrate(&adminCredentialAudit{}) != nil {
		t.Fatal("audit schema unavailable")
	}
	store := &sqlCredentialAudit{db: db}
	id, err := store.Begin(context.Background(), "admin-actor", "target-user", 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(context.Background(), id, "issued"); err != nil {
		t.Fatal(err)
	}
	var record adminCredentialAudit
	if db.First(&record, "id=?", id).Error != nil || record.Actor != "admin-actor" || record.Target != "target-user" || record.Platform != 10 || record.State != "issued" || record.FinishedAt == nil {
		t.Fatal("credential audit lost actor/target/state")
	}
	if store.Finish(context.Background(), id, "failed") == nil {
		t.Fatal("completed credential audit could be rewritten")
	}
	if err := db.Migrator().DropTable(&adminCredentialAudit{}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Begin(context.Background(), "admin-actor", "target-user", 10); err == nil {
		t.Fatal("storage failure fabricated durable audit")
	}
}
