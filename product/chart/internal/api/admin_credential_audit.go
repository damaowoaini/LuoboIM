package api

import (
	"context"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"time"
)

type credentialIssuanceAudit interface {
	Begin(context.Context, string, string, int32) (string, error)
	Finish(context.Context, string, string) error
}
type adminCredentialAudit struct {
	ID         string     `gorm:"column:id;primaryKey;type:varchar(36)"`
	Actor      string     `gorm:"column:actor;type:varchar(64);index"`
	Target     string     `gorm:"column:target;type:varchar(64);index"`
	Platform   int32      `gorm:"column:platform"`
	State      string     `gorm:"column:state;type:varchar(16)"`
	CreatedAt  time.Time  `gorm:"column:created_at;index"`
	FinishedAt *time.Time `gorm:"column:finished_at"`
}

func (adminCredentialAudit) TableName() string { return "admin_credential_issuance_audit" }

type sqlCredentialAudit struct{ db *gorm.DB }

func (a *sqlCredentialAudit) Begin(ctx context.Context, actor, target string, platform int32) (string, error) {
	id := uuid.NewString()
	return id, a.db.WithContext(ctx).Create(&adminCredentialAudit{ID: id, Actor: actor, Target: target, Platform: platform, State: "pending", CreatedAt: time.Now().UTC()}).Error
}
func (a *sqlCredentialAudit) Finish(ctx context.Context, id, state string) error {
	now := time.Now().UTC()
	result := a.db.WithContext(ctx).Model(&adminCredentialAudit{}).Where("id=? AND state=?", id, "pending").Updates(map[string]any{"state": state, "finished_at": now})
	if result.Error == nil && result.RowsAffected != 1 {
		return gorm.ErrRecordNotFound
	}
	return result.Error
}
