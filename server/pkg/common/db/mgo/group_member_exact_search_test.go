package mgo

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func exactMemberIDs(members []*relation.GroupMemberModel) []string {
	ids := make([]string, 0, len(members))
	for _, member := range members {
		ids = append(ids, member.UserID)
	}
	return ids
}

func TestMongoGroupMemberExactSearchPreservesEffectiveNicknameContract(t *testing.T) {
	db := isolatedCommandMongo(t)
	t.Logf("isolated_database=%s", db.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A case-insensitive collection default must not change the old Go == rule.
	for _, name := range []string{"group_member", "user"} {
		if err := db.CreateCollection(ctx, name, options.CreateCollection().SetCollation(&options.Collation{Locale: "en", Strength: 2})); err != nil {
			t.Fatal(err)
		}
	}
	store, err := NewGroupMember(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewUserMongo(db); err != nil {
		t.Fatal(err)
	}
	members := []interface{}{
		bson.M{"group_id": "g", "user_id": "needle", "nickname": ""},
		bson.M{"group_id": "g", "user_id": "u-a", "nickname": "needle", "role_level": 100},
		bson.M{"group_id": "g", "user_id": "u-b", "nickname": "", "role_level": 60},
		bson.M{"group_id": "g", "user_id": "u-c", "nickname": "override"},
		bson.M{"group_id": "g", "user_id": "u-d", "nickname": "needle-more"},
		bson.M{"group_id": "g", "user_id": "u-e", "nickname": ""},
		bson.M{"group_id": "g", "user_id": "u-regex", "nickname": ".*"},
		bson.M{"group_id": "g", "user_id": "u-missing"},
		bson.M{"group_id": "g", "user_id": "u-null", "nickname": nil},
		bson.M{"group_id": "g", "user_id": "orphan", "nickname": ""},
		bson.M{"group_id": "foreign", "user_id": "foreign-member", "nickname": "needle"},
	}
	if _, err := db.Collection("group_member").InsertMany(ctx, members); err != nil {
		t.Fatal(err)
	}
	users := []interface{}{}
	for id, name := range map[string]string{
		"needle": "needle", "u-a": "other", "u-b": "needle", "u-c": "needle",
		"u-d": "other", "u-e": "Needle", "u-regex": "other", "u-missing": "needle",
		"u-null": "needle", "foreign-member": "needle", "not-a-member": "needle",
	} {
		users = append(users, bson.M{"user_id": id, "nickname": name})
	}
	if _, err := db.Collection("user").InsertMany(ctx, users); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		keyword string
		ids     []string
	}{
		{"needle", []string{"needle", "u-a", "u-b", "u-missing", "u-null"}},
		{"Needle", []string{"u-e"}},
		{"need", []string{}},
		{".*", []string{"u-regex"}},
		{"override", []string{"u-c"}},
		{"orphan", []string{"orphan"}},
	} {
		t.Run(tc.keyword, func(t *testing.T) {
			total, found, err := store.SearchMemberExact(ctx, "g", tc.keyword, &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 20})
			if err != nil || found == nil || total != int64(len(tc.ids)) || !reflect.DeepEqual(exactMemberIDs(found), tc.ids) {
				t.Fatal("exact ID/card/global fallback, case, literal regex or scope changed", total, exactMemberIDs(found), err)
			}
			for _, member := range found {
				if member.GroupID != "g" || (member.UserID != tc.keyword && member.Nickname != tc.keyword) {
					t.Fatal("a nonmatching or foreign member escaped the database filter")
				}
			}
		})
	}
	var all []string
	for page := int32(1); page <= 4; page++ {
		total, found, err := store.SearchMemberExact(ctx, "g", "needle", &sdkws.RequestPagination{PageNumber: page, ShowNumber: 2})
		if err != nil || total != 5 || len(found) > 2 {
			t.Fatal("count or bounded page changed", total, err)
		}
		all = append(all, exactMemberIDs(found)...)
	}
	if !reflect.DeepEqual(all, []string{"needle", "u-a", "u-b", "u-missing", "u-null"}) {
		t.Fatal("pagination skipped, duplicated or reordered the same member", all)
	}
	for _, page := range []*sdkws.RequestPagination{nil, {}, {PageNumber: -1, ShowNumber: 2}, {PageNumber: 1, ShowNumber: 0}} {
		total, found, err := store.SearchMemberExact(ctx, "g", "needle", page)
		if err != nil || found == nil || total != 5 || len(found) != 0 {
			t.Fatal("invalid pagination changed legacy count/empty-page behavior", total, err)
		}
	}
	if _, err := db.Collection("user").UpdateOne(ctx, bson.M{"user_id": "u-b"}, bson.M{"$set": bson.M{"nickname": "new-global"}}); err != nil {
		t.Fatal(err)
	}
	total, _, err := store.SearchMemberExact(ctx, "g", "needle", &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 20})
	if err != nil || total != 4 {
		t.Fatal("current global profile update was not reflected", total, err)
	}
}

func TestMongoGroupMemberSearchIndexSetupIsRepeatableAndAdditive(t *testing.T) {
	db := isolatedCommandMongo(t)
	t.Logf("isolated_database=%s", db.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := NewGroupMember(db); err != nil {
		t.Fatal(err)
	}
	if err := ensureGroupMemberExactSearchIndexes(ctx, db); err != nil {
		t.Fatal("repeated setup changed an existing index", err)
	}
	for collection, names := range map[string][]string{
		"group_member": {"group_id_1_user_id_1", groupMemberExactNicknameIndex},
		"user":         {userExactNicknameIndex},
	} {
		cursor, err := db.Collection(collection).Indexes().List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var indexes []bson.M
		if err := cursor.All(ctx, &indexes); err != nil {
			t.Fatal(err)
		}
		found := map[string]bool{}
		for _, index := range indexes {
			found[index["name"].(string)] = true
			if index["name"] == "group_id_1_user_id_1" && index["unique"] != true {
				t.Fatal("membership identity uniqueness was changed")
			}
		}
		for _, name := range names {
			if !found[name] {
				t.Fatalf("required additive index missing: %s/%s", collection, name)
			}
		}
	}
}

func TestMongoGroupMemberExactSearchUsesEqualityIndexesOnSyntheticGroup(t *testing.T) {
	db := isolatedCommandMongo(t)
	t.Logf("isolated_database=%s", db.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := NewGroupMember(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewUserMongo(db); err != nil {
		t.Fatal(err)
	}
	const size = 2048 // representative index probe; not a production-scale benchmark
	members, users := make([]interface{}, 0, size), make([]interface{}, 0, size)
	for i := 0; i < size; i++ {
		id := fmt.Sprintf("synthetic-%05d", i)
		card, nickname := "", "unrelated"
		if i == 41 {
			card = "unique-needle"
		}
		if i == 79 {
			nickname = "unique-needle"
		}
		members = append(members, bson.M{"group_id": "large-fixture", "user_id": id, "nickname": card})
		users = append(users, bson.M{"user_id": id, "nickname": nickname})
	}
	if _, err := db.Collection("group_member").InsertMany(ctx, members); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Collection("user").InsertMany(ctx, users); err != nil {
		t.Fatal(err)
	}
	page := &sdkws.RequestPagination{PageNumber: 1, ShowNumber: 1}
	total, found, err := store.SearchMemberExact(ctx, "large-fixture", "unique-needle", page)
	if err != nil || total != 2 || len(found) != 1 {
		t.Fatal("representative group search changed count/page", total, err)
	}
	var explain bson.M
	err = db.RunCommand(ctx, bson.D{
		{Key: "explain", Value: bson.D{
			{Key: "aggregate", Value: "group_member"},
			{Key: "pipeline", Value: exactGroupMemberSearchPipeline("large-fixture", "unique-needle", page)},
			{Key: "cursor", Value: bson.D{}},
			{Key: "collation", Value: bson.D{{Key: "locale", Value: "simple"}}},
			{Key: "allowDiskUse", Value: true},
		}},
		{Key: "verbosity", Value: "executionStats"},
	}).Decode(&explain)
	if err != nil {
		t.Fatal(err)
	}
	indexes := map[string]bool{}
	metrics := 0
	var walk func(any)
	walk = func(value any) {
		switch value := value.(type) {
		case bson.D:
			object := bson.M{}
			for _, field := range value {
				object[field.Key] = field.Value
			}
			walk(object)
		case bson.M:
			if value["stage"] == "COLLSCAN" {
				t.Fatal("an executed/winning branch scanned the whole collection")
			}
			if name, ok := value["indexName"].(string); ok {
				indexes[name] = true
			}
			for key, nested := range value {
				if key == "rejectedPlans" || key == "allPlansExecution" {
					continue
				}
				if key == "totalDocsExamined" {
					metrics++
					var examined int64
					switch n := nested.(type) {
					case int32:
						examined = int64(n)
					case int64:
						examined = n
					case float64:
						examined = int64(n)
					default:
						t.Fatalf("unrecognized explain metric type %T", nested)
					}
					if examined > 32 {
						t.Fatal("selective exact search examined too many synthetic documents", examined)
					}
				}
				walk(nested)
			}
		case bson.A:
			for _, item := range value {
				walk(item)
			}
		}
	}
	walk(explain)
	if metrics == 0 || !indexes[groupMemberExactNicknameIndex] || !indexes[userExactNicknameIndex] {
		t.Fatal("explain did not prove both selective nickname branches used their equality indexes", metrics, indexes)
	}
	// Explain documents contain only this test's synthetic IDs and constants.
	encoded, err := json.Marshal(explain)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("synthetic_explain=%s", encoded)
	t.Logf("synthetic_members=%d matching=%d page=%d execution_metrics=%d named_nickname_indexes_used=true", size, total, len(found), metrics)
}
