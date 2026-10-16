package profilesync

import (
	"context"
	table "github.com/OpenIMSDK/chat/pkg/common/db/table/chat"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// Job is written in the same transaction as the canonical chart profile.
// Raw tokens and caller-supplied actor metadata never enter this table.
type Job struct {
	UserID      string     `gorm:"column:user_id;primaryKey;type:char(64);index:idx_profile_pending,priority:2"`
	Generation  int64      `gorm:"column:generation;not null"`
	Nickname    string     `gorm:"column:nickname;type:varchar(64);not null"`
	FaceURL     string     `gorm:"column:face_url;type:varchar(255);not null"`
	RequestedAt time.Time  `gorm:"column:requested_at;not null"`
	CompletedAt *time.Time `gorm:"column:completed_at;index:idx_profile_pending,priority:1"`
}

func (Job) TableName() string { return "profile_sync_jobs" }
func Queue(ctx context.Context, tx *gorm.DB, profile *table.Attribute) error {
	job := Job{UserID: profile.UserID, Generation: 1, Nickname: profile.Nickname, FaceURL: profile.FaceURL, RequestedAt: time.Now().UTC()}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}}, DoUpdates: clause.Assignments(map[string]any{"generation": gorm.Expr("generation + 1"), "nickname": job.Nickname, "face_url": job.FaceURL, "requested_at": job.RequestedAt, "completed_at": nil})}).Create(&job).Error
}

type Projection interface {
	ImAdminTokenWithDefaultAdmin(context.Context) (string, error)
	UpdateUserProfile(context.Context, string, string, string) error
}
type Service struct {
	Ownership func(context.Context, string) error
	DB        *gorm.DB
	IM        Projection
}

func (s *Service) ProcessUser(ctx context.Context, id string) error {
	if s.Ownership != nil {
		if err := s.Ownership(ctx, id); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, constant.OperationID, "profile-sync-"+id)
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var job Job
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", id).Take(&job).Error; err != nil {
			return err
		}
		if job.CompletedAt != nil {
			return nil
		}
		token, err := s.IM.ImAdminTokenWithDefaultAdmin(ctx)
		if err != nil {
			return err
		}
		if err = s.IM.UpdateUserProfile(mctx.WithApiToken(ctx, token), job.UserID, job.Nickname, job.FaceURL); err != nil {
			return err
		}
		result := tx.Model(&Job{}).Where("user_id = ? AND generation = ? AND completed_at IS NULL", job.UserID, job.Generation).Update("completed_at", time.Now().UTC())
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errs.ErrInternalServer.Wrap("profile generation changed")
		}
		return nil
	})
}
func (s *Service) Pending(ctx context.Context, id string) (bool, error) {
	var count int64
	err := s.DB.WithContext(ctx).Model(&Job{}).Where("user_id = ? AND completed_at IS NULL", id).Count(&count).Error
	return count > 0, err
}
func (s *Service) Reconcile(ctx context.Context) error {
	after := ""
	var first error
	for {
		var jobs []Job
		if err := s.DB.WithContext(ctx).Where("user_id > ? AND completed_at IS NULL", after).Order("user_id").Limit(100).Find(&jobs).Error; err != nil {
			return err
		}
		for _, job := range jobs {
			if err := s.ProcessUser(ctx, job.UserID); err != nil && first == nil {
				first = err
			}
			after = job.UserID
		}
		if len(jobs) < 100 {
			return first
		}
	}
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		iteration, cancel := context.WithTimeout(ctx, 45*time.Second)
		if err := s.Reconcile(iteration); err != nil {
			log.ZWarn(ctx, "Profile synchronization pending", nil)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
