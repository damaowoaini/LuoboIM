package accountstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/db/cache"
	imconstant "github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/log"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SessionRevocation struct {
	ID            string     `gorm:"primaryKey;size:64"`
	UserID        string     `gorm:"size:128;not null;index"`
	ChatTokenHash string     `gorm:"size:64;not null"`
	IMTokenHash   string     `gorm:"size:64;not null"`
	Platform      int        `gorm:"not null"`
	RequestedAt   time.Time  `gorm:"not null"`
	CompletedAt   *time.Time `gorm:"index"`
}

func (SessionRevocation) TableName() string { return "session_revocations" }
func SessionDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

type SessionService struct {
	Ownership func(context.Context, string) error
	DB        *gorm.DB
	Tokens    *cache.TokenCacheRedis
	RevokeIM  func(context.Context, string, int, string) error
}

func NewSessionService(db *gorm.DB, rdb redis.UniversalClient, revoke func(context.Context, string, int, string) error) *SessionService {
	return &SessionService{DB: db, Tokens: cache.NewTokenInterface(rdb), RevokeIM: revoke}
}
func (s *SessionService) Request(ctx context.Context, user string, platform int, chatToken, imToken string) (*SessionRevocation, error) {
	if s.Ownership != nil {
		if err := s.Ownership(ctx, user); err != nil {
			return nil, err
		}
	}
	if user == "" || imconstant.PlatformIDToName(platform) == "" || chatToken == "" || imToken == "" {
		return nil, fmt.Errorf("invalid session revocation")
	}
	row := SessionRevocation{ID: SessionDigest(chatToken), UserID: user, ChatTokenHash: SessionDigest(chatToken), IMTokenHash: SessionDigest(imToken), Platform: platform, RequestedAt: time.Now().UTC()}
	if err := s.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return nil, err
	}
	var stored SessionRevocation
	if err := s.DB.WithContext(ctx).First(&stored, "id = ?", row.ID).Error; err != nil {
		return nil, err
	}
	if stored.UserID != row.UserID || stored.ChatTokenHash != row.ChatTokenHash || stored.IMTokenHash != row.IMTokenHash || stored.Platform != row.Platform {
		return nil, fmt.Errorf("session revocation binding mismatch")
	}
	return &stored, nil
}
func (s *SessionService) RevokeChat(ctx context.Context, row *SessionRevocation) error {
	tokens, err := s.Tokens.GetTokensWithoutError(ctx, row.UserID)
	if err != nil {
		return err
	}
	for token := range tokens {
		if SessionDigest(token) == row.ChatTokenHash {
			if err := s.Tokens.DeleteToken(ctx, row.UserID, token); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *SessionService) Process(ctx context.Context, row *SessionRevocation) error {
	if s.Ownership != nil {
		if err := s.Ownership(ctx, row.UserID); err != nil {
			return err
		}
	}
	if operation, _ := ctx.Value(imconstant.OperationID).(string); operation == "" {
		ctx = context.WithValue(ctx, imconstant.OperationID, "session-revoke-"+row.ID)
	}
	if err := s.RevokeChat(ctx, row); err != nil {
		return err
	}
	if row.CompletedAt != nil {
		return nil
	}
	if s.RevokeIM == nil {
		return fmt.Errorf("exact-session IM revocation unavailable")
	}
	if err := s.RevokeIM(ctx, row.UserID, row.Platform, row.IMTokenHash); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Model(&SessionRevocation{}).Where("id = ? AND completed_at IS NULL", row.ID).Update("completed_at", time.Now().UTC()).Error
}
func (s *SessionService) Reconcile(ctx context.Context) error {
	var rows []SessionRevocation
	if err := s.DB.WithContext(ctx).Where("completed_at IS NULL").Order("requested_at ASC").Limit(100).Find(&rows).Error; err != nil {
		return err
	}
	var first error
	for i := range rows {
		if err := s.Process(ctx, &rows[i]); err != nil && first == nil {
			first = err
		}
	}
	return first
}
func (s *SessionService) Run(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 20*time.Second)
		if err := s.Reconcile(attempt); err != nil {
			log.ZWarn(ctx, "Exact-session revocation reconciliation pending", nil)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
