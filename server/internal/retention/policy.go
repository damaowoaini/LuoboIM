// Package retention restricts an owner's server-visible history. It never
// promises physical erasure on a peer, device, attachment store or export.
package retention

import (
	"errors"
	"math"
	"time"
)

var (
	ErrPolicyChanged = errors.New("retention policy changed")
	ErrInvalidPolicy = errors.New("retention policy is invalid")
	ErrSourceChanged = errors.New("retention source changed")
	ErrUnavailable   = errors.New("retention capability unavailable")
	ErrSyncPending   = errors.New("retention preference committed; synchronization pending")
)

// This is an arithmetic bound, not a new product limit. Existing positive
// durations outside the old UI's 180-day choices are not silently truncated.
const MaxDurationSeconds int64 = math.MaxInt64 / 1000
const MaximumChunk = 100

type Policy struct {
	Owner            string    `bson:"owner_user_id" json:"ownerUserID"`
	Conversation     string    `bson:"conversation_id" json:"conversationID"`
	ConversationType int32     `bson:"conversation_type" json:"conversationType"`
	Peer             string    `bson:"user_id" json:"userID"`
	Group            string    `bson:"group_id" json:"groupID"`
	Private          bool      `bson:"is_private_chat" json:"isPrivateChat"`
	BurnDuration     int32     `bson:"burn_duration" json:"burnDuration"`
	Enabled          bool      `bson:"is_msg_destruct" json:"isMsgDestruct"`
	Duration         int64     `bson:"msg_destruct_time" json:"msgDestructTime"`
	Revision         int64     `bson:"retention_policy_revision" json:"policyRevision"`
	Prepared         bool      `bson:"retention_policy_prepared" json:"-"`
	Generation       int64     `bson:"retention_scan_generation" json:"-"`
	LastScan         time.Time `bson:"latest_msg_destruct_time" json:"lastSuccessfulScan"`
}

func ValidateDuration(enabled bool, seconds int64) error {
	if seconds < 0 || seconds > MaxDurationSeconds || (enabled && seconds == 0) {
		return ErrInvalidPolicy
	}
	return nil
}

func (p Policy) Eligible() bool {
	return p.Owner != "" && p.Conversation != "" && p.Enabled && p.Prepared &&
		p.Revision > 0 && p.Revision < math.MaxInt64 &&
		p.Generation >= 0 && p.Generation < math.MaxInt64 &&
		ValidateDuration(true, p.Duration) == nil
}

// A zero LastScan is only scheduling metadata; it cannot enable a policy.
func (p Policy) Cutoff(now time.Time) (int64, error) {
	if !p.Eligible() {
		return 0, ErrInvalidPolicy
	}
	nowMS := now.UnixMilli()
	delta := p.Duration * 1000
	if nowMS < math.MinInt64+delta {
		return 0, ErrInvalidPolicy
	}
	return nowMS - delta, nil
}

type Cursor struct {
	Owner        string `bson:"owner"`
	Conversation string `bson:"conversation"`
	Version      int64  `bson:"version"`
}

type Slot struct {
	Seq      int64
	SendTime int64
	Known    bool
	Hidden   bool
	Document string
}

type Plan struct {
	Policy    Policy
	FromFloor int64
	ToFloor   int64
	At        time.Time
	Sequences []int64
	Documents []string
	Limit     int
}
