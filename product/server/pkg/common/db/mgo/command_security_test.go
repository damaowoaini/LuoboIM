package mgo

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestMongoCommandPersistenceScopesIdentityAndPreservesExtension(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("add is stable and conflict does not overwrite", func(mt *mtest.T) {
		store := &UserMgo{coll: mt.Coll}
		ns := mt.DB.Name() + ".userCommands"
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}), mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "value", Value: "value"}, {Key: "ex", Value: "extension"}}))
		if err := store.AddUserCommand(context.Background(), "owner", 2, "id", "value", "extension"); err != nil {
			t.Fatal(err)
		}
		event := mt.GetStartedEvent()
		updates := event.Command.Lookup("updates").Array()
		items, _ := updates.Values()
		update := items[0].Document()
		filter := update.Lookup("q").Document()
		if filter.Lookup("userID").StringValue() != "owner" || filter.Lookup("type").Int32() != 2 || filter.Lookup("uuid").StringValue() != "id" {
			t.Fatal("write identity scope missing")
		}
		if !update.Lookup("upsert").Boolean() || update.Lookup("u").Document().Lookup("$setOnInsert").Document().Lookup("ex").StringValue() != "extension" {
			t.Fatal("retry can overwrite or lose ex")
		}
		mt.ClearEvents()
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}), mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "value", Value: "old"}, {Key: "ex", Value: "extension"}}))
		if err := store.AddUserCommand(context.Background(), "owner", 2, "id", "new", "extension"); err == nil {
			t.Fatal("conflicting retry reported success")
		}
	})
	mt.Run("partial update keeps value and requires existing owner", func(mt *mtest.T) {
		store := &UserMgo{coll: mt.Coll}
		empty := ""
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 1}, bson.E{Key: "nModified", Value: 1}))
		if err := store.UpdateUserCommand(context.Background(), "owner", 2, "id", nil, &empty); err != nil {
			t.Fatal(err)
		}
		event := mt.GetStartedEvent()
		items, _ := event.Command.Lookup("updates").Array().Values()
		item := items[0].Document()
		fields := item.Lookup("u").Document().Lookup("$set").Document()
		if _, err := fields.LookupErr("value"); err == nil {
			t.Fatal("omitted value overwritten")
		}
		if item.Lookup("q").Document().Lookup("userID").StringValue() != "owner" {
			t.Fatal("update missing owner")
		}
		mt.AddMockResponses(mtest.CreateSuccessResponse(bson.E{Key: "n", Value: 0}, bson.E{Key: "nModified", Value: 0}))
		if err := store.UpdateUserCommand(context.Background(), "other-owner", 2, "id", nil, &empty); err == nil {
			t.Fatal("missing owner record reported update success")
		}
	})
	mt.Run("all commands stays scoped and decodes all protocol fields", func(mt *mtest.T) {
		store := &UserMgo{coll: mt.Coll}
		ns := mt.DB.Name() + ".userCommands"
		mt.AddMockResponses(mtest.CreateCursorResponse(0, ns, mtest.FirstBatch, bson.D{{Key: "type", Value: int32(3)}, {Key: "uuid", Value: "id"}, {Key: "value", Value: "v"}, {Key: "ex", Value: "e"}, {Key: "createTime", Value: int64(123)}}))
		commands, err := store.GetUserCommand(context.Background(), "owner", 0)
		if err != nil || len(commands) != 1 || commands[0].Type != 3 || commands[0].Ex != "e" || commands[0].CreateTime != 123 {
			t.Fatal("protocol field persistence lost", err)
		}
		filter := mt.GetStartedEvent().Command.Lookup("filter").Document()
		if filter.Lookup("userID").StringValue() != "owner" {
			t.Fatal("GetAll missing owner")
		}
		if _, err := filter.LookupErr("type"); err == nil {
			t.Fatal("GetAll restricted to zero type")
		}
	})
}
