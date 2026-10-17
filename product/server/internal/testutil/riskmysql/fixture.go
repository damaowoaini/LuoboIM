// Package riskmysql is an explicit synthetic integration-test fixture reader.
// No runtime service imports it. It never creates, drops or discovers databases.
package riskmysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

type privateDocument struct {
	Version    int    `json:"version"`
	Address    string `json:"address"`
	Database   string `json:"database"`
	User       string `json:"user"`
	Password   string `json:"password"`
	Claim      string `json:"claim"`
	ServerUUID string `json:"serverUUID"`
}

type Fixture struct {
	DB  *sql.DB
	doc privateDocument
}

func (f *Fixture) String() string { return "owned synthetic SQL fixture" }

func Open(t *testing.T) *Fixture {
	t.Helper()
	name := os.Getenv("LUOBO_RISK_TEST_MYSQL_FIXTURE")
	if name == "" {
		t.Skip("explicit owned SQL fixture not selected")
	}
	info, err := os.Lstat(name)
	parent, parentErr := os.Lstat(filepath.Dir(name))
	if err != nil || parentErr != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 4096 || !parent.IsDir() || parent.Mode().Perm() != 0700 || parent.Mode()&os.ModeSymlink != 0 {
		t.Fatal("SQL fixture file is not private regular input")
	}
	fileStat, ok := info.Sys().(*syscall.Stat_t)
	parentStat, parentOK := parent.Sys().(*syscall.Stat_t)
	if !ok || !parentOK || fileStat.Uid != uint32(os.Getuid()) || parentStat.Uid != uint32(os.Getuid()) || fileStat.Nlink != 1 {
		t.Fatal("SQL fixture ownership is invalid")
	}
	file, err := os.Open(name)
	if err != nil {
		t.Fatal("SQL fixture read failed")
	}
	opened, statErr := file.Stat()
	if statErr != nil || !os.SameFile(info, opened) {
		_ = file.Close()
		t.Fatal("SQL fixture input identity changed")
	}
	d := json.NewDecoder(io.LimitReader(file, 4097))
	d.DisallowUnknownFields()
	var doc privateDocument
	var extra any
	err = d.Decode(&doc)
	trailing := d.Decode(&extra)
	closeErr := file.Close()
	if err != nil || trailing != io.EOF || closeErr != nil {
		t.Fatal("SQL fixture document is invalid")
	}
	hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
	if doc.Version != 1 || doc.Address != "127.0.0.1:38406" || !regexp.MustCompile(`^luobo_risk_test_[0-9a-f]{24}$`).MatchString(doc.Database) || !regexp.MustCompile(`^lr_[0-9a-f]{16}$`).MatchString(doc.User) || !hex64.MatchString(doc.Password) || !hex64.MatchString(doc.Claim) {
		t.Fatal("SQL fixture is outside the exact owned contract")
	}
	if id, err := uuid.Parse(doc.ServerUUID); err != nil || id.String() != doc.ServerUUID {
		t.Fatal("SQL fixture server binding is invalid")
	}
	driver := mysqlDriver.NewConfig()
	driver.Net = "tcp"
	driver.Addr = doc.Address
	driver.User = doc.User
	driver.Passwd = doc.Password
	driver.DBName = doc.Database
	driver.Timeout = 2 * time.Second
	driver.ReadTimeout = 2 * time.Second
	driver.WriteTimeout = 2 * time.Second
	driver.Logger = quietLogger{}
	connector, err := mysqlDriver.NewConnector(driver)
	if err != nil {
		t.Fatal("SQL fixture connector failed")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(3)
	db.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error("SQL fixture close unconfirmed")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if db.PingContext(ctx) != nil {
		t.Fatal("SQL fixture connection failed")
	}
	var server, database, user, claim string
	if db.QueryRowContext(ctx, "SELECT @@server_uuid,DATABASE(),CURRENT_USER()").Scan(&server, &database, &user) != nil || server != doc.ServerUUID || database != doc.Database || strings.SplitN(user, "@", 2)[0] != doc.User {
		t.Fatal("SQL fixture server/database/user binding failed")
	}
	if db.QueryRowContext(ctx, "SELECT nonce FROM _luobo_risk_claim WHERE id=1").Scan(&claim) != nil || claim != doc.Claim {
		t.Fatal("SQL fixture claim unavailable")
	}
	return &Fixture{DB: db, doc: doc}
}

type quietLogger struct{}

func (quietLogger) Print(...interface{}) {}

func (f *Fixture) Config() *config.MYSQL {
	return &config.MYSQL{Address: []string{f.doc.Address}, Username: f.doc.User, Password: f.doc.Password, Database: f.doc.Database, MaxOpenConn: 2, MaxIdleConn: 1, MaxLifeTime: 60}
}

func (f *Fixture) WrongConfig() *config.MYSQL {
	c := f.Config()
	c.Password = "not-the-owned-password"
	return c
}

func (f *Fixture) ReplacePolicy(t *testing.T, values map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tx, err := f.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal("SQL fixture policy transaction unavailable")
	}
	committed := false
	defer func() {
		if !committed {
			if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
				t.Error("SQL fixture rollback unconfirmed")
			}
		}
	}()
	if _, err = tx.ExecContext(ctx, "DELETE FROM client_config"); err != nil {
		t.Fatal("SQL fixture policy reset failed")
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err = tx.ExecContext(ctx, "INSERT INTO client_config (`key`,value) VALUES (?,?)", k, values[k]); err != nil {
			t.Fatal("SQL fixture policy insert failed")
		}
	}
	if tx.Commit() != nil {
		t.Fatal("SQL fixture policy commit unconfirmed")
	}
	committed = true
}
