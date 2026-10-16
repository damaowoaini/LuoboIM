package riskcontrol

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	gormMysql "gorm.io/driver/mysql"
	"gorm.io/gorm"

	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

const (
	RiskControlEnabledKey     = "risk_control_enabled"
	RiskControlStartKey       = "risk_control_start"
	RiskControlEndKey         = "risk_control_end"
	RiskDisableGroupActionKey = "risk_disable_group_action"
	RiskDisableAddFriendKey   = "risk_disable_add_friend"
	RiskDisableGroupMsgKey    = "risk_disable_group_message"
	RiskDisablePrivateMsgKey  = "risk_disable_private_message"
	RiskDisableUpdateUserKey  = "risk_disable_update_user_info"
)

type ClientConfig struct {
	Key   string `gorm:"column:key;primaryKey;type:varchar(255)"`
	Value string `gorm:"column:value;not null;type:text"`
}

func (ClientConfig) TableName() string {
	return "client_config"
}

var (
	dbOnce sync.Once
	dbInst *gorm.DB
	dbErr  error
)

func getDB() (*gorm.DB, error) {
	dbOnce.Do(func() {
		conf := config.Config.Mysql
		if conf == nil || len(conf.Address) == 0 {
			dbErr = fmt.Errorf("mysql config is empty")
			return
		}
		dsn := fmt.Sprintf("%s:%s@tcp(%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
			conf.Username, conf.Password, conf.Address[0], conf.Database)
		dbInst, dbErr = gorm.Open(gormMysql.Open(dsn), &gorm.Config{})
		if dbErr != nil {
			return
		}
		sqlDB, err := dbInst.DB()
		if err != nil {
			dbErr = err
			return
		}
		if conf.MaxOpenConn > 0 {
			sqlDB.SetMaxOpenConns(conf.MaxOpenConn)
		}
		if conf.MaxIdleConn > 0 {
			sqlDB.SetMaxIdleConns(conf.MaxIdleConn)
		}
		if conf.MaxLifeTime > 0 {
			sqlDB.SetConnMaxLifetime(time.Duration(conf.MaxLifeTime) * time.Second)
		}
	})
	return dbInst, dbErr
}

func GetConfigMap(ctx context.Context) (map[string]string, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	var rows []*ClientConfig
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make(map[string]string, len(rows))
	for _, row := range rows {
		result[row.Key] = row.Value
	}
	return result, nil
}

func ParseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func InDailyWindow(start string, end string, now time.Time) bool {
	startTime, err := time.Parse("15:04", strings.TrimSpace(start))
	if err != nil {
		return false
	}
	endTime, err := time.Parse("15:04", strings.TrimSpace(end))
	if err != nil {
		return false
	}
	currentMinutes := now.Hour()*60 + now.Minute()
	startMinutes := startTime.Hour()*60 + startTime.Minute()
	endMinutes := endTime.Hour()*60 + endTime.Minute()
	if startMinutes == endMinutes {
		return true
	}
	if startMinutes < endMinutes {
		return currentMinutes >= startMinutes && currentMinutes < endMinutes
	}
	return currentMinutes >= startMinutes || currentMinutes < endMinutes
}

func IsActionBlocked(ctx context.Context, actionKey string) (bool, error) {
	conf, err := GetConfigMap(ctx)
	if err != nil {
		return false, err
	}
	if !ParseBool(conf[RiskControlEnabledKey]) {
		return false, nil
	}
	if !ParseBool(conf[actionKey]) {
		return false, nil
	}
	return InDailyWindow(conf[RiskControlStartKey], conf[RiskControlEndKey], time.Now()), nil
}
