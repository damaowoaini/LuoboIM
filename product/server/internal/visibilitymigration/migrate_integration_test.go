package visibilitymigration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
	ut "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// SCAN is scoped to this fixture's random namespace; every returned key and
// value still comes from actual Redis DB15 shared with other isolated tests.
type scopedScan struct {
	redis.UniversalClient
	prefix string
}

func (s scopedScan) Scan(ctx context.Context, cursor uint64, match string, count int64) *redis.ScanCmd {
	keys, next, err := s.UniversalClient.Scan(ctx, cursor, match, count).Result()
	filtered := []string{}
	for _, key := range keys {
		if strings.Contains(key, s.prefix) {
			filtered = append(filtered, key)
		}
	}
	return redis.NewScanCmdResult(filtered, next, err)
}

func TestRealVisibilityMigrationPrivateBackupResumeRollbackAndOwnerIsolation(t *testing.T) {
	uri, socket := os.Getenv("LUOBO_INTEGRATION_MONGO_URI"), os.Getenv("LUOBO_INTEGRATION_REDIS_SOCKET")
	if uri == "" || socket == "" {
		t.Skip("requires private Mongo/Redis fixtures")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") || !strings.HasSuffix(socket, "/references/integration/redis8.2/redis.sock") {
		t.Fatal("private fixtures only")
	}
	ctx := context.Background()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	name := "luobo_floor_migration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	db := client.Database(name)
	t.Cleanup(func() { db.Drop(ctx); client.Disconnect(ctx) })
	rdb := redis.NewClient(&redis.Options{Network: "unix", Addr: socket, DB: 15})
	t.Cleanup(func() { rdb.Close() })
	scan := scopedScan{rdb, name}
	conversation := "si_" + name
	owner := "owner"
	globalKey := "MIN_SEQ:" + conversation
	personalKey := "CON_USER_MIN_SEQ:" + conversation + "u:" + owner
	orphanKey := "CON_USER_MIN_SEQ:" + conversation + "u:unknown"
	maxKey := "MAX_SEQ:" + conversation
	notificationKey := "MIN_SEQ:n_" + name
	notificationMax := "MAX_SEQ:n_" + name
	t.Cleanup(func() { rdb.Del(ctx, globalKey, personalKey, orphanKey, maxKey, notificationKey, notificationMax) })
	if _, err := db.Collection("conversation").InsertOne(ctx, bson.M{"owner_user_id": owner, "conversation_id": conversation}); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]int64{globalKey: 2, personalKey: 5, maxKey: 10, notificationKey: 3, notificationMax: 8} {
		if err := rdb.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	store := unrelation.NewMsgMongoDriver(db)
	if err := store.AdvanceMessageVisibility(ctx, []ut.MessageVisibilityFloor{{ConversationID: conversation, MinSeq: 3}}); err != nil {
		t.Fatal(err)
	}
	backup, err := Snapshot(ctx, db, scan, "tenant-one")
	if err != nil || len(backup.Entries) != 3 || len(backup.Unresolved) != 0 {
		t.Fatal("snapshot failed", err)
	}
	path := filepath.Join(t.TempDir(), "backup.json")
	if err := Save(path, backup); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, backup); err == nil {
		t.Fatal("overwrote unique backup")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("backup is not private")
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db, scan, "tenant-two", loaded); err == nil {
		t.Fatal("cross tenant apply allowed")
	}
	other := client.Database(name + "_other")
	t.Cleanup(func() { other.Drop(ctx) })
	if _, err := identity(ctx, other, true); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, other, scan, "tenant-one", loaded); err == nil {
		t.Fatal("cross database/instance apply allowed")
	}
	if err := Apply(ctx, db, scan, "tenant-one", loaded); err != nil {
		t.Fatal(err)
	}
	if err := Apply(ctx, db, scan, "tenant-one", loaded); err != nil {
		t.Fatal("resume failed", err)
	}
	floors, err := store.ReadMessageVisibility(ctx, []ut.MessageVisibilityFloor{{ConversationID: conversation}, {ConversationID: conversation, UserID: owner}})
	if err != nil || len(floors) != 2 {
		t.Fatal(err)
	}
	for _, floor := range floors {
		if floor.UserID == "" && floor.MinSeq != 3 {
			t.Fatal("migration lowered existing global floor")
		}
	}
	if err := Rollback(ctx, db, "tenant-one", loaded); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(ctx, db, "tenant-one", loaded); err != nil {
		t.Fatal("idempotent rollback failed", err)
	}
	floors, err = store.ReadMessageVisibility(ctx, []ut.MessageVisibilityFloor{{ConversationID: conversation}, {ConversationID: conversation, UserID: owner}})
	if err != nil || len(floors) != 1 || floors[0].MinSeq != 3 || floors[0].UserID != "" {
		t.Fatal("rollback did not restore exact old floor", err)
	}
	if err := Apply(ctx, db, scan, "tenant-one", loaded); err != nil {
		t.Fatal(err)
	}
	if err := store.AdvanceMessageVisibility(ctx, []ut.MessageVisibilityFloor{{ConversationID: conversation, UserID: owner, MinSeq: 9}}); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(ctx, db, "tenant-one", loaded); err == nil {
		t.Fatal("rollback erased a subsequent clear")
	}
	if err := Apply(ctx, db, scan, "tenant-one", loaded); err == nil {
		t.Fatal("reapply overwrote a subsequent clear")
	}
	// Unknown owners are preserved in the backup, but are never guessed/applied.
	rdb.Set(ctx, orphanKey, 7, 0)
	unresolved, err := Snapshot(ctx, db, scan, "tenant-one")
	if err != nil || len(unresolved.Unresolved) != 1 {
		t.Fatal("orphan was not retained for review", err)
	}
	if err := Apply(ctx, db, scan, "tenant-one", unresolved); err == nil {
		t.Fatal("unknown owner applied")
	}
	fullPath := filepath.Join(t.TempDir(), "full-with-unresolved.json")
	if err := Save(fullPath, unresolved); err != nil {
		t.Fatal(err)
	}
	selection, err := Select(fullPath, []string{unrelation.MessageVisibilityID(conversation, "")})
	if err != nil || selection.Selection.SkippedUnresolved != 1 || len(selection.Entries) != 1 {
		t.Fatal("explicit known selection failed", err)
	}
	if err := Apply(ctx, db, scan, "tenant-one", selection); err != nil {
		t.Fatal("known selection blocked by retained unresolved owner", err)
	}
	if value, err := rdb.Get(ctx, orphanKey).Result(); err != nil || value != "7" {
		t.Fatal("selection changed unresolved source key", err)
	}
	if n, err := floorCollection(db).CountDocuments(ctx, bson.M{"user_id": "unknown"}); err != nil || n != 0 {
		t.Fatal("selection guessed unknown owner", err)
	}
	if err := Rollback(ctx, db, "tenant-one", selection); err != nil {
		t.Fatal(err)
	}
	selection.Entries[0].Floor.MinSeq++
	if err := Apply(ctx, db, scan, "tenant-one", selection); err == nil {
		t.Fatal("selection tamper ignored full-backup binding")
	}
	rdb.Del(ctx, orphanKey)
	fresh, err := Snapshot(ctx, db, scan, "tenant-one")
	if err != nil {
		t.Fatal(err)
	}
	rdb.Set(ctx, personalKey, 6, 0)
	if err := Apply(ctx, db, scan, "tenant-one", fresh); err == nil {
		t.Fatal("changed Redis preimage applied")
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("public backup accepted")
	}
	t.Log("LUOBO_VISIBILITY_MIGRATION_BACKUP_ROLLBACK_PASS")
}
