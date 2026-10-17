package favorite

import (
	"context"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type MongoStore struct {
	items, categories *mongo.Collection
	client            *mongo.Client
}

func NewMongoStore(ctx context.Context, db *mongo.Database) (*MongoStore, error) {
	var topology struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err := db.Client().Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&topology); err != nil {
		return nil, err
	}
	if topology.SetName == "" && topology.Msg != "isdbgrid" {
		return nil, errs.ErrInternalServer.Wrap("favorites require a Mongo replica set or mongos; follow deployment migration before enabling the new API")
	}

	collOpts := options.Collection().SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true)))
	store := &MongoStore{items: db.Collection("favorite_items", collOpts), categories: db.Collection("favorite_categories", collOpts), client: db.Client()}
	_, err := store.items.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "favoriteID", Value: 1}}, Options: options.Index().SetUnique(true).SetName("favorite_owner_id")},
		{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "favoriteType", Value: 1}, {Key: "conversationID", Value: 1}, {Key: "sourceID", Value: 1}}, Options: options.Index().SetUnique(true).SetName("favorite_owner_source")},
		{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "createTime", Value: -1}, {Key: "favoriteID", Value: 1}}},
		{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "categoryID", Value: 1}, {Key: "createTime", Value: -1}}},
		{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "tags", Value: 1}, {Key: "createTime", Value: -1}}},
	})
	if err != nil {
		return nil, err
	}
	_, err = store.categories.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "categoryID", Value: 1}}, Options: options.Index().SetUnique(true).SetName("category_owner_id")},
		{Keys: bson.D{{Key: "userID", Value: 1}, {Key: "nameKey", Value: 1}}, Options: options.Index().SetUnique(true).SetName("category_owner_name")},
	})
	if err != nil {
		return nil, err
	}
	return store, nil
}
func (m *MongoStore) Execute(ctx context.Context, owner, operation string, req Request, now time.Time) (any, error) {
	switch operation {
	case "list", "search":
		return m.list(ctx, owner, operation, req)
	case "info":
		var item Favorite
		err := m.items.FindOne(ctx, bson.M{"userID": owner, "favoriteID": req.FavoriteID}).Decode(&item)
		if err == mongo.ErrNoDocuments {
			return nil, missing()
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"favoriteInfo": item}, nil
	case "category/list":
		return m.categoryList(ctx, owner)
	case "stats":
		return m.stats(ctx, owner, now)
	}
	session, err := m.client.StartSession()
	if err != nil {
		return nil, err
	}
	defer session.EndSession(ctx)
	response, err := session.WithTransaction(ctx, func(tx mongo.SessionContext) (interface{}, error) { return m.mutate(tx, owner, operation, req, now) }, options.Transaction().SetReadConcern(readconcern.Snapshot()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	if mongo.IsDuplicateKeyError(err) {
		if operation == "add" {
			var item Favorite
			if lookupErr := m.items.FindOne(ctx, sourceFilter(owner, req)).Decode(&item); lookupErr == nil {
				return map[string]any{"favoriteInfo": item}, nil
			}
		}
		return nil, errs.ErrDuplicateKey.Wrap("收藏来源或分类名称已存在")
	}
	return response, err
}
func sourceFilter(owner string, req Request) bson.M {
	return bson.M{"userID": owner, "favoriteType": req.FavoriteType, "conversationID": req.ConversationID, "sourceID": req.SourceID}
}
func (m *MongoStore) list(ctx context.Context, owner, operation string, req Request) (any, error) {
	if operation == "search" && strings.TrimSpace(req.Keyword) == "" {
		return nil, argument("搜索关键词不能为空")
	}
	page, size := req.Pagination.PageNumber, req.Pagination.ShowNumber
	if page == 0 {
		page = 1
	}
	if size == 0 {
		size = 20
	}
	if page < 1 || page > 100000 || size < 1 || size > 100 {
		return nil, argument("分页参数超出范围")
	}
	filter := bson.M{"userID": owner}
	if req.FavoriteType != 0 {
		filter["favoriteType"] = req.FavoriteType
	}
	if req.CategoryID != nil && *req.CategoryID != "" {
		filter["categoryID"] = categoryID(*req.CategoryID)
	}
	if req.SourceID != "" {
		filter["sourceID"] = req.SourceID
	}
	if req.ConversationID != "" {
		filter["conversationID"] = req.ConversationID
	}
	if req.Tags != nil && len(*req.Tags) > 0 {
		filter["tags"] = bson.M{"$all": *req.Tags}
	}
	if keyword := strings.TrimSpace(req.Keyword); keyword != "" {
		pattern := primitive.Regex{Pattern: regexp.QuoteMeta(keyword), Options: "i"}
		filter["$or"] = bson.A{bson.M{"title": pattern}, bson.M{"content": pattern}, bson.M{"notes": pattern}, bson.M{"tags": pattern}}
	}
	total, err := m.items.CountDocuments(ctx, filter)
	if err != nil {
		return nil, err
	}
	cursor, err := m.items.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "createTime", Value: -1}, {Key: "favoriteID", Value: 1}}).SetSkip(int64((page-1)*size)).SetLimit(int64(size)))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	items := []Favorite{}
	if err := cursor.All(ctx, &items); err != nil {
		return nil, err
	}
	return map[string]any{"favorites": items, "total": total, "hasMore": int64(page*size) < total}, nil
}
func (m *MongoStore) categoryList(ctx context.Context, owner string) (any, error) {
	cursor, err := m.categories.Find(ctx, bson.M{"userID": owner}, options.Find().SetSort(bson.D{{Key: "sortOrder", Value: 1}, {Key: "categoryID", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	state := &State{UserID: owner, Categories: []Category{}}
	if err := cursor.All(ctx, &state.Categories); err != nil {
		return nil, err
	}
	counts, err := m.items.Aggregate(ctx, mongo.Pipeline{{{Key: "$match", Value: bson.M{"userID": owner}}}, {{Key: "$group", Value: bson.M{"_id": "$categoryID", "count": bson.M{"$sum": 1}}}}})
	if err != nil {
		return nil, err
	}
	defer counts.Close(ctx)
	countMap := map[string]int{}
	for counts.Next(ctx) {
		var row struct {
			ID    string `bson:"_id"`
			Count int    `bson:"count"`
		}
		if err := counts.Decode(&row); err != nil {
			return nil, err
		}
		countMap[row.ID] = row.Count
	}
	if err := counts.Err(); err != nil {
		return nil, err
	}
	result := categories(state)
	for i := range result {
		result[i].FavoriteCount = countMap[result[i].CategoryID]
	}
	return map[string]any{"categories": result}, nil
}
func (m *MongoStore) mutate(ctx mongo.SessionContext, owner, operation string, req Request, now time.Time) (any, error) {
	if operation == "category/delete" {
		return m.deleteCategory(ctx, owner, req, now)
	}
	state := &State{UserID: owner, Favorites: []Favorite{}, Categories: []Category{}}
	itemFilter := bson.M{"userID": owner}
	loadItems := true
	switch operation {
	case "add":
		itemFilter = sourceFilter(owner, req)
	case "update":
		itemFilter["favoriteID"] = req.FavoriteID
	case "remove", "batch_operate":
		itemFilter["favoriteID"] = bson.M{"$in": req.FavoriteIDs}
	default:
		loadItems = false
	}
	if loadItems {
		cursor, err := m.items.Find(ctx, itemFilter)
		if err != nil {
			return nil, err
		}
		defer cursor.Close(ctx)
		if err := cursor.All(ctx, &state.Favorites); err != nil {
			return nil, err
		}
	}
	if operation == "add" && len(state.Favorites) > 0 {
		return map[string]any{"favoriteInfo": state.Favorites[0]}, nil
	}
	categoryFilter := bson.M{"userID": owner}
	target := ""
	loadCategories := false
	switch operation {
	case "add", "update":
		if req.CategoryID != nil {
			target = categoryID(*req.CategoryID)
		} else if operation == "add" {
			target = DefaultCategoryID
		}
	case "batch_operate":
		if req.Operation == 1 {
			target = categoryID(req.TargetCategoryID)
		}
	case "category/create", "category/update":
		loadCategories = true
		conditions := bson.A{}
		if req.CategoryID != nil {
			conditions = append(conditions, bson.M{"categoryID": *req.CategoryID})
		}
		if req.CategoryName != nil {
			conditions = append(conditions, bson.M{"nameKey": strings.ToLower(strings.TrimSpace(*req.CategoryName))})
		}
		if len(conditions) > 0 {
			categoryFilter["$or"] = conditions
		} else {
			categoryFilter["categoryID"] = ""
		}
	}
	if target != "" && target != DefaultCategoryID {
		loadCategories = true
		categoryFilter["categoryID"] = target
	}
	if loadCategories {
		cursor, err := m.categories.Find(ctx, categoryFilter)
		if err != nil {
			return nil, err
		}
		defer cursor.Close(ctx)
		if err := cursor.All(ctx, &state.Categories); err != nil {
			return nil, err
		}
	}
	if target != "" && target != DefaultCategoryID {
		// Serialize only operations referencing the same category. Without this write
		// a concurrent category delete and favorite insert could leave an orphan.
		result, err := m.categories.UpdateOne(ctx, bson.M{"userID": owner, "categoryID": target}, bson.M{"$inc": bson.M{"version": 1}})
		if err != nil {
			return nil, err
		}
		if result.MatchedCount != 1 {
			return nil, missing()
		}
	}
	if operation == "category/create" && req.SortOrder == nil {
		count, err := m.categories.CountDocuments(ctx, bson.M{"userID": owner})
		if err != nil {
			return nil, err
		}
		order := int(count) + 1
		req.SortOrder = &order
	}
	beforeItems := append([]Favorite{}, state.Favorites...)
	beforeCategories := append([]Category{}, state.Categories...)
	result, err := (&Service{Now: func() time.Time { return now }}).mutate(state, operation, req)
	if err != nil {
		return nil, err
	}
	for _, item := range state.Favorites {
		found := false
		for _, before := range beforeItems {
			if before.FavoriteID == item.FavoriteID {
				found = true
				if !reflect.DeepEqual(before, item) {
					res, err := m.items.ReplaceOne(ctx, bson.M{"userID": owner, "favoriteID": item.FavoriteID}, item)
					if err != nil {
						return nil, err
					}
					if res.MatchedCount != 1 {
						return nil, missing()
					}
				}
				break
			}
		}
		if !found {
			if _, err := m.items.InsertOne(ctx, item); err != nil {
				return nil, err
			}
		}
	}
	deleted := []string{}
	for _, before := range beforeItems {
		if findFavorite(state, before.FavoriteID) < 0 {
			deleted = append(deleted, before.FavoriteID)
		}
	}
	if len(deleted) > 0 {
		if _, err := m.items.DeleteMany(ctx, bson.M{"userID": owner, "favoriteID": bson.M{"$in": deleted}}); err != nil {
			return nil, err
		}
	}
	for _, category := range state.Categories {
		found := false
		for _, before := range beforeCategories {
			if before.CategoryID == category.CategoryID {
				found = true
				if !reflect.DeepEqual(before, category) {
					category.NameKey = strings.ToLower(category.CategoryName)
					res, err := m.categories.ReplaceOne(ctx, bson.M{"userID": owner, "categoryID": category.CategoryID}, category)
					if err != nil {
						return nil, err
					}
					if res.MatchedCount != 1 {
						return nil, missing()
					}
				}
				break
			}
		}
		if !found {
			category.NameKey = strings.ToLower(category.CategoryName)
			if _, err := m.categories.InsertOne(ctx, category); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}
func (m *MongoStore) deleteCategory(ctx mongo.SessionContext, owner string, req Request, now time.Time) (any, error) {
	id := text(req.CategoryID)
	if id == DefaultCategoryID || id == "" {
		return nil, argument("默认分类不可删除")
	}
	result, err := m.categories.DeleteOne(ctx, bson.M{"userID": owner, "categoryID": id})
	if err != nil {
		return nil, err
	}
	if result.DeletedCount != 1 {
		return nil, missing()
	}
	filter := bson.M{"userID": owner, "categoryID": id}
	if req.MoveToDefault == nil || *req.MoveToDefault {
		_, err = m.items.UpdateMany(ctx, filter, bson.M{"$set": bson.M{"categoryID": DefaultCategoryID, "updateTime": now.UnixMilli()}})
	} else {
		_, err = m.items.DeleteMany(ctx, filter)
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true}, nil
}
func (m *MongoStore) stats(ctx context.Context, owner string, now time.Time) (any, error) {
	day := time.Date(now.UTC().Year(), now.UTC().Month(), now.UTC().Day(), 0, 0, 0, 0, time.UTC)
	week := day.AddDate(0, 0, -(int(day.Weekday())+6)%7)
	month := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
	sumIf := func(condition any) bson.M { return bson.M{"$sum": bson.M{"$cond": bson.A{condition, 1, 0}}} }
	group := bson.M{"_id": nil, "totalCount": bson.M{"$sum": 1}, "lastFavoriteTime": bson.M{"$max": "$createTime"}}
	names := []string{"", "messageCount", "fileCount", "imageCount", "videoCount", "audioCount", "linkCount"}
	for i, name := range names[1:] {
		group[name] = sumIf(bson.M{"$eq": bson.A{"$favoriteType", i + 1}})
	}
	for key, start := range map[string]time.Time{"todayAddedCount": day, "weekAddedCount": week, "monthAddedCount": month} {
		group[key] = sumIf(bson.M{"$gte": bson.A{"$createTime", start.UnixMilli()}})
	}
	result := map[string]any{"totalCount": 0, "lastFavoriteTime": int64(0), "todayAddedCount": 0, "weekAddedCount": 0, "monthAddedCount": 0}
	for _, name := range names[1:] {
		result[name] = 0
	}
	cursor, err := m.items.Aggregate(ctx, mongo.Pipeline{{{Key: "$match", Value: bson.M{"userID": owner}}}, {{Key: "$group", Value: group}}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	if cursor.Next(ctx) {
		var values bson.M
		if err := cursor.Decode(&values); err != nil {
			return nil, err
		}
		for key, value := range values {
			if key != "_id" {
				result[key] = value
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	listed, err := m.categoryList(ctx, owner)
	if err != nil {
		return nil, err
	}
	categories := listed.(map[string]any)["categories"].([]Category)
	result["categoryCount"] = len(categories)
	result["categoriesWithCount"] = categories
	return result, nil
}
