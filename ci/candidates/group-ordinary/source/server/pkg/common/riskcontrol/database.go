package riskcontrol

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	gormMysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	riskReadBudget    = 5 * time.Second
	riskConnectBudget = 3 * time.Second
	riskIOTimeout     = 2 * time.Second
	riskRetryDelay    = time.Second
)

// Never retain or return driver errors containing addresses or credentials.
var errRiskDatabaseUnavailable = errors.New("risk control database unavailable")

// Only failed initialization is retried. A successfully initialized sql.DB is
// shared for the process lifetime and reconnects through database/sql. A query
// failure never falls back to an older policy or creates another pool.
var riskDatabase = newRiskDatabaseManager(openRiskDatabase, time.Now)

type riskDatabaseManager struct {
	mu           sync.Mutex
	db           *gorm.DB
	initializing chan struct{}
	retryAt      time.Time
	open         func(context.Context) (*gorm.DB, error)
	now          func() time.Time
}

func newRiskDatabaseManager(open func(context.Context) (*gorm.DB, error), now func() time.Time) *riskDatabaseManager {
	return &riskDatabaseManager{open: open, now: now}
}

func (m *riskDatabaseManager) get(ctx context.Context) (*gorm.DB, error) {
	if m == nil || ctx == nil {
		return nil, errRiskDatabaseUnavailable
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if m.db != nil {
			db := m.db
			m.mu.Unlock()
			return db, nil
		}
		if wait := m.initializing; wait != nil {
			m.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-wait:
				continue
			}
		}
		if m.now().Before(m.retryAt) {
			m.mu.Unlock()
			return nil, errRiskDatabaseUnavailable
		}
		wait := make(chan struct{})
		m.initializing = wait
		m.mu.Unlock()

		attempt, cancel := context.WithTimeout(ctx, riskConnectBudget)
		db, err := m.open(attempt)
		cancel()
		if err == nil && db == nil {
			err = errRiskDatabaseUnavailable
		}
		m.mu.Lock()
		if err == nil {
			m.db = db
			m.retryAt = time.Time{}
		} else {
			// Includes missing initial configuration. It is not a permanent
			// sync.Once failure, and concurrent callers do not open N pools.
			m.retryAt = m.now().Add(riskRetryDelay)
		}
		m.initializing = nil
		close(wait)
		m.mu.Unlock()
		if err != nil {
			return nil, riskDatabaseError(ctx, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return db, nil
	}
}

func riskDatabaseError(ctx context.Context, _ error) error {
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return errRiskDatabaseUnavailable
}

type quietRiskMySQLLogger struct{}

func (quietRiskMySQLLogger) Print(...interface{}) {}

func riskDriverConfig(conf *config.MYSQL) (*mysqlDriver.Config, error) {
	if conf == nil || len(conf.Address) == 0 || strings.TrimSpace(conf.Address[0]) == "" || strings.TrimSpace(conf.Database) == "" {
		return nil, errRiskDatabaseUnavailable
	}
	// NewConfig/Connector preserve escaping without interpolating a raw DSN.
	driver := mysqlDriver.NewConfig()
	driver.User, driver.Passwd = conf.Username, conf.Password
	driver.Net, driver.Addr, driver.DBName = "tcp", conf.Address[0], conf.Database
	driver.ParseTime, driver.Loc = true, time.Local
	driver.Params = map[string]string{"charset": "utf8mb4"}
	driver.Timeout, driver.ReadTimeout, driver.WriteTimeout = riskIOTimeout, riskIOTimeout, riskIOTimeout
	driver.Logger = quietRiskMySQLLogger{}
	return driver, nil
}

func openRiskDatabase(ctx context.Context) (*gorm.DB, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, riskDatabaseError(ctx, errRiskDatabaseUnavailable)
	}
	// Runtime configuration is loaded before RPC serving. Missing initial
	// configuration may be corrected before a subsequent initialization attempt.
	conf := config.Config.Mysql
	driver, err := riskDriverConfig(conf)
	if err != nil {
		return nil, err
	}
	connector, err := mysqlDriver.NewConnector(driver)
	if err != nil {
		return nil, riskDatabaseError(ctx, err)
	}
	pool := sql.OpenDB(connector)
	maxOpen, maxIdle := 8, 2
	if conf.MaxOpenConn > 0 {
		maxOpen = conf.MaxOpenConn
	}
	if conf.MaxIdleConn > 0 {
		maxIdle = conf.MaxIdleConn
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	pool.SetMaxOpenConns(maxOpen)
	pool.SetMaxIdleConns(maxIdle)
	lifetime := 5 * time.Minute
	if conf.MaxLifeTime > 0 && uint64(conf.MaxLifeTime) <= uint64((1<<63-1)/int64(time.Second)) {
		lifetime = time.Duration(conf.MaxLifeTime) * time.Second
	}
	pool.SetConnMaxLifetime(lifetime)
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close() // Failed pool is never published, even if close fails.
		return nil, riskDatabaseError(ctx, err)
	}
	// This package only SELECTs client_config. Avoid GORM's background-context
	// SELECT VERSION()/automatic ping: the explicit PingContext above is bounded.
	// No schema migration, update, or version-dependent DDL is performed here.
	db, err := gorm.Open(gormMysql.New(gormMysql.Config{Conn: pool, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		_ = pool.Close()
		return nil, riskDatabaseError(ctx, err)
	}
	if ctx.Err() != nil {
		_ = pool.Close()
		return nil, ctx.Err()
	}
	return db, nil
}
