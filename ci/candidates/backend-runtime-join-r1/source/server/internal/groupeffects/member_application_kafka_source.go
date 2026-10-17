package groupeffects

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

func memberApplicationBranches(message *sdkws.MsgData) []*sdkws.MsgData {
	if message == nil {
		return nil
	}
	stored, ephemeral, storedTips, silentTips, modifiers := msgprocessor.SplitStorageMessages([]*sdkws.MsgData{proto.Clone(message).(*sdkws.MsgData)})
	if len(modifiers) != 0 {
		return nil
	}
	result := append(stored, ephemeral...)
	result = append(result, storedTips...)
	return append(result, silentTips...)
}

type MemberApplicationKafkaSource struct {
	store   *Store
	row     memberApplicationDirect
	message *sdkws.MsgData
}

// Call after the existing broker-coordinate receipt lookup. This capability
// is an original source binding, not a reconstructed current membership grant.
// Only SDK encapsulateMsgData's ServerMsgID and SendTime may differ. The three
// notification types never enter callbackMsgModify (which is text-only).
func (s *Store) ReadMemberApplicationKafkaSource(ctx context.Context, message *sdkws.MsgData, actor string) (*MemberApplicationKafkaSource, error) {
	if s == nil || message == nil || message.SessionType != constant.SingleChatType || message.MsgFrom != constant.SysMsgType || message.GroupID != "" || message.Seq != 0 || !validIdentity(message.ServerMsgID) || message.SendTime <= 0 {
		return nil, ErrInvalid
	}
	switch message.ContentType {
	case constant.JoinGroupApplicationNotification, constant.GroupApplicationAcceptedNotification, constant.GroupApplicationRejectedNotification:
	default:
		return nil, ErrInvalid
	}
	if err := receiverSession(ctx, s.db); err != nil {
		return nil, err
	}
	if err := s.checkSchema(ctx); err != nil {
		return nil, err
	}
	row, err := s.readMemberApplicationDirect(ctx, message.ClientMsgID)
	if err != nil {
		return nil, err
	}
	if row.Source.ServiceUser != actor || row.State != "dispatching" && row.State != "unknown" && row.State != "source-acked" || row.Claim == "" {
		return nil, ErrConflict
	}
	if row.State == "source-acked" && (row.ServerMsgID != message.ServerMsgID || row.SendTime != message.SendTime) {
		return nil, ErrConflict
	}
	normalized := proto.Clone(message).(*sdkws.MsgData)
	normalized.ServerMsgID = ""
	normalized.SendTime = 0
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	if digest(body) != row.Source.Proof.MessageSHA256 || !row.Redacted && string(body) != string(row.Payload) {
		return nil, ErrConflict
	}
	return &MemberApplicationKafkaSource{store: s, row: row, message: proto.Clone(message).(*sdkws.MsgData)}, nil
}

func (cap *MemberApplicationKafkaSource) OperationID() string {
	if cap == nil || cap.store == nil {
		return ""
	}
	return cap.row.Source.Operation
}
func (cap *MemberApplicationKafkaSource) MatchesScope(epoch, activation string) bool {
	return cap != nil && cap.store != nil && cap.row.Source.Proof.Command.Epoch == epoch && cap.row.Source.Activation == activation
}

// The caller supplies the actual SplitStorageMessages branch and its complete
// original single-chat roster. A subset, different clone/options or another
// CID cannot borrow the source. Narrowing the cloned owner maps creates no new
// revisions and never consults today's privacy state to refresh the ticket.
func (cap *MemberApplicationKafkaSource) BranchCut(message *sdkws.MsgData, users []string) (deliveryprofile.OrdinaryVisibilityCut, error) {
	if cap == nil || cap.store == nil || cap.message == nil || message == nil {
		return deliveryprofile.OrdinaryVisibilityCut{}, ErrInvalid
	}
	matched := false
	for _, candidate := range memberApplicationBranches(cap.message) {
		if proto.Equal(candidate, message) {
			if matched {
				return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
			}
			matched = true
		}
	}
	if !matched {
		return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
	}
	wanted := []string{message.SendID}
	if message.RecvID != message.SendID {
		wanted = append(wanted, message.RecvID)
	}
	if !msgprocessor.Options(message.Options).IsSenderSync() {
		filtered := wanted[:0]
		for _, uid := range wanted {
			if uid != message.SendID {
				filtered = append(filtered, uid)
			}
		}
		wanted = filtered
	}
	actual := append([]string(nil), users...)
	sort.Strings(actual)
	sort.Strings(wanted)
	if len(actual) != len(wanted) {
		return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
	}
	for i, uid := range wanted {
		if actual[i] != uid {
			return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
		}
	}
	frozen, ok := cap.row.Source.Cuts[msgprocessor.GetConversationIDByMsg(message)]
	if !ok {
		return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
	}
	body, err := json.Marshal(frozen)
	if err != nil {
		return deliveryprofile.OrdinaryVisibilityCut{}, err
	}
	var cut deliveryprofile.OrdinaryVisibilityCut
	if json.Unmarshal(body, &cut) != nil || !cut.Matches(message) {
		return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
	}
	owners := map[string]int64{}
	branchOwners := map[string]int64{}
	for _, uid := range users {
		key := deliveryprofile.SHA([]byte(uid))
		normal, ok := cut.Normal.Owners[key]
		if !ok {
			return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
		}
		branch, ok := cut.Branch.Owners[key]
		if !ok {
			return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
		}
		owners[key], branchOwners[key] = normal, branch
	}
	cut.Normal.Owners, cut.Branch.Owners = owners, branchOwners
	if !cut.Valid() {
		return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
	}
	return cut, nil
}

// The source's first actual broker receipt commits with the normal allocator.
// A second coordinate is a conflict and rolls back its attempted allocation,
// even if the repeated bytes/client ID happened to be identical.
func (cap *MemberApplicationKafkaSource) BindKafkaReceipt(ctx context.Context, sourceID, recordSHA, receiptSHA string) error {
	if cap == nil || cap.store == nil || !validID(sourceID) || !validSHA(recordSHA) || !validSHA(receiptSHA) {
		return ErrInvalid
	}
	s := cap.store
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	row, err := s.readMemberApplicationDirect(ctx, cap.row.ID)
	if err != nil {
		return err
	}
	if row.SourceSHA != cap.row.SourceSHA || row.Claim != cap.row.Claim || row.State != "dispatching" && row.State != "unknown" && row.State != "source-acked" {
		return ErrConflict
	}
	if row.KafkaSourceID != "" {
		if row.KafkaSourceID != sourceID || row.KafkaRecordSHA != recordSHA || row.KafkaReceiptSHA != receiptSHA {
			return ErrConflict
		}
		return nil
	}
	update, err := s.collection.UpdateOne(ctx, bson.M{"_id": row.ID, "source_sha256": row.SourceSHA, "claim": row.Claim, "revision": row.Revision, "kafka_source_id": "", "kafka_record_sha256": "", "kafka_receipt_sha256": ""}, bson.M{"$set": bson.M{"kafka_source_id": sourceID, "kafka_record_sha256": recordSHA, "kafka_receipt_sha256": receiptSHA, "redacted": true, "payload": []byte{}}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return err
	}
	if update.MatchedCount != 1 || update.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
