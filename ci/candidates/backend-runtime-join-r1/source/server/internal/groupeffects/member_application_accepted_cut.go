package groupeffects

import (
	"context"
	"encoding/json"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"google.golang.org/protobuf/proto"
)

// This is source custody, not an actor/recipient grant. The actual accepted
// receipt and every branch must independently match in messagedelivery.
type MemberApplicationAcceptedBinding struct {
	ApplicationID string `bson:"application_id"`
	SourceSHA256  string `bson:"source_sha256"`
	ClaimID       string `bson:"claim_id"`
	RecordSHA256  string `bson:"record_sha256"`
	ReceiptSHA256 string `bson:"receipt_sha256"`
}

func (b MemberApplicationAcceptedBinding) Valid() bool {
	return validID(b.ApplicationID) && validSHA(b.SourceSHA256) && validID(b.ClaimID) && validSHA(b.RecordSHA256) && validSHA(b.ReceiptSHA256)
}

type MemberApplicationAcceptedSource struct{ row memberApplicationDirect }

func (cap *MemberApplicationKafkaSource) AcceptedBinding(recordSHA, receiptSHA string) (MemberApplicationAcceptedBinding, error) {
	if cap == nil || cap.store == nil || !validSHA(recordSHA) || !validSHA(receiptSHA) {
		return MemberApplicationAcceptedBinding{}, ErrInvalid
	}
	b := MemberApplicationAcceptedBinding{ApplicationID: cap.row.ID, SourceSHA256: cap.row.SourceSHA, ClaimID: cap.row.Claim, RecordSHA256: recordSHA, ReceiptSHA256: receiptSHA}
	if !b.Valid() {
		return b, ErrConflict
	}
	return b, nil
}

// A completed broker receipt must already have been bound by the ORIGINAL
// claim in the same acceptance TX. An arbitrary public binding DTO is useless
// without that immutable source row. No fresh privacy capture occurs here.
func (s *Store) ReadAcceptedMemberApplicationSource(ctx context.Context, b MemberApplicationAcceptedBinding, sourceID string) (*MemberApplicationAcceptedSource, error) {
	if s == nil || ctx == nil || !b.Valid() || !validID(sourceID) {
		return nil, ErrInvalid
	}
	if err := s.checkSchema(ctx); err != nil {
		return nil, err
	}
	row, err := s.readMemberApplicationDirect(ctx, b.ApplicationID)
	if err != nil {
		return nil, err
	}
	if row.SourceSHA != b.SourceSHA256 || row.Claim != b.ClaimID || row.KafkaSourceID != sourceID || row.KafkaRecordSHA != b.RecordSHA256 || row.KafkaReceiptSHA != b.ReceiptSHA256 || row.State != "dispatching" && row.State != "unknown" && row.State != "source-acked" {
		return nil, ErrConflict
	}
	return &MemberApplicationAcceptedSource{row: row}, nil
}
func (cap *MemberApplicationAcceptedSource) MatchesScope(epoch, activation, operation string) bool {
	return cap != nil && cap.row.Source.Proof.Command.Epoch == epoch && cap.row.Source.Activation == activation && cap.row.Source.Operation == operation
}

func (cap *MemberApplicationAcceptedSource) Branch(cid string) (*sdkws.MsgData, deliveryprofile.OrdinaryVisibilityCut, error) {
	if cap == nil {
		return nil, deliveryprofile.OrdinaryVisibilityCut{}, ErrInvalid
	}
	message, err := cap.row.message()
	if err != nil {
		return nil, deliveryprofile.OrdinaryVisibilityCut{}, err
	}
	var found *sdkws.MsgData
	for _, branch := range memberApplicationBranches(message) {
		if msgprocessor.GetConversationIDByMsg(branch) == cid {
			if found != nil {
				return nil, deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
			}
			found = branch
		}
	}
	if found == nil {
		return nil, deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
	}
	users := []string{found.SendID}
	if found.RecvID != found.SendID {
		users = append(users, found.RecvID)
	}
	if !msgprocessor.Options(found.Options).IsSenderSync() {
		kept := users[:0]
		for _, uid := range users {
			if uid != found.SendID {
				kept = append(kept, uid)
			}
		}
		users = kept
	}
	cut, err := cap.Cut(cid, users)
	return found, cut, err
}

func (cap *MemberApplicationAcceptedSource) Cut(cid string, users []string) (deliveryprofile.OrdinaryVisibilityCut, error) {
	if cap == nil {
		return deliveryprofile.OrdinaryVisibilityCut{}, ErrInvalid
	}
	frozen, ok := cap.row.Source.Cuts[cid]
	if !ok {
		return deliveryprofile.OrdinaryVisibilityCut{}, ErrConflict
	}
	body, err := json.Marshal(frozen)
	if err != nil {
		return deliveryprofile.OrdinaryVisibilityCut{}, err
	}
	var cut deliveryprofile.OrdinaryVisibilityCut
	if json.Unmarshal(body, &cut) != nil {
		return cut, ErrConflict
	}
	cut.Normal.Owners = map[string]int64{}
	cut.Branch.Owners = map[string]int64{}
	seen := map[string]bool{}
	for _, uid := range users {
		if !validIdentity(uid) || seen[uid] || uid != cap.row.Source.Proof.Command.Actor && uid != cap.row.Source.Proof.Recipient {
			return cut, ErrConflict
		}
		seen[uid] = true
		k := deliveryprofile.SHA([]byte(uid))
		a, ok := frozen.Normal.Owners[k]
		if !ok {
			return cut, ErrConflict
		}
		b, ok := frozen.Branch.Owners[k]
		if !ok {
			return cut, ErrConflict
		}
		cut.Normal.Owners[k], cut.Branch.Owners[k] = a, b
	}
	if !cut.Valid() {
		return cut, ErrConflict
	}
	return cut, nil
}
func (cap *MemberApplicationAcceptedSource) MatchesMessage(message *sdkws.MsgData) bool {
	if cap == nil || message == nil {
		return false
	}
	m := proto.Clone(message).(*sdkws.MsgData)
	m.Seq = 0
	m.SendTime = 0
	m.ServerMsgID = ""
	// SplitStorageMessages intentionally changes notification routing options.
	// Restore only the exact ORIGINAL options, then require the full source hash.
	m.Options = map[string]bool{}
	for k, v := range cap.row.Source.Options {
		m.Options[k] = v
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
	if err != nil || digest(body) != cap.row.Source.Proof.MessageSHA256 {
		return false
	}
	actual := proto.Clone(message).(*sdkws.MsgData)
	actual.Seq = 0
	actual.SendTime = 0
	actual.ServerMsgID = ""
	matched := 0
	for _, branch := range memberApplicationBranches(m) {
		if proto.Equal(branch, actual) {
			matched++
		}
	}
	return matched == 1
}
