// Package groupeffects stores group postcommit obligations. It does not perform
// group business writes or replay a group RPC. Receiver retry support is a
// separate explicit contract; a stable message ID alone does not confer it.
package groupeffects

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const Collection = "group_authority_effects"
const MaxPayload = 1 << 20

var (
	ErrInvalid     = errors.New("invalid group effect")
	ErrUnavailable = errors.New("group effects storage unavailable")
	ErrConflict    = errors.New("group effect state changed")
	ErrUnconfirmed = errors.New("group effect result unconfirmed")
)

// storageFailure keeps transaction labels and cancellation available to the
// strong transaction runner without exposing a driver message/URI to callers.
// A fixed public message is not a reason to erase retry/unknown-commit facts.
type storageFailure struct{ cause error }

func (e storageFailure) Error() string        { return ErrUnavailable.Error() }
func (e storageFailure) Unwrap() error        { return e.cause }
func (e storageFailure) Is(target error) bool { return target == ErrUnavailable }

func unavailable(cause error) error {
	if cause == nil {
		return ErrUnavailable
	}
	return storageFailure{cause: cause}
}

type Kind string

const (
	Notification  Kind = "notification-v1"
	AfterCallback Kind = "after-callback-v1"
)

type State string

const (
	Pending     State = "pending"
	Dispatching State = "dispatching"
	Unconfirmed State = "unconfirmed"
	Done        State = "done"
)

// Callback names are logical code-level destinations, never caller URLs. The
// endpoint/config binding must still match before the single dispatch attempt.
type CallbackName string

const (
	AfterCreate    CallbackName = "after-create"
	AfterSetInfo   CallbackName = "after-set-info"
	AfterSetMember CallbackName = "after-set-member"
	AfterJoin      CallbackName = "after-join"
	AfterQuit      CallbackName = "after-quit"
	AfterKick      CallbackName = "after-kick"
	AfterDismiss   CallbackName = "after-dismiss"
	AfterTransfer  CallbackName = "after-transfer"
)

type CallbackPayload struct {
	Name             CallbackName    `json:"name"`
	EndpointBinding  string          `json:"endpointBinding"`
	TraceOperationID string          `json:"traceOperationID"`
	Body             json.RawMessage `json:"body"`
}

// Frozen is a prepared immutable effect. NewNotification/NewCallback execute
// outside transaction retry, freezing identifiers, timestamps and wire bytes.
// Queue then validates the live private group scope inside the same Mongo TX.
type Frozen struct {
	ID            string    `bson:"_id"`
	OperationID   string    `bson:"operation_id"`
	Ordinal       int32     `bson:"ordinal"`
	GroupID       string    `bson:"group_id"`
	Actor         string    `bson:"actor"`
	Kind          Kind      `bson:"kind"`
	Payload       []byte    `bson:"payload,omitempty"`
	PayloadSHA256 string    `bson:"payload_sha256"`
	CreatedAt     time.Time `bson:"created_at"`
}

// Receipt is the minimal durable outcome. Completed effects contain no payload,
// callback body or raw HTTP response. Unconfirmed effects retain frozen bytes
// for exact inspection/confirmation, never an automatically changed intent.
type Receipt struct {
	Frozen           `bson:",inline"`
	State            State     `bson:"state"`
	Revision         int64     `bson:"revision"`
	Attempts         int32     `bson:"attempts"`
	Claim            string    `bson:"claim,omitempty"`
	SenderInstanceID string    `bson:"sender_instance_id,omitempty"`
	ClaimedAt        time.Time `bson:"claimed_at,omitempty"`
	OutcomeSHA256    string    `bson:"outcome_sha256,omitempty"`
	Decision         string    `bson:"decision,omitempty"`
	EvidenceSHA256   string    `bson:"evidence_sha256,omitempty"`
	UpdatedAt        time.Time `bson:"updated_at"`
}

type Summary struct {
	ID               string    `json:"id"`
	OperationID      string    `json:"operationID"`
	Kind             Kind      `json:"kind"`
	State            State     `json:"state"`
	Revision         int64     `json:"revision"`
	Attempts         int32     `json:"attempts"`
	SenderInstanceID string    `json:"senderInstanceID,omitempty"`
	ClaimedAt        time.Time `json:"claimedAt,omitempty"`
	PayloadSHA256    string    `json:"payloadSHA256"`
	OutcomeSHA256    string    `json:"outcomeSHA256,omitempty"`
	Decision         string    `json:"decision,omitempty"`
	EvidenceSHA256   string    `json:"evidenceSHA256,omitempty"`
}

func (r Receipt) Summary() Summary {
	return Summary{ID: r.ID, OperationID: r.OperationID, Kind: r.Kind, State: r.State, Revision: r.Revision, Attempts: r.Attempts, SenderInstanceID: r.SenderInstanceID, ClaimedAt: r.ClaimedAt, PayloadSHA256: r.PayloadSHA256, OutcomeSHA256: r.OutcomeSHA256, Decision: r.Decision, EvidenceSHA256: r.EvidenceSHA256}
}

func validID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed.String() == value && parsed != uuid.Nil
}
func validIdentity(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, "\x00\r\n")
}
func validSHA(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && hex.EncodeToString(raw) == value
}
func digest(payload []byte) string { sum := sha256.Sum256(payload); return hex.EncodeToString(sum[:]) }

func effectIdentity(operation string, ordinal int32, gid, actor string, kind Kind, at time.Time) (Frozen, error) {
	if !validID(operation) || ordinal < 0 || ordinal > 4095 || !validIdentity(gid) || !validIdentity(actor) || at.IsZero() {
		return Frozen{}, ErrInvalid
	}
	key, _ := json.Marshal([]any{ordinal, gid, actor, kind})
	parent, _ := uuid.Parse(operation)
	id := uuid.NewSHA1(parent, key).String()
	return Frozen{ID: id, OperationID: operation, Ordinal: ordinal, GroupID: gid, Actor: actor, Kind: kind, CreatedAt: at.UTC().Truncate(time.Millisecond)}, nil
}

func NewNotification(operation string, ordinal int32, gid, actor string, message *sdkws.MsgData, at time.Time) (Frozen, error) {
	f, err := effectIdentity(operation, ordinal, gid, actor, Notification, at)
	if err != nil || message == nil {
		return Frozen{}, ErrInvalid
	}
	m := proto.Clone(message).(*sdkws.MsgData)
	if m.SendID != actor || m.MsgFrom != constant.SysMsgType || !groupNotificationType(m.ContentType) || !validIdentity(m.RecvID) {
		return Frozen{}, ErrInvalid
	}
	if m.SessionType == constant.SuperGroupChatType {
		if m.GroupID != gid || m.RecvID != gid {
			return Frozen{}, ErrInvalid
		}
	} else if m.SessionType != constant.SingleChatType && m.SessionType != constant.NotificationChatType {
		return Frozen{}, ErrInvalid
	}
	m.ClientMsgID = "ga3:" + f.ID
	m.ServerMsgID = "" // The future receipt receiver owns its stable server identity.
	m.CreateTime = f.CreatedAt.UnixMilli()
	m.SendTime = f.CreatedAt.UnixMilli()
	m.Seq = 0
	f.Payload, err = proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil || len(f.Payload) == 0 || len(f.Payload) > MaxPayload {
		return Frozen{}, ErrInvalid
	}
	f.PayloadSHA256 = digest(f.Payload)
	return f, nil
}
func groupNotificationType(contentType int32) bool {
	switch contentType {
	case constant.GroupCreatedNotification, constant.GroupInfoSetNotification, constant.JoinGroupApplicationNotification,
		constant.MemberQuitNotification, constant.GroupApplicationAcceptedNotification, constant.GroupApplicationRejectedNotification,
		constant.GroupOwnerTransferredNotification, constant.MemberKickedNotification, constant.MemberInvitedNotification,
		constant.MemberEnterNotification, constant.GroupDismissedNotification, constant.GroupMutedNotification,
		constant.GroupCancelMutedNotification, constant.GroupMemberMutedNotification, constant.GroupMemberCancelMutedNotification,
		constant.GroupMemberInfoSetNotification, constant.GroupMemberSetToAdminNotification, constant.GroupMemberSetToOrdinaryUserNotification,
		constant.GroupInfoSetAnnouncementNotification, constant.GroupInfoSetNameNotification:
		return true
	default:
		return false
	}
}
func NewCallback(operation string, ordinal int32, gid, actor string, payload CallbackPayload, at time.Time) (Frozen, error) {
	f, err := effectIdentity(operation, ordinal, gid, actor, AfterCallback, at)
	if err != nil {
		return Frozen{}, err
	}
	switch payload.Name {
	case AfterCreate, AfterSetInfo, AfterSetMember, AfterJoin, AfterQuit, AfterKick, AfterDismiss, AfterTransfer:
	default:
		return Frozen{}, ErrInvalid
	}
	if !validSHA(payload.EndpointBinding) || len(payload.Body) == 0 || !json.Valid(payload.Body) {
		return Frozen{}, ErrInvalid
	}
	if payload.TraceOperationID == "" {
		payload.TraceOperationID = operation
	}
	if !validIdentity(payload.TraceOperationID) {
		return Frozen{}, ErrInvalid
	}
	canonical, _, err := canonicalCallback(gid, payload)
	if err != nil {
		return Frozen{}, err
	}
	// Round-trip into RawMessage preserves the immutable semantic request body;
	// caller-owned byte slices can no longer alter the stored envelope.
	payload.Body = append(json.RawMessage(nil), canonical...)
	f.Payload, err = json.Marshal(payload)
	if err != nil || len(f.Payload) > MaxPayload {
		return Frozen{}, ErrInvalid
	}
	f.PayloadSHA256 = digest(f.Payload)
	return f, nil
}

func (f Frozen) validate() error {
	if !validID(f.ID) || !validID(f.OperationID) || f.Ordinal < 0 || f.Ordinal > 4095 || !validIdentity(f.GroupID) || !validIdentity(f.Actor) || f.CreatedAt.IsZero() || !validSHA(f.PayloadSHA256) || len(f.Payload) == 0 || len(f.Payload) > MaxPayload || digest(f.Payload) != f.PayloadSHA256 {
		return ErrInvalid
	}
	want, err := effectIdentity(f.OperationID, f.Ordinal, f.GroupID, f.Actor, f.Kind, f.CreatedAt)
	if err != nil || want.ID != f.ID {
		return ErrInvalid
	}
	switch f.Kind {
	case Notification:
		var msg sdkws.MsgData
		if proto.Unmarshal(f.Payload, &msg) != nil {
			return ErrInvalid
		}
		rebuilt, err := NewNotification(f.OperationID, f.Ordinal, f.GroupID, f.Actor, &msg, f.CreatedAt)
		if err != nil || rebuilt.PayloadSHA256 != f.PayloadSHA256 {
			return ErrInvalid
		}
	case AfterCallback:
		var payload CallbackPayload
		if json.Unmarshal(f.Payload, &payload) != nil {
			return ErrInvalid
		}
		rebuilt, err := NewCallback(f.OperationID, f.Ordinal, f.GroupID, f.Actor, payload, f.CreatedAt)
		if err != nil || rebuilt.PayloadSHA256 != f.PayloadSHA256 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
