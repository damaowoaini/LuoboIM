package chat

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	drivermysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func complaintMySQL(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("LUOBO_COMPLAINT_MYSQL_DSN")
	if dsn == "" {
		t.Skip("explicit isolated MySQL fixture DSN required")
	}
	config, err := drivermysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid isolated MySQL fixture DSN")
	}
	if config.Net != "unix" || !strings.Contains(config.Addr, "/LuoboIM/references/integration/") {
		t.Fatal("complaint integration accepts only the task's private Unix MySQL fixture")
	}
	config.DBName = ""
	root, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("private MySQL connection unavailable")
	}
	name := "luobo_complaint_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err := root.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal("isolated complaint schema unavailable")
	}
	config.DBName = name
	db, err := gorm.Open(mysql.Open(config.FormatDSN()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("isolated complaint schema connection failed")
	}
	pool, _ := db.DB()
	pool.SetMaxOpenConns(4)
	t.Cleanup(func() { pool.Close(); root.Exec("DROP DATABASE `" + name + "`"); p, _ := root.DB(); p.Close() })
	if err := db.AutoMigrate(&table.Complaint{}); err != nil {
		t.Fatal("complaint fixture schema migration failed")
	}
	return db
}

func complaintCandidate(owner, key, hash string) *table.Complaint {
	return &table.Complaint{ID: uuid.NewString(), ComplaintUserID: owner, ClientSubmissionID: &key, PayloadHash: hash,
		ComplaintType: 1, TargetID: "synthetic-target", Reason: "其他", Evidence: "[]", CreateTime: time.Now(), UpdateTime: time.Now()}
}

func TestComplaintMySQLConcurrentReceiptAndOwnerIsolation(t *testing.T) {
	db := complaintMySQL(t)
	store := NewComplaint(db)
	key := uuid.NewString()
	hash := strings.Repeat("a", 64)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	results := make(chan *table.Complaint, 12)
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row := complaintCandidate("owner-a", key, hash)
			if err := store.Create(ctx, row); err != nil {
				failures <- err
				return
			}
			results <- row
		}()
	}
	wg.Wait()
	close(results)
	close(failures)
	for range failures {
		t.Fatal("concurrent receipt creation failed")
	}
	ids := map[string]bool{}
	for row := range results {
		ids[row.ID] = true
	}
	if len(ids) != 1 {
		t.Fatal("same owner submission created multiple complaint IDs")
	}
	var count int64
	db.Model(&table.Complaint{}).Count(&count)
	if count != 1 {
		t.Fatal("same-key row count is not one")
	}
	// Simulate a lost ACK followed by another process recovering the same key.
	restarted := NewComplaint(db)
	replay := complaintCandidate("owner-a", key, hash)
	if err := restarted.Create(ctx, replay); err != nil || !ids[replay.ID] {
		t.Fatal("lost ACK replay failed to recover original complaint")
	}
	changed := complaintCandidate("owner-a", key, strings.Repeat("b", 64))
	if err := store.Create(ctx, changed); !errors.Is(err, table.ErrComplaintSubmissionConflict) {
		t.Fatal("changed payload stole the existing receipt")
	}
	other := complaintCandidate("owner-b", key, hash)
	if err := store.Create(ctx, other); err != nil || ids[other.ID] {
		t.Fatal("different owners incorrectly shared a receipt")
	}
	if _, err := store.FindSubmission(ctx, "owner-c", key); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("foreign receipt lookup was not isolated")
	}
	original, err := store.FindSubmission(ctx, "owner-a", key)
	if err != nil || original.PayloadHash != hash || !ids[original.ID] {
		t.Fatal("original receipt changed")
	}
}

func TestComplaintMySQLLookupFailureRollsBackAndRetryRecovers(t *testing.T) {
	db := complaintMySQL(t)
	store := NewComplaint(db)
	key := uuid.NewString()
	row := complaintCandidate("owner", key, strings.Repeat("a", 64))
	const callback = "fixture:complaint-query-failure"
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "complaints" {
			tx.AddError(errors.New("synthetic lookup outage"))
		}
	}); err != nil {
		t.Fatal("fixture callback unavailable")
	}
	if err := store.Create(context.Background(), row); err == nil {
		t.Fatal("lookup failure unexpectedly acknowledged")
	}
	db.Callback().Query().Remove(callback)
	var count int64
	db.Model(&table.Complaint{}).Count(&count)
	if count != 0 {
		t.Fatal("receipt lookup failure left an orphan insert")
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatal("retry after rollback failed")
	}
	db.Model(&table.Complaint{}).Count(&count)
	if count != 1 {
		t.Fatal("rollback retry created duplicate rows")
	}
}

func TestComplaintMySQLNullableLegacyMigrationPreservesRows(t *testing.T) {
	db := complaintMySQL(t)
	if err := db.Migrator().DropIndex(&table.Complaint{}, "uniq_complaint_submission"); err != nil {
		t.Fatal("fixture legacy index preparation failed")
	}
	for _, column := range []string{"ClientSubmissionID", "PayloadHash", "RemovedAt"} {
		if err := db.Migrator().DropColumn(&table.Complaint{}, column); err != nil {
			t.Fatal("fixture legacy column preparation failed")
		}
	}
	for i := 0; i < 2; i++ {
		if err := db.Exec("INSERT INTO complaints(id,complaint_user_id,reason,status,evidence) VALUES(?,?,?,?,?)", uuid.NewString(), "legacy-owner", "保留资料", 0, "[]").Error; err != nil {
			t.Fatal("legacy complaint seed failed")
		}
	}
	if err := db.AutoMigrate(&table.Complaint{}); err != nil {
		t.Fatal("nullable complaint migration failed")
	}
	var count int64
	db.Model(&table.Complaint{}).Where("complaint_user_id = ? AND client_submission_id IS NULL", "legacy-owner").Count(&count)
	if count != 2 {
		t.Fatal("migration lost or assigned a submission ID to legacy rows")
	}
	store := NewComplaint(db)
	legacy := complaintCandidate("legacy-owner", uuid.NewString(), "")
	legacy.ClientSubmissionID = nil
	if err := store.Create(context.Background(), legacy); err != nil {
		t.Fatal("legacy client without request ID no longer works")
	}
}

func TestComplaintMySQLDeletedReceiptCannotBeResurrected(t *testing.T) {
	db := complaintMySQL(t)
	store := NewComplaint(db)
	ctx := context.Background()
	key, hash := uuid.NewString(), strings.Repeat("c", 64)
	original := complaintCandidate("owner", key, hash)
	original.Description, original.TargetName = "private complaint body", "private target name"
	original.Evidence = `["https://objects.example.test/private.png"]`
	now := time.Now()
	original.HandlerID, original.HandleResult, original.HandleTime = "private handler", "private handling result", &now
	if err := store.Create(ctx, original); err != nil {
		t.Fatal("complaint fixture creation failed")
	}
	legacy := complaintCandidate("owner", uuid.NewString(), "")
	legacy.ClientSubmissionID = nil
	if err := store.Create(ctx, legacy); err != nil {
		t.Fatal("legacy complaint fixture creation failed")
	}
	if err := store.Delete(ctx, []string{original.ID, legacy.ID}); err != nil {
		t.Fatal("complaint removal failed")
	}
	replay := complaintCandidate("owner", key, hash)
	if err := store.Create(ctx, replay); err != nil || replay.ID != original.ID {
		t.Fatal("late replay recreated the deleted complaint instead of its original receipt")
	}
	if _, err := store.Take(ctx, original.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("deleted complaint remained visible by ID")
	}
	if count, _, err := store.Search(ctx, "owner", "", nil, nil, 1, 20); err != nil || count != 0 {
		t.Fatal("deleted complaint remained visible in search")
	}
	var retained table.Complaint
	if err := db.Where("id = ?", original.ID).Take(&retained).Error; err != nil {
		t.Fatal("minimal receipt mapping disappeared")
	}
	if retained.Description != "" || retained.Reason != "" || retained.Evidence != "" || retained.TargetID != "" || retained.TargetName != "" || retained.HandlerID != "" || retained.HandleResult != "" || retained.HandleTime != nil {
		t.Fatal("removed receipt retained private complaint content")
	}
	if retained.RemovedAt == nil {
		t.Fatal("removed receipt was not marked as removed")
	}
	var legacyCount int64
	db.Model(&table.Complaint{}).Where("id = ?", legacy.ID).Count(&legacyCount)
	if legacyCount != 0 {
		t.Fatal("legacy unkeyed complaint was not physically removed")
	}
	if err := store.Update(ctx, original.ID, map[string]any{"handle_result": "must stay removed"}); err != nil {
		t.Fatal("removed record update failed")
	}
	if err := db.Where("id = ?", original.ID).Take(&retained).Error; err != nil || retained.HandleResult != "" {
		t.Fatal("handler update repopulated a removed complaint")
	}
	changed := complaintCandidate("owner", key, strings.Repeat("d", 64))
	if err := store.Create(ctx, changed); !errors.Is(err, table.ErrComplaintSubmissionConflict) {
		t.Fatal("removed receipt accepted a different payload")
	}
	var wg sync.WaitGroup
	failures := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row := complaintCandidate("owner", key, hash)
			failures <- store.Create(ctx, row) != nil || row.ID != original.ID || store.Delete(ctx, []string{original.ID}) != nil
		}()
	}
	wg.Wait()
	close(failures)
	for bad := range failures {
		if bad {
			t.Fatal("concurrent deletion/replay lost the stable receipt")
		}
	}
	var count int64
	db.Model(&table.Complaint{}).Where("complaint_user_id = ? AND client_submission_id = ?", "owner", key).Count(&count)
	if count != 1 {
		t.Fatal("concurrent deletion/replay created duplicate receipts")
	}
}
