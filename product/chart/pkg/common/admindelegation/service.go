// Package admindelegation issues browser capabilities bound to a live chart
// administrator session. It never calls IM and never stores raw credentials.
package admindelegation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	admintable "github.com/OpenIMSDK/chat/pkg/common/db/table/admin"
	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const Purpose = "luobo-admin-delegation-v1"
const Audience = "luobo-admin-http-v1"

type Binding struct {
	Version     int    `json:"version"`
	AdminID     string `json:"adminID"`
	SessionHash string `json:"sessionHash"`
}
type Claims struct {
	UserID     string
	PlatformID int32
	jwt.RegisteredClaims
	Purpose    string   `json:"purpose"`
	Delegation *Binding `json:"delegation"`
}
type sessionClaims struct {
	UserID     string
	UserType   int32
	PlatformID int32
	jwt.RegisteredClaims
}
type Record struct {
	TokenHash   string    `gorm:"column:token_hash;type:char(64);primaryKey"`
	AdminID     string    `gorm:"column:admin_id;type:varchar(64);not null;index"`
	SessionHash string    `gorm:"column:session_hash;type:char(64);not null;index"`
	Issuer      string    `gorm:"column:issuer;type:varchar(128);not null"`
	Identity    string    `gorm:"column:identity;type:varchar(64);not null"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	ExpiresAt   time.Time `gorm:"column:expires_at;not null;index"`
}

func (Record) TableName() string { return "admin_im_delegations" }
func Hash(s string) string       { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }

type Service struct {
	DB     *gorm.DB
	Redis  redis.UniversalClient
	Secret []byte
	Issuer string
}

func (s *Service) configured() bool {
	return s.DB != nil && s.Redis != nil && len(s.Secret) >= 32 && s.Issuer != "" && len(s.Issuer) <= 128
}
func (s *Service) session(raw string) (*sessionClaims, error) {
	if raw == "" || len(raw) > 16384 {
		return nil, errors.New("invalid administrator session")
	}
	c := &sessionClaims{}
	p, err := jwt.ParseWithClaims(raw, c, func(*jwt.Token) (any, error) { return s.Secret, nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || p == nil || !p.Valid || c.UserType != 2 || c.PlatformID != 0 || c.UserID == "" || c.ExpiresAt == nil {
		return nil, errors.New("invalid administrator session")
	}
	return c, nil
}
func (s *Service) liveAdmin(ctx context.Context, id string) (int32, error) {
	var admins []admintable.Admin
	if err := s.DB.WithContext(ctx).Select("user_id", "level", "disabled", "google_auth_enabled", "google_auth_secret").Where("user_id=?", id).Limit(2).Find(&admins).Error; err != nil {
		return 0, err
	}
	if len(admins) != 1 || admins[0].Disabled || !admins[0].GoogleAuthEnabled || admins[0].GoogleAuthSecret == "" || admins[0].Level < 1 || admins[0].Level > 100 {
		return 0, errors.New("administrator unavailable")
	}
	return admins[0].Level, nil
}

// AuthorizeSession checks an existing chart administrator session directly.
// Recovery and provisioning queries must not rely only on stale type metadata.
func (s *Service) AuthorizeSession(ctx context.Context, raw string) (string, int32, error) {
	if !s.configured() {
		return "", 0, errors.New("administrator session unavailable")
	}
	claims, err := s.session(raw)
	if err != nil {
		return "", 0, err
	}
	level, err := s.liveAdmin(ctx, claims.UserID)
	if err != nil {
		return "", 0, err
	}
	status, err := s.Redis.HGet(ctx, "CHAT_UID_TOKEN_STATUS:"+claims.UserID, raw).Result()
	if err != nil || status != "0" {
		return "", 0, errors.New("administrator session revoked")
	}
	return claims.UserID, level, nil
}
func (s *Service) Issue(ctx context.Context, adminID, session, identity string) (string, error) {
	if !s.configured() || identity == "" {
		return "", errors.New("administrator delegation is not configured")
	}
	c, err := s.session(session)
	if err != nil || c.UserID != adminID {
		return "", errors.New("administrator session mismatch")
	}
	if _, err = s.liveAdmin(ctx, adminID); err != nil {
		return "", err
	}
	status, err := s.Redis.HGet(ctx, "CHAT_UID_TOKEN_STATUS:"+adminID, session).Result()
	if err != nil || status != "0" {
		return "", errors.New("administrator session is revoked")
	}
	now := time.Now().UTC()
	claims := Claims{UserID: identity, PlatformID: 10, RegisteredClaims: jwt.RegisteredClaims{Issuer: s.Issuer, Subject: adminID, Audience: jwt.ClaimStrings{Audience}, ID: uuid.NewString(), IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now.Add(-time.Second * 30)), ExpiresAt: c.ExpiresAt}, Purpose: Purpose, Delegation: &Binding{Version: 1, AdminID: adminID, SessionHash: Hash(session)}}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.Secret)
	if err != nil {
		return "", err
	}
	record := Record{TokenHash: Hash(raw), AdminID: adminID, SessionHash: Hash(session), Identity: identity, Issuer: s.Issuer, CreatedAt: now, ExpiresAt: c.ExpiresAt.Time}
	if err = s.DB.WithContext(ctx).Create(&record).Error; err != nil {
		return "", err
	}
	return raw, nil
}
func (s *Service) Validate(ctx context.Context, raw string) (string, int32, error) {
	deny := errors.New("delegated administrator session is unavailable")
	if !s.configured() || len(raw) > 16384 {
		return "", 0, deny
	}
	c := &Claims{}
	p, err := jwt.ParseWithClaims(raw, c, func(*jwt.Token) (any, error) { return s.Secret, nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || p == nil || !p.Valid || c.Purpose != Purpose || c.PlatformID != 10 || c.Issuer != s.Issuer || c.ExpiresAt == nil || c.ID == "" || !c.VerifyAudience(Audience, true) || c.Delegation == nil || c.Delegation.Version != 1 || c.Delegation.AdminID == "" || c.Subject != c.Delegation.AdminID || len(c.Delegation.SessionHash) != 64 {
		return "", 0, deny
	}
	var r Record
	if err = s.DB.WithContext(ctx).Where("token_hash=?", Hash(raw)).Take(&r).Error; err != nil {
		return "", 0, deny
	}
	if r.AdminID != c.Delegation.AdminID || r.SessionHash != c.Delegation.SessionHash || r.Identity != c.UserID || r.Issuer != s.Issuer || !r.ExpiresAt.After(time.Now()) {
		return "", 0, deny
	}
	level, err := s.liveAdmin(ctx, r.AdminID)
	if err != nil {
		return "", 0, deny
	}
	sessions, err := s.Redis.HGetAll(ctx, "CHAT_UID_TOKEN_STATUS:"+r.AdminID).Result()
	if err != nil {
		return "", 0, deny
	}
	for token, status := range sessions {
		if status == "0" && Hash(token) == r.SessionHash {
			if sc, e := s.session(token); e == nil && sc.UserID == r.AdminID {
				return r.AdminID, level, nil
			}
		}
	}
	return "", 0, deny
}

type RequestAudit struct {
	ID        string    `gorm:"type:varchar(36);primaryKey"`
	TokenHash string    `gorm:"type:char(64);not null;index"`
	AdminID   string    `gorm:"type:varchar(64);not null;index"`
	Method    string    `gorm:"type:varchar(8);not null"`
	Path      string    `gorm:"type:varchar(128);not null"`
	BodyHash  string    `gorm:"type:char(64);not null"`
	CreatedAt time.Time `gorm:"not null;index"`
}

func (RequestAudit) TableName() string { return "admin_im_request_audit" }
func (s *Service) Audit(ctx context.Context, token, actor, method, path, hash string) error {
	return s.DB.WithContext(ctx).Create(&RequestAudit{ID: uuid.NewString(), TokenHash: Hash(token), AdminID: actor, Method: method, Path: path, BodyHash: hash, CreatedAt: time.Now().UTC()}).Error
}

// RevokeSession deliberately requires no live enabled-role check: a signed
// current session may always revoke itself, including after disable/reset.
func (s *Service) RevokeSession(ctx context.Context, raw string) error {
	if s.Redis == nil || len(s.Secret) < 32 {
		return errors.New("administrator session store unavailable")
	}
	c, e := s.session(raw)
	if e != nil {
		return e
	}
	return s.Redis.HDel(ctx, "CHAT_UID_TOKEN_STATUS:"+c.UserID, raw).Err()
}
