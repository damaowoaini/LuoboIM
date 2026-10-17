package mgo

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
)

func TestMongoBootstrapConcurrentStartsPreserveExistingProfiles(t *testing.T) {
	db := isolatedCommandMongo(t)
	store, err := NewUserMongo(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	created := time.Now().Truncate(time.Millisecond).UTC()
	existing := &relation.UserModel{UserID: "real-user", Nickname: "edited by user", FaceURL: "https://example.test/user.png", Ex: "retained", AppMangerLevel: 1, GlobalRecvMsgOpt: 2, CreateTime: created}
	if err := store.Create(ctx, []*relation.UserModel{existing}); err != nil {
		t.Fatal(err)
	}
	bootstrap := []*relation.UserModel{{UserID: "new-manager", Nickname: "initial manager", AppMangerLevel: 2}, {UserID: "real-user", Nickname: "configuration must not reset profile", AppMangerLevel: 2}, {UserID: "new-manager", Nickname: "duplicate must not overwrite", AppMangerLevel: 3}}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for _, account := range bootstrap {
				if err := store.CreateIfAbsent(ctx, account); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	count, err := db.Collection("user").CountDocuments(ctx, bson.M{})
	if err != nil || count != 2 {
		t.Fatal("concurrent starts duplicated users", count, err)
	}
	got, err := store.Take(ctx, existing.UserID)
	if err != nil || got.Nickname != existing.Nickname || got.FaceURL != existing.FaceURL || got.Ex != existing.Ex || got.AppMangerLevel != existing.AppMangerLevel || got.GlobalRecvMsgOpt != existing.GlobalRecvMsgOpt || !got.CreateTime.Equal(created) {
		t.Fatal("startup changed an existing user's data", err)
	}
	got, err = store.Take(ctx, "new-manager")
	if err != nil || got.Nickname != "initial manager" || got.AppMangerLevel != 2 {
		t.Fatal("duplicate configuration downgraded the new management account", err)
	}
	if err := store.CreateIfAbsent(ctx, bootstrap[0]); err != nil {
		t.Fatal("restart was not idempotent", err)
	}
	t.Log("LUOBO_USER_BOOTSTRAP_CONCURRENT_MONGO_PASS")
}
