package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	relation "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// KafkaRecord is built by the owned Kafka consumer from the broker record and
// its validated four named context headers. It is not an HTTP or wire DTO.
// This preserves the existing INTERNAL Kafka producer trust boundary; it does
// not turn opuser metadata into an external authentication proof.
type KafkaRecord struct {
	Topic         string
	Partition     int32
	Offset        int64
	Key           []byte
	Body          []byte
	Actor         string
	OperationID   string
	ContextSHA256 string
}

type kafkaReceipt struct {
	ID            string `bson:"_id"`
	Kind          string `bson:"kind"`
	Epoch         string `bson:"epoch"`
	SourceID      string `bson:"source_id"`
	RecordSHA256  string `bson:"record_sha256"`
	InputSHA256   string `bson:"input_sha256"`
	ReceiptSHA256 string `bson:"receipt_sha256"`
	PolicySHA256  string `bson:"policy_sha256"`
}

func (s *Store) kafkaIdentity(r KafkaRecord) (string, string, error) {
	if s == nil || s.db == nil || !identity(r.Topic) || r.Partition < 0 || r.Offset < 0 || len(r.Key) == 0 || len(r.Key) > 2048 || len(r.Body) == 0 || len(r.Body) > 4<<20 || !identity(r.Actor) || r.OperationID != "" && !identity(r.OperationID) || !validSHA(r.ContextSHA256) {
		return "", "", ErrInvalid
	}
	// Only source position determines identity. Different bytes/actor
	// at that position are a conflict, not permission to allocate another ID.
	// Current policy is NOT source identity: a committed replay keeps the first
	// frozen policy rather than retagging it after a configuration change.
	position, _ := json.Marshal([]any{s.epoch, s.db.Name(), r.Topic, r.Partition, r.Offset})
	namespace, err := uuid.Parse(s.epoch)
	if err != nil {
		return "", "", ErrInvalid
	}
	source := uuid.NewSHA1(namespace, position).String()
	proof, _ := json.Marshal([]any{hash(position), hash(r.Key), hash(r.Body), r.Actor, r.OperationID, r.ContextSHA256})
	return source, hash(proof), nil
}

// AcceptKafkaRecord preserves the original raw source proof but never retains
// a second plaintext intake. On replay it resolves the minimal receipt BEFORE
// looking at a current roster or reconstructing message bodies. Ordinary and
// stored notification branches call the SAME Accept allocator/slot writer.
// The current runtime startup remains fenced until policy-aware workers,
// nonpersistent n_ delivery and migration are all actually implemented.
func (s *Store) AcceptKafkaRecord(ctx context.Context, r KafkaRecord, policySHA string, policies ...KafkaDeliveryPolicy) (Receipt, error) {
	if ctx == nil || s == nil || s.db == nil || !s.membershipRequired || !validSHA(policySHA) || mcontext.GetOpUserID(ctx) != r.Actor || mongo.SessionFromContext(ctx) != nil {
		return Receipt{}, ErrUnprepared
	}
	source, recordSHA, err := s.kafkaIdentity(r)
	if err != nil {
		return Receipt{}, err
	}
	if err := s.CheckReady(ctx); err != nil {
		return Receipt{}, err
	}
	var accepted Receipt
	err = s.transaction(ctx, func(tx context.Context) error {
		prior, err := s.readKafkaReceipt(tx, source, recordSHA)
		if err == nil {
			accepted = prior
			return nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		// A core receipt without its same-TX Kafka proof is corruption, not an
		// invitation to adopt or re-tag an old duty.
		err = s.db.Collection(Receipts).FindOne(tx, bson.M{"_id": key(s.epoch, "source", source)}).Err()
		if err == nil {
			return ErrConflict
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		if len(policies) != 1 {
			return ErrUnprepared
		}
		if err := s.freezeKafkaPolicy(tx, policies[0], policySHA); err != nil {
			return err
		}
		var message sdkws.MsgData
		if proto.Unmarshal(r.Body, &message) != nil || message.Seq != 0 || message.SendTime <= 0 || message.CreateTime <= 0 || !identity(message.SendID) || !identity(message.RecvID) || !identity(message.ServerMsgID) {
			return ErrInvalid
		}
		if err := CheckSupportedMessageContentType(message.ContentType); err != nil {
			return err
		}
		if r.Actor != message.SendID && !authverify.IsManagerUserID(r.Actor) {
			return ErrInvalid
		}
		application, err := s.applicationKafkaSource(tx, &message, r)
		if err != nil {
			return err
		}
		roster, err := s.freezeKafkaRoster(tx, &message, r.Actor)
		if err != nil {
			return err
		}
		stored, ephemeral, storedTips, silentTips, modifiers := msgprocessor.SplitStorageMessages([]*sdkws.MsgData{&message})
		if len(modifiers) != 0 {
			return ErrUnsupportedModifier
		} // Legacy modifiers require separate raw custody, never projection.
		envelope := Envelope{ID: source}
		ordinaryCuts := map[string]deliveryprofile.OrdinaryVisibilityCut{}
		appendBranch := func(messages []*sdkws.MsgData, persist bool) error {
			for _, m := range messages {
				body, err := proto.MarshalOptions{Deterministic: true}.Marshal(m)
				if err != nil {
					return ErrInvalid
				}
				recipients, err := roster.recipients(m)
				if err != nil {
					return err
				}
				if !msgprocessor.Options(m.Options).IsSenderSync() {
					filtered := recipients[:0]
					for _, uid := range recipients {
						if uid != m.SendID {
							filtered = append(filtered, uid)
						}
					}
					recipients = filtered
				}
				branchID := kafkaBranchID(source, len(envelope.Branches))
				if application != nil {
					cut, err := application.BranchCut(m, recipients)
					if err != nil {
						return err
					}
					ordinaryCuts[branchID] = cut
				} else if !persist {
					cut, err := deliveryprofile.CaptureOrdinaryVisibilityCut(tx, s.db, s.epoch, s.activationSHA, m, recipients)
					if err != nil {
						return err
					}
					ordinaryCuts[branchID] = cut
				}
				envelope.Branches = append(envelope.Branches, Branch{ID: branchID, ConversationID: msgprocessor.GetConversationIDByMsg(m), Payload: body, Recipients: recipients, Persist: persist})
			}
			return nil
		}
		if err := appendBranch(stored, true); err != nil {
			return err
		}
		if err := appendBranch(ephemeral, false); err != nil {
			return err
		}
		if err := appendBranch(storedTips, true); err != nil {
			return err
		}
		if err := appendBranch(silentTips, false); err != nil {
			return err
		}
		frozen, inputSHA, err := freezeEnvelope(envelope)
		if err != nil {
			return err
		}
		accepted, err = s.acceptSourceEnvelope(tx, frozen, false, nil)
		if err != nil {
			return err
		}
		receiptBody, err := json.Marshal([]any{accepted, policySHA})
		if err != nil {
			return ErrConflict
		}
		var applicationBinding *groupeffects.MemberApplicationAcceptedBinding
		if application != nil {
			binding, err := application.AcceptedBinding(recordSHA, hash(receiptBody))
			if err != nil {
				return err
			}
			applicationBinding = &binding
			if err := application.BindKafkaReceipt(tx, source, recordSHA, hash(receiptBody)); err != nil {
				return err
			}
		}
		// Old policy-blind recovery must NOT pick up a newly accepted duty.
		// Its required version check is part of this still-WIP source closure.
		for _, branch := range accepted.Branches {
			fields := bson.M{"producer_version": "ordinary-kafka-v1", "policy_sha256": policySHA, "source_operation_id": r.OperationID}
			if !branch.Persist || applicationBinding != nil {
				if applicationBinding != nil {
					fields["member_application_source"] = applicationBinding
				}
				cut, ok := ordinaryCuts[branch.ID]
				if !ok {
					return ErrConflict
				}
				fields["ordinary_visibility"] = cut
				fields["ordinary_visibility_sha256"] = deliveryprofile.Digest(cut)
				for _, peer := range accepted.Branches {
					if peer.Persist && peer.ConversationID == cut.Normal.ConversationID && peer.ConversationID != branch.ConversationID {
						fields["stored_peer_cid"] = peer.ConversationID
						fields["stored_peer_seq"] = peer.Seq
					}
				}
			}
			result, err := s.db.Collection(Effects).UpdateOne(tx, bson.M{"_id": key(s.epoch, "branch", branch.ID), "epoch": s.epoch, "source_id": source}, bson.M{"$set": fields})
			if err != nil {
				return err
			}
			if result.MatchedCount != 1 {
				return ErrConflict
			}
		}
		body, err := json.Marshal([]any{accepted, policySHA})
		if err != nil {
			return ErrConflict
		}
		_, err = s.db.Collection(Receipts).InsertOne(tx, kafkaReceipt{ID: key(s.epoch, "kafka-source", source), Kind: "kafka-source-v1", Epoch: s.epoch, SourceID: source, RecordSHA256: recordSHA, InputSHA256: inputSHA, ReceiptSHA256: hash(body), PolicySHA256: policySHA})
		return err
	})
	if err != nil {
		return Receipt{}, err
	} // Unknown commit never grants offset ACK.
	return accepted, nil
}

// ResolveKafkaRecord is a bounded, read-only inspection of this SAME owned
// broker coordinate. A miss is unknown, never an instruction to change source
// ID, payload, policy or recipients. A later runtime policy is irrelevant to an
// already committed result: the original duty keeps its frozen policy binding.
func (s *Store) ResolveKafkaRecord(ctx context.Context, r KafkaRecord) (Receipt, error) {
	if ctx == nil || s == nil || s.db == nil || !s.membershipRequired || mongo.SessionFromContext(ctx) != nil || mcontext.GetOpUserID(ctx) != r.Actor {
		return Receipt{}, ErrUnprepared
	}
	source, recordSHA, err := s.kafkaIdentity(r)
	if err != nil {
		return Receipt{}, err
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return Receipt{}, err
	}
	receipt, err := s.readKafkaReceipt(current, source, recordSHA)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return Receipt{}, ErrCommitUnconfirmed
	}
	return receipt, err
}

func (s *Store) readKafkaReceipt(ctx context.Context, source, recordSHA string) (Receipt, error) {
	var raw bson.Raw
	if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "kafka-source", source)}).Decode(&raw); err != nil {
		return Receipt{}, err
	}
	fields, err := raw.Elements()
	if err != nil || len(fields) != 8 {
		return Receipt{}, ErrConflict
	}
	allowed := map[string]bool{"_id": true, "kind": true, "epoch": true, "source_id": true, "record_sha256": true, "input_sha256": true, "receipt_sha256": true, "policy_sha256": true}
	seen := map[string]bool{}
	for _, field := range fields {
		_, stringOK := field.Value().StringValueOK()
		if !allowed[field.Key()] || seen[field.Key()] || !stringOK {
			return Receipt{}, ErrConflict
		}
		seen[field.Key()] = true
	}
	var prior kafkaReceipt
	if bson.Unmarshal(raw, &prior) != nil || prior.ID != key(s.epoch, "kafka-source", source) || prior.Kind != "kafka-source-v1" || prior.Epoch != s.epoch || prior.SourceID != source || prior.RecordSHA256 != recordSHA || !validSHA(prior.PolicySHA256) || !validSHA(prior.InputSHA256) || !validSHA(prior.ReceiptSHA256) {
		return Receipt{}, ErrConflict
	}
	if _, err := s.readKafkaPolicy(ctx, prior.PolicySHA256); err != nil {
		return Receipt{}, err
	}
	var accepted Receipt
	if err := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": key(s.epoch, "source", source)}).Decode(&accepted); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return Receipt{}, ErrConflict
		}
		return Receipt{}, err
	}
	body, err := json.Marshal([]any{accepted, prior.PolicySHA256})
	if err != nil || hash(body) != prior.ReceiptSHA256 || accepted.ID != key(s.epoch, "source", source) || accepted.Epoch != s.epoch || accepted.SourceID != source || accepted.InputSHA256 != prior.InputSHA256 || accepted.AcceptedAt.IsZero() || len(accepted.Branches) < 1 || len(accepted.Branches) > 2 {
		return Receipt{}, ErrConflict
	}
	for i, branch := range accepted.Branches {
		if branch.ID != kafkaBranchID(source, i) || !identity(branch.ConversationID) || branch.Persist && branch.Seq < 1 || !branch.Persist && (branch.Seq != 0 || branch.Hidden) {
			return Receipt{}, ErrConflict
		}
	}
	return accepted, nil
}

func kafkaBranchID(source string, index int) string {
	namespace, err := uuid.Parse(source)
	if err != nil {
		return ""
	}
	return uuid.NewSHA1(namespace, []byte{byte(index)}).String()
}

// This is a same-session authoritative read plus a REAL write to the group
// revision document shared with all scoped role/status writers. It is not a
// read-only role check followed by an unrelated sequence write. No cache key
// or notification-recipient capability is granted to the caller.
func (s *Store) freezeKafkaRoster(ctx context.Context, message *sdkws.MsgData, actor string) (*kafkaRoster, error) {
	if err := s.requireTX(ctx); err != nil {
		return nil, err
	}
	if message.SessionType == constant.SingleChatType || message.SessionType == constant.NotificationChatType {
		roster := []string{message.SendID}
		if message.RecvID != message.SendID {
			roster = append(roster, message.RecvID)
		}
		sort.Strings(roster)
		return &kafkaRoster{users: roster}, nil
	}
	if message.SessionType != constant.SuperGroupChatType && message.SessionType != constant.GroupChatType {
		return nil, ErrInvalid
	}
	if !identity(message.GroupID) || message.RecvID != message.GroupID {
		return nil, ErrInvalid
	}
	var group bson.Raw
	if err := s.db.Collection("group").FindOne(ctx, bson.M{"group_id": message.GroupID}).Decode(&group); err != nil {
		return nil, err
	}
	revision, ok := group.Lookup(groupauthority.RevisionField).Int64OK()
	status, statusOK := group.Lookup("status").Int32OK()
	if !ok || revision < 1 || revision >= math.MaxInt64 || !statusOK {
		return nil, ErrUnprepared
	}
	// MsgRPC already verified the internal notification producer before Kafka.
	// This does not authenticate a public SysMsgType DTO. Typed group effects
	// use their pre/post action roster path, not this legacy-source intake.
	if message.MsgFrom != constant.SysMsgType && message.MsgFrom != constant.UserMsgType {
		return nil, ErrInvalid
	}
	ordinary := message.MsgFrom != constant.SysMsgType
	if ordinary {
		if status == constant.GroupStatusDismissed {
			return nil, ErrInvalid
		}
		if !authverify.IsManagerUserID(actor) {
			var sender relation.GroupMemberModel
			if err := s.db.Collection("group_member").FindOne(ctx, bson.M{"group_id": message.GroupID, "user_id": message.SendID}).Decode(&sender); err != nil {
				return nil, err
			}
			if sender.GroupID != message.GroupID || sender.UserID != message.SendID {
				return nil, ErrConflict
			}
			if sender.RoleLevel != constant.GroupOwner && sender.RoleLevel != constant.GroupAdmin && sender.RoleLevel != constant.GroupOrdinaryUsers {
				return nil, ErrInvalid
			}
			// Match the existing send contract: owner is exempt; an admin's
			// personal mute still blocks SEND, while global mute allows admin.
			// This does not restrict that admin's separate management actions.
			if sender.RoleLevel != constant.GroupOwner {
				if sender.MuteEndTime.UnixMilli() >= time.Now().UnixMilli() || status == constant.GroupStatusMuted && sender.RoleLevel != constant.GroupAdmin {
					return nil, ErrInvalid
				}
			}
		}
	}
	result, err := s.db.Collection("group").UpdateOne(ctx, bson.M{"group_id": message.GroupID, groupauthority.RevisionField: revision}, bson.M{"$inc": bson.M{groupauthority.RevisionField: int64(1)}})
	if err != nil {
		return nil, err
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return nil, ErrConflict
	}
	cursor, err := s.db.Collection("group_member").Find(ctx, bson.M{"group_id": message.GroupID})
	if err != nil {
		return nil, err
	}
	var members []relation.GroupMemberModel
	err = cursor.All(ctx, &members)
	closeErr := cursor.Close(ctx)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	roster := make([]string, 0, len(members))
	seen := make(map[string]bool, len(members))
	roles := make(map[string]int32, len(members))
	for _, member := range members {
		if member.GroupID != message.GroupID || !identity(member.UserID) || seen[member.UserID] {
			return nil, ErrConflict
		}
		seen[member.UserID] = true
		roles[member.UserID] = member.RoleLevel
		roster = append(roster, member.UserID)
	}
	sort.Strings(roster)
	return &kafkaRoster{groupID: message.GroupID, users: roster, roles: roles}, nil
}
