package adminpreview

import (
	"context"
	"github.com/OpenIMSDK/protocol/conversation"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/controller"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"regexp"
	"strconv"
)

type DataSource struct {
	Database      *mongo.Database
	Redis         redis.UniversalClient
	Reader        controller.MessageReader
	Conversations conversation.ConversationClient
}

func (d *DataSource) Get(ctx context.Context, user, conversationID string) (*relation.ConversationModel, error) {
	var row relation.ConversationModel
	err := d.Database.Collection("conversation").FindOne(ctx, bson.M{"owner_user_id": user, "conversation_id": conversationID}).Decode(&row)
	if err == mongo.ErrNoDocuments {
		return nil, errs.ErrNoPermission.Wrap("目标用户没有此会话")
	}
	return &row, err
}
func (d *DataSource) List(ctx context.Context, user string) ([]Conversation, error) {
	response, err := d.Conversations.GetAllConversations(ctx, &conversation.GetAllConversationsReq{OwnerUserID: user})
	if err != nil {
		return nil, err
	}
	rows := map[string]*conversation.Conversation{}
	ids := []string{}
	for _, row := range response.Conversations {
		model := &relation.ConversationModel{OwnerUserID: row.OwnerUserID, ConversationID: row.ConversationID, ConversationType: row.ConversationType, UserID: row.UserID, GroupID: row.GroupID}
		if validConversation(model, user) {
			rows[row.ConversationID] = row
			ids = append(ids, row.ConversationID)
		}
	}
	result := []Conversation{}
	if len(ids) == 0 {
		return result, nil
	}
	summaries, err := d.Conversations.GetConversationList(ctx, &conversation.GetConversationListReq{UserID: user, ConversationIDs: ids})
	if err != nil {
		return nil, err
	}
	included := map[string]bool{}
	for _, summary := range summaries.ConversationElems {
		row := rows[summary.ConversationID]
		if row == nil {
			continue
		}
		item := Conversation{ConversationID: row.ConversationID, ConversationType: row.ConversationType, UserID: row.UserID, GroupID: row.GroupID, IsPinned: row.IsPinned, ShowName: row.UserID}
		if row.GroupID != "" {
			item.ShowName = row.GroupID
		}
		if info := summary.MsgInfo; info != nil {
			item.LatestMsgSendTime = info.LatestMsgRecvTime
			if row.GroupID != "" {
				if info.GroupName != "" {
					item.ShowName = info.GroupName
				}
				item.FaceURL = info.GroupFaceURL
			} else {
				if info.SenderName != "" {
					item.ShowName = info.SenderName
				}
				item.FaceURL = info.FaceURL
			}
		}
		result = append(result, item)
		included[row.ConversationID] = true
	}
	for _, id := range ids {
		if !included[id] {
			row := rows[id]
			name := row.UserID
			if row.GroupID != "" {
				name = row.GroupID
			}
			result = append(result, Conversation{ConversationID: id, ConversationType: row.ConversationType, UserID: row.UserID, GroupID: row.GroupID, ShowName: name, IsPinned: row.IsPinned})
		}
	}
	return result, nil
}
func (d *DataSource) Bounds(ctx context.Context, row *relation.ConversationModel) (int64, int64, error) {
	maximum, err := d.Reader.GetMaxSeq(ctx, row.ConversationID)
	if err != nil && errs.Unwrap(err) != redis.Nil {
		return 0, 0, err
	}
	if errs.Unwrap(err) == redis.Nil {
		exists, err := d.Database.Collection("msg").CountDocuments(ctx, bson.M{"doc_id": bson.M{"$regex": "^" + regexp.QuoteMeta(row.ConversationID) + ":"}})
		if err != nil {
			return 0, 0, err
		}
		if exists > 0 {
			return 0, 0, errs.ErrData.Wrap("会话序号元数据不可用，需要恢复后重试")
		}
	}

	if row.MaxSeq > 0 {
		maximum = min(maximum, row.MaxSeq)
	}
	minimum, err := d.Reader.GetMinSeq(ctx, row.ConversationID)
	if err != nil && errs.Unwrap(err) != redis.Nil {
		return 0, 0, err
	}
	userMin, err := d.Reader.GetConversationUserMinSeq(ctx, row.ConversationID, row.OwnerUserID)
	if err != nil && errs.Unwrap(err) != redis.Nil {
		return 0, 0, err
	}
	return max(minimum, userMin, row.MinSeq, 1), maximum, nil
}
func (d *DataSource) Read(ctx context.Context, user, conversationID string, seqs []int64) ([]*sdkws.MsgData, error) {
	_, _, messages, err := d.Reader.GetMsgBySeqs(ctx, user, conversationID, seqs)
	return messages, err
}
func (d *DataSource) Key(ctx context.Context, conversationID string, version int32) (string, error) {
	return d.Redis.HGet(ctx, cache.EncryptionKeyPrefix+conversationID, strconv.Itoa(int(version))).Result()
}
