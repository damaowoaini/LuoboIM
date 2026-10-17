package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/credentialmigration"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "credential-migrate -action apply|rollback -backup /private/path/credentials.enc -maintenance\nRequires LUOBO_MIGRATION_DSN and LUOBO_CREDENTIAL_BACKUP_KEY (64 hex characters). Stop old credential writers first; keep the dual-format verifier deployed for rollback.")
	}
	action := flag.String("action", "apply", "apply or rollback")
	backup := flag.String("backup", "", "encrypted backup file")
	maintenance := flag.Bool("maintenance", false, "credential writers are stopped")
	flag.Parse()
	if !*maintenance || *backup == "" || (*action != "apply" && *action != "rollback") {
		flag.Usage()
		os.Exit(2)
	}
	key, err := hex.DecodeString(os.Getenv("LUOBO_CREDENTIAL_BACKUP_KEY"))
	if err != nil || len(key) != 32 {
		fmt.Fprintln(os.Stderr, "invalid backup key")
		os.Exit(2)
	}
	dsn := os.Getenv("LUOBO_MIGRATION_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "migration DSN is required")
		os.Exit(2)
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot connect to migration database")
		os.Exit(1)
	}
	sqlDB, err := db.DB()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot acquire database connection")
		os.Exit(1)
	}
	defer sqlDB.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var result credentialmigration.Result
	if *action == "apply" {
		result, err = credentialmigration.Apply(ctx, db, key, *backup)
	} else {
		result, err = credentialmigration.Rollback(ctx, db, key, *backup)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "credential migration failed:", err)
		os.Exit(1)
	}
	json.NewEncoder(os.Stdout).Encode(result)
}
