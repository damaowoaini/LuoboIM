//go:build linux

package runtimeacceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/robfig/cron/v3"
)

// Read the actual private configuration writer's output. No service, database,
// scheduler goroutine or Ready/cleanup permit is created by this contract.
func TestFreshFixtureCronConfigurationMatchesDefaultScheduler(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "runtime-configuration"
		if legacy {
			name = "legacy-configuration"
		}
		t.Run(name, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(root, 0700); err != nil {
				t.Fatal(err)
			}
			credentials := map[string]string{"serviceSecret": "synthetic-fixture-secret", "minioUser": "synthetic_minio", "minioSecret": "synthetic-fixture-minio"}
			for _, database := range []string{"mongo", "redis", "kafka"} {
				for _, scope := range []string{"Legacy", "Runtime"} {
					credentials[database+scope+"User"] = "synthetic_" + strings.ToLower(database+scope)
					credentials[database+scope+"Password"] = "synthetic-fixture-password"
				}
			}
			boot := &Bootstrap{Plan: PrivatePlan{Work: root}, Header: namespaceimport.Header{Database: "synthetic_cron_configuration"}, Resource: ResourcePlane{Nonce: strings.Repeat("a", 32)}, ManagerID: "synthetic_cron_manager", Credential: credentials, Ports: map[string]int{"api": 31000, "msggateway": 31002, "push": 31019, "rpc-auth": 31010, "rpc-conversation": 31011, "rpc-encryption": 31012, "rpc-friend": 31013, "rpc-group": 31014, "rpc-msg": 31015, "rpc-rtc": 31016, "rpc-third": 31017, "rpc-user": 31018}}
			directory, err := boot.writeConfiguration(name, legacy, "127.0.0.1:6379", []string{"synthetic_online", "synthetic_offline", "synthetic_push"}, []string{"synthetic_online_group", "synthetic_offline_group", "synthetic_push_group"})
			if err != nil {
				t.Fatal("actual fixture writer failed", err)
			}
			// writePrivate emits JSON, which is the existing config.yaml wire form.
			raw, err := os.ReadFile(filepath.Join(directory, "config.yaml"))
			if err != nil {
				t.Fatal(err)
			}
			var generated struct {
				Clear    string `json:"chatRecordsClearTime"`
				Destruct string `json:"msgDestructTime"`
			}
			if err := json.Unmarshal(raw, &generated); err != nil {
				t.Fatal(err)
			}
			scheduler := cron.New()
			reference := time.Date(2030, time.January, 2, 23, 57, 0, 0, time.UTC)
			for index, spec := range []string{generated.Clear, generated.Destruct} {
				parsed, err := cron.ParseStandard(spec)
				if err != nil {
					t.Fatal("generated configuration is not a default cron schedule", err)
				}
				id, err := scheduler.AddFunc(spec, func() { t.Error("contract must not start the scheduler") })
				if err != nil {
					t.Fatal("actual default scheduler rejected generated configuration", err)
				}
				entry := scheduler.Entry(id)
				if entry.Schedule == nil {
					t.Fatal("default scheduler lost registered job")
				}
				next := entry.Schedule.Next(reference)
				if !next.Equal(parsed.Next(reference)) || next.Hour() != 23 || next.Minute() != 59-index || next.Second() != 0 || next.Day() != reference.Day() || entry.Schedule.Next(next).Sub(next) != 24*time.Hour {
					t.Fatal("generated schedule did not preserve the intended daily clock time")
				}
			}
			if len(scheduler.Entries()) != 2 {
				t.Fatal("fixture must register both cron jobs")
			}
		})
	}
	t.Run("hour-minute-shorthand-is-rejected", func(t *testing.T) {
		scheduler := cron.New()
		for _, spec := range []string{"23:59", "23:58"} {
			if _, err := cron.ParseStandard(spec); err == nil {
				t.Fatal("HH:MM became a valid cron expression")
			}
			if _, err := scheduler.AddFunc(spec, func() {}); err == nil {
				t.Fatal("default scheduler accepted the old fixture shorthand")
			}
		}
		if len(scheduler.Entries()) != 0 {
			t.Fatal("rejected shorthand registered a job")
		}
	})
}
