// visibility-floor-migrate is an explicit, tenant-scoped maintenance command.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/visibilitymigration"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func run() error {
	mode := flag.String("mode", "snapshot", "snapshot, select, apply, or rollback")
	path := flag.String("backup", "", "exclusive private backup file path")
	source := flag.String("source-backup", "", "original full private backup for explicit select")
	selected := flag.String("reviewed-ids", "", "comma-separated reviewed known tuple IDs for select")
	maintenance := flag.Bool("maintenance-confirmed", false, "all relevant application writers are stopped and backups retained")
	flag.Parse()
	if *path == "" {
		return fmt.Errorf("backup file path required")
	}
	if *mode != "snapshot" && *mode != "select" && *mode != "apply" && *mode != "rollback" {
		return fmt.Errorf("unknown mode")
	}
	if *mode == "select" {
		if *source == "" || *selected == "" {
			return fmt.Errorf("select needs original source-backup and reviewed-ids")
		}
		backup, err := visibilitymigration.Select(*source, strings.Split(*selected, ","))
		if err != nil {
			return err
		}
		if err := visibilitymigration.Save(*path, backup); err != nil {
			return err
		}
		fmt.Printf("reviewed selection saved: entries=%d skippedKnown=%d unresolvedRetainedInSource=%d\n", len(backup.Entries), backup.Selection.SkippedKnown, backup.Selection.SkippedUnresolved)
		return nil
	}
	if *mode != "snapshot" && !*maintenance {
		return fmt.Errorf("apply/rollback require explicit maintenance confirmation")
	}
	uri, database, tenant := os.Getenv("LUOBO_MONGO_URI"), os.Getenv("LUOBO_MONGO_DATABASE"), os.Getenv("LUOBO_TENANT_ID")
	if uri == "" || database == "" || tenant == "" {
		return fmt.Errorf("LUOBO_MONGO_URI, LUOBO_MONGO_DATABASE and LUOBO_TENANT_ID are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return fmt.Errorf("Mongo connection failed")
	}
	defer client.Disconnect(context.Background())
	db := client.Database(database)
	var rdb *redis.Client
	if *mode != "rollback" {
		redisURL := os.Getenv("LUOBO_REDIS_URL")
		if redisURL == "" {
			return fmt.Errorf("LUOBO_REDIS_URL required")
		}
		config, err := redis.ParseURL(redisURL)
		if err != nil {
			return fmt.Errorf("invalid Redis connection configuration")
		}
		rdb = redis.NewClient(config)
		defer rdb.Close()
	}
	if *mode == "snapshot" {
		backup, err := visibilitymigration.Snapshot(ctx, db, rdb, tenant)
		if err != nil {
			return err
		}
		if err := visibilitymigration.Save(*path, backup); err != nil {
			return err
		}
		fmt.Printf("snapshot saved: entries=%d unresolved=%d; unresolved entries block apply\n", len(backup.Entries), len(backup.Unresolved))
		return nil
	}
	backup, err := visibilitymigration.Load(*path)
	if err != nil {
		return err
	}
	if *mode == "apply" {
		err = visibilitymigration.Apply(ctx, db, rdb, tenant, backup)
	} else {
		err = visibilitymigration.Rollback(ctx, db, tenant, backup)
	}
	if err != nil {
		return err
	}
	fmt.Printf("%s completed: entries=%d\n", *mode, len(backup.Entries))
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "visibility migration failed:", err)
		os.Exit(1)
	}
}
