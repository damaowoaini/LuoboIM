package service

import (
	"errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"openim-route-service/db"
	"openim-route-service/model"
	"strings"
	"time"
)

type RouteService struct{}

func NewRouteService() *RouteService { return &RouteService{} }
func (s *RouteService) GetServerByInviteCode(code string) (*model.RouteResponse, error) {
	var server model.ServerConfig
	if code == "" {
		if err := db.DB.Where("is_default = 1 AND status = 1").First(&server).Error; err != nil {
			return nil, errors.New("默认服务器暂时不可用")
		}
	} else {
		var invite model.InviteCode
		if db.DB.Where("code = ? AND status = 1", code).First(&invite).Error != nil {
			return nil, errors.New("邀请码不存在或不可用")
		}
		if invite.ExpireTime != nil && !invite.ExpireTime.After(time.Now()) {
			return nil, errors.New("邀请码已过期")
		}
		if invite.MaxUseCount > 0 && invite.UsedCount >= invite.MaxUseCount {
			return nil, errors.New("邀请码已达到最大使用次数")
		}
		if db.DB.Where("server_id = ? AND status = 1", invite.ServerID).First(&server).Error != nil {
			return nil, errors.New("服务器暂时不可用")
		}
	}
	if server.MaxUsers > 0 && server.CurrentUsers >= server.MaxUsers {
		return nil, errors.New("服务器已达到最大用户数")
	}
	return routeResponse(server)
}
func (s *RouteService) GetServerByUserID(key string) (*model.RouteResponse, error) {
	var binding model.UserServerBinding
	if err := db.DB.Where("user_id = ?", key).First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccountNotBound
		}
		return nil, errors.New("账号路由暂时不可用")
	}
	var server model.ServerConfig
	if db.DB.Where("server_id = ? AND status = 1", binding.ServerID).First(&server).Error != nil {
		return nil, errors.New("服务器暂时不可用")
	}
	return routeResponse(server)
}
func routeResponse(server model.ServerConfig) (*model.RouteResponse, error) {
	domains := parseDomains(server.Domains)
	if len(domains) == 0 {
		return nil, errors.New("服务器配置没有有效HTTPS域名")
	}
	return &model.RouteResponse{ServerID: server.ServerID, ServerName: server.ServerName, Domains: domains}, nil
}
func (s *RouteService) BindUserToServer(key, serverID, code string) error {
	if key == "" || serverID == "" || len(key) > 50 || len(serverID) > 50 {
		return errors.New("绑定参数错误")
	}
	return db.DB.Transaction(func(tx *gorm.DB) error {
		// Lock server first so capacity and invite use counts cannot race.
		var server model.ServerConfig
		if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("server_id = ? AND status = 1", serverID).First(&server).Error != nil {
			return errors.New("服务器不存在或不可用")
		}
		if err := lockAccountKeys(tx, []string{key}); err != nil {
			return err
		}
		var binding model.UserServerBinding
		err := tx.Where("user_id = ?", key).First(&binding).Error
		if err == nil {
			if binding.ServerID == serverID {
				return nil
			}
			return errors.New("账号已绑定其他服务器")
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("查询绑定失败")
		}
		var pending int64
		if err := tx.Model(&model.RegistrationIntent{}).Where("account_key = ? AND state <> ?", key, "bound").Count(&pending).Error; err != nil {
			return errors.New("查询注册恢复记录失败")
		}
		if pending > 0 {
			return errors.New("账号有待恢复注册，请通过原注册请求完成绑定")
		}
		if server.MaxUsers > 0 && server.CurrentUsers >= server.MaxUsers {
			return errors.New("服务器已达到最大用户数")
		}
		var invite model.InviteCode
		if code != "" {
			if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ? AND status = 1", code).First(&invite).Error != nil {
				return errors.New("邀请码不可用")
			}
			if invite.ServerID != serverID {
				return errors.New("邀请码与目标服务器不一致")
			}
			if invite.ExpireTime != nil && !invite.ExpireTime.After(time.Now()) {
				return errors.New("邀请码已过期")
			}
			if invite.MaxUseCount > 0 && invite.UsedCount >= invite.MaxUseCount {
				return errors.New("邀请码已达到最大使用次数")
			}
		}
		binding = model.UserServerBinding{UserID: key, ServerID: serverID, InviteCode: code, BindTime: time.Now()}
		if tx.Create(&binding).Error != nil {
			return errors.New("创建绑定失败")
		}
		if code != "" {
			if tx.Model(&invite).UpdateColumn("used_count", gorm.Expr("used_count + 1")).Error != nil {
				return errors.New("更新邀请码计数失败")
			}
		}
		if tx.Model(&server).UpdateColumn("current_users", gorm.Expr("current_users + 1")).Error != nil {
			return errors.New("更新服务器计数失败")
		}
		return nil
	})
}
func (s *RouteService) GetAllServers() ([]model.ServerConfig, error) {
	var servers []model.ServerConfig
	if db.DB.Where("status = 1").Find(&servers).Error != nil {
		return nil, errors.New("服务器列表暂时不可用")
	}
	return servers, nil
}
func parseDomains(value string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, item := range strings.Split(value, ",") {
		domain := stripProtocol(strings.TrimSpace(item))
		if _, err := domainURL(domain, ""); err == nil && !seen[domain] {
			result = append(result, domain)
			seen[domain] = true
		}
	}
	return result
}
func stripProtocol(value string) string {
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	return strings.TrimSuffix(value, "/")
}
