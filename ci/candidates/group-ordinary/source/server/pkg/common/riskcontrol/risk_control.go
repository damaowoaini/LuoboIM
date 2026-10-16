package riskcontrol

import (
	"context"
	"strings"
	"time"
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

func GetConfigMap(ctx context.Context) (map[string]string, error) {
	if ctx == nil {
		return nil, errRiskDatabaseUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, riskReadBudget)
	defer cancel()
	db, err := riskDatabase.get(ctx)
	if err != nil {
		return nil, err
	}
	var rows []*ClientConfig
	if err := db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, riskDatabaseError(ctx, err)
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
	return evaluateActionPolicy(conf, actionKey, time.Now())
}
