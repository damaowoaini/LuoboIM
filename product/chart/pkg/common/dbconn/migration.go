package dbconn

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// WithSchemaLock serializes every schema writer for the selected tenant on the
// same MySQL server. GET_LOCK is session-scoped, so both the lock and all DDL
// must use one pinned physical connection, rather than the general SQL pool.
// DDL implicitly commits in MySQL; a transaction cannot replace this lock.
func WithSchemaLock(ctx context.Context, db *gorm.DB, migrate func(*gorm.DB) error) error {
	if db.Dialector.Name() != "mysql" {
		return fmt.Errorf("schema migration lock requires MySQL")
	}
	return db.WithContext(ctx).Connection(func(locked *gorm.DB) (err error) {
		// Connection returns a statement-bound handle (clone=0). Keep a clean
		// session so lock-query destinations/SQL never leak into Migrator calls.
		locked = locked.Session(&gorm.Session{NewDB: true})
		conn, ok := locked.Statement.ConnPool.(*sql.Conn)
		if !ok {
			return fmt.Errorf("schema migration did not obtain a dedicated connection")
		}
		discard := func() { _ = conn.Raw(func(any) error { return driver.ErrBadConn }) }
		var name string
		if err := locked.Raw("SELECT DATABASE()").Scan(&name).Error; err != nil || name == "" {
			return fmt.Errorf("schema migration requires a selected database")
		}
		// Case aliases must share a lock on lower_case_table_names deployments.
		// Case-sensitive deployments may serialize two names unnecessarily,
		// which is preferable to racing the same schema on another platform.
		digest := sha256.Sum256([]byte(strings.ToLower(name)))
		const prefix = "luobo:schema:"
		key := prefix + hex.EncodeToString(digest[:])[:64-len(prefix)]
		var acquired sql.NullInt64
		if err := locked.Raw("SELECT GET_LOCK(?, ?)", key, 60).Scan(&acquired).Error; err != nil {
			// A canceled acquire can race with the server granting the lock.
			// Never return that possibly locked session to the connection pool.
			discard()
			return fmt.Errorf("acquire tenant schema migration lock: %w", err)
		}
		if !acquired.Valid || acquired.Int64 != 1 {
			return fmt.Errorf("tenant schema migration lock unavailable")
		}
		defer func() {
			// Cancellation of the migration must not cancel lock cleanup.
			releaseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var released sql.NullInt64
			releaseErr := locked.Session(&gorm.Session{NewDB: true}).WithContext(releaseCtx).Raw("SELECT RELEASE_LOCK(?)", key).Scan(&released).Error
			if releaseErr != nil || !released.Valid || released.Int64 != 1 {
				discard()
				err = errors.Join(err, fmt.Errorf("release tenant schema migration lock failed"))
			}
		}()
		return migrate(locked)
	})
}

func AutoMigrate(ctx context.Context, db *gorm.DB, models ...any) error {
	return WithSchemaLock(ctx, db, func(locked *gorm.DB) error { return locked.AutoMigrate(models...) })
}
