package groupeffects

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	pbmsg "github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

const memberApplicationDirectKind = "member-application-direct-v1"

var ErrMemberApplicationDispatchUnconfirmed = errors.New("member application notification source ACK unconfirmed; inspect original intent, do not resend")

type memberApplicationDirectSource struct {
	Proof       groupauthority.MemberApplicationMessageInfo      `bson:"proof"`
	Operation   string                                           `bson:"operation"`
	ServiceUser string                                           `bson:"service_user"`
	Activation  string                                           `bson:"activation"`
	Options     map[string]bool                                  `bson:"options"`
	Cuts        map[string]deliveryprofile.OrdinaryVisibilityCut `bson:"cuts"`
	CIDs        []string                                         `bson:"cids"`
}
type memberApplicationDirect struct {
	ID              string                        `bson:"_id"`
	Kind            string                        `bson:"kind"`
	Source          memberApplicationDirectSource `bson:"source"`
	SourceSHA       string                        `bson:"source_sha256"`
	Payload         []byte                        `bson:"payload"`
	Redacted        bool                          `bson:"redacted"`
	State           string                        `bson:"state"`
	Revision        int64                         `bson:"revision"`
	Claim           string                        `bson:"claim"`
	AttemptedAt     time.Time                     `bson:"attempted_at"`
	ServerMsgID     string                        `bson:"server_msg_id"`
	SendTime        int64                         `bson:"send_time"`
	KafkaSourceID   string                        `bson:"kafka_source_id"`
	KafkaRecordSHA  string                        `bson:"kafka_record_sha256"`
	KafkaReceiptSHA string                        `bson:"kafka_receipt_sha256"`
}
type MemberApplicationDirectStatus struct {
	ID, CommandID, OperationID, ClientMsgID, Sender, Recipient, Applicant, State, ClaimID, ServerMsgID, KafkaSourceID, KafkaRecordSHA, KafkaReceiptSHA string
	SendTime                                                                                                                                           int64
	AttemptedAt                                                                                                                                        time.Time
	PayloadRedacted                                                                                                                                    bool
}

func (row memberApplicationDirect) message() (*sdkws.MsgData, error) {
	var message sdkws.MsgData
	if row.Kind != memberApplicationDirectKind || !validID(row.ID) || row.Revision < 1 || row.Revision >= math.MaxInt64-1 || !validSHA(row.SourceSHA) || !validID(row.Source.Proof.Command.ID) || !validID(row.Source.Proof.Command.Epoch) || !validIdentity(row.Source.Proof.Command.Actor) || !validIdentity(row.Source.Proof.Command.GroupID) || !validIdentity(row.Source.Proof.Applicant) || !validIdentity(row.Source.Proof.Recipient) || !validIdentity(row.Source.ServiceUser) || !validIdentity(row.Source.Operation) || !validSHA(row.Source.Activation) || !validSHA(row.Source.Proof.MessageSHA256) {
		return nil, ErrConflict
	}
	if row.Redacted {
		if len(row.Payload) != 0 || row.State == "pending" {
			return nil, ErrConflict
		}
		message = sdkws.MsgData{ClientMsgID: row.ID, SendID: row.Source.Proof.Command.Actor, RecvID: row.Source.Proof.Recipient, MsgFrom: constant.SysMsgType, SessionType: constant.SingleChatType, CreateTime: row.Source.Proof.Command.CreatedAt.UnixMilli(), Options: row.Source.Options}
		switch row.Source.Proof.Kind {
		case groupauthority.MemberApplicationCreated:
			message.ContentType = constant.JoinGroupApplicationNotification
		case groupauthority.MemberApplicationAccepted:
			message.ContentType = constant.GroupApplicationAcceptedNotification
		case groupauthority.MemberApplicationRejected:
			message.ContentType = constant.GroupApplicationRejectedNotification
		default:
			return nil, ErrConflict
		}
	} else if len(row.Payload) == 0 || len(row.Payload) > 4<<20 || proto.Unmarshal(row.Payload, &message) != nil || digest(row.Payload) != row.Source.Proof.MessageSHA256 {
		return nil, ErrConflict
	}
	body, err := json.Marshal(row.Source)
	if err != nil || digest(body) != row.SourceSHA || row.ID != message.ClientMsgID || message.SendID != row.Source.Proof.Command.Actor || message.RecvID != row.Source.Proof.Recipient || message.GroupID != "" || message.SessionType != constant.SingleChatType || message.MsgFrom != constant.SysMsgType || message.Seq != 0 || message.SendTime != 0 || message.ServerMsgID != "" {
		return nil, ErrConflict
	}
	plans := memberApplicationBranches(&message)
	if len(plans) != len(row.Source.Cuts) || len(plans) == 0 {
		return nil, ErrConflict
	}
	if len(row.Source.CIDs) != 2 || row.Source.CIDs[0] >= row.Source.CIDs[1] {
		return nil, ErrConflict
	}
	for _, plan := range plans {
		cut, ok := row.Source.Cuts[msgprocessor.GetConversationIDByMsg(plan)]
		if !ok || !cut.Matches(plan) {
			return nil, ErrConflict
		}
	}
	for _, cid := range row.Source.CIDs {
		found := false
		for _, cut := range row.Source.Cuts {
			if cid == cut.Normal.ConversationID || cid == cut.Branch.ConversationID {
				found = true
			}
		}
		if !found {
			return nil, ErrConflict
		}
	}
	if row.KafkaSourceID == "" {
		if row.KafkaRecordSHA != "" || row.KafkaReceiptSHA != "" {
			return nil, ErrConflict
		}
	} else if !validID(row.KafkaSourceID) || !validSHA(row.KafkaRecordSHA) || !validSHA(row.KafkaReceiptSHA) {
		return nil, ErrConflict
	}
	expected := int32(constant.JoinGroupApplicationNotification)
	switch row.Source.Proof.Kind {
	case groupauthority.MemberApplicationCreated:
	case groupauthority.MemberApplicationAccepted:
		expected = constant.GroupApplicationAcceptedNotification
	case groupauthority.MemberApplicationRejected:
		expected = constant.GroupApplicationRejectedNotification
	default:
		return nil, ErrConflict
	}
	if message.ContentType != expected {
		return nil, ErrConflict
	}
	switch row.State {
	case "pending":
		if row.Revision != 1 || row.Claim != "" || !row.AttemptedAt.IsZero() || row.ServerMsgID != "" || row.SendTime != 0 {
			return nil, ErrConflict
		}
	case "dispatching", "unknown":
		if !validID(row.Claim) || row.AttemptedAt.IsZero() || row.ServerMsgID != "" || row.SendTime != 0 {
			return nil, ErrConflict
		}
	case "source-acked":
		if !validID(row.Claim) || row.AttemptedAt.IsZero() || !validIdentity(row.ServerMsgID) || row.SendTime <= 0 {
			return nil, ErrConflict
		}
	case "suppressed":
		if row.Claim != "" || row.ServerMsgID != "" || row.SendTime != 0 {
			return nil, ErrConflict
		}
	default:
		return nil, ErrConflict
	}
	return &message, nil
}

// Call only AFTER the same-TX namespace Store has accepted cap.InspectMessage
// and reserved actual normal/n pair heads. No absent counter is guessed here.
func (s *Store) QueueMemberApplicationDirect(ctx context.Context, cap *groupauthority.MemberApplicationScope, message *sdkws.MsgData, serviceUser, activation, operation string) error {
	if s == nil || cap == nil || !validIdentity(serviceUser) || !authverify.IsManagerUserID(serviceUser) || !validSHA(activation) {
		return ErrInvalid
	}
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	if err := s.checkSchema(ctx); err != nil {
		return err
	}
	proof, err := cap.InspectMessage(ctx, s.db, message)
	if err != nil {
		return err
	}
	if operation == "" {
		operation = proof.Command.ID
	}
	if !validIdentity(operation) {
		return ErrInvalid
	}
	users := []string{message.SendID}
	if message.RecvID != message.SendID {
		users = append(users, message.RecvID)
	}
	cuts := map[string]deliveryprofile.OrdinaryVisibilityCut{}
	for _, branch := range memberApplicationBranches(message) {
		cid := msgprocessor.GetConversationIDByMsg(branch)
		if _, exists := cuts[cid]; exists {
			return ErrInvalid
		}
		cut, err := deliveryprofile.CaptureOrdinaryVisibilityCut(ctx, s.db, proof.Command.Epoch, activation, branch, users)
		if err != nil {
			return err
		}
		cuts[cid] = cut
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return err
	}
	cids := []string{msgprocessor.GetChatConversationIDByMsg(message), msgprocessor.GetNotificationConversationIDByMsg(message)}
	sort.Strings(cids)
	source := memberApplicationDirectSource{Proof: proof, Operation: operation, ServiceUser: serviceUser, Activation: activation, Options: message.Options, Cuts: cuts, CIDs: cids}
	encoded, err := json.Marshal(source)
	if err != nil {
		return err
	}
	row := memberApplicationDirect{ID: message.ClientMsgID, Kind: memberApplicationDirectKind, Source: source, SourceSHA: digest(encoded), Payload: body, State: "pending", Revision: 1}
	if _, err := row.message(); err != nil {
		return err
	}
	_, err = s.collection.InsertOne(ctx, row)
	return err
}

func (s *Store) readMemberApplicationDirect(ctx context.Context, id string) (memberApplicationDirect, error) {
	var row memberApplicationDirect
	var raw bson.Raw
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&raw); err != nil {
		return row, err
	}
	fields, err := raw.Elements()
	if err != nil || len(fields) != 15 {
		return row, ErrConflict
	}
	seen := map[string]bool{}
	for _, f := range fields {
		if seen[f.Key()] {
			return row, ErrConflict
		}
		seen[f.Key()] = true
		switch f.Key() {
		case "_id", "kind", "source_sha256", "state", "claim", "server_msg_id", "kafka_source_id", "kafka_record_sha256", "kafka_receipt_sha256":
			if _, ok := f.Value().StringValueOK(); !ok {
				return row, ErrConflict
			}
		case "revision", "send_time":
			if _, ok := f.Value().Int64OK(); !ok {
				return row, ErrConflict
			}
		case "attempted_at":
			if _, ok := f.Value().DateTimeOK(); !ok {
				return row, ErrConflict
			}
		case "source":
			if _, ok := f.Value().DocumentOK(); !ok {
				return row, ErrConflict
			}
		case "payload":
			if _, _, ok := f.Value().BinaryOK(); !ok {
				return row, ErrConflict
			}
		case "redacted":
			if _, ok := f.Value().BooleanOK(); !ok {
				return row, ErrConflict
			}
		default:
			return row, ErrConflict
		}
	}
	if bson.Unmarshal(raw, &row) != nil || row.ID != id {
		return row, ErrConflict
	}
	if _, err := row.message(); err != nil {
		return row, err
	}
	return row, nil
}
func (s *Store) InspectMemberApplicationDirect(ctx context.Context, id string) (MemberApplicationDirectStatus, error) {
	if s == nil || ctx == nil || !validID(id) {
		return MemberApplicationDirectStatus{}, ErrInvalid
	}
	row, err := s.readMemberApplicationDirect(ctx, id)
	if err != nil {
		return MemberApplicationDirectStatus{}, err
	}
	p := row.Source.Proof
	return MemberApplicationDirectStatus{ID: row.ID, CommandID: p.Command.ID, OperationID: row.Source.Operation, ClientMsgID: row.ID, Sender: p.Command.Actor, Recipient: p.Recipient, Applicant: p.Applicant, State: row.State, ClaimID: row.Claim, ServerMsgID: row.ServerMsgID, SendTime: row.SendTime, AttemptedAt: row.AttemptedAt, KafkaSourceID: row.KafkaSourceID, KafkaRecordSHA: row.KafkaRecordSHA, KafkaReceiptSHA: row.KafkaReceiptSHA, PayloadRedacted: row.Redacted}, nil
}
func (s *Store) ListMemberApplicationDirect(ctx context.Context, after string, limit int64) ([]MemberConversationPending, error) {
	if s == nil || ctx == nil || after != "" && !validID(after) || limit < 1 || limit > 64 {
		return nil, ErrInvalid
	}
	filter := bson.M{"kind": memberApplicationDirectKind, "state": "pending"}
	if after != "" {
		filter["_id"] = bson.M{"$gt": after}
	}
	cur, err := s.collection.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit).SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	var rows []struct {
		ID string `bson:"_id"`
	}
	err = cur.All(ctx, &rows)
	closed := cur.Close(ctx)
	if err != nil || closed != nil {
		return nil, errors.Join(err, closed)
	}
	result := make([]MemberConversationPending, 0, len(rows))
	for _, row := range rows {
		if !validID(row.ID) {
			return nil, ErrConflict
		}
		result = append(result, MemberConversationPending{ID: row.ID, Cursor: row.ID})
	}
	return result, nil
}

// At most one physical RPC attempt after the durable claim ACK. A claim whose
// commit ACK was lost is never used to send; a second inspection sees unknown.
// The real SendMsg path retains its own callbacks/risk/MQ/receipt contracts.
func (s *Store) DeliverMemberApplicationDirect(ctx context.Context, id string, send func(context.Context, *pbmsg.SendMsgReq) (*pbmsg.SendMsgResp, error)) error {
	if s == nil || !validID(id) || send == nil {
		return ErrInvalid
	}
	var claimed memberApplicationDirect
	var outgoing *sdkws.MsgData
	err := s.memberProjectionTransaction(ctx, func(tx context.Context) error {
		// WithTransaction may retry an aborted body. Only the final committed
		// attempt may leave an outgoing physical operation in this invocation.
		claimed, outgoing = memberApplicationDirect{}, nil
		row, err := s.readMemberApplicationDirect(tx, id)
		if err != nil {
			return err
		}
		if row.State == "source-acked" || row.State == "suppressed" {
			return nil
		}
		if row.State != "pending" {
			return ErrMemberApplicationDispatchUnconfirmed
		}
		message, err := row.message()
		if err != nil {
			return err
		}
		users := []string{message.SendID}
		if message.RecvID != message.SendID {
			users = append(users, message.RecvID)
		}
		cut, ok := row.Source.Cuts[msgprocessor.GetConversationIDByMsg(message)]
		if !ok {
			return ErrConflict
		}
		denied, err := deliveryprofile.CompareOrdinaryVisibilityCut(tx, s.db, row.Source.Proof.Command.Epoch, row.Source.Activation, cut, message, users, 0, true)
		if err != nil {
			return err
		}
		allowed := !denied[message.RecvID]
		p := row.Source.Proof
		if allowed && p.Recipient != p.Command.Actor && !(p.Kind == groupauthority.MemberApplicationCreated && p.Recipient == p.Applicant) {
			var group relation.GroupModel
			var groupRaw bson.Raw
			if err = s.db.Collection("group").FindOne(tx, bson.M{"group_id": p.Command.GroupID}).Decode(&groupRaw); err != nil {
				return err
			}
			if bson.Unmarshal(groupRaw, &group) != nil {
				return ErrConflict
			}
			revision, valid := groupRaw.Lookup(groupauthority.RevisionField).Int64OK()
			if !valid || revision < 1 || revision >= math.MaxInt64-1 {
				return ErrConflict
			}
			cursor, err := s.db.Collection("group_member").Find(tx, bson.M{"group_id": p.Command.GroupID, "user_id": p.Recipient}, options.Find().SetLimit(2))
			if err != nil {
				return err
			}
			var members []relation.GroupMemberModel
			err = cursor.All(tx, &members)
			closed := cursor.Close(tx)
			if err != nil || closed != nil {
				return errors.Join(err, closed)
			}
			if len(members) > 1 {
				return ErrConflict
			}
			allowed = false
			if len(members) == 1 {
				member := members[0]
				allowed = group.GroupID == p.Command.GroupID && group.Status != constant.GroupStatusDismissed && member.GroupID == p.Command.GroupID && member.UserID == p.Recipient && (member.RoleLevel == constant.GroupOwner || member.RoleLevel == constant.GroupAdmin)
			}
			if allowed {
				result, err := s.db.Collection("group").UpdateOne(tx, bson.M{"group_id": group.GroupID, groupauthority.RevisionField: revision}, bson.M{"$inc": bson.M{groupauthority.RevisionField: int64(1)}})
				if err != nil {
					return err
				}
				if result.MatchedCount != 1 || result.ModifiedCount != 1 {
					return ErrConflict
				}
			}
		}
		if !allowed {
			update, err := s.collection.UpdateOne(tx, bson.M{"_id": id, "state": "pending", "revision": row.Revision, "source_sha256": row.SourceSHA}, bson.M{"$set": bson.M{"state": "suppressed", "redacted": true, "payload": []byte{}}, "$inc": bson.M{"revision": int64(1)}})
			if err != nil {
				return err
			}
			if update.MatchedCount != 1 || update.ModifiedCount != 1 {
				return ErrConflict
			}
			return nil
		}
		if !authverify.IsManagerUserID(row.Source.ServiceUser) {
			return ErrUnavailable
		}
		claim, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		stamp := time.Now().UTC().Truncate(time.Millisecond)
		update, err := s.collection.UpdateOne(tx, bson.M{"_id": id, "state": "pending", "revision": row.Revision, "source_sha256": row.SourceSHA}, bson.M{"$set": bson.M{"state": "dispatching", "claim": claim.String(), "attempted_at": stamp}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if update.MatchedCount != 1 || update.ModifiedCount != 1 {
			return ErrConflict
		}
		row.State, row.Claim, row.AttemptedAt, row.Revision = "dispatching", claim.String(), stamp, row.Revision+1
		claimed, outgoing = row, message
		return nil
	})
	if err != nil {
		return err
	}
	if outgoing == nil {
		return nil
	}
	work, cancel := context.WithTimeout(ctx, 15*time.Second)
	work = context.WithValue(work, constant.OpUserID, claimed.Source.ServiceUser)
	work = context.WithValue(work, constant.OperationID, claimed.Source.Operation)
	response, dispatchErr := send(work, &pbmsg.SendMsgReq{MsgData: proto.Clone(outgoing).(*sdkws.MsgData)})
	cancel()
	acked := dispatchErr == nil && response != nil && response.ClientMsgID == outgoing.ClientMsgID && validIdentity(response.ServerMsgID) && response.SendTime > 0
	// A completed attempt is recorded even if the worker was canceled while the
	// RPC was returning. This bounded original receipt write performs no resend.
	end, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	err = s.memberProjectionTransaction(end, func(tx context.Context) error {
		row, err := s.readMemberApplicationDirect(tx, id)
		if err != nil {
			return err
		}
		if row.SourceSHA != claimed.SourceSHA || row.Claim != claimed.Claim || row.State != "dispatching" && row.State != "unknown" {
			return ErrConflict
		}
		values := bson.M{"state": "unknown"}
		if acked {
			values = bson.M{"state": "source-acked", "server_msg_id": response.ServerMsgID, "send_time": response.SendTime}
		}
		update, err := s.collection.UpdateOne(tx, bson.M{"_id": id, "state": row.State, "claim": claimed.Claim, "revision": row.Revision, "source_sha256": row.SourceSHA}, bson.M{"$set": values, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if update.MatchedCount != 1 || update.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !acked {
		return errors.Join(ErrMemberApplicationDispatchUnconfirmed, dispatchErr)
	}
	return nil
}
