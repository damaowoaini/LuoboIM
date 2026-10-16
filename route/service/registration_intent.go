package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"openim-route-service/db"
	"openim-route-service/model"
)

var ErrRegistrationPending = errors.New("注册正在确认，请稍后重试；账号创建成功后会自动恢复绑定")
var ErrRegistrationRecovered = errors.New("账号注册和路由已恢复，请返回登录")

type RegistrationAttempt struct {
	Intent model.RegistrationIntent
	Server *model.RouteResponse
	Fresh  bool
}
type RegistrationStore interface {
	Prepare(context.Context, string, string) (*RegistrationAttempt, error)
	Confirm(context.Context, string, string) error
	Complete(context.Context, string) error
	Abort(context.Context, string) error
}
type SQLRegistrationStore struct{}

func registrationID() (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", errors.New("无法创建注册请求")
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	value := hex.EncodeToString(id[:])
	return value[:8] + "-" + value[8:12] + "-" + value[12:16] + "-" + value[16:20] + "-" + value[20:], nil
}
func intentByID(tx *gorm.DB, id string) (model.RegistrationIntent, error) {
	var v model.RegistrationIntent
	err := tx.Where("registration_id = ?", id).First(&v).Error
	return v, err
}
func lockIntentServer(tx *gorm.DB, id string) (model.RegistrationIntent, model.ServerConfig, error) {
	first, err := intentByID(tx, id)
	if err != nil {
		return first, model.ServerConfig{}, err
	}
	var server model.ServerConfig
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("server_id = ?", first.ServerID).First(&server).Error; err != nil {
		return first, server, err
	}
	var current model.RegistrationIntent
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("registration_id = ?", id).First(&current).Error
	return current, server, err
}
func (s *SQLRegistrationStore) Prepare(ctx context.Context, key, code string) (*RegistrationAttempt, error) {
	// Existing intents keep their original tenant even if defaults or capacity change.
	var existing model.RegistrationIntent
	err := db.DB.WithContext(ctx).Where("account_key = ?", key).First(&existing).Error
	var server *model.RouteResponse
	if err == nil {
		var row model.ServerConfig
		if existing.InviteCode != code {
			return nil, errors.New("已有注册请求，请使用原邀请码继续")
		}
		if db.DB.WithContext(ctx).Where("server_id = ? AND status = 1", existing.ServerID).First(&row).Error != nil {
			return nil, errors.New("原注册服务器暂时不可用")
		}
		server, err = routeResponse(row)
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		server, err = NewRouteService().GetServerByInviteCode(code)
	}
	if err != nil {
		return nil, errors.New("注册路由暂时不可用，请稍后重试")
	}
	var result RegistrationAttempt
	err = db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var target model.ServerConfig
		if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("server_id = ? AND status = 1", server.ServerID).First(&target).Error != nil {
			return errors.New("注册服务器暂时不可用")
		}
		if err := lockAccountKeys(tx, []string{key}); err != nil {
			return err
		}
		var intent model.RegistrationIntent
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("account_key = ?", key).First(&intent).Error
		if err == nil {
			if intent.ServerID != target.ServerID || intent.InviteCode != code {
				return errors.New("账号已有其他注册请求")
			}
			if intent.State == "bound" {
				return ErrRegistrationRecovered
			}
			if intent.State != "confirmed" && intent.LeaseUntil.After(time.Now()) {
				return ErrRegistrationPending
			}
			if err := tx.Model(&intent).Update("lease_until", time.Now().Add(60*time.Second)).Error; err != nil {
				return err
			}
			result = RegistrationAttempt{Intent: intent, Server: server}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var bound int64
		if err := tx.Model(&model.UserServerBinding{}).Where("user_id = ?", key).Count(&bound).Error; err != nil {
			return err
		}
		if bound > 0 {
			return errors.New("无法完成该账户的注册操作")
		}
		if target.MaxUsers > 0 && target.CurrentUsers >= target.MaxUsers {
			return errors.New("服务器已达到最大用户数")
		}
		var invite model.InviteCode
		if code != "" {
			if tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ? AND status = 1", code).First(&invite).Error != nil {
				return errors.New("邀请码不可用")
			}
			if invite.ServerID != target.ServerID || (invite.ExpireTime != nil && !invite.ExpireTime.After(time.Now())) || (invite.MaxUseCount > 0 && invite.UsedCount >= invite.MaxUseCount) {
				return errors.New("邀请码不可用或已达到上限")
			}
		}
		id, err := registrationID()
		if err != nil {
			return err
		}
		intent = model.RegistrationIntent{RegistrationID: id, AccountKey: key, ServerID: target.ServerID, InviteCode: code, State: "reserved", LeaseUntil: time.Now().Add(60 * time.Second)}
		if tx.Create(&intent).Error != nil {
			return errors.New("无法保存注册恢复记录")
		}
		// Reservation and capacity are committed together before any external effect.
		if code != "" {
			if err := tx.Model(&invite).UpdateColumn("used_count", gorm.Expr("used_count + 1")).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&target).UpdateColumn("current_users", gorm.Expr("current_users + 1")).Error; err != nil {
			return err
		}
		result = RegistrationAttempt{Intent: intent, Server: server, Fresh: true}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}
func (s *SQLRegistrationStore) Confirm(ctx context.Context, id, userID string) error {
	if userID == "" {
		return errors.New("注册回执缺少用户身份")
	}
	return db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		v, _, err := lockIntentServer(tx, id)
		if err != nil {
			return err
		}
		if v.UpstreamUserID != "" && v.UpstreamUserID != userID {
			return errors.New("注册回执身份冲突")
		}
		if v.State == "bound" {
			return nil
		}
		return tx.Model(&v).Updates(map[string]any{"state": "confirmed", "upstream_user_id": userID}).Error
	})
}
func (s *SQLRegistrationStore) Complete(ctx context.Context, id string) error {
	return db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		v, _, err := lockIntentServer(tx, id)
		if err != nil {
			return err
		}
		if v.State == "bound" {
			return nil
		}
		if v.State != "confirmed" || v.UpstreamUserID == "" {
			return ErrRegistrationPending
		}
		var binding model.UserServerBinding
		err = tx.Where("user_id = ?", v.AccountKey).First(&binding).Error
		if err == nil {
			if binding.ServerID != v.ServerID {
				return errors.New("账号路由归属冲突，请联系管理员")
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			if tx.Create(&model.UserServerBinding{UserID: v.AccountKey, ServerID: v.ServerID, UpstreamUserID: v.UpstreamUserID, InviteCode: v.InviteCode, BindTime: time.Now()}).Error != nil {
				return errors.New("保存账号路由失败")
			}
		} else {
			return err
		}
		// Counters were already reserved. Repeating completion never increments them.
		return tx.Model(&v).Updates(map[string]any{"state": "bound", "lease_until": time.Now()}).Error
	})
}
func (s *SQLRegistrationStore) Abort(ctx context.Context, id string) error {
	return db.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		v, server, err := lockIntentServer(tx, id)
		if err != nil {
			return err
		}
		if v.State != "reserved" {
			return ErrRegistrationPending
		}
		if v.InviteCode != "" {
			if err := tx.Model(&model.InviteCode{}).Where("code = ? AND used_count > 0", v.InviteCode).UpdateColumn("used_count", gorm.Expr("used_count - 1")).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&server).Where("current_users > 0").UpdateColumn("current_users", gorm.Expr("current_users - 1")).Error; err != nil {
			return err
		}
		return tx.Delete(&v).Error
	})
}
