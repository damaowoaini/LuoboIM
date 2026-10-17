package office

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/constant"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/office"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func momentFixture(t *testing.T) (context.Context, *WorkMomentModel) {
	t.Helper()
	uri := os.Getenv("LUOBO_OFFICE_TEST_MONGO_URI")
	if uri == "" {
		t.Skip("explicit isolated loopback Mongo fixture required")
	}
	parsed, err := url.Parse(uri)
	if err != nil || (parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost") {
		t.Fatal("loopback fixture only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	db := client.Database("luobo_moment_test_" + hex.EncodeToString(nonce[:]))
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = db.Drop(cleanup)
		_ = client.Disconnect(cleanup)
	})
	model, err := NewWorkMomentModel(db.Collection("work_moment"))
	if err != nil {
		t.Fatal(err)
	}
	return ctx, model.(*WorkMomentModel)
}

func TestMomentConcurrentLikesAreUniqueAndUnlikeKeepsOtherPeople(t *testing.T) {
	ctx, model := momentFixture(t)
	item := &table.WorkMoment{UserID: "alice", Content: &table.WorkMomentContent{Text: "synthetic"}, CreateTime: time.Now(), LikeUsers: []*table.LikeUser{{UserID: "carol", CreateTime: time.Now()}}}
	if err := model.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 24)
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := model.SetWorkMomentLike(ctx, item.WorkMomentID.Hex(), "bob", true, time.Now())
			if err != nil {
				failures <- err
			}
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	saved, err := model.Get(ctx, item.WorkMomentID.Hex())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.LikeUsers) != 2 {
		t.Fatalf("concurrent same-actor likes duplicated: got %d, want2", len(saved.LikeUsers))
	}
	before := saved.LikeUsers[1].CreateTime
	saved, err = model.SetWorkMomentLike(ctx, item.WorkMomentID.Hex(), "bob", true, time.Now().Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !saved.LikeUsers[1].CreateTime.Equal(before) {
		t.Fatal("idempotent like invented a fresh unread interaction")
	}
	literal, err := model.SetWorkMomentLike(ctx, item.WorkMomentID.Hex(), "$literal-user", true, time.Now())
	if err != nil || len(literal.LikeUsers) != 3 || literal.LikeUsers[2].UserID != "$literal-user" {
		t.Fatal("identity was interpreted as an aggregation expression")
	}
	if _, err := model.SetWorkMomentLike(ctx, item.WorkMomentID.Hex(), "$literal-user", false, time.Now()); err != nil {
		t.Fatal(err)
	}
	saved, err = model.SetWorkMomentLike(ctx, item.WorkMomentID.Hex(), "bob", false, time.Now())
	if err != nil || len(saved.LikeUsers) != 1 || saved.LikeUsers[0].UserID != "carol" {
		t.Fatal("unlike removed another actor or failed")
	}
}

func TestMomentHistoricalInvisibleMentionsNeverLeakThroughUnreadOrLogs(t *testing.T) {
	ctx, model := momentFixture(t)
	for _, permission := range []int32{constant.WorkMomentPrivate, constant.WorkMomentPublic, constant.WorkMomentPermissionCanSee, constant.WorkMomentPermissionCantSee} {
		audience := []string{"carol"}
		if permission == constant.WorkMomentPermissionCantSee {
			audience = []string{"bob"}
		}
		item := &table.WorkMoment{UserID: "alice", Content: &table.WorkMomentContent{Text: "private legacy marker"}, CreateTime: time.Now(), Permission: permission, PermissionUserIDs: audience, AtUserIDs: []string{"bob"}}
		if err := model.Create(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	count, err := model.GetUnreadCount(ctx, "bob", nil)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("invisible legacy content counted for excluded recipient: %d", count)
	}
	logs, err := model.FindRelevant(ctx, "bob", nil, 20, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Permission != constant.WorkMomentPublic {
		t.Fatal("invisible legacy content leaked in interaction logs")
	}
}
