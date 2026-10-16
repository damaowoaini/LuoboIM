package mgo

import (
	"context"
	"fmt"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/pagination"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	groupMemberExactNicknameIndex = "group_member_group_nickname_user_exact_v1"
	userExactNicknameIndex        = "user_nickname_user_exact_v1"
)

// These additive, named indexes enable the exact-search path. Index failures
// are explicit instead of silently running a global nickname collection scan.
// Repeated startup preserves existing indexes and data.
func ensureGroupMemberExactSearchIndexes(ctx context.Context, db *mongo.Database) error {
	for _, spec := range []struct {
		collection string
		name       string
		keys       bson.D
	}{
		{"group_member", groupMemberExactNicknameIndex, bson.D{{Key: "group_id", Value: 1}, {Key: "nickname", Value: 1}, {Key: "user_id", Value: 1}}},
		{"user", userExactNicknameIndex, bson.D{{Key: "nickname", Value: 1}, {Key: "user_id", Value: 1}}},
	} {
		_, err := db.Collection(spec.collection).Indexes().CreateOne(ctx, mongo.IndexModel{
			Keys: spec.keys,
			Options: options.Index().SetName(spec.name).
				SetCollation(&options.Collation{Locale: "simple"}),
		})
		if err != nil {
			return fmt.Errorf("exact group-member search index %s: %w", spec.name, err)
		}
	}
	return nil
}

func exactGroupMemberSearchPipeline(groupID, keyword string, page pagination.Pagination) mongo.Pipeline {
	// The old RPC compared Go strings after PopulateGroupMember: an explicit
	// group card wins; an empty/missing card uses the current global nickname.
	// Begin both branches at an equality index, not at all members of a group.
	fallback := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{{Key: "nickname", Value: keyword}}}},
		bson.D{{Key: "$lookup", Value: bson.D{
			{Key: "from", Value: "group_member"},
			{Key: "let", Value: bson.D{{Key: "userID", Value: "$user_id"}}},
			{Key: "pipeline", Value: mongo.Pipeline{
				bson.D{{Key: "$match", Value: bson.D{
					{Key: "group_id", Value: groupID},
					{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$user_id", "$$userID"}}}},
					{Key: "$or", Value: bson.A{bson.D{{Key: "nickname", Value: ""}}, bson.D{{Key: "nickname", Value: nil}}}},
				}}},
			}},
			{Key: "as", Value: "matched_member"},
		}}},
		bson.D{{Key: "$unwind", Value: "$matched_member"}},
		// Keep the matching global nickname for later page-only hydration;
		// a stale profile cache must not change which nickname matched.
		bson.D{{Key: "$set", Value: bson.D{{Key: "matched_member.nickname", Value: "$nickname"}}}},
		bson.D{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$matched_member"}}}},
	}
	pageStages := mongo.Pipeline{bson.D{{Key: "$match", Value: bson.D{{Key: "$expr", Value: false}}}}}
	if page != nil && page.GetPageNumber() > 0 && page.GetShowNumber() > 0 {
		pageStages = mongo.Pipeline{
			bson.D{{Key: "$skip", Value: int64(page.GetPageNumber()-1) * int64(page.GetShowNumber())}},
			bson.D{{Key: "$limit", Value: int64(page.GetShowNumber())}},
		}
	}
	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.D{
			{Key: "group_id", Value: groupID},
			{Key: "$or", Value: bson.A{bson.D{{Key: "user_id", Value: keyword}}, bson.D{{Key: "nickname", Value: keyword}}}},
		}}},
		bson.D{{Key: "$unionWith", Value: bson.D{{Key: "coll", Value: "user"}, {Key: "pipeline", Value: fallback}}}},
		// A user can match their ID and fallback nickname. Dedupe before count
		// and pagination, including overlapping branches during profile changes.
		bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: "$user_id"}, {Key: "member", Value: bson.D{{Key: "$first", Value: "$$ROOT"}}}}}},
		bson.D{{Key: "$replaceRoot", Value: bson.D{{Key: "newRoot", Value: "$member"}}}},
		// Sort outside $facet so allowDiskUse can spill a broad exact match;
		// the facet itself only retains the requested page and one count.
		bson.D{{Key: "$sort", Value: bson.D{{Key: "user_id", Value: 1}}}},
		bson.D{{Key: "$facet", Value: bson.D{
			{Key: "total", Value: mongo.Pipeline{bson.D{{Key: "$count", Value: "count"}}}},
			{Key: "members", Value: pageStages},
		}}},
	}
}

func (g *GroupMemberMgo) SearchMemberExact(ctx context.Context, groupID, keyword string, page pagination.Pagination) (int64, []*relation.GroupMemberModel, error) {
	if groupID == "" || keyword == "" {
		return 0, nil, errs.ErrArgs.Wrap("group ID and exact search keyword required")
	}
	cursor, err := g.coll.Aggregate(ctx, exactGroupMemberSearchPipeline(groupID, keyword, page),
		options.Aggregate().SetCollation(&options.Collation{Locale: "simple"}).SetAllowDiskUse(true))
	if err != nil {
		return 0, nil, err
	}
	defer cursor.Close(ctx)
	var result []struct {
		Total []struct {
			Count int64 `bson:"count"`
		} `bson:"total"`
		Members []*relation.GroupMemberModel `bson:"members"`
	}
	if err := cursor.All(ctx, &result); err != nil {
		return 0, nil, err
	}
	if len(result) == 0 || len(result[0].Total) == 0 {
		return 0, []*relation.GroupMemberModel{}, nil
	}
	members := result[0].Members
	if members == nil {
		members = []*relation.GroupMemberModel{}
	}
	return result[0].Total[0].Count, members, nil
}
