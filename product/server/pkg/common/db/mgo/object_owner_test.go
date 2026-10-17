package mgo

import (
	"context"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"strings"
	"testing"
	"time"
)

func TestObjectAliasPersistsOwnerAndHash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	mc, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"))
	if err != nil {
		t.Fatal(err)
	}
	defer mc.Disconnect(context.Background())
	if err = mc.Ping(ctx, nil); err != nil {
		t.Skip("isolated Mongo unavailable")
	}
	db := mc.Database("luobo_object_owner_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	defer db.Drop(context.Background())
	store, err := NewS3Mongo(db)
	if err != nil {
		t.Fatal(err)
	}
	want := &relation.ObjectModel{Name: "synthetic-owner/file", UserID: "synthetic-owner", Hash: "synthetic-hash", Engine: "fixture", Key: "server-generated", Size: 12, CreateTime: time.Now()}
	if err = store.SetObject(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Take(ctx, want.Engine, want.Name)
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != want.UserID || got.Hash != want.Hash {
		t.Fatal("object alias lost owner/hash")
	}
	t.Log("LUOBO_OBJECT_ALIAS_OWNER_PERSISTENCE_PASS")
}
