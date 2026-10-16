// Package accountstate keeps account cancellation separate from administrative
// bans. It retains the original account data until an explicit retention policy
// permits erasure, and durably retries session revocation after partial failure.
package accountstate

import (
	"context"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/apicall"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"github.com/OpenIMSDK/chat/pkg/common/mctx"
	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// DisabledKeyPrefix is shared with server/pkg/common/db/cache/msg.go. Deploy
// chart and IM against the same tenant-specific Redis database.
const DisabledKeyPrefix = "ACCOUNT_DISABLED:"

type Cancellation struct {
	UserID            string     `gorm:"column:user_id;primaryKey;type:varchar(64)" json:"userID"`
	RequestedBy       string     `gorm:"column:requested_by;type:varchar(64)" json:"-"`
	RequestedAt       time.Time  `gorm:"column:requested_at;not null" json:"requestedAt"`
	SessionsRevokedAt *time.Time `gorm:"column:sessions_revoked_at" json:"sessionsRevokedAt,omitempty"`
}

func (Cancellation) TableName() string { return "account_cancellations" }

type Store interface {
	Request(context.Context, string, string) (*Cancellation, error)
	Complete(context.Context, string, time.Time) error
	ListAfter(context.Context, string, int) ([]*Cancellation, error)
}

type SQLStore struct{ db *gorm.DB }

func NewSQLStore(db *gorm.DB) *SQLStore { return &SQLStore{db: db} }

func IsCancelled(ctx context.Context, db *gorm.DB, userID string) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Model(&Cancellation{}).Where("user_id = ?", userID).Count(&count).Error
	return count != 0, errs.Wrap(err)
}

func (s *SQLStore) Request(ctx context.Context, userID, actor string) (*Cancellation, error) {
	record := &Cancellation{UserID: userID, RequestedBy: actor, RequestedAt: time.Now().UTC()}
	err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(record).Error
	if err != nil {
		return nil, errs.Wrap(err)
	}
	err = s.db.WithContext(ctx).Where("user_id = ?", userID).Take(record).Error
	return record, errs.Wrap(err)
}

func (s *SQLStore) Complete(ctx context.Context, userID string, at time.Time) error {
	return errs.Wrap(s.db.WithContext(ctx).Model(&Cancellation{}).
		Where("user_id = ? AND sessions_revoked_at IS NULL", userID).
		Update("sessions_revoked_at", at).Error)
}

func (s *SQLStore) ListAfter(ctx context.Context, after string, limit int) ([]*Cancellation, error) {
	var records []*Cancellation
	err := s.db.WithContext(ctx).Where("user_id > ?", after).Order("user_id").Limit(limit).Find(&records).Error
	return records, errs.Wrap(err)
}

type SessionRevoker interface {
	Disable(context.Context, string) error
	Revoke(context.Context, string) error
}

type Service struct {
	Ownership func(context.Context, string) error
	Store     Store
	Sessions  SessionRevoker
}

// Request persists the login prohibition before any external side effect.
// A nonnil record with an error means accepted and pending cleanup, not erased.
func (s *Service) Request(ctx context.Context, userID, actor string) (*Cancellation, error) {
	if s.Ownership != nil {
		if err := s.Ownership(ctx, userID); err != nil {
			return nil, err
		}
	}
	if userID == "" || actor == "" {
		return nil, errs.ErrArgs.Wrap("用户身份不能为空")
	}
	record, err := s.Store.Request(ctx, userID, actor)
	if err != nil {
		return nil, err
	}
	return record, s.process(ctx, record)
}

func (s *Service) process(ctx context.Context, record *Cancellation) error {
	if s.Ownership != nil {
		if err := s.Ownership(ctx, record.UserID); err != nil {
			return err
		}
	}
	if err := s.Sessions.Disable(ctx, record.UserID); err != nil {
		return err
	}
	if record.SessionsRevokedAt != nil {
		return nil
	}
	if err := s.Sessions.Revoke(ctx, record.UserID); err != nil {
		return err
	}
	at := time.Now().UTC()
	if err := s.Store.Complete(ctx, record.UserID, at); err != nil {
		return err
	}
	record.SessionsRevokedAt = &at
	return nil
}

// Reconcile also restores durable login prohibitions after Redis data loss.
func (s *Service) Reconcile(ctx context.Context) error {
	after := ""
	var firstErr error
	for {
		records, err := s.Store.ListAfter(ctx, after, 100)
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := s.process(ctx, record); err != nil && firstErr == nil {
				firstErr = err
			}
			after = record.UserID
		}
		if len(records) < 100 {
			return firstErr
		}
	}
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.Reconcile(attempt)
		cancel()
		if err != nil {
			log.ZWarn(ctx, "account cancellation reconciliation pending", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type RedisSessions struct {
	redis  redis.UniversalClient
	caller apicall.CallerInterface
}

func NewRedisSessions(rdb redis.UniversalClient, caller apicall.CallerInterface) *RedisSessions {
	return &RedisSessions{redis: rdb, caller: caller}
}

func (s *RedisSessions) Disable(ctx context.Context, userID string) error {
	return errs.Wrap(s.redis.Set(ctx, DisabledKeyPrefix+userID, "cancelled", 0).Err())
}

func (s *RedisSessions) Revoke(ctx context.Context, userID string) error {
	if operationID, _ := ctx.Value(constant.OperationID).(string); operationID == "" {
		ctx = context.WithValue(ctx, constant.OperationID, "credential-revoke-"+uuid.NewString())
	}
	if err := s.redis.Del(ctx, "CHAT_UID_TOKEN_STATUS:"+userID).Err(); err != nil {
		return errs.Wrap(err)
	}
	token, err := s.caller.ImAdminTokenWithDefaultAdmin(ctx)
	if err != nil {
		return err
	}
	imUserID := config.GetIMAdmin(userID)
	if imUserID == "" {
		imUserID = userID
	}
	return s.caller.ForceOffLine(mctx.WithApiToken(ctx, token), imUserID)
}
