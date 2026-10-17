package dbconn

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	driver "github.com/go-sql-driver/mysql"
	"gorm.io/gorm/logger"
)

func TestMysqlSchemaStartupWithNoSystemDatabaseAccess(t *testing.T) {
	raw := os.Getenv("LUOBO_MYSQL_STARTUP_TEST_DSN")
	if raw == "" {
		t.Skip("requires explicit isolated MySQL Unix-socket administrator fixture")
	}
	admin, err := driver.ParseDSN(raw)
	if err != nil || admin.Net != "unix" || !strings.Contains(admin.Addr, "references/integration/") {
		t.Fatal("fixture must be the task-only Unix socket")
	}
	pool, err := sql.Open("mysql", admin.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatal(err)
	}
	suffix := hex.EncodeToString(bytes[:])
	name, createName, user, password := "luobo_start_"+suffix, "luobo_create_"+suffix, "luobo_"+suffix, suffix+suffix
	exec := func(query string) {
		t.Helper()
		if _, err := pool.Exec(query); err != nil {
			t.Fatal("fixture setup failed")
		}
	}
	exec("CREATE DATABASE `" + name + "`")
	defer pool.Exec("DROP DATABASE IF EXISTS `" + name + "`")
	defer pool.Exec("DROP DATABASE IF EXISTS `" + createName + "`")
	exec(fmt.Sprintf("CREATE USER '%s'@'localhost' IDENTIFIED BY '%s'", user, password))
	defer pool.Exec(fmt.Sprintf("DROP USER IF EXISTS '%s'@'localhost'", user))
	exec(fmt.Sprintf("GRANT SELECT ON `%s`.* TO '%s'@'localhost'", name, user))
	exec(fmt.Sprintf("GRANT CREATE,SELECT ON `%s`.* TO '%s'@'localhost'", createName, user))
	limited := admin.Clone()
	limited.User = user
	limited.Passwd = password
	limited.DBName = name
	db, err := openMysqlDatabase(limited, logger.Default.LogMode(logger.Silent))
	if err != nil {
		t.Fatal("existing schema-only user could not start")
	}
	var actual string
	if err := db.Raw("SELECT DATABASE()").Scan(&actual).Error; err != nil || actual != name {
		t.Fatal("wrong selected schema")
	}
	closeMysqlPool(db)
	// This account cannot select mysql.*. The previous implementation required
	// it unconditionally, even when the configured schema already existed.
	denied := limited.Clone()
	denied.DBName = "mysql"
	_, err = openMysqlDatabase(denied, logger.Default.LogMode(logger.Silent))
	var mysqlError *driver.MySQLError
	if !errors.As(err, &mysqlError) || mysqlError.Number != 1044 {
		t.Fatal("fixture unexpectedly had system database access")
	}
	missing := limited.Clone()
	missing.DBName = createName
	db, err = openMysqlDatabase(missing, logger.Default.LogMode(logger.Silent))
	if err != nil {
		t.Fatal("scoped create privilege could not initialize its configured database")
	}
	if err := db.Raw("SELECT DATABASE()").Scan(&actual).Error; err != nil || actual != createName {
		t.Fatal("created wrong schema")
	}
	closeMysqlPool(db)
}

func TestMysqlDatabaseIdentifiersCannotEscapeCreateStatement(t *testing.T) {
	for _, invalid := range []string{"", "../other", "a/b", "a\\b", "a\x00b", " trailing", strings.Repeat("a", 65)} {
		if _, err := mysqlDatabaseIdentifier(invalid); err == nil {
			t.Fatal("invalid database identifier accepted")
		}
	}
	if quoted, err := mysqlDatabaseIdentifier("tenant`name"); err != nil || quoted != "`tenant``name`" {
		t.Fatal("identifier quote was not escaped")
	}
}
