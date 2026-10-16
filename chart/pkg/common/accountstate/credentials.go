package accountstate

import (
	"context"
	"github.com/OpenIMSDK/chat/pkg/common/config"
	"strconv"
	"time"

	"github.com/OpenIMSDK/tools/errs"
	"github.com/OpenIMSDK/tools/log"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CredentialRevocation is an outbox written in the same SQL transaction as a
// password change. Generation prevents an old worker completing a newer job.
type CredentialRevocation struct {
	UserID      string     `gorm:"column:user_id;primaryKey;type:varchar(64)"`
	Generation  int64      `gorm:"column:generation;not null"`
	RequestedAt time.Time  `gorm:"column:requested_at;not null"`
	CompletedAt *time.Time `gorm:"column:completed_at"`
}

func (CredentialRevocation) TableName() string { return "credential_revocations" }

func QueueCredentialRevocation(ctx context.Context, tx *gorm.DB, userID string) error {
	job := &CredentialRevocation{UserID: userID, Generation: 1, RequestedAt: time.Now().UTC()}
	return errs.Wrap(tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{"generation": gorm.Expr("generation + 1"), "requested_at": job.RequestedAt, "completed_at": nil}),
	}).Create(job).Error)
}

func HasPendingCredentials(ctx context.Context, db *gorm.DB, userID string) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Model(&CredentialRevocation{}).Where("user_id = ? AND completed_at IS NULL", userID).Count(&count).Error
	return count != 0, errs.Wrap(err)
}

type CredentialStore interface {
	Get(context.Context, string) (*CredentialRevocation, error)
	ListAfter(context.Context, string, int) ([]*CredentialRevocation, error)
	Complete(context.Context, string, int64) (bool, error)
}
type SQLCredentialStore struct{ db *gorm.DB }

func NewSQLCredentialStore(db *gorm.DB) *SQLCredentialStore { return &SQLCredentialStore{db: db} }
func (s *SQLCredentialStore) Get(ctx context.Context, id string) (*CredentialRevocation, error) {
	var job CredentialRevocation
	return &job, errs.Wrap(s.db.WithContext(ctx).Where("user_id = ?", id).Take(&job).Error)
}
func (s *SQLCredentialStore) ListAfter(ctx context.Context, after string, limit int) ([]*CredentialRevocation, error) {
	var jobs []*CredentialRevocation
	err := s.db.WithContext(ctx).Where("user_id > ?", after).Order("user_id").Limit(limit).Find(&jobs).Error
	return jobs, errs.Wrap(err)
}
func (s *SQLCredentialStore) Complete(ctx context.Context, id string, generation int64) (bool, error) {
	result := s.db.WithContext(ctx).Model(&CredentialRevocation{}).
		Where("user_id = ? AND generation = ? AND completed_at IS NULL", id, generation).
		Update("completed_at", time.Now().UTC())
	if result.Error != nil {
		return false, errs.Wrap(result.Error)
	}
	if result.RowsAffected == 1 {
		return true, nil
	}
	job, err := s.Get(ctx, id)
	return err == nil && job.Generation == generation && job.CompletedAt != nil, err
}

type CredentialSessions interface {
	SessionRevoker
	DisableForCredentialChange(context.Context, string, int64) error
	ClearCredentialGate(context.Context, string, int64) error
}
type CredentialService struct {
	Ownership func(context.Context, string) error
	Store     CredentialStore
	Sessions  CredentialSessions
}

func (s *CredentialService) ProcessUser(ctx context.Context, id string) error {
	job, err := s.Store.Get(ctx, id)
	if err != nil {
		return err
	}
	return s.process(ctx, job)
}
func (s *CredentialService) process(ctx context.Context, job *CredentialRevocation) error {
	if s.Ownership != nil {
		if err := s.Ownership(ctx, job.UserID); err != nil {
			return err
		}
	}
	if job.CompletedAt != nil {
		return s.Sessions.ClearCredentialGate(ctx, job.UserID, job.Generation)
	}
	if err := s.Sessions.DisableForCredentialChange(ctx, job.UserID, job.Generation); err != nil {
		return err
	}
	if err := s.Sessions.Revoke(ctx, job.UserID); err != nil {
		return err
	}
	completed, err := s.Store.Complete(ctx, job.UserID, job.Generation)
	if err != nil {
		return err
	}
	if !completed {
		return nil
	}
	return s.Sessions.ClearCredentialGate(ctx, job.UserID, job.Generation)
}
func (s *CredentialService) Reconcile(ctx context.Context) error {
	after := ""
	var firstErr error
	for {
		jobs, err := s.Store.ListAfter(ctx, after, 100)
		if err != nil {
			return err
		}
		for _, job := range jobs {
			if err := s.process(ctx, job); err != nil && firstErr == nil {
				firstErr = err
			}
			after = job.UserID
		}
		if len(jobs) < 100 {
			return firstErr
		}
	}
}
func (s *CredentialService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 45*time.Second)
		err := s.Reconcile(attempt)
		cancel()
		if err != nil {
			log.ZWarn(ctx, "credential session revocation pending", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *RedisSessions) DisableForCredentialChange(ctx context.Context, id string, generation int64) error {
	// A temporary password gate must never replace a permanent cancellation.
	for _, userID := range credentialIdentities(id) {
		if err := s.redis.Eval(ctx, `local v=redis.call('GET',KEYS[1]); if not v or (string.sub(v,1,15)=='password_reset:' and tonumber(string.sub(v,16))<=tonumber(ARGV[2])) then redis.call('SET',KEYS[1],ARGV[1]); end; return 1`,
			[]string{DisabledKeyPrefix + userID}, "password_reset:"+strconv.FormatInt(generation, 10), generation).Err(); err != nil {
			return errs.Wrap(err)
		}
	}
	return nil
}
func (s *RedisSessions) ClearCredentialGate(ctx context.Context, id string, generation int64) error {
	for _, userID := range credentialIdentities(id) {
		if err := s.redis.Eval(ctx, `if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]); end; return 0`,
			[]string{DisabledKeyPrefix + userID}, "password_reset:"+strconv.FormatInt(generation, 10)).Err(); err != nil {
			return errs.Wrap(err)
		}
	}
	return nil
}

func credentialIdentities(id string) []string {
	identities := []string{id}
	if imID := config.GetIMAdmin(id); imID != "" && imID != id {
		identities = append(identities, imID)
	}
	return identities
}
