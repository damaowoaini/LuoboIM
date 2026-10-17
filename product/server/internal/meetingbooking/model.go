// Package meetingbooking manages explicit owner-scoped bookings, never media.
// It has no RTC room, token issuer, participant, platform or active-lease handle.
package meetingbooking

import (
	"context"
	"errors"
	"time"
)

var (
	ErrInvalid     = errors.New("invalid booking request")
	ErrDenied      = errors.New("booking access denied")
	ErrNotFound    = errors.New("booking not found")
	ErrConflict    = errors.New("booking intent or version conflict")
	ErrUnavailable = errors.New("booking storage readiness unavailable")
)

type Principal struct{ Tenant, Owner string }
type Payload struct {
	MeetingName     string   `json:"meetingName" bson:"meeting_name"`
	StartTime       int64    `json:"startTime" bson:"start_time"`
	MeetingDuration int64    `json:"meetingDuration" bson:"meeting_duration"`
	InviteeUserIDs  []string `json:"inviteeUserIDList" bson:"invitee_user_ids"`
	Ex              string   `json:"ex" bson:"ex"`
}
type CreateRequest struct {
	ClientRequestID string `json:"clientRequestID"`
	Payload
}
type UpdateRequest struct {
	ClientRequestID string `json:"clientRequestID"`
	BookingID       string `json:"bookingID"`
	ExpectedVersion int64  `json:"expectedVersion"`
	Payload
}
type CancelRequest struct {
	ClientRequestID string `json:"clientRequestID"`
	BookingID       string `json:"bookingID"`
	ExpectedVersion int64  `json:"expectedVersion"`
}
type IntentRequest struct {
	ClientRequestID string `json:"clientRequestID"`
}
type DetailRequest struct {
	BookingID string `json:"bookingID"`
}
type ListRequest struct {
	PageSize         int    `json:"pageSize"`
	Cursor           string `json:"cursor"`
	IncludeCancelled bool   `json:"includeCancelled"`
}
type Booking struct {
	ID        string    `bson:"_id"`
	Tenant    string    `bson:"tenant"`
	Owner     string    `bson:"owner"`
	State     string    `bson:"state"`
	Version   int64     `bson:"version"`
	Payload   *Payload  `bson:"payload,omitempty"`
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}

// Intent is an immutable receipt, including for resolve-before-create. No raw
// payload, JWT, platform or session is retained in it. No TTL may erase it.
type Intent struct {
	Tenant         string    `bson:"tenant"`
	Owner          string    `bson:"owner"`
	RequestID      string    `bson:"request_id"`
	Hash           string    `bson:"hash"`
	Action         string    `bson:"action"`
	BookingID      string    `bson:"booking_id,omitempty"`
	AppliedVersion int64     `bson:"applied_version"`
	Resolved       bool      `bson:"resolved"`
	CreatedAt      time.Time `bson:"created_at"`
}
type PublicBooking struct {
	BookingID  string   `json:"bookingID"`
	HostUserID string   `json:"hostUserID"`
	State      string   `json:"state"`
	Version    int64    `json:"version"`
	Payload    *Payload `json:"payload,omitempty"`
	CreatedAt  int64    `json:"createdAt"`
	UpdatedAt  int64    `json:"updatedAt"`
	MediaReady bool     `json:"mediaReady"` // Always false in this independent slice.
}
type Result struct {
	Found                bool           `json:"found"`
	ClientRequestID      string         `json:"clientRequestID,omitempty"`
	Action               string         `json:"action,omitempty"`
	Applied              bool           `json:"applied"`
	AppliedVersion       int64          `json:"appliedVersion,omitempty"`
	DefinitiveNotApplied bool           `json:"definitiveNotApplied"`
	ReplaySafe           bool           `json:"replaySafe"`
	Booking              *PublicBooking `json:"booking,omitempty"`
}
// Descriptive compatibility/readiness metadata from the authenticated owner's
// ordinary List read. It is never a bearer grant or a media admission token.
// A response is emitted only after the existing ready store List succeeds.
type ListResult struct {
	Contract        string `json:"contract"`
	ContractVersion int    `json:"contractVersion"`
	OwnerUserID     string `json:"ownerUserID"`
	ReplaySafe      bool   `json:"replaySafe"`
	MediaReady      bool   `json:"mediaReady"`
	Bookings   []PublicBooking `json:"bookings"`
	HasMore    bool            `json:"hasMore"`
	NextCursor string          `json:"nextCursor,omitempty"`
}
type ListQuery struct {
	Limit            int
	IncludeCancelled bool
	BeforeTime       time.Time
	BeforeID         string
}
type Tx interface {
	GetIntent(context.Context, string) (*Intent, error)
	InsertIntent(context.Context, *Intent) error
	GetBooking(context.Context, string) (*Booking, error)
	InsertBooking(context.Context, *Booking) error
	ReplaceBooking(context.Context, *Booking, int64) error
}
type Store interface {
	Run(context.Context, Principal, func(Tx) (*Result, error)) (*Result, error)
	List(context.Context, Principal, ListQuery) ([]Booking, bool, error)
}
type Access interface {
	// Authoritative account existence, block and current invite policy checks.
	// Runs before a new write transaction, never inside the transaction.
	Check(context.Context, Principal, []string) error
}
