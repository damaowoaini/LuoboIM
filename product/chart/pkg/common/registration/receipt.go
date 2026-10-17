// Package registration stores proof of a specific, authenticated route intent.
// It never uses an account-existence check as proof of who created the account.
package registration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

const MetadataKey = "x-luobo-route-registration-id"

type Receipt struct {
	RegistrationID     string     `gorm:"column:registration_id;primaryKey;type:char(36)" json:"registrationID"`
	NormalizedIdentity string     `gorm:"column:normalized_identity;uniqueIndex;type:varchar(128);not null" json:"normalizedIdentity"`
	UserID             string     `gorm:"column:user_id;uniqueIndex;type:varchar(64);not null" json:"userID"`
	CreatedAt          time.Time  `json:"-"`
	IMProvisionedAt    *time.Time `json:"-"`
	ManagedByOutbox    bool       `gorm:"column:managed_by_outbox;not null;default:false" json:"-"`
}

func (Receipt) TableName() string { return "route_registration_receipts" }
func ValidID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.Version() == 4 && parsed.String() == id
}
func ID(ctx context.Context) string {
	values, _ := ctx.Value(MetadataKey).([]string)
	if len(values) == 1 && ValidID(values[0]) {
		return values[0]
	}
	return ""
}
func WithID(ctx context.Context, id string) context.Context {
	headers, _ := ctx.Value(constant.RpcCustomHeader).([]string)
	headers = append(append([]string{}, headers...), MetadataKey)
	ctx = context.WithValue(ctx, constant.RpcCustomHeader, headers)
	return context.WithValue(ctx, MetadataKey, []string{id})
}
func Identity(email, area, phone string) (string, error) {
	if email = strings.ToLower(strings.TrimSpace(email)); email != "" {
		h := sha256.Sum256([]byte(email))
		return "email:" + base64.RawURLEncoding.EncodeToString(h[:]), nil
	}
	phone = strings.TrimSpace(phone)
	area = strings.TrimSpace(area)
	if area != "" && !strings.HasPrefix(area, "+") {
		area = "+" + area
	}
	if phone == "" || area == "" {
		return "", fmt.Errorf("route registration requires phone or email identity")
	}
	for _, part := range []string{strings.TrimPrefix(area, "+"), phone} {
		for _, c := range part {
			if c < '0' || c > '9' {
				return "", fmt.Errorf("invalid phone identity")
			}
		}
	}
	return "phone:" + area + ":" + phone, nil
}
func Create(ctx context.Context, tx *gorm.DB, id, email, area, phone, userID string, managed ...bool) error {
	if id == "" {
		return nil
	}
	if !ValidID(id) {
		return fmt.Errorf("invalid route registration ID")
	}
	identity, err := Identity(email, area, phone)
	if err != nil {
		return err
	}
	return tx.WithContext(ctx).Create(&Receipt{RegistrationID: id, NormalizedIdentity: identity, UserID: userID, CreatedAt: time.Now().UTC(), ManagedByOutbox: len(managed) > 0 && managed[0]}).Error
}
