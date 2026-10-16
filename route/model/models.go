package model

import "time"

// ServerConfig 服务器配置
type ServerConfig struct {
	ID           int64     `json:"id" gorm:"primaryKey;autoIncrement"`
	ServerID     string    `json:"server_id" gorm:"uniqueIndex;size:50;not null"`
	ServerName   string    `json:"server_name" gorm:"size:100;not null"`
	Domains      string    `json:"domains" gorm:"type:text;not null"` // 主域名列表（逗号分隔）
	IsDefault    int8      `json:"is_default" gorm:"default:0;index"` // 是否默认服务器
	Status       int8      `json:"status" gorm:"default:1;index"`
	MaxUsers     int       `json:"max_users" gorm:"default:0"`
	CurrentUsers int       `json:"current_users" gorm:"default:0"`
	Description  string    `json:"description" gorm:"size:500"`
	CreatedAt    time.Time `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt    time.Time `json:"updated_at" gorm:"autoUpdateTime"`
}

func (ServerConfig) TableName() string {
	return "server_config"
}

// InviteCode 邀请码
type InviteCode struct {
	ID          int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	Code        string     `json:"code" gorm:"uniqueIndex;size:50;not null"`
	ServerID    string     `json:"server_id" gorm:"index;size:50;not null"`
	MaxUseCount int        `json:"max_use_count" gorm:"default:0"`
	UsedCount   int        `json:"used_count" gorm:"default:0"`
	ExpireTime  *time.Time `json:"expire_time"`
	Status      int8       `json:"status" gorm:"default:1;index"`
	CreatedBy   string     `json:"created_by" gorm:"size:50"`
	Remark      string     `json:"remark" gorm:"size:500"`
	CreatedAt   time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt   time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (InviteCode) TableName() string {
	return "invite_code"
}

// UserServerBinding 用户服务器绑定
type UserServerBinding struct {
	ID             int64      `json:"id" gorm:"primaryKey;autoIncrement"`
	UserID         string     `json:"user_id" gorm:"uniqueIndex;size:50;not null"`
	ServerID       string     `json:"server_id" gorm:"index;size:50;not null"`
	UpstreamUserID string     `json:"-" gorm:"size:100"`
	InviteCode     string     `json:"invite_code" gorm:"index;size:50"`
	BindTime       time.Time  `json:"bind_time" gorm:"autoCreateTime"`
	LastLoginTime  *time.Time `json:"last_login_time"`
	CreatedAt      time.Time  `json:"created_at" gorm:"autoCreateTime"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (UserServerBinding) TableName() string {
	return "user_server_binding"
}

// RouteResponse 路由响应
type RouteResponse struct {
	ServerID   string   `json:"server_id"`
	ServerName string   `json:"server_name"`
	Domains    []string `json:"domains"` // 主域名列表，前端自行拼接路径
}

// RegistrationIntent contains recovery metadata only. Never store password,
// password digest, verification code, authentication token or request body here.
type RegistrationIntent struct {
	RegistrationID string    `json:"registrationID" gorm:"primaryKey;size:36"`
	AccountKey     string    `json:"-" gorm:"uniqueIndex;size:50;not null"`
	ServerID       string    `json:"serverID" gorm:"index;size:50;not null"`
	InviteCode     string    `json:"-" gorm:"size:50"`
	State          string    `json:"state" gorm:"index;size:16;not null"`
	UpstreamUserID string    `json:"-" gorm:"size:100"`
	LeaseUntil     time.Time `json:"-"`
	CreatedAt      time.Time `gorm:"autoCreateTime"`
	UpdatedAt      time.Time `gorm:"autoUpdateTime"`
}

func (RegistrationIntent) TableName() string { return "registration_intent" }

// AccountRouteLock serializes all writers of an identity, including missing rows.
type AccountRouteLock struct {
	AccountKey string `gorm:"primaryKey;size:50"`
}

func (AccountRouteLock) TableName() string { return "account_route_lock" }

type AdminProvisionReceipt struct {
	ServerID    string `gorm:"primaryKey;size:50"`
	ReceiptID   string `gorm:"primaryKey;size:36"`
	UserID      string `gorm:"size:100;not null"`
	ActorUserID string `gorm:"size:100;not null"`
	Fingerprint string `gorm:"size:64;not null"`
	AccountKeys string `gorm:"size:512;not null"`
	State       string `gorm:"size:16;not null"`
	Reason      string `gorm:"size:64"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (AdminProvisionReceipt) TableName() string { return "admin_provision_receipt" }

// One account may have both phone and email bindings, but consumes capacity once.
type AdminProvisionOwner struct {
	ServerID string `gorm:"primaryKey;size:50"`
	UserID   string `gorm:"primaryKey;size:100"`
}

func (AdminProvisionOwner) TableName() string { return "admin_provision_owner" }

type AdminProvisionNonce struct {
	Digest    string    `gorm:"primaryKey;size:64"`
	ExpiresAt time.Time `gorm:"index"`
}

func (AdminProvisionNonce) TableName() string { return "admin_provision_nonce" }
