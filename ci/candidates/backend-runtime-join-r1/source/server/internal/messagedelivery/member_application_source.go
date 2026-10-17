package messagedelivery

import (
	"context"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

type memberApplicationNamespaceReceipt struct {
	ID            string `bson:"_id"`
	Kind          string `bson:"kind"`
	Epoch         string `bson:"epoch"`
	CommandID     string `bson:"command_id"`
	Actor         string `bson:"actor"`
	GroupID       string `bson:"group_id"`
	Applicant     string `bson:"applicant"`
	Recipient     string `bson:"recipient"`
	MessageSHA256 string `bson:"message_sha256"`
	OriginSHA256  string `bson:"origin_sha256"`
}

// A private real group-request transition must have constructed and registered
// this exact standard SingleChat notification. A caller-supplied message hash
// or recipient list cannot mint this proof. This reserves metadata only; it is
// not a SendMsg permission, queue delivery receipt, or an external dispatch.
func (s *Store) RecordMemberApplicationSource(ctx context.Context, cap *groupauthority.MemberApplicationScope, message *sdkws.MsgData) error {
	if s == nil || s.db == nil || ctx == nil || cap == nil || message == nil {
		return ErrInvalid
	}
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	if err := s.checkMembershipSchema(ctx); err != nil {
		return err
	}
	info, err := cap.InspectMessage(ctx, s.db, message)
	if err != nil {
		return err
	}
	if info.Command.Epoch != s.epoch || message.SessionType != constant.SingleChatType || message.GroupID != "" || message.MsgFrom != constant.SysMsgType || message.SendID != info.Command.Actor || message.RecvID != info.Recipient || message.Seq != 0 {
		return ErrConflict
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return err
	}
	if hash(body) != info.MessageSHA256 {
		return ErrConflict
	}
	d, err := DescribeNamespace(message)
	if err != nil {
		return err
	}
	if d.Kind != "single" {
		return ErrConflict
	}
	origins := map[string]messageOrigin{}
	principals := map[string]NamespaceOriginDescriptor{}
	for _, cid := range []string{msgprocessor.GetChatConversationIDByMsg(message), msgprocessor.GetNotificationConversationIDByMsg(message)} {
		copy := d
		copy.CID = cid
		if err := validateNamespaceDescriptor(copy); err != nil {
			return err
		}
		origins[cid] = messageOrigin{Kind: copy.Kind, SHA256: copy.SHA256, CID: cid}
		principals[cid] = copy
	}
	if err := s.reserveTypedNamespacePairs(ctx, origins, principals); err != nil {
		return err
	}
	r := memberApplicationNamespaceReceipt{ID: key(s.epoch, "member-application-source", info.Command.ID+"\x00"+info.MessageSHA256), Kind: "member-application-source-v1", Epoch: s.epoch, CommandID: info.Command.ID, Actor: info.Command.Actor, GroupID: info.Command.GroupID, Applicant: info.Applicant, Recipient: info.Recipient, MessageSHA256: info.MessageSHA256, OriginSHA256: d.SHA256}
	return s.insertNoticeRecord(ctx, r.ID, r, func(raw bson.Raw) (any, error) {
		var old memberApplicationNamespaceReceipt
		if !exactFields(raw, []string{"_id", "kind", "epoch", "command_id", "actor", "group_id", "applicant", "recipient", "message_sha256", "origin_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &old) != nil {
			return nil, ErrConflict
		}
		return old, nil
	})
}
