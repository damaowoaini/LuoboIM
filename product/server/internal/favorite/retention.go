package favorite

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
)

// RetentionSource resolves the real message in the actor's visible history.
// Client-supplied snapshot flags alone cannot authorize permanent retention.
type RetentionSource interface {
	Retainable(context.Context, string, string, string) (bool, error)
}

type RetentionGate struct{ Source RetentionSource }

func MessageAllowsRetention(attachedInfo string, contentType int32, content []byte) bool {
	wire := map[string]any{"attachedInfo": attachedInfo}
	// Plain protocol quotes/merges carry nested message flags. Encrypted content
	// remains opaque here; its recipient-side decoded snapshot is checked too.
	if contentType == constant.Quote || contentType == constant.Merger {
		if strings.HasPrefix(strings.TrimSpace(string(content)), "{") {
			var nested map[string]any
			if json.Unmarshal(content, &nested) != nil {
				return false
			}
			key := "quoteElem"
			if contentType == constant.Merger {
				key = "mergeElem"
			}
			wire[key] = nested
		}
	}
	encoded, _ := json.Marshal(wire)
	privacy := inspectRetention(string(encoded))
	return privacy.complete && !privacy.private
}

func (g *RetentionGate) Authorize(ctx context.Context, actor string, req Request) error {
	if actor == "" || actor != req.UserID {
		return errs.ErrNoPermission.Wrap("无权访问其他账号收藏")
	}
	if err := validRequest(req); err != nil {
		return err
	}
	privacy := inspectRetention(req.Content)
	if privacy.private || !privacy.complete {
		return errs.ErrNoPermission.Wrap("消息隐私状态不允许永久收藏")
	}
	// Standalone legacy files/links have no conversation. All chat kinds and
	// recognizable SDK snapshots must resolve a real source, including files.
	needsSource := req.FavoriteType == 1 || req.ConversationID != "" || privacy.message
	if !needsSource {
		return nil
	}
	if req.SourceID == "" || len(req.SourceID) > 256 || req.ConversationID == "" ||
		(privacy.clientID != "" && privacy.clientID != req.SourceID) {
		return errs.ErrNoPermission.Wrap("收藏原消息无法确认")
	}
	if g == nil || g.Source == nil {
		return errs.ErrInternalServer.Wrap("收藏来源校验暂不可用")
	}
	allowed, err := g.Source.Retainable(ctx, actor, req.ConversationID, req.SourceID)
	if err != nil {
		return errs.ErrInternalServer.Wrap("收藏来源校验暂不可用")
	}
	if !allowed {
		return errs.ErrNoPermission.Wrap("收藏原消息无法确认或不允许永久保存")
	}
	return nil
}

type retentionInspection struct {
	private, message bool
	complete         bool
	clientID         string
}

// Inspect only protocol message/quote/merge fields. Opaque custom application
// JSON and ordinary single or encrypted chats do not imply burn-after-reading.
func inspectRetention(content string) retentionInspection {
	result := retentionInspection{complete: true}
	if !strings.HasPrefix(strings.TrimSpace(content), "{") {
		return result
	}
	var root map[string]any
	if json.Unmarshal([]byte(content), &root) != nil {
		result.complete = false
		return result
	}
	nodes := 0
	var visit func(any, int, bool)
	visit = func(value any, depth int, top bool) {
		if value == nil {
			return
		}
		nodes++
		message, ok := value.(map[string]any)
		if !ok || depth > 32 || nodes > 4096 {
			result.complete = false
			return
		}
		if top {
			if id, exists := message["clientMsgID"]; exists {
				result.message = true
				result.clientID, ok = id.(string)
				if !ok || result.clientID == "" {
					result.complete = false
				}
			}
			if _, ok := message["contentType"].(float64); ok {
				result.message = true
			}
		}
		for _, key := range []string{"attachedInfo", "attachedInfoElem"} {
			value := message[key]
			if value == nil || value == "" {
				continue
			}
			result.message = true
			var info map[string]any
			if key == "attachedInfo" {
				encoded, ok := value.(string)
				if !ok || len(encoded) > 65536 || json.Unmarshal([]byte(encoded), &info) != nil || info == nil {
					result.complete = false
					continue
				}
			} else {
				info, ok = value.(map[string]any)
				if !ok {
					result.complete = false
					continue
				}
			}
			if flag, exists := info["isPrivateChat"]; exists {
				if flag == true {
					result.private = true
				}
				if _, ok := flag.(bool); !ok {
					result.complete = false
				}
			}
		}
		if quote, exists := message["quoteElem"]; exists && quote != nil {
			result.message = true
			if object, ok := quote.(map[string]any); ok {
				visit(object["quoteMessage"], depth+1, false)
			} else {
				result.complete = false
			}
		}
		if merge, exists := message["mergeElem"]; exists && merge != nil {
			result.message = true
			if object, ok := merge.(map[string]any); ok {
				if items, exists := object["multiMessage"]; exists && items != nil {
					if list, ok := items.([]any); ok {
						for _, item := range list {
							if nodes >= 4096 {
								result.complete = false
								break
							}
							visit(item, depth+1, false)
						}
					} else {
						result.complete = false
					}
				}
			} else {
				result.complete = false
			}
		}
	}
	visit(root, 0, true)
	return result
}

// Known private/undecodable legacy snapshots remain deletable without returning
// their retained body. This does not claim that all unmarked old data is proven
// ordinary, and it never silently deletes a user's stored record.
func publicRetention(item Favorite) Favorite {
	privacy := inspectRetention(item.Content)
	if !privacy.private && privacy.complete {
		return item
	}
	item.RetentionBlocked = true
	item.Title = "私密收藏已隔离"
	item.Content, item.ThumbnailURL, item.Notes, item.Ex = "", "", "", ""
	item.Tags = nil
	return item
}

func PublicRetentionResult(value any) any {
	result, ok := value.(map[string]any)
	if !ok {
		return value
	}
	copy := make(map[string]any, len(result))
	for key, item := range result {
		copy[key] = item
	}
	if item, ok := result["favoriteInfo"].(Favorite); ok {
		copy["favoriteInfo"] = publicRetention(item)
	}
	if items, ok := result["favorites"].([]Favorite); ok {
		filtered := make([]Favorite, len(items))
		for i, item := range items {
			filtered[i] = publicRetention(item)
		}
		copy["favorites"] = filtered
	}
	return copy
}
