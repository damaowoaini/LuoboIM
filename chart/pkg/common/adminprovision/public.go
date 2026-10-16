package adminprovision

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/OpenIMSDK/chat/pkg/common/registration"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const publicSource = "public_creation"
const AdminPurpose = "administrator"
const PublicPurpose = "registration"

// PublicCreation is server-owned. An ordinary caller cannot attach either this
// context or trusted route metadata through a JSON request field. A lost direct
// registration response needs no new client identifier: the SQL outbox keeps
// provisioning the original account, which can subsequently use its password.
type PublicCreation struct {
	ReceiptID, ServerID, UserID string
}
type publicCreationKey struct{}

func NewPublicCreation(ctx context.Context, serverID string) *PublicCreation {
	id := registration.ID(ctx)
	if id == "" {
		id = uuid.NewString()
	}
	return &PublicCreation{ReceiptID: id, ServerID: serverID}
}
func WithPublicCreation(ctx context.Context, c *PublicCreation) context.Context {
	return context.WithValue(ctx, publicCreationKey{}, c)
}
func PublicCreationFrom(ctx context.Context) *PublicCreation {
	c, _ := ctx.Value(publicCreationKey{}).(*PublicCreation)
	return c
}

// QueuePublic shares the existing row-locked worker but deliberately never
// commits a central route binding: the authenticated route saga owns that
// reservation. This transaction consumes the OTP and creates the account,
// registration receipt and job together, or rolls all of them back.
func QueuePublic(ctx context.Context, tx *gorm.DB, c *PublicCreation, userID, email, area, phone string) error {
	if c == nil || !registration.ValidID(c.ReceiptID) || userID == "" {
		return errors.New("invalid public creation receipt")
	}
	accounts, e := Accounts(email, area, phone)
	if e != nil || len(accounts) == 0 {
		return errors.New("public registration identity unavailable")
	}
	if e := registration.Create(ctx, tx, c.ReceiptID, email, area, phone, userID, true); e != nil {
		return e
	}
	now := time.Now().UTC()
	payload := Payload{Version: 1, ReceiptID: c.ReceiptID, ServerID: c.ServerID, UserID: userID, ActorUserID: userID, IMProvisioned: true, Accounts: accounts}
	data, e := json.Marshal(payload)
	if e != nil {
		return e
	}
	job := Job{ReceiptID: c.ReceiptID, UserID: userID, ActorID: userID, OriginalCreatorID: userID, Source: publicSource, Payload: string(data), PayloadHash: Hash(data), State: "awaiting_im", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	if e := tx.WithContext(ctx).Create(&job).Error; e != nil {
		return e
	}
	c.UserID = userID
	return nil
}

// PublicStatus accepts only the server-owned tuple created by the account
// transaction. It is not an administrator-principal lookup: equal userID text
// in the separate admin and business namespaces confers no receipt access.
func (s *Service) PublicStatus(ctx context.Context, c *PublicCreation) (Status, error) {
	if c == nil || !registration.ValidID(c.ReceiptID) || c.UserID == "" {
		return Status{}, errors.New("public creation status unavailable")
	}
	var job Job
	if e := s.DB.WithContext(ctx).Where("receipt_id=? AND user_id=? AND source=?", c.ReceiptID, c.UserID, publicSource).Take(&job).Error; e != nil {
		return Status{}, e
	}
	return statusFor(job), nil
}

func publicReceipt(ctx context.Context, tx *gorm.DB, job Job, email, area, phone string) error {
	var receipt registration.Receipt
	if e := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("registration_id=? AND user_id=?", job.ReceiptID, job.UserID).Take(&receipt).Error; e != nil {
		return e
	}
	identity, e := registration.Identity(email, area, phone)
	if e != nil || !receipt.ManagedByOutbox || receipt.NormalizedIdentity != identity {
		return errOwnerConflict
	}
	return nil
}
