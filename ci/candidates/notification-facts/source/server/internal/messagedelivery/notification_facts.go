package messagedelivery

import (
	"context"
	"encoding/json"
	"math"
	"strconv"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"google.golang.org/protobuf/proto"
)

// Facts ONLY. This data type deliberately has no Allows/Authorize/Ready method.
// A delivery recipient is NOT asserted to have historical message-read rights.
const ownedNotificationFactsKind = "owned-notification-facts-v1"
const ownedNotificationFactsPageKind = "owned-notification-facts-page-v1"
const ownedNotificationFactsPageBytes = 32 * 1024

type notificationFactBlob struct {
	Label  string `bson:"label"`
	SHA256 string `bson:"sha256"`
	Bytes  int64  `bson:"bytes"`
	Pages  int64  `bson:"pages"`
}
type notificationFacts struct {
	ID                 string                 `bson:"_id"`
	Kind               string                 `bson:"kind"`
	FactsOnly          bool                   `bson:"facts_only"`
	Database           string                 `bson:"database"`
	Epoch              string                 `bson:"epoch"`
	Activation         string                 `bson:"activation_sha256"`
	SourceID           string                 `bson:"source_id"`
	SourceOperationID  string                 `bson:"source_operation_id"`
	CommandID          string                 `bson:"command_id"`
	Action             string                 `bson:"action"`
	GroupID            string                 `bson:"group_id"`
	Actor              string                 `bson:"actor"`
	Producer           string                 `bson:"producer_version"`
	BranchID           string                 `bson:"branch_id"`
	CID                string                 `bson:"conversation_id"`
	Seq                int64                  `bson:"seq"`
	Hidden             bool                   `bson:"accepted_hidden"`
	OriginKind         string                 `bson:"origin_kind"`
	OriginSHA          string                 `bson:"origin_sha256"`
	NormalCID          string                 `bson:"normal_cid"`
	NormalPositionKind string                 `bson:"normal_position_kind"`
	NormalPosition     int64                  `bson:"normal_position"`
	SourceInputSHA     string                 `bson:"source_input_sha256"`
	AcceptedPayloadSHA string                 `bson:"accepted_payload_sha256"`
	ProfileSHA         string                 `bson:"profile_sha256"`
	RecipientCount     int64                  `bson:"recipient_count"`
	Blobs              []notificationFactBlob `bson:"blobs"`
	SHA256             string                 `bson:"sha256"`
}
type notificationFactsPage struct {
	ID        string `bson:"_id"`
	Kind      string `bson:"kind"`
	HeaderID  string `bson:"header_id"`
	HeaderSHA string `bson:"header_sha256"`
	Label     string `bson:"label"`
	Number    int64  `bson:"number"`
	SHA256    string `bson:"sha256"`
	Data      []byte `bson:"data"`
}

func notificationFactsID(epoch, branch string) string {
	return key(epoch, "owned-notification-facts", branch)
}
func notificationFactsPageID(epoch, branch, label string, number int64) string {
	return key(epoch, "owned-notification-facts-page", branch+"\x00"+label+"\x00"+strconv.FormatInt(number, 10))
}
func factBlob(label string, data []byte) notificationFactBlob {
	pages := len(data) / ownedNotificationFactsPageBytes
	if len(data)%ownedNotificationFactsPageBytes != 0 {
		pages++
	}
	return notificationFactBlob{Label: label, SHA256: hash(data), Bytes: int64(len(data)), Pages: int64(pages)}
}

// All values originate in the actual owned source/receipt in this transaction.
// The original cut is serialized losslessly; it is never refreshed to current
// membership. Pages bound document size, not roster size. There is no new total
// member cap, DB scan, new collection/index, endpoint or external operation.
func (s *Store) freezeOwnedNotificationFacts(ctx context.Context, in ownedActionInput, r Receipt, envelope Envelope, denied map[string][]string) error {
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	if in.store != s || in.effects == nil || in.cap == nil || r.SourceID != in.frozen.ID || r.Epoch != s.epoch || envelope.ID != r.SourceID || !validSHA(r.InputSHA256) {
		return ErrConflict
	}
	original, sourceTargets, _, sourceProfile, sourceProducer := in.cap.Snapshot()
	if original.ID != in.frozen.ID || original.PayloadSHA256 != in.frozen.PayloadSHA256 || sourceProfile != in.profileSHA || sourceProducer != in.producer {
		return ErrConflict
	}
	command, ok := in.cap.SourceFacts()
	if !ok || command.Epoch != s.epoch || in.profile.Transport.ActivationSHA256 != s.activationSHA {
		return ErrConflict
	}
	cut := in.cap.VisibilitySnapshot()
	if !cut.Valid() {
		return ErrConflict
	}
	normal, notice, err := groupNamespacePair(in.frozen.GroupID)
	if err != nil || cut.ConversationID != normal || cut.Notification == nil || cut.Notification.ConversationID != notice {
		return ErrConflict
	}
	cutBytes, err := json.Marshal(cut)
	if err != nil {
		return ErrInvalid
	}
	position, positionKind := cut.SourceHead, "original-frozen-head"
	for _, b := range r.Branches {
		if b.ConversationID == normal && b.Persist {
			if b.Seq < 1 || b.Seq == math.MaxInt64 {
				return ErrConflict
			}
			position, positionKind = b.Seq, "accepted-normal-sequence"
		}
	}
	// Preserve a head as a head. Never infer head+1 or compare n_ seq with sg_ upper.
	if position < 0 || position >= math.MaxInt64 {
		return ErrConflict
	}
	for _, accepted := range r.Branches {
		if !accepted.Persist || !msgprocessor.IsNotification(accepted.ConversationID) {
			continue
		}
		if accepted.ConversationID != notice || accepted.Seq < 1 || accepted.Seq == math.MaxInt64 {
			return ErrConflict
		}
		var branch *Branch
		for i := range envelope.Branches {
			if envelope.Branches[i].ID == accepted.ID {
				if branch != nil {
					return ErrConflict
				}
				branch = &envelope.Branches[i]
			}
		}
		if branch == nil || !branch.Persist || branch.ConversationID != notice {
			return ErrConflict
		}
		// Compare with the real cap's exact branch, not its multi-branch cut union.
		var recipients []string
		found := false
		for _, target := range sourceTargets {
			if target.BranchID == accepted.ID {
				if found {
					return ErrConflict
				}
				recipients = target.UserIDs
				found = true
			}
		}
		if !found || len(recipients) != len(branch.Recipients) {
			return ErrConflict
		}
		allowed := map[string]bool{}
		for i, uid := range recipients {
			if !identity(uid) || i > 0 && uid <= recipients[i-1] || uid != branch.Recipients[i] {
				return ErrConflict
			}
			allowed[uid] = true
		}
		for _, uid := range denied[accepted.ID] {
			if !allowed[uid] {
				return ErrConflict
			}
		}
		var message sdkws.MsgData
		if proto.Unmarshal(branch.Payload, &message) != nil {
			return ErrInvalid
		}
		origin, err := messageNamespace(&message)
		if err != nil {
			return err
		}
		if origin.Kind != "group" || origin.GroupID != in.frozen.GroupID || origin.CID != notice {
			return ErrConflict
		}
		if err = s.requireNamespace(ctx, origin); err != nil {
			return err
		}
		blobs := map[string][]byte{"original-cut-json": cutBytes}
		// Exact permanent keys only. namespaceOwner already verifies each shape and
		// the corresponding origin row. Preserve actual BSON, not a made-up claim.
		for label, cid := range map[string]string{"normal-claim-bson": normal, "notification-claim-bson": notice} {
			actual, _, err := s.namespaceOwner(ctx, cid)
			if err != nil {
				return err
			}
			if actual.Kind != origin.Kind || actual.GroupID != origin.GroupID || actual.SHA256 != origin.SHA256 {
				return ErrConflict
			}
			var raw bson.Raw
			if err = s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceClaimID(cid)}).Decode(&raw); err != nil {
				return err
			}
			blobs[label] = append([]byte(nil), raw...)
		}
		var raw bson.Raw
		if err = s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": groupOriginClaimID(origin.GroupID)}).Decode(&raw); err != nil {
			return err
		}
		blobs["group-origin-claim-bson"] = append([]byte(nil), raw...)
		blobs["branch-recipients-json"], err = json.Marshal(recipients)
		if err != nil {
			return ErrInvalid
		}
		blobs["accept-denied-json"], err = json.Marshal(denied[accepted.ID])
		if err != nil {
			return ErrInvalid
		}
		message.Seq = accepted.Seq
		body, err := proto.MarshalOptions{Deterministic: true}.Marshal(&message)
		if err != nil {
			return ErrInvalid
		}
		header := notificationFacts{ID: notificationFactsID(s.epoch, accepted.ID), Kind: ownedNotificationFactsKind, FactsOnly: true, Database: s.db.Name(), Epoch: s.epoch, Activation: s.activationSHA, SourceID: r.SourceID, SourceOperationID: in.frozen.OperationID, CommandID: command.CommandID, Action: command.Action, GroupID: in.frozen.GroupID, Actor: in.frozen.Actor, Producer: in.producer, BranchID: accepted.ID, CID: notice, Seq: accepted.Seq, Hidden: accepted.Hidden, OriginKind: origin.Kind, OriginSHA: origin.SHA256, NormalCID: normal, NormalPositionKind: positionKind, NormalPosition: position, SourceInputSHA: r.InputSHA256, AcceptedPayloadSHA: hash(body), ProfileSHA: in.profileSHA, RecipientCount: int64(len(recipients))}
		for _, label := range []string{"original-cut-json", "branch-recipients-json", "normal-claim-bson", "notification-claim-bson", "group-origin-claim-bson", "accept-denied-json"} {
			header.Blobs = append(header.Blobs, factBlob(label, blobs[label]))
		}
		encoded, err := json.Marshal(header)
		if err != nil {
			return ErrInvalid
		}
		header.SHA256 = hash(encoded)
		if err = s.insertNotificationFacts(ctx, header, blobs); err != nil {
			return err
		}
	}
	return nil
}
func (s *Store) insertNotificationFacts(ctx context.Context, header notificationFacts, blobs map[string][]byte) error {
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	for _, blob := range header.Blobs {
		data, ok := blobs[blob.Label]
		if !ok || factBlob(blob.Label, data) != blob {
			return ErrConflict
		}
		number := int64(0)
		for start := 0; start < len(data); {
			if err := ctx.Err(); err != nil {
				return err
			}
			end := start + min(ownedNotificationFactsPageBytes, len(data)-start)
			chunk := data[start:end]
			page := notificationFactsPage{ID: notificationFactsPageID(s.epoch, header.BranchID, blob.Label, number), Kind: ownedNotificationFactsPageKind, HeaderID: header.ID, HeaderSHA: header.SHA256, Label: blob.Label, Number: number, SHA256: hash(chunk), Data: chunk}
			if _, err := s.db.Collection(Receipts).InsertOne(ctx, page); err != nil {
				return err
			}
			start = end
			number++
		}
		if number != blob.Pages {
			return ErrConflict
		}
	}
	// Header last, but every page/header is in the SAME original accept TX. No
	// partial prefix can be committed on a page/size/timeout/duplicate error.
	_, err := s.db.Collection(Receipts).InsertOne(ctx, header)
	return err
}
