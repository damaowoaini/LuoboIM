package groupeffects

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

var errGroupEffectPlan = errors.New("invalid frozen group notification plan")

// NotificationBranch is the immutable input to the future durable receiver.
// Receipt/sequence/DB/push acceptance is deliberately NOT implied by a plan.
// Both ordinary chat and these visible copies must use one sequence authority.
type NotificationBranch struct {
	ID, EffectID, ConversationID, PayloadSHA256 string
	Payload                                     []byte
	Persist                                     bool
}

func groupEffectHash(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// PlanNotificationBranches freezes the two existing OpenIM notification projections.
// It has zero I/O and never reads a later group/member cache. The trusted
// caller must load the exact durable effect from the tenant's own collection;
// an external SendMsg carrying a ga3-looking ID is not such a capability.
func PlanNotificationBranches(f Frozen) ([]NotificationBranch, error) {
	if f.Kind != Notification || len(f.Payload) == 0 || len(f.Payload) > MaxPayload {
		return nil, errGroupEffectPlan
	}
	var base sdkws.MsgData
	if proto.Unmarshal(f.Payload, &base) != nil {
		return nil, errGroupEffectPlan
	}
	rebuilt, err := NewNotification(f.OperationID, f.Ordinal, f.GroupID, f.Actor, &base, f.CreatedAt)
	if err != nil || rebuilt.ID != f.ID || rebuilt.PayloadSHA256 != f.PayloadSHA256 || groupEffectHash(f.Payload) != f.PayloadSHA256 || groupEffectHash(rebuilt.Payload) != f.PayloadSHA256 {
		return nil, errGroupEffectPlan
	}
	options := msgprocessor.Options(base.Options)
	if options.IsNotNotification() {
		return nil, errGroupEffectPlan
	}
	var result []NotificationBranch
	appendBranch := func(label, cid string, message *sdkws.MsgData) error {
		if cid == "" {
			return errGroupEffectPlan
		}
		parent, err := uuid.Parse(f.ID)
		if err != nil {
			return errGroupEffectPlan
		}
		branchID := uuid.NewSHA1(parent, []byte(label+"\x00"+cid)).String()
		// Server identity is frozen per branch; the ordinary encapsulator must
		// not replace it on this separately admitted receiver path.
		message.ServerMsgID = uuid.NewSHA1(parent, []byte("server\x00"+branchID)).String()
		message.Seq = 0
		body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
		if err != nil {
			return errGroupEffectPlan
		}
		result = append(result, NotificationBranch{ID: branchID, EffectID: f.ID, ConversationID: cid, PayloadSHA256: groupEffectHash(body), Payload: body, Persist: msgprocessor.Options(message.Options).IsHistory()})
		return nil
	}
	if options.IsSendMsg() {
		visible := proto.Clone(&base).(*sdkws.MsgData)
		// Preserve the existing split semantics exactly: normal message defaults
		// plus moved unread/offline flags, while the silent notification loses
		// only those moved flags. No user-visible wire field is invented.
		if base.Options != nil {
			visible.Options = msgprocessor.NewMsgOptions()
		}
		if options.IsOfflinePush() {
			base.Options = msgprocessor.WithOptions(base.Options, msgprocessor.WithOfflinePush(false))
			visible.Options = msgprocessor.WithOptions(visible.Options, msgprocessor.WithOfflinePush(true))
		}
		if options.IsUnreadCount() {
			base.Options = msgprocessor.WithOptions(base.Options, msgprocessor.WithUnreadCount(false))
			visible.Options = msgprocessor.WithOptions(visible.Options, msgprocessor.WithUnreadCount(true))
		}
		if err := appendBranch("chat-copy", msgprocessor.GetChatConversationIDByMsg(&base), visible); err != nil {
			return nil, err
		}
	}
	if err := appendBranch("notification", msgprocessor.GetNotificationConversationIDByMsg(&base), &base); err != nil {
		return nil, err
	}
	return result, nil
}
