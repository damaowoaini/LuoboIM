package mgo

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/grouphistory"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func isolatedCommandMongo(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("LUOBO_INTEGRATION_MONGO_URI")
	if uri == "" {
		t.Skip("requires task-private replica set")
	}
	if !strings.HasPrefix(uri, "mongodb://127.0.0.1:37017/") {
		t.Fatal("only private task fixture allowed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		t.Fatal(err)
	}
	db := client.Database("luobo_commands_test_" + strings.ReplaceAll(uuid.NewString(), "-", ""))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		db.Drop(ctx)
		client.Disconnect(ctx)
	})
	return db
}
func TestMongoUserCommandPersistentCRUDAndConcurrentRetry(t *testing.T) {
	db := isolatedCommandMongo(t)
	ctx := context.Background()
	store, err := NewUserMongo(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddUserCommand(ctx, "u1", 1, "id", "value", "extension"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddUserCommand(ctx, "u1", 1, "id", "different", "extension"); err == nil {
		t.Fatal("conflicting source retry overwrote command")
	}
	empty := ""
	if err := store.UpdateUserCommand(ctx, "u1", 1, "id", nil, &empty); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateUserCommand(ctx, "u2", 1, "id", nil, &empty); err == nil {
		t.Fatal("cross-owner update reported success")
	}
	if err := store.DeleteUserCommand(ctx, "u2", 1, "id"); err != nil {
		t.Fatal(err)
	}
	commands, err := store.GetUserCommand(ctx, "u1", 0)
	if err != nil || len(commands) != 1 || commands[0].Value != "value" || commands[0].Ex != "" || commands[0].Type != 1 {
		t.Fatal("command readback lost fields/owner", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := store.AddUserCommand(ctx, "u1", 2, "concurrent", "saved", "ex"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if count, _ := db.Collection("userCommands").CountDocuments(ctx, bson.M{"userID": "u1", "type": 2, "uuid": "concurrent"}); count != 1 {
		t.Fatal("concurrent retry created duplicate command")
	}
	commands, err = store.GetUserCommand(ctx, "u1", 0)
	if err != nil || len(commands) != 2 {
		t.Fatal("all commands does not include every type", err)
	}
	if err := store.DeleteUserCommand(ctx, "u1", 1, "id"); err != nil {
		t.Fatal(err)
	}
	commands, err = store.GetUserCommand(ctx, "u1", 1)
	if err != nil || len(commands) != 0 {
		t.Fatal("delete not persisted", err)
	}
}
func TestMongoMembershipHistoryTracksAuthoritativeLeaveAndRollback(t *testing.T) {
	db := isolatedCommandMongo(t)
	ctx := context.Background()
	members, err := NewGroupMember(db)
	if err != nil {
		t.Fatal(err)
	}
	history, err := grouphistory.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	joined := time.Now().Add(-time.Hour).UTC()
	if err := members.Create(ctx, []*relation.GroupMemberModel{{GroupID: "g1", UserID: "u1", JoinTime: joined}}); err != nil {
		t.Fatal(err)
	}
	if current, err := history.Current(ctx, "g1", "u1"); err != nil || !current {
		t.Fatal("new member proof missing", err)
	}
	session, err := db.Client().StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		if err := members.Delete(tx, "g1", []string{"u1"}); err != nil {
			return nil, err
		}
		return nil, errors.New("simulate post-delete transaction failure")
	})
	if err == nil {
		t.Fatal("injected failure committed")
	}
	if current, _ := history.Current(ctx, "g1", "u1"); !current {
		t.Fatal("failed leave removed membership")
	}
	if left, err := history.LastLeft(ctx, "g1", "u1"); err != nil || left != nil {
		t.Fatal("failed leave committed historical cutoff", err)
	}
	_, err = session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) {
		return nil, members.Delete(tx, "g1", []string{"u1"})
	})
	if err != nil {
		t.Fatal(err)
	}
	left, err := history.LastLeft(ctx, "g1", "u1")
	if err != nil || left == nil || left.Before(joined) {
		t.Fatal("authoritative leave did not preserve historical entitlement", err)
	}
	if err := history.ObserveCurrent(ctx, "g1", "u1"); err != nil {
		t.Fatal(err)
	}
	after, _ := history.LastLeft(ctx, "g1", "u1")
	if after == nil || !after.Equal(*left) {
		t.Fatal("in-flight observation erased authoritative leave")
	}
	// Conversation existence is never a source of group membership.
	db.Collection("conversations").InsertOne(ctx, bson.M{"owner_user_id": "attacker", "conversation_id": "sg_g1"})
	if left, err := history.LastLeft(ctx, "g1", "attacker"); err == nil || left != nil {
		t.Fatal("untrusted legacy conversation became membership proof")
	}
}
