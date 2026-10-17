package messagedelivery

import (
	"context"
	"encoding/json"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

func isMemberApplicationMessage(message *sdkws.MsgData) bool {
	if message == nil {
		return false
	}
	switch message.ContentType {
	case constant.JoinGroupApplicationNotification, constant.GroupApplicationAcceptedNotification, constant.GroupApplicationRejectedNotification:
		return true
	}
	return false
}

func (s *Store) applicationKafkaSource(ctx context.Context, message *sdkws.MsgData, record KafkaRecord) (*groupeffects.MemberApplicationKafkaSource, error) {
	if !isMemberApplicationMessage(message) {
		return nil, nil
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return nil, err
	}
	cap, err := effects.ReadMemberApplicationKafkaSource(ctx, message, record.Actor)
	if err != nil {
		return nil, err
	}
	if !cap.MatchesScope(s.epoch, s.activationSHA) || cap.OperationID() != record.OperationID {
		return nil, ErrConflict
	}
	return cap, nil
}

// Valid only after the original source has bound this SAME Kafka receipt.
// An interleaved allocator can move the peer beyond captured H+1. Such a peer
// is accepted solely by its exact branch in this immutable receipt, never by
// changing SourceHead or relaxing the ordinary ephemeral path's H+1 rule.
func (s *Store) acceptedApplicationBranch(ctx context.Context, d deliveryDuty) (*sdkws.MsgData, error) {
	if d.ApplicationSource == nil || !d.ApplicationSource.Valid() || d.OrdinaryCut == nil || !d.OrdinaryCut.Valid() || deliveryprofile.Digest(*d.OrdinaryCut) != d.OrdinaryCutSHA {
		return nil, ErrConflict
	}
	effects, err := groupeffects.NewStore(s.db)
	if err != nil {
		return nil, err
	}
	cap, err := effects.ReadAcceptedMemberApplicationSource(ctx, *d.ApplicationSource, d.SourceID)
	if err != nil {
		return nil, err
	}
	if !cap.MatchesScope(s.epoch, s.activationSHA, d.SourceOperationID) {
		return nil, ErrConflict
	}
	message, cut, err := cap.Branch(d.CID)
	if err != nil {
		return nil, err
	}
	if deliveryprofile.Digest(cut) != d.OrdinaryCutSHA || d.Sender != message.SendID || int64(len(cut.Normal.Owners)) != d.RecipientCount {
		return nil, ErrConflict
	}
	receipt, err := s.readKafkaReceipt(ctx, d.SourceID, d.ApplicationSource.RecordSHA256)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal([]any{receipt, d.PolicySHA})
	if err != nil || hash(body) != d.ApplicationSource.ReceiptSHA256 || receipt.InputSHA256 != d.InputSHA {
		return nil, ErrConflict
	}
	if len(d.Payload) > 0 {
		var actual sdkws.MsgData
		if proto.Unmarshal(d.Payload, &actual) != nil || actual.Seq != d.Seq || !cap.MatchesMessage(&actual) {
			return nil, ErrConflict
		}
	}
	// readKafkaReceipt independently proves policy/input/receipt hashes.
	matched := 0
	peerCID := ""
	peerSeq := int64(0)
	for _, branch := range receipt.Branches {
		if branch.ID == d.BranchID {
			if branch.ConversationID != d.CID || branch.Seq != d.Seq || branch.Persist != d.Persist || branch.Hidden != d.Hidden && d.Hidden == false {
				return nil, ErrConflict
			}
			matched++
		}
		if branch.Persist && branch.ConversationID == cut.Normal.ConversationID && branch.ConversationID != d.CID {
			if peerCID != "" {
				return nil, ErrConflict
			}
			peerCID, peerSeq = branch.ConversationID, branch.Seq
		}
	}
	if matched != 1 || d.StoredPeerCID != peerCID || d.StoredPeerSeq != peerSeq || peerSeq > 0 && peerSeq <= cut.Normal.SourceHead || d.Persist && d.Seq <= cut.Branch.SourceHead {
		return nil, ErrConflict
	}
	return message, nil
}

func (s *Store) compareApplicationVisibility(ctx context.Context, d deliveryDuty, users []string, fence bool) (map[string]bool, error) {
	message, err := s.acceptedApplicationBranch(ctx, d)
	if err != nil {
		return nil, err
	}
	// The original cut remains authoritative even when the branch was written
	// after Clear. A newly allocated positive sequence cannot renew that ticket.
	return deliveryprofile.CompareOrdinaryVisibilityCut(ctx, s.db, s.epoch, s.activationSHA, *d.OrdinaryCut, message, users, d.StoredPeerSeq, fence)
}
func (s *Store) requireApplicationVisibility(ctx context.Context, d deliveryDuty, users []string) error {
	if d.ApplicationSource == nil {
		return nil
	}
	check := func(tx context.Context) error {
		denied, err := s.compareApplicationVisibility(tx, d, users, true)
		if err != nil {
			return err
		}
		for _, uid := range users {
			if denied[uid] {
				return ErrConflict
			}
		}
		return nil
	}
	if mongo.SessionFromContext(ctx) != nil {
		return check(ctx)
	}
	return s.transaction(ctx, check)
}
