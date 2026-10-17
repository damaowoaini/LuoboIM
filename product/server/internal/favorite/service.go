package favorite

import (
	"context"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/google/uuid"
)

type Service struct {
	Store Store
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func text(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
func categoryID(value string) string {
	if value == "" {
		return DefaultCategoryID
	}
	return value
}
func validTags(tags []string) error {
	if len(tags) > 20 {
		return argument("最多20个标签")
	}
	for _, tag := range tags {
		if strings.TrimSpace(tag) == "" || len(tag) > 96 {
			return argument("标签为空或过长")
		}
	}
	return nil
}
func distinct(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if !seen[tag] {
			out = append(out, tag)
			seen[tag] = true
		}
	}
	return out
}
func validURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && u.User == nil
}
func validRequest(req Request) error {
	if len(req.UserID) > 64 || len(req.SourceID) > 512 || len(req.ConversationID) > 256 || len(text(req.Title)) > 1024 || len(req.Content) > 128*1024 || len(text(req.Notes)) > 8192 || len(text(req.Ex)) > 8192 || len(req.Keyword) > 256 {
		return argument("收藏内容超过长度限制")
	}
	if !validURL(req.ThumbnailURL) {
		return argument("缩略图地址须为HTTP或HTTPS")
	}
	if req.Tags != nil {
		if err := validTags(*req.Tags); err != nil {
			return err
		}
	}
	if err := validTags(req.TargetTags); err != nil {
		return err
	}
	for _, id := range append(append([]string{req.FavoriteID, text(req.CategoryID), req.TargetCategoryID}, req.FavoriteIDs...), req.UserID) {
		if len(id) > 128 {
			return argument("收藏或分类ID过长")
		}
	}
	if len(req.FavoriteIDs) > 100 {
		return argument("单次最多操作100条收藏")
	}
	if req.FavoriteType < 0 || req.FavoriteType > 6 {
		return argument("收藏类型无效")
	}
	return nil
}
func (s *Service) Execute(ctx context.Context, actor, operation string, req Request) (any, error) {
	if actor == "" || req.UserID != actor {
		return nil, errs.ErrNoPermission.Wrap("无权访问其他账号收藏")
	}
	if operation == "add" && (req.SourceID == "" || req.FavoriteType < 1) {
		return nil, argument("收藏来源和类型不能为空")
	}
	if err := validRequest(req); err != nil {
		return nil, err
	}
	switch operation {
	case "list", "search", "info", "stats", "category/list", "add", "update", "remove", "batch_operate", "category/create", "category/update", "category/delete":
		return s.Store.Execute(ctx, actor, operation, req, s.now())
	default:
		return nil, argument("未知收藏操作")
	}
}

func findFavorite(state *State, id string) int {
	for index, f := range state.Favorites {
		if f.FavoriteID == id {
			return index
		}
	}
	return -1
}
func findCategory(state *State, id string) int {
	for index, c := range state.Categories {
		if c.CategoryID == id {
			return index
		}
	}
	return -1
}
func requireCategory(state *State, id string) error {
	if id != DefaultCategoryID && findCategory(state, id) < 0 {
		return missing()
	}
	return nil
}
func categories(state *State) []Category {
	result := append([]Category{{CategoryID: DefaultCategoryID, UserID: state.UserID, CategoryName: "默认分类", CategoryColor: "#2196F3", SortOrder: 0}}, state.Categories...)
	counts := map[string]int{}
	for _, f := range state.Favorites {
		counts[f.CategoryID]++
	}
	for i := range result {
		result[i].FavoriteCount = counts[result[i].CategoryID]
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].CategoryID == DefaultCategoryID {
			return true
		}
		if result[j].CategoryID == DefaultCategoryID {
			return false
		}
		if result[i].SortOrder != result[j].SortOrder {
			return result[i].SortOrder < result[j].SortOrder
		}
		return result[i].CategoryID < result[j].CategoryID
	})
	return result
}
func (s *Service) mutate(state *State, operation string, req Request) (any, error) {
	now := s.now().UnixMilli()
	switch operation {
	case "add":
		if req.SourceID == "" || req.FavoriteType < 1 {
			return nil, argument("收藏来源和类型不能为空")
		}
		for _, f := range state.Favorites {
			if f.FavoriteType == req.FavoriteType && f.SourceID == req.SourceID && f.ConversationID == req.ConversationID {
				return map[string]any{"favoriteInfo": f}, nil
			}
		}
		target := categoryID(text(req.CategoryID))
		if err := requireCategory(state, target); err != nil {
			return nil, err
		}
		tags := []string{}
		if req.Tags != nil {
			tags = distinct(*req.Tags)
		}
		f := Favorite{FavoriteID: uuid.NewString(), UserID: state.UserID, FavoriteType: req.FavoriteType, SourceID: req.SourceID, ConversationID: req.ConversationID, Title: text(req.Title), Content: req.Content, ThumbnailURL: req.ThumbnailURL, CategoryID: target, Tags: tags, Notes: text(req.Notes), Ex: text(req.Ex), CreateTime: now, UpdateTime: now}
		state.Favorites = append(state.Favorites, f)
		return map[string]any{"favoriteInfo": f}, nil
	case "update":
		i := findFavorite(state, req.FavoriteID)
		if i < 0 {
			return nil, missing()
		}
		if req.CategoryID != nil {
			target := categoryID(*req.CategoryID)
			if err := requireCategory(state, target); err != nil {
				return nil, err
			}
			state.Favorites[i].CategoryID = target
		}
		f := &state.Favorites[i]
		if req.Title != nil {
			f.Title = *req.Title
		}
		if req.Notes != nil {
			f.Notes = *req.Notes
		}
		if req.Tags != nil {
			f.Tags = distinct(*req.Tags)
		}
		if req.Ex != nil {
			f.Ex = *req.Ex
		}
		f.UpdateTime = now
		return map[string]any{"favoriteInfo": *f}, nil
	case "remove", "batch_operate":
		if len(req.FavoriteIDs) == 0 {
			return nil, argument("收藏ID列表不能为空")
		}
		indices := map[string]int{}
		for _, id := range req.FavoriteIDs {
			if _, ok := indices[id]; ok {
				return nil, argument("收藏ID重复")
			}
			i := findFavorite(state, id)
			if i < 0 {
				return nil, missing()
			}
			indices[id] = i
		}
		op := req.Operation
		if operation == "remove" {
			op = 4
		}
		if op < 1 || op > 4 {
			return nil, argument("批量操作类型无效")
		}
		target := categoryID(req.TargetCategoryID)
		if op == 1 {
			if err := requireCategory(state, target); err != nil {
				return nil, err
			}
		}
		// Validate every resulting tag set before changing any item.
		if op == 2 {
			for _, i := range indices {
				if err := validTags(distinct(append(append([]string{}, state.Favorites[i].Tags...), req.TargetTags...))); err != nil {
					return nil, err
				}
			}
		}
		if op == 4 {
			kept := make([]Favorite, 0, len(state.Favorites))
			for _, f := range state.Favorites {
				if _, remove := indices[f.FavoriteID]; !remove {
					kept = append(kept, f)
				}
			}
			state.Favorites = kept
		} else {
			for _, i := range indices {
				f := &state.Favorites[i]
				switch op {
				case 1:
					f.CategoryID = target
				case 2:
					f.Tags = distinct(append(f.Tags, req.TargetTags...))
				case 3:
					removed := map[string]bool{}
					for _, tag := range req.TargetTags {
						removed[tag] = true
					}
					kept := []string{}
					for _, tag := range f.Tags {
						if !removed[tag] {
							kept = append(kept, tag)
						}
					}
					f.Tags = kept
				}
				f.UpdateTime = now
			}
		}
		return map[string]any{"success": true, "affected": len(indices)}, nil
	case "category/create":
		name, color, err := categoryFields(state, "", req, true)
		if err != nil {
			return nil, err
		}
		order := len(state.Categories) + 1
		if req.SortOrder != nil {
			order = *req.SortOrder
		}
		c := Category{CategoryID: uuid.NewString(), UserID: state.UserID, CategoryName: name, CategoryColor: color, SortOrder: order, CreateTime: now, UpdateTime: now, Ex: text(req.Ex)}
		state.Categories = append(state.Categories, c)
		return map[string]any{"categoryInfo": c}, nil
	case "category/update":
		id := text(req.CategoryID)
		if id == DefaultCategoryID {
			return nil, argument("默认分类不可修改")
		}
		i := findCategory(state, id)
		if i < 0 {
			return nil, missing()
		}
		name, color, err := categoryFields(state, id, req, false)
		if err != nil {
			return nil, err
		}
		c := &state.Categories[i]
		if req.CategoryName != nil {
			c.CategoryName = name
		}
		if req.CategoryColor != nil {
			c.CategoryColor = color
		}
		if req.SortOrder != nil {
			c.SortOrder = *req.SortOrder
		}
		if req.Ex != nil {
			c.Ex = *req.Ex
		}
		c.UpdateTime = now
		for _, f := range state.Favorites {
			if f.CategoryID == id {
				c.FavoriteCount++
			}
		}
		return map[string]any{"categoryInfo": *c}, nil
	case "category/delete":
		id := text(req.CategoryID)
		if id == DefaultCategoryID {
			return nil, argument("默认分类不可删除")
		}
		i := findCategory(state, id)
		if i < 0 {
			return nil, missing()
		}
		move := req.MoveToDefault == nil || *req.MoveToDefault
		kept := make([]Favorite, 0, len(state.Favorites))
		for _, f := range state.Favorites {
			if f.CategoryID == id {
				if !move {
					continue
				}
				f.CategoryID = DefaultCategoryID
				f.UpdateTime = now
			}
			kept = append(kept, f)
		}
		state.Favorites = kept
		state.Categories = append(state.Categories[:i], state.Categories[i+1:]...)
		return map[string]any{"success": true}, nil
	}
	return nil, argument("未知收藏操作")
}

var colorPattern = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)

func categoryFields(state *State, id string, req Request, create bool) (string, string, error) {
	name := strings.TrimSpace(text(req.CategoryName))
	color := text(req.CategoryColor)
	if create || req.CategoryName != nil {
		if name == "" || len(name) > 128 {
			return "", "", argument("分类名称为空或过长")
		}
		for _, c := range categories(state) {
			if c.CategoryID != id && strings.EqualFold(c.CategoryName, name) {
				return "", "", argument("分类名称已存在")
			}
		}
	}
	if color == "" {
		color = "#2196F3"
	}
	if !colorPattern.MatchString(color) {
		return "", "", argument("分类颜色须为六位十六进制颜色")
	}
	if req.SortOrder != nil && (*req.SortOrder < 0 || *req.SortOrder > 100000) {
		return "", "", argument("分类排序超出范围")
	}
	return name, color, nil
}
