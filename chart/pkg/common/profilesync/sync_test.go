package profilesync_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/OpenIMSDK/chat/pkg/common/db/database"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/profilesync"
	"github.com/google/uuid"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"strings"
	"sync"
	"testing"
	"time"
)

const socket = "/Users/chuzu/Documents/projects/LuoboIM/references/integration/mysql8.4/mysql.sock"

type projection struct {
	mu               sync.Mutex
	nickname, avatar string
	fail             bool
	unknownOnce      bool
	calls            int
	entered          chan struct{}
	release          chan struct{}
}

func (p *projection) ImAdminTokenWithDefaultAdmin(context.Context) (string, error) {
	return "synthetic-service-token", nil
}
func (p *projection) UpdateUserProfile(ctx context.Context, id, nickname, avatar string) error {
	p.mu.Lock()
	p.calls++
	p.nickname = nickname
	p.avatar = avatar
	fail := p.fail
	unknown := p.unknownOnce
	p.unknownOnce = false
	entered, release := p.entered, p.release
	p.entered = nil
	p.mu.Unlock()
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if fail || unknown {
		return errors.New("synthetic unknown IM response")
	}
	return nil
}
func fixture(t *testing.T) (*gorm.DB, database.ChatDatabaseInterface, *projection, *profilesync.Service) {
	t.Helper()
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	admin, err := gorm.Open(mysql.Open("root@unix("+socket+")/luobo_integration?charset=utf8mb4&parseTime=true"), cfg)
	if err != nil {
		t.Skip("isolated MySQL unavailable")
	}
	name := "luobo_profile_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4").Error; err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(mysql.Open("root@unix("+socket+")/"+name+"?charset=utf8mb4&parseTime=true"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		raw, _ := db.DB()
		raw.Close()
		_ = admin.Exec("DROP DATABASE `" + name + "`").Error
		a, _ := admin.DB()
		a.Close()
	})
	if err = db.AutoMigrate(&table.Attribute{}, &profilesync.Job{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&table.Attribute{UserID: "owner", Nickname: "original", FaceURL: "https://fixture.invalid/original.png", Gender: 1, CreateTime: time.Now(), ChangeTime: time.Now(), BirthTime: time.Unix(0, 0)}).Error; err != nil {
		t.Fatal(err)
	}
	im := &projection{}
	return db, database.NewChatDatabase(db), im, &profilesync.Service{DB: db, IM: im}
}
func TestProfileSettingsPartialPreservesIMFieldsAndDoesNotQueue(t *testing.T) {
	db, store, im, _ := fixture(t)
	ctx := context.Background()
	if err := store.UpdateUseInfo(ctx, "owner", map[string]any{"gender": 2, "allow_beep": 2, "allow_vibration": 2, "allow_add_friend": 2, "birth_time": time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	row, err := store.TakeAttributeByUserID(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if row.Nickname != "original" || row.FaceURL != "https://fixture.invalid/original.png" || row.Gender != 2 {
		t.Fatal("partial update damaged untouched fields")
	}
	var n int64
	if err = db.Model(&profilesync.Job{}).Count(&n).Error; err != nil || n != 0 || im.calls != 0 {
		t.Fatal("settings-only update queued unnecessary IM profile write")
	}
	t.Log("LUOBO_PROFILE_PARTIAL_SETTINGS_PRESERVED_PASS")
}
func TestProfileClearAvatarUnknownResponseAndRestart(t *testing.T) {
	db, store, im, s := fixture(t)
	ctx := context.Background()
	if err := store.UpdateUseInfo(ctx, "owner", map[string]any{"nickname": "new-name", "face_url": ""}); err != nil {
		t.Fatal(err)
	}
	im.unknownOnce = true
	if err := s.ProcessUser(ctx, "owner"); err == nil {
		t.Fatal("unknown response falsely marked done")
	}
	if pending, err := s.Pending(ctx, "owner"); err != nil || !pending {
		t.Fatal("pending intent lost")
	}
	restart := &profilesync.Service{DB: db, IM: im}
	if err := restart.ProcessUser(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	if pending, err := restart.Pending(ctx, "owner"); err != nil || pending {
		t.Fatal("restart failed to complete")
	}
	if im.nickname != "new-name" || im.avatar != "" || im.calls != 2 {
		t.Fatal("presence/empty-avatar/retry semantics lost")
	}
	var job profilesync.Job
	if err := db.Take(&job, "user_id = ?", "owner").Error; err != nil {
		t.Fatal(err)
	}
	if job.Generation != 1 || job.CompletedAt == nil {
		t.Fatal("idempotent retry created new generation")
	}
	t.Log("LUOBO_PROFILE_UNKNOWN_RESPONSE_RESTART_CLEAR_AVATAR_PASS")
}
func TestProfileOutboxFailureRollsBackCanonicalUpdate(t *testing.T) {
	db, store, _, _ := fixture(t)
	if err := db.Migrator().DropTable(&profilesync.Job{}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateUseInfo(context.Background(), "owner", map[string]any{"nickname": "must-rollback"}); err == nil {
		t.Fatal("missing outbox accepted")
	}
	row, err := store.TakeAttributeByUserID(context.Background(), "owner")
	if err != nil || row.Nickname != "original" {
		t.Fatal("profile committed without durable projection intent")
	}
	t.Log("LUOBO_PROFILE_ATOMIC_OUTBOX_ROLLBACK_PASS")
}
func TestProfileConcurrentGenerationCannotOverwriteLaterUpdate(t *testing.T) {
	db, store, im, s := fixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := store.UpdateUseInfo(ctx, "owner", map[string]any{"nickname": "first"}); err != nil {
		t.Fatal(err)
	}
	im.entered = make(chan struct{})
	im.release = make(chan struct{})
	entered := im.entered
	done := make(chan error, 1)
	go func() { done <- s.ProcessUser(ctx, "owner") }()
	<-entered
	updated := make(chan error, 1)
	go func() {
		updated <- store.UpdateUseInfo(ctx, "owner", map[string]any{"face_url": "", "nickname": "second"})
	}()
	close(im.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := <-updated; err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessUser(ctx, "owner"); err != nil {
		t.Fatal(err)
	}
	var job profilesync.Job
	if err := db.Take(&job, "user_id = ?", "owner").Error; err != nil {
		t.Fatal(err)
	}
	if job.Generation != 2 || job.CompletedAt == nil || im.nickname != "second" || im.avatar != "" {
		t.Fatal(fmt.Sprintf("wrong final generation/state:%d", job.Generation))
	}
	t.Log("LUOBO_PROFILE_CONCURRENT_GENERATION_PASS")
}
