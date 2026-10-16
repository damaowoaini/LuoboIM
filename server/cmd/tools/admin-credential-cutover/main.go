package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/openimsdk/open-im-server/v3/internal/admincutover"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/redis/go-redis/v9"
	"os"
	"path/filepath"
	"time"
)

func write(path string, plan *admincutover.Plan, exclusive bool) error {
	data, e := json.MarshalIndent(plan, "", "  ")
	if e != nil {
		return e
	}
	var f *os.File
	var temporary string
	if exclusive {
		f, e = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	} else {
		f, e = os.CreateTemp(filepath.Dir(path), ".admin-cutover-*")
		if e == nil {
			temporary = f.Name()
			defer os.Remove(temporary)
			e = f.Chmod(0600)
		}
	}
	if e != nil {
		return fmt.Errorf("private audit file unavailable")
	}
	if _, e = f.Write(append(data, '\n')); e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if !exclusive {
		if e = os.Rename(temporary, path); e != nil {
			return e
		}
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func run() error {
	directory := flag.String("config", "", "tenant server configuration directory")
	mode := flag.String("mode", "inspect", "inspect or revoke")
	report := flag.String("report", "", "new private metadata-only audit path")
	maintenance := flag.Bool("maintenance-confirmed", false, "all public management/gateway instances and signers have the new guard; old signers stopped")
	flag.Parse()
	if *directory == "" || *report == "" || (*mode != "inspect" && *mode != "revoke") {
		return fmt.Errorf("explicit config/report/mode required")
	}
	if *mode == "revoke" && !*maintenance {
		return fmt.Errorf("revocation requires coordinated deployment confirmation")
	}
	if e := config.InitConfig(*directory); e != nil {
		return fmt.Errorf("tenant configuration unavailable")
	}
	// Explicit tenant config is authoritative. Do not use the application
	// NewRedis factory, whose environment overrides could select another tenant.
	var rdb redis.UniversalClient
	cfg := config.Config.Redis
	if len(cfg.Address) == 0 {
		return fmt.Errorf("explicit tenant Redis endpoint required")
	}
	if cfg.ClusterMode || len(cfg.Address) > 1 {
		rdb = redis.NewClusterClient(&redis.ClusterOptions{Addrs: cfg.Address, Username: cfg.Username, Password: cfg.Password, DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	} else {
		rdb = redis.NewClient(&redis.Options{Addr: cfg.Address[0], Username: cfg.Username, Password: cfg.Password, DB: 0, DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	}
	defer rdb.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e := rdb.Ping(ctx).Err(); e != nil {
		return fmt.Errorf("explicit tenant Redis unavailable")
	}
	ids := append(append([]string{}, config.Config.Manager.UserID...), config.Config.IMAdmin.UserID...)
	plan, e := admincutover.Inspect(ctx, rdb, config.Config.Secret, config.Config.AdminDelegation.Issuer, ids)
	if e != nil {
		return e
	}
	if *mode == "revoke" {
		plan.State = "prepared"
	}
	if e = write(*report, plan, true); e != nil {
		return e
	}
	if *mode == "revoke" {
		e = admincutover.Revoke(ctx, rdb, plan)
		if saved := write(*report, plan, false); saved != nil {
			return saved
		}
		if e != nil {
			return e
		}
	}
	fmt.Println("tenant management cutover", plan.State, admincutover.MetadataOnly(plan))
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "management cutover failed:", e)
		os.Exit(1)
	}
}
