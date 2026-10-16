package favorite

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func isolatedMongo(t *testing.T) (*mongo.Database, *Service) {
	t.Helper()
	uri := os.Getenv("LUOBO_INTEGRATION_MONGO_URI")
	if uri == "" {
		t.Skip("requires explicitly configured task-local replica set")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") {
		t.Fatal("only the isolated task Mongo endpoint is permitted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("luobo_favorite_test_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := db.Drop(cleanup); err != nil {
			t.Error("test database cleanup failed", err)
		}
		client.Disconnect(cleanup)
	})
	store, err := NewMongoStore(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	return db, &Service{Store: store}
}
func ptr[T any](value T) *T { return &value }
func callFavorite(t *testing.T, s *Service, owner, op string, req Request) any {
	t.Helper()
	req.UserID = owner
	value, err := s.Execute(context.Background(), owner, op, req)
	if err != nil {
		t.Fatalf("%s failed: %v", op, err)
	}
	return value
}
func getFavorite(t *testing.T, value any) Favorite {
	t.Helper()
	return value.(map[string]any)["favoriteInfo"].(Favorite)
}
func getCategory(t *testing.T, value any) Category {
	t.Helper()
	return value.(map[string]any)["categoryInfo"].(Category)
}
func TestMongoFavoriteTwelveContractsAndOwnership(t *testing.T) {
	db, s := isolatedMongo(t)
	owner := "u1"
	ctx := context.Background()
	category := getCategory(t, callFavorite(t, s, owner, "category/create", Request{CategoryName: ptr("工作"), CategoryColor: ptr("#123ABC")}))
	updatedCategory := getCategory(t, callFavorite(t, s, owner, "category/update", Request{CategoryID: &category.CategoryID, CategoryName: ptr("资料"), SortOrder: ptr(5)}))
	if updatedCategory.CategoryColor != "#123ABC" {
		t.Fatal("partial category update changed color")
	}
	one := getFavorite(t, callFavorite(t, s, owner, "add", Request{FavoriteType: 1, SourceID: "message-1", ConversationID: "conv-1", Title: ptr("Hello"), Content: "message body", CategoryID: &category.CategoryID, Tags: ptr([]string{"工作"}), Notes: ptr("note")}))
	two := getFavorite(t, callFavorite(t, s, owner, "add", Request{FavoriteType: 2, SourceID: "file-1", Title: ptr("file.pdf")}))
	replay := getFavorite(t, callFavorite(t, s, owner, "add", Request{FavoriteType: 1, SourceID: "message-1", ConversationID: "conv-1", Title: ptr("does not overwrite"), CategoryID: ptr("missing-category")}))
	if replay.FavoriteID != one.FavoriteID || replay.Title != "Hello" {
		t.Fatal("source retry not stable")
	}
	changed := getFavorite(t, callFavorite(t, s, owner, "update", Request{FavoriteID: one.FavoriteID, Title: ptr("HELLO new"), Tags: ptr([]string{})}))
	if changed.Notes != "note" || len(changed.Tags) != 0 {
		t.Fatal("partial update/null versus empty collection incorrect")
	}
	info := getFavorite(t, callFavorite(t, s, owner, "info", Request{FavoriteID: one.FavoriteID}))
	if info.Title != "HELLO new" {
		t.Fatal("info not persisted")
	}
	list := callFavorite(t, s, owner, "list", Request{}).(map[string]any)
	if list["total"].(int64) != 2 {
		t.Fatal("list total wrong")
	}
	search := callFavorite(t, s, owner, "search", Request{Keyword: "hello"}).(map[string]any)
	if search["total"].(int64) != 1 {
		t.Fatal("search not scoped/filtered")
	}
	callFavorite(t, s, owner, "batch_operate", Request{FavoriteIDs: []string{one.FavoriteID, two.FavoriteID}, Operation: 2, TargetTags: []string{"saved"}})
	callFavorite(t, s, owner, "batch_operate", Request{FavoriteIDs: []string{two.FavoriteID}, Operation: 1, TargetCategoryID: category.CategoryID})
	stats := callFavorite(t, s, owner, "stats", Request{}).(map[string]any)
	raw, _ := json.Marshal(stats)
	var count struct {
		Total    int `json:"totalCount"`
		Messages int `json:"messageCount"`
		Files    int `json:"fileCount"`
	}
	json.Unmarshal(raw, &count)
	if count.Total != 2 || count.Messages != 1 || count.Files != 1 {
		t.Fatal("statistics incorrect")
	}
	cats := callFavorite(t, s, owner, "category/list", Request{}).(map[string]any)["categories"].([]Category)
	if len(cats) != 2 || cats[1].FavoriteCount != 2 {
		t.Fatal("category counts stale")
	}
	// Every cross-owner identifier lookup and batch must reject before any write.
	for op, req := range map[string]Request{"info": {FavoriteID: one.FavoriteID}, "update": {FavoriteID: one.FavoriteID, Title: ptr("stolen")}, "remove": {FavoriteIDs: []string{one.FavoriteID}}, "batch_operate": {FavoriteIDs: []string{one.FavoriteID}, Operation: 4}, "category/update": {CategoryID: &category.CategoryID, CategoryName: ptr("stolen")}, "category/delete": {CategoryID: &category.CategoryID}, "add": {FavoriteType: 1, SourceID: "bad", CategoryID: &category.CategoryID}} {
		req.UserID = "u2"
		if _, err := s.Execute(ctx, "u2", op, req); err == nil {
			t.Fatalf("%s accepted another owner's resource", op)
		}
	}
	if _, err := s.Execute(ctx, "u2", "list", Request{UserID: owner}); err == nil {
		t.Fatal("body owner override accepted")
	}
	// One foreign ID prevents the whole batch from changing an owned favorite.
	foreign := getFavorite(t, callFavorite(t, s, "u2", "add", Request{FavoriteType: 3, SourceID: "foreign"}))
	if _, err := s.Execute(ctx, owner, "batch_operate", Request{UserID: owner, FavoriteIDs: []string{one.FavoriteID, foreign.FavoriteID}, Operation: 4}); err == nil {
		t.Fatal("cross-owner partial batch succeeded")
	}
	if _, err := s.Execute(ctx, owner, "info", Request{UserID: owner, FavoriteID: one.FavoriteID}); err != nil {
		t.Fatal("rejected batch partially deleted owned item")
	}
	callFavorite(t, s, owner, "category/delete", Request{CategoryID: &category.CategoryID})
	info = getFavorite(t, callFavorite(t, s, owner, "info", Request{FavoriteID: one.FavoriteID}))
	if info.CategoryID != DefaultCategoryID {
		t.Fatal("delete category did not atomically move favorites")
	}
	callFavorite(t, s, owner, "remove", Request{FavoriteIDs: []string{one.FavoriteID, two.FavoriteID}})
	var residual int64
	residual, _ = db.Collection("favorite_items").CountDocuments(ctx, bson.M{"userID": owner})
	if residual != 0 {
		t.Fatal("remove returned success without deleting")
	}
}
func TestMongoFavoriteConcurrentRetryAndCategoryDeletion(t *testing.T) {
	db, s := isolatedMongo(t)
	owner := "u1"
	ctx := context.Background()
	var wg sync.WaitGroup
	ids := make(chan string, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := s.Execute(ctx, owner, "add", Request{UserID: owner, FavoriteType: 1, SourceID: "same-source"})
			if err != nil {
				t.Error(err)
				return
			}
			ids <- value.(map[string]any)["favoriteInfo"].(Favorite).FavoriteID
		}()
	}
	wg.Wait()
	close(ids)
	identity := ""
	for id := range ids {
		if identity != "" && identity != id {
			t.Fatal("concurrent retry created multiple favorites")
		}
		identity = id
	}
	if n, _ := db.Collection("favorite_items").CountDocuments(ctx, bson.M{"userID": owner}); n != 1 {
		t.Fatal("source index did not enforce idempotency")
	}
	category := getCategory(t, callFavorite(t, s, owner, "category/create", Request{CategoryName: ptr("Concurrent")}))
	started := make(chan struct{})
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-started
		_, err := s.Execute(ctx, owner, "add", Request{UserID: owner, FavoriteType: 2, SourceID: "racing-source", CategoryID: &category.CategoryID})
		errs <- err
	}()
	go func() {
		defer wg.Done()
		<-started
		_, err := s.Execute(ctx, owner, "category/delete", Request{UserID: owner, CategoryID: &category.CategoryID})
		errs <- err
	}()
	close(started)
	wg.Wait()
	close(errs)
	if n, _ := db.Collection("favorite_items").CountDocuments(ctx, bson.M{"userID": owner, "categoryID": category.CategoryID}); n != 0 {
		t.Fatal("concurrent category deletion left an orphaned favorite")
	}
}

func TestMongoCategoryDeleteFailureRollsBackCategoryAndItems(t *testing.T) {
	db, s := isolatedMongo(t)
	owner := "u1"
	ctx := context.Background()
	category := getCategory(t, callFavorite(t, s, owner, "category/create", Request{CategoryName: ptr("Rollback")}))
	item := getFavorite(t, callFavorite(t, s, owner, "add", Request{FavoriteType: 1, SourceID: "cat-source", CategoryID: &category.CategoryID}))
	callFavorite(t, s, owner, "add", Request{FavoriteType: 2, SourceID: "default-source"})
	// Inject an actual Mongo write failure after the transaction has deleted the
	// category: moving its item into default conflicts with this temporary index.
	_, err := db.Collection("favorite_items").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "categoryID", Value: 1}}, Options: options.Index().SetUnique(true).SetName("test_force_move_failure")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Execute(ctx, owner, "category/delete", Request{UserID: owner, CategoryID: &category.CategoryID}); err == nil {
		t.Fatal("injected write failure did not abort category deletion")
	}
	if n, _ := db.Collection("favorite_categories").CountDocuments(ctx, bson.M{"userID": owner, "categoryID": category.CategoryID}); n != 1 {
		t.Fatal("failed transaction partially removed category")
	}
	saved := getFavorite(t, callFavorite(t, s, owner, "info", Request{FavoriteID: item.FavoriteID}))
	if saved.CategoryID != category.CategoryID {
		t.Fatal("failed category deletion partially moved favorite")
	}
}
