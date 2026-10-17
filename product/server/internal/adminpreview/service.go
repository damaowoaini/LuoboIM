package adminpreview

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"github.com/redis/go-redis/v9"
	"strings"
	"time"
)

type cursor struct {
	Actor, Target, Conversation, Filter string
	Before, Floor, Snapshot, Expires    int64
	Unavailable                         int
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func validConversation(row *relation.ConversationModel, user string) bool {
	if row == nil || row.OwnerUserID != user {
		return false
	}
	var expected string
	switch row.ConversationType {
	case constant.SingleChatType:
		if row.UserID == "" {
			return false
		}
		expected = msgprocessor.GetConversationIDBySessionType(constant.SingleChatType, user, row.UserID)
	case constant.GroupChatType, constant.SuperGroupChatType:
		if row.GroupID == "" {
			return false
		}
		expected = msgprocessor.GetConversationIDBySessionType(int(row.ConversationType), row.GroupID)
	case constant.NotificationChatType:
		expected = "sn_" + row.UserID + "_" + user
		if row.ConversationID == msgprocessor.GetConversationIDBySessionType(constant.NotificationChatType, user) {
			return true
		}
	default:
		return false
	}
	return row.ConversationID == expected
}
func validate(req *Request) error {
	if req.UserID == "" || len(req.UserID) > 64 || len(req.ConversationID) > 256 || len(req.Keyword) > 512 || req.StartTime < 0 || req.EndTime < 0 || req.EndTime > 0 && req.StartTime > req.EndTime || len(req.Cursor) > 4096 {
		return errs.ErrArgs.Wrap("预览查询参数无效")
	}
	if req.PageSize == 0 {
		req.PageSize = 40
	}
	if req.PageSize < 1 || req.PageSize > 100 {
		return errs.ErrArgs.Wrap("每页须为1..100条")
	}
	return nil
}
func filterHash(req Request) string {
	data, _ := json.Marshal([]any{req.Keyword, req.StartTime, req.EndTime, req.PageSize})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func (s *Service) encode(value cursor) string {
	data, _ := json.Marshal(value)
	mac := hmac.New(sha256.New, s.CursorKey)
	mac.Write(data)
	return base64.RawURLEncoding.EncodeToString(data) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Service) decode(token string) (cursor, error) {
	var result cursor
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return result, errs.ErrArgs.Wrap("预览游标无效")
	}
	data, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return result, errs.ErrArgs.Wrap("预览游标无效")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	mac := hmac.New(sha256.New, s.CursorKey)
	mac.Write(data)
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) || json.Unmarshal(data, &result) != nil || result.Expires < s.now().Unix() {
		return result, errs.ErrArgs.Wrap("预览游标无效或已过期")
	}
	return result, nil
}
func (s *Service) Conversations(ctx context.Context, actor string, req Request) (*ConversationsResponse, error) {
	if actor == "" || len(actor) > 64 {
		return nil, errs.ErrNoPermission.Wrap("缺少管理员身份")
	}
	if err := validate(&req); err != nil {
		return nil, err
	}
	if req.PageNumber == 0 {
		req.PageNumber = 1
	}
	if req.PageNumber < 1 || req.PageNumber > 100000 {
		return nil, errs.ErrArgs.Wrap("预览页码无效")
	}
	audit, err := s.Audit.Begin(ctx, actor, "conversations", req)
	if err != nil {
		return nil, errs.ErrInternalServer.Wrap("预览审计写入失败")
	}
	rows, err := s.Source.List(ctx, req.UserID)
	if err != nil {
		s.Audit.Finish(ctx, audit, "failed", 0, 0)
		return nil, err
	}
	if req.Keyword != "" {
		needle := strings.ToLower(req.Keyword)
		filtered := make([]Conversation, 0, len(rows))
		for _, row := range rows {
			if strings.Contains(strings.ToLower(row.ShowName+"\n"+row.UserID+"\n"+row.GroupID), needle) {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
	}
	start := (req.PageNumber - 1) * req.PageSize
	if start > len(rows) {
		start = len(rows)
	}
	end := min(start+req.PageSize, len(rows))
	result := &ConversationsResponse{Conversations: rows[start:end], Total: len(rows), PageNumber: req.PageNumber, PageSize: req.PageSize}
	if err := s.Audit.Finish(ctx, audit, "complete", len(result.Conversations), len(rows)); err != nil {
		return nil, errs.ErrInternalServer.Wrap("预览审计收尾失败")
	}
	return result, nil
}
func (s *Service) Search(ctx context.Context, actor string, req Request) (*SearchResponse, error) {
	if actor == "" || len(actor) > 64 {
		return nil, errs.ErrNoPermission.Wrap("缺少管理员身份")
	}
	if err := validate(&req); err != nil {
		return nil, err
	}
	if req.ConversationID == "" || req.PageNumber > 1 || len(s.CursorKey) < 32 {
		return nil, errs.ErrArgs.Wrap("历史预览需要会话和有效游标配置")
	}
	audit, err := s.Audit.Begin(ctx, actor, "search", req)
	if err != nil {
		return nil, errs.ErrInternalServer.Wrap("预览审计写入失败")
	}
	row, err := s.Source.Get(ctx, req.UserID, req.ConversationID)
	if err != nil {
		s.Audit.Finish(ctx, audit, "failed", 0, 0)
		return nil, err
	}
	if !validConversation(row, req.UserID) {
		s.Audit.Finish(ctx, audit, "denied", 0, 0)
		return nil, errs.ErrNoPermission.Wrap("会话归属无效")
	}
	result, err := s.search(ctx, actor, req, row)
	if err != nil {
		s.Audit.Finish(ctx, audit, "failed", 0, 0)
		return nil, err
	}
	if err := s.Audit.Finish(ctx, audit, "complete", len(result.Messages), result.ScannedCount); err != nil {
		return nil, errs.ErrInternalServer.Wrap("预览审计收尾失败")
	}
	return result, nil
}
func (s *Service) search(ctx context.Context, actor string, req Request, row *relation.ConversationModel) (*SearchResponse, error) {
	floor, maximum, err := s.Source.Bounds(ctx, row)
	if err != nil {
		return nil, err
	}
	floor = max(floor, 1)
	position := cursor{Actor: actor, Target: req.UserID, Conversation: req.ConversationID, Filter: filterHash(req), Before: maximum, Snapshot: maximum, Floor: floor, Expires: s.now().Add(30 * time.Minute).Unix()}
	if req.Cursor != "" {
		position, err = s.decode(req.Cursor)
		if err != nil {
			return nil, err
		}
		if position.Actor != actor || position.Target != req.UserID || position.Conversation != req.ConversationID || position.Filter != filterHash(req) {
			return nil, errs.ErrArgs.Wrap("游标与当前管理员、用户或过滤条件不匹配")
		}
		position.Floor = max(position.Floor, floor)
		position.Before = min(position.Before, maximum)
	}
	result := &SearchResponse{Messages: []map[string]any{}}
	keyCache := map[int32]string{}
	keyword := strings.ToLower(req.Keyword)
	scannedBytes, responseBytes := 0, 256
	budgetReached := false
	for position.Before >= position.Floor && result.ScannedCount < 2000 && len(result.Messages) < req.PageSize && !budgetReached {
		lower := max(position.Floor, position.Before-19)
		seqs := make([]int64, 0, 100)
		for seq := lower; seq <= position.Before; seq++ {
			seqs = append(seqs, seq)
		}
		messages, err := s.Source.Read(ctx, req.UserID, req.ConversationID, seqs)
		if err != nil {
			return nil, err
		}
		bySeq := map[int64]*sdkws.MsgData{}
		for _, message := range messages {
			if message != nil {
				bySeq[message.Seq] = message
			}
		}
		for seq := position.Before; seq >= lower; seq-- {
			value := bySeq[seq]
			if value != nil && scannedBytes+len(value.Content) > 8*1024*1024 && result.ScannedCount > 0 {
				position.Before = seq
				budgetReached = true
				break
			}
			position.Before = seq - 1
			result.ScannedCount++
			if value == nil {
				continue
			}
			message := value
			scannedBytes += len(message.Content)
			if message.SendTime < req.StartTime || req.EndTime > 0 && message.SendTime > req.EndTime {
				continue
			}
			content, state := message.Content, "plain"
			if message.KeyVersion > 0 {
				key, known := keyCache[message.KeyVersion]
				if !known {
					key, err = s.Source.Key(ctx, req.ConversationID, message.KeyVersion)
					if err != nil {
						if errs.Unwrap(err) != redis.Nil {
							return nil, err
						}
						key = ""
					}
					keyCache[message.KeyVersion] = key
				}
				if key == "" {
					state = "unavailable"
				} else {
					content, err = decryptLegacy(content, key)
					if err != nil {
						state = "invalid"
					} else {
						state = "decrypted"
					}
				}
			}
			if state == "unavailable" || state == "invalid" {
				result.UnsearchableCount++
				position.Unavailable++
				if keyword != "" {
					continue
				}
			}
			if keyword != "" && !strings.Contains(searchable(content), keyword) {
				continue
			}
			item := messageItem(message, content, state)
			encoded, _ := json.Marshal(item)
			if responseBytes+len(encoded) > 3*1024*1024 && len(result.Messages) > 0 {
				position.Before = seq
				result.ScannedCount--
				if state == "unavailable" || state == "invalid" {
					result.UnsearchableCount--
					position.Unavailable--
				}
				budgetReached = true
				break
			}
			if len(encoded) > 3*1024*1024 {
				item = messageItem(message, nil, "invalid")
				item["attachedInfo"] = ""
				item["ex"] = ""
				item["previewError"] = "message exceeds preview response limit"
				encoded, _ = json.Marshal(item)
			}
			responseBytes += len(encoded)
			result.Messages = append(result.Messages, item)
			if len(result.Messages) >= req.PageSize {
				break
			}
		}
	}
	result.HasMore = position.Before >= position.Floor
	if result.HasMore {
		result.NextCursor = s.encode(position)
	}
	result.SearchComplete = !result.HasMore && position.Unavailable == 0
	return result, nil
}
