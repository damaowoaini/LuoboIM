package riskcontrol

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/testutil/riskmysql"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

func actualRiskSQL(t *testing.T) *riskmysql.Fixture {
	t.Helper()
	f := riskmysql.Open(t)
	oldDB, oldConf := riskDatabase, config.Config.Mysql
	riskDatabase = newRiskDatabaseManager(openRiskDatabase, time.Now)
	config.Config.Mysql = f.Config()
	owned := riskDatabase
	t.Cleanup(func() {
		if owned.db != nil {
			pool, err := owned.db.DB()
			if err != nil || pool.Close() != nil {
				t.Error("risk shared pool close unconfirmed")
			}
		}
		riskDatabase, config.Config.Mysql = oldDB, oldConf
	})
	f.ReplacePolicy(t, map[string]string{RiskControlEnabledKey: "true", RiskDisableGroupActionKey: "true", RiskControlStartKey: "00:00", RiskControlEndKey: "00:00"})
	return f
}

func TestRiskSQLInitialCredentialFailureRecoversWithoutProcessRestart(t *testing.T) {
	f := actualRiskSQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	config.Config.Mysql = f.WrongConfig()
	if rows, err := GetConfigMap(ctx); rows != nil || err != errRiskDatabaseUnavailable || riskDatabase.db != nil {
		t.Fatal("initial SQL failure was not fail-closed")
	}
	config.Config.Mysql = f.Config()
	riskDatabase.mu.Lock()
	retry := riskDatabase.retryAt
	riskDatabase.mu.Unlock()
	wait := time.NewTimer(time.Until(retry))
	defer wait.Stop()
	select {
	case <-wait.C:
	case <-ctx.Done():
		t.Fatal("SQL retry budget expired")
	}
	if blocked, err := IsActionBlocked(ctx, RiskDisableGroupActionKey); err != nil || !blocked || riskDatabase.db == nil {
		t.Fatal("real SQL recovery did not reload blocking policy")
	}
	f.ReplacePolicy(t, map[string]string{RiskControlEnabledKey: "true", RiskDisableGroupActionKey: "false"})
	if blocked, err := IsActionBlocked(ctx, RiskDisableGroupActionKey); err != nil || blocked {
		t.Fatal("real SQL policy change was cached or invalid")
	}
}

func TestRiskSQLLockedReadHonorsContextAndRecoversSamePool(t *testing.T) {
	f := actualRiskSQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := GetConfigMap(ctx); err != nil {
		t.Fatal("real policy prerequisite failed")
	}
	pool := riskDatabase.db
	lock, err := f.DB.Conn(ctx)
	if err != nil {
		t.Fatal("owned SQL lock session unavailable")
	}
	defer func() {
		if lock.Close() != nil {
			t.Error("owned lock session close unconfirmed")
		}
	}()
	if _, err = lock.ExecContext(ctx, "LOCK TABLES client_config WRITE"); err != nil {
		t.Fatal("exact owned table lock was not acquired")
	}
	locked := true
	defer func() {
		if locked {
			end, stop := context.WithTimeout(context.Background(), 2*time.Second)
			defer stop()
			if _, err := lock.ExecContext(end, "UNLOCK TABLES"); err != nil {
				t.Error("owned SQL unlock unconfirmed")
			}
		}
	}()
	short, stop := context.WithTimeout(ctx, 80*time.Millisecond)
	rows, readErr := GetConfigMap(short)
	stop()
	if rows != nil || !errors.Is(readErr, context.DeadlineExceeded) {
		t.Fatal("locked risk policy did not reject by caller deadline")
	}
	if _, err = lock.ExecContext(ctx, "UNLOCK TABLES"); err != nil {
		t.Fatal("owned SQL unlock failed")
	}
	locked = false
	if blocked, err := IsActionBlocked(ctx, RiskDisableGroupActionKey); err != nil || !blocked || riskDatabase.db != pool {
		t.Fatal("policy read did not recover through the same shared pool")
	}
}

func TestRiskSQLKilledOwnedConnectionReconnectsWithoutPoolReplacement(t *testing.T) {
	f := actualRiskSQL(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := GetConfigMap(ctx); err != nil {
		t.Fatal("real policy prerequisite failed")
	}
	want := riskDatabase.db
	pool, err := want.DB()
	if err != nil {
		t.Fatal("shared SQL pool unavailable")
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	conn, err := pool.Conn(ctx)
	if err != nil {
		t.Fatal("owned risk connection unavailable")
	}
	var id uint64
	err = conn.QueryRowContext(ctx, "SELECT CONNECTION_ID()").Scan(&id)
	closeErr := conn.Close()
	if err != nil || closeErr != nil || id == 0 {
		t.Fatal("owned risk connection identity unavailable")
	}
	if _, err = f.DB.ExecContext(ctx, "KILL CONNECTION "+strconv.FormatUint(id, 10)); err != nil {
		t.Fatal("exact same-user connection kill did not ACK")
	}
	if blocked, err := IsActionBlocked(ctx, RiskDisableGroupActionKey); err != nil || !blocked || riskDatabase.db != want || pool.Stats().OpenConnections > 1 {
		t.Fatal("owned SQL connection recovery replaced/failed the shared pool")
	}
}
