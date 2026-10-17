package credentialmigration

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/dbconn"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type FormatUpgrade struct {
	SourceTable     string    `gorm:"column:source_table;primaryKey;type:varchar(32)"`
	SourceKey       string    `gorm:"column:source_key;primaryKey;type:varchar(128)"`
	UserID          string    `gorm:"column:user_id;type:varchar(64);not null"`
	HashFingerprint string    `gorm:"column:hash_fingerprint;type:char(64);not null"`
	UpgradedAt      time.Time `gorm:"column:upgraded_at;not null"`
}

func (FormatUpgrade) TableName() string { return "credential_format_upgrades" }

func sourceColumn(source string) (string, error) {
	switch source {
	case "accounts":
		return "user_id", nil
	case "admins":
		return "account", nil
	}
	return "", fmt.Errorf("unsupported credential source")
}
func fingerprint(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

// Upgrade changes only representation, and does not reset the user's password
// or revoke sessions. Account owner and previous verifier both participate in CAS.
func Upgrade(ctx context.Context, db *gorm.DB, source, key, userID, old, hashed string) (bool, error) {
	column, err := sourceColumn(source)
	if err != nil {
		return false, err
	}
	changed := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Table(source).Where(column+" = ? AND user_id = ? AND password = ?", key, userID, old).UpdateColumn("password", hashed)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return nil
		}
		entry := &FormatUpgrade{SourceTable: source, SourceKey: key, UserID: userID, HashFingerprint: fingerprint(hashed), UpgradedAt: time.Now().UTC()}
		if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(entry).Error; err != nil {
			return err
		}
		changed = true
		return nil
	})
	return changed, err
}

// Existing narrow columns require an explicit backup before expansion. Fresh
// installations have no table yet and can create the wide schema normally.
func CheckSchema(ctx context.Context, db *gorm.DB, source string) error {
	if _, err := sourceColumn(source); err != nil {
		return err
	}
	if !db.Migrator().HasTable(source) {
		return nil
	}
	columns, err := db.WithContext(ctx).Migrator().ColumnTypes(source)
	if err != nil {
		return err
	}
	for _, column := range columns {
		if column.Name() == "password" {
			if length, ok := column.Length(); ok && length < 255 {
				return fmt.Errorf("%s.password requires encrypted backup and expansion; run credential-migrate apply before deploying", source)
			}
			return nil
		}
	}
	return fmt.Errorf("%s.password is missing", source)
}

// MigrateSchema keeps the credential-column preflight and model selection under
// the same tenant lock as DDL. Existing password column definitions are only
// changed by the explicit encrypted-backup migration tool.
func MigrateSchema(ctx context.Context, db *gorm.DB, source string, fresh, metadata any, tables ...any) error {
	return dbconn.WithSchemaLock(ctx, db, func(locked *gorm.DB) error {
		if err := CheckSchema(ctx, locked, source); err != nil {
			return err
		}
		model := fresh
		if locked.Migrator().HasTable(source) {
			model = metadata
		}
		return locked.AutoMigrate(append(tables, model)...)
	})
}

type backupRow struct {
	Source   string `json:"source"`
	Key      string `json:"key"`
	UserID   string `json:"userID"`
	Password string `json:"password"`
}
type snapshot struct {
	Version    int         `json:"version"`
	Database   string      `json:"database"`
	InstanceID string      `json:"instanceID"`
	CreatedAt  time.Time   `json:"createdAt"`
	Rows       []backupRow `json:"rows"`
}
type Result struct {
	BackedUp int `json:"backedUp"`
	Restored int `json:"restored"`
	Skipped  int `json:"skipped"`
}

func backupCipher(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("backup key must contain 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func readBackup(path string, key []byte) (*snapshot, error) {
	aead, err := backupCipher(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) < aead.NonceSize() {
		return nil, fmt.Errorf("invalid encrypted backup")
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte("luobo-credentials-v1"))
	if err != nil {
		return nil, fmt.Errorf("backup decryption failed")
	}
	var backup snapshot
	if err := json.Unmarshal(plain, &backup); err != nil {
		return nil, err
	}
	if backup.Version != 1 {
		return nil, fmt.Errorf("unsupported backup format")
	}
	return &backup, nil
}
func databaseName(ctx context.Context, db *gorm.DB) (string, error) {
	var name string
	err := db.WithContext(ctx).Raw("SELECT DATABASE()").Scan(&name).Error
	if err == nil && name == "" {
		err = fmt.Errorf("select a tenant database before migration")
	}
	return name, err
}
func instanceID(ctx context.Context, db *gorm.DB) (string, error) {
	var id string
	err := db.WithContext(ctx).Raw("SELECT @@server_uuid").Scan(&id).Error
	return id, err
}

// Expand only the storage capacity: preserve deployment-specific constraints,
// character comparison semantics, defaults and comments from the existing column.
func expandPasswordColumn(ctx context.Context, db *gorm.DB, source string) error {
	if _, err := sourceColumn(source); err != nil {
		return err
	}
	var column struct {
		DataType             string         `gorm:"column:DATA_TYPE"`
		IsNullable           string         `gorm:"column:IS_NULLABLE"`
		ColumnDefault        sql.NullString `gorm:"column:COLUMN_DEFAULT"`
		CharacterSetName     string         `gorm:"column:CHARACTER_SET_NAME"`
		CollationName        string         `gorm:"column:COLLATION_NAME"`
		ColumnComment        string         `gorm:"column:COLUMN_COMMENT"`
		Extra                string         `gorm:"column:EXTRA"`
		GenerationExpression string         `gorm:"column:GENERATION_EXPRESSION"`
	}
	result := db.WithContext(ctx).Raw(`SELECT DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT, CHARACTER_SET_NAME, COLLATION_NAME, COLUMN_COMMENT, EXTRA, GENERATION_EXPRESSION FROM INFORMATION_SCHEMA.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME='password'`, source).Scan(&column)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 || column.DataType != "varchar" || column.GenerationExpression != "" {
		return fmt.Errorf("unsupported %s.password definition", source)
	}
	identifier := regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	if !identifier.MatchString(column.CharacterSetName) || !identifier.MatchString(column.CollationName) {
		return fmt.Errorf("invalid credential character set metadata")
	}
	var mode string
	if err := db.WithContext(ctx).Raw("SELECT @@SESSION.sql_mode").Scan(&mode).Error; err != nil {
		return err
	}
	literal := func(value string) string {
		if !strings.Contains(strings.ToUpper(mode), "NO_BACKSLASH_ESCAPES") {
			value = strings.ReplaceAll(value, `\`, `\\`)
		}
		return "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	definition := "VARCHAR(255) CHARACTER SET " + column.CharacterSetName + " COLLATE " + column.CollationName
	if column.IsNullable == "YES" {
		definition += " NULL"
	} else {
		definition += " NOT NULL"
	}
	if column.ColumnDefault.Valid {
		if strings.Contains(column.Extra, "DEFAULT_GENERATED") {
			definition += " DEFAULT (" + column.ColumnDefault.String + ")"
		} else {
			definition += " DEFAULT " + literal(column.ColumnDefault.String)
		}
	} else if column.IsNullable == "YES" {
		definition += " DEFAULT NULL"
	}
	if strings.TrimSpace(strings.ReplaceAll(column.Extra, "DEFAULT_GENERATED", "")) != "" {
		return fmt.Errorf("unsupported credential column extra attributes")
	}
	definition += " COMMENT " + literal(column.ColumnComment)
	return db.WithContext(ctx).Exec("ALTER TABLE `" + source + "` MODIFY COLUMN `password` " + definition).Error
}

// Apply must run with old credential writers stopped. It never overwrites the
// sole encrypted backup, and re-running with that backup is safe.
func Apply(ctx context.Context, db *gorm.DB, key []byte, path string) (Result, error) {
	var result Result
	err := dbconn.WithSchemaLock(ctx, db, func(locked *gorm.DB) error {
		var err error
		result, err = apply(ctx, locked, key, path)
		return err
	})
	return result, err
}

func apply(ctx context.Context, db *gorm.DB, key []byte, path string) (Result, error) {
	var result Result
	name, err := databaseName(ctx, db)
	if err != nil {
		return result, err
	}
	instance, err := instanceID(ctx, db)
	if err != nil {
		return result, err
	}
	backup, err := readBackup(path, key)
	if os.IsNotExist(err) {
		backup = &snapshot{Version: 1, Database: name, InstanceID: instance, CreatedAt: time.Now().UTC()}
		for _, source := range []string{"accounts", "admins"} {
			if !db.Migrator().HasTable(source) {
				continue
			}
			column, _ := sourceColumn(source)
			var rows []struct {
				Key      string
				UserID   string
				Password string
			}
			if err := db.WithContext(ctx).Table(source).Select(column + " AS `key`, user_id, password").Scan(&rows).Error; err != nil {
				return result, err
			}
			for _, row := range rows {
				backup.Rows = append(backup.Rows, backupRow{Source: source, Key: row.Key, UserID: row.UserID, Password: row.Password})
			}
		}
		aead, err := backupCipher(key)
		if err != nil {
			return result, err
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return result, err
		}
		plain, err := json.Marshal(backup)
		if err != nil {
			return result, err
		}
		encrypted := aead.Seal(nonce, nonce, plain, []byte("luobo-credentials-v1"))
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return result, err
		}
		if _, err := file.Write(encrypted); err != nil {
			file.Close()
			return result, err
		}
		if err := file.Sync(); err != nil {
			file.Close()
			return result, err
		}
		if err := file.Close(); err != nil {
			return result, err
		}
	} else if err != nil {
		return result, err
	}
	if backup.Database != name || backup.InstanceID != instance {
		return result, fmt.Errorf("backup belongs to a different tenant database or instance")
	}
	// Sync the parent even when resuming after a previously interrupted run.
	// A flushed file alone does not durably persist its new directory entry.
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return result, err
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return result, err
	}
	if err := directory.Close(); err != nil {
		return result, err
	}
	result.BackedUp = len(backup.Rows)
	if err := db.WithContext(ctx).AutoMigrate(&FormatUpgrade{}); err != nil {
		return result, err
	}
	for _, source := range []string{"accounts", "admins"} {
		if db.Migrator().HasTable(source) {
			if err := expandPasswordColumn(ctx, db, source); err != nil {
				return result, err
			}
		}
	}
	return result, nil
}

// Rollback restores only unchanged format upgrades. A password changed after
// migration, a reassigned account, or a newly created user is never overwritten.
// Keep the dual-format reader deployed; columns are intentionally not narrowed.
func Rollback(ctx context.Context, db *gorm.DB, key []byte, path string) (Result, error) {
	var result Result
	err := dbconn.WithSchemaLock(ctx, db, func(locked *gorm.DB) error {
		var err error
		result, err = rollback(ctx, locked, key, path)
		return err
	})
	return result, err
}

func rollback(ctx context.Context, db *gorm.DB, key []byte, path string) (Result, error) {
	var result Result
	backup, err := readBackup(path, key)
	if err != nil {
		return result, err
	}
	name, err := databaseName(ctx, db)
	if err != nil {
		return result, err
	}
	if name != backup.Database {
		return result, fmt.Errorf("backup belongs to a different tenant database")
	}
	instance, err := instanceID(ctx, db)
	if err != nil {
		return result, err
	}
	if instance != backup.InstanceID {
		return result, fmt.Errorf("backup belongs to a different MySQL instance")
	}
	for _, row := range backup.Rows {
		column, err := sourceColumn(row.Source)
		if err != nil {
			return result, err
		}
		err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var current struct{ Password string }
			query := tx.Table(row.Source).Where(column+" = ? AND user_id = ?", row.Key, row.UserID)
			if err := query.Take(&current).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					result.Skipped++
					return nil
				}
				return err
			}
			var entry FormatUpgrade
			if err := tx.Where("source_table = ? AND source_key = ? AND user_id = ?", row.Source, row.Key, row.UserID).Take(&entry).Error; err != nil {
				if err == gorm.ErrRecordNotFound {
					result.Skipped++
					return nil
				}
				return err
			}
			if fingerprint(current.Password) != entry.HashFingerprint {
				result.Skipped++
				return nil
			}
			change := tx.Table(row.Source).Where(column+" = ? AND user_id = ? AND password = ?", row.Key, row.UserID, current.Password).UpdateColumn("password", row.Password)
			if change.Error != nil {
				return change.Error
			}
			if change.RowsAffected == 1 {
				result.Restored++
			} else {
				result.Skipped++
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	return result, nil
}
