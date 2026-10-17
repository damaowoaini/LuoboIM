// Package adminprovision owns durable administrator account-creation intents
// and the IM/route outbox. Passwords, OTPs and tokens never enter its models.
package adminprovision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Account struct {
	AreaCode    string `json:"areaCode,omitempty"`
	PhoneNumber string `json:"phoneNumber,omitempty"`
	Email       string `json:"email,omitempty"`
}
type Payload struct {
	Version       int       `json:"version"`
	ReceiptID     string    `json:"receiptID"`
	ServerID      string    `json:"serverID"`
	UserID        string    `json:"userID"`
	ActorUserID   string    `json:"actorUserID"`
	IMProvisioned bool      `json:"imProvisioned"`
	Accounts      []Account `json:"accounts"`
}
type Intent struct {
	ActorID     string    `gorm:"column:actor_id;type:varchar(64);primaryKey"`
	RequestID   string    `gorm:"column:request_id;type:char(36);primaryKey"`
	Fingerprint string    `gorm:"column:fingerprint;type:char(64);not null"`
	State       string    `gorm:"column:state;type:varchar(16);not null"`
	UserID      string    `gorm:"column:user_id;type:varchar(64)"`
	ReceiptID   string    `gorm:"column:receipt_id;type:char(36)"`
	Reason      string    `gorm:"column:reason;type:varchar(32)"`
	CreatedAt   time.Time `gorm:"column:created_at;not null"`
	UpdatedAt   time.Time `gorm:"column:updated_at;not null"`
}

func (Intent) TableName() string { return "admin_creation_intents" }

type Job struct {
	ReceiptID           string     `gorm:"column:receipt_id;type:char(36);primaryKey"`
	UserID              string     `gorm:"column:user_id;type:varchar(64);uniqueIndex;not null"`
	ActorID             string     `gorm:"column:actor_id;type:varchar(64);not null;index"`
	RequestID           string     `gorm:"column:request_id;type:char(36)"`
	OriginalCreatorID   string     `gorm:"column:original_creator_id;type:varchar(64)"`
	Source              string     `gorm:"column:source;type:varchar(24);not null"`
	Payload             string     `gorm:"column:payload;type:longtext;not null"`
	PayloadHash         string     `gorm:"column:payload_hash;type:char(64);not null"`
	RouteRequired       bool       `gorm:"column:route_required;not null"`
	DefaultsAll         bool       `gorm:"column:defaults_all;not null"`
	DefaultsPlan        *string    `gorm:"column:defaults_plan;type:json"`
	IMProvisionedAt     *time.Time `gorm:"column:im_provisioned_at"`
	DefaultsCompletedAt *time.Time `gorm:"column:defaults_completed_at"`
	RouteBoundAt        *time.Time `gorm:"column:route_bound_at"`
	State               string     `gorm:"column:state;type:varchar(24);not null;index:idx_admin_provision_pending,priority:1"`
	Reason              string     `gorm:"column:reason;type:varchar(32)"`
	Attempts            int        `gorm:"column:attempts;not null"`
	NextAttemptAt       time.Time  `gorm:"column:next_attempt_at;not null;index:idx_admin_provision_pending,priority:2"`
	CreatedAt           time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;not null"`
}

func (Job) TableName() string { return "admin_provision_jobs" }

type RecoveryAudit struct {
	ID                     string    `gorm:"column:id;type:char(36);primaryKey"`
	ActorID                string    `gorm:"column:actor_id;type:varchar(64);not null;index"`
	UserID                 string    `gorm:"column:user_id;type:varchar(64);not null;index"`
	ReceiptID              string    `gorm:"column:receipt_id;type:char(36);not null"`
	OriginalCreatorUnknown bool      `gorm:"column:original_creator_unknown;not null"`
	Reason                 string    `gorm:"column:reason;type:varchar(1024);not null"`
	SnapshotHash           string    `gorm:"column:snapshot_hash;type:char(64);not null"`
	IMCreateTime           int64     `gorm:"column:im_create_time;not null"`
	CreatedAt              time.Time `gorm:"column:created_at;not null"`
}

func (RecoveryAudit) TableName() string { return "admin_provision_recovery_audit" }
func Hash(data []byte) string           { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func ValidID(id string) bool {
	value, e := uuid.Parse(id)
	return e == nil && value.Version() == 4 && value.String() == id
}

// BatchRequestID freezes each row's intent without retaining any credentials.
// The namespace and row position are stable across an explicit batch retry.
func BatchRequestID(batch string, index int) (string, error) {
	if !ValidID(batch) || index < 0 || index >= 10000 {
		return "", errors.New("invalid batch creation intent")
	}
	digest := sha256.Sum256([]byte("luobo-admin-creation-row/v1\n" + batch + "\n" + strconv.Itoa(index)))
	var id uuid.UUID
	copy(id[:], digest[:16])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String(), nil
}

var phonePattern = regexp.MustCompile(`^[0-9]{6,24}$`)
var areaPattern = regexp.MustCompile(`^\+[0-9]{1,4}$`)

// Accounts derives both identities; it never treats email as proof for a
// different phone and never invents an absent country code for legacy data.
func Accounts(email, area, phone string) ([]Account, error) {
	values := []Account{}
	email = strings.ToLower(strings.TrimSpace(email))
	area = strings.TrimSpace(area)
	phone = strings.TrimSpace(phone)
	if phone != "" {
		if !strings.HasPrefix(area, "+") {
			area = "+" + area
		}
		if !phonePattern.MatchString(phone) || !areaPattern.MatchString(area) {
			return nil, errors.New("invalid phone identity")
		}
		values = append(values, Account{AreaCode: area, PhoneNumber: phone})
	} else if area != "" {
		return nil, errors.New("area code without phone")
	}
	if email != "" {
		value, e := mail.ParseAddress(email)
		if e != nil || value.Address != email || len(email) > 254 {
			return nil, errors.New("invalid email identity")
		}
		values = append(values, Account{Email: email})
	}
	return values, nil
}

// Profile contains only non-credential registration parameters for idempotency.
// A retry never claims a changed password was applied; it returns recovered
// status and requires a separate authenticated password-change/login flow.
type Profile struct {
	RequestedUserID string `json:"userID"`
	Account         string `json:"account"`
	Email           string `json:"email"`
	AreaCode        string `json:"areaCode"`
	PhoneNumber     string `json:"phoneNumber"`
	Nickname        string `json:"nickname"`
	FaceURL         string `json:"faceURL"`
	Gender          int32  `json:"gender"`
	Birth           int64  `json:"birth"`
	DefaultsAll     bool   `json:"defaultsAll"`
}

func (p Profile) Fingerprint() (string, error) {
	accounts, e := Accounts(p.Email, p.AreaCode, p.PhoneNumber)
	if e != nil {
		return "", e
	}
	p.Email = ""
	p.AreaCode = ""
	p.PhoneNumber = ""
	for _, account := range accounts {
		if account.Email != "" {
			p.Email = account.Email
		} else {
			p.AreaCode = account.AreaCode
			p.PhoneNumber = account.PhoneNumber
		}
	}
	data, e := json.Marshal(p)
	return Hash(data), e
}

type Creation struct {
	ActorID, RequestID, Fingerprint, UserID, ReceiptID string
	Recovered                                          bool
	DefaultsAll                                        bool
	ServerID                                           string
	RouteEnabled                                       bool
}
type creationKey struct{}

func WithCreation(ctx context.Context, c *Creation) context.Context {
	return context.WithValue(ctx, creationKey{}, c)
}
func CreationFrom(ctx context.Context) *Creation {
	c, _ := ctx.Value(creationKey{}).(*Creation)
	return c
}

// LockCreation serializes final account writes with retries and definitive
// rejection. The caller holds this lock through account+outbox commit.
func LockCreation(ctx context.Context, tx *gorm.DB, c *Creation) (bool, error) {
	var intent Intent
	if e := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("actor_id=? AND request_id=?", c.ActorID, c.RequestID).Take(&intent).Error; e != nil {
		return false, e
	}
	if intent.Fingerprint != c.Fingerprint {
		return false, errors.New("creation intent payload conflict")
	}
	switch intent.State {
	case "created":
		c.UserID = intent.UserID
		c.ReceiptID = intent.ReceiptID
		c.Recovered = true
		return true, nil
	case "reserved":
		return false, nil
	default:
		return false, errors.New("creation intent is definitively rejected")
	}
}
func Queue(ctx context.Context, tx *gorm.DB, c *Creation, userID, email, area, phone string) error {
	accounts, e := Accounts(email, area, phone)
	if e != nil {
		return e
	}
	now := time.Now().UTC()
	id := uuid.NewString()
	payload := Payload{Version: 1, ReceiptID: id, ServerID: c.ServerID, UserID: userID, ActorUserID: c.ActorID, IMProvisioned: true, Accounts: accounts}
	data, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	job := Job{ReceiptID: id, UserID: userID, ActorID: c.ActorID, RequestID: c.RequestID, OriginalCreatorID: c.ActorID, Source: "new_creation", Payload: string(data), PayloadHash: Hash(data), RouteRequired: c.RouteEnabled && len(accounts) > 0, DefaultsAll: c.DefaultsAll, State: "awaiting_im", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	if e = tx.WithContext(ctx).Create(&job).Error; e != nil {
		return e
	}
	result := tx.WithContext(ctx).Model(&Intent{}).Where("actor_id=? AND request_id=? AND state=? AND fingerprint=?", c.ActorID, c.RequestID, "reserved", c.Fingerprint).Updates(map[string]any{"state": "created", "user_id": userID, "receipt_id": id, "updated_at": now})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("creation intent changed before commit")
	}
	c.UserID = userID
	c.ReceiptID = id
	return nil
}
