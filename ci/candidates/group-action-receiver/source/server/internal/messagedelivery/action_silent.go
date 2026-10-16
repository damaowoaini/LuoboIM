package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

func (s *Store) validateSilentRecipient(ctx context.Context, d deliveryDuty, user string, fence bool) error {
	if d.ProducerVersion != ownedGroupProducer || d.ActionCut == nil || !identity(user) {
		return ErrUnprepared
	}
	for _, denied := range d.VisibilityDenied {
		if denied == hash([]byte(user)) {
			return ErrConflict
		}
	}
	check := func(tx context.Context) error {
		denied, err := deliveryprofile.CompareBranchVisibilityCut(tx, s.db, s.epoch, s.activationSHA, *d.ActionCut, d.CID, []string{user}, d.Seq, d.StoredPeerSeq, fence)
		if err != nil {
			return err
		}
		if denied[user] {
			return ErrConflict
		}
		return nil
	}
	if mongo.SessionFromContext(ctx) != nil {
		return check(ctx)
	}
	return s.transaction(ctx, check)
}
func (s *Store) refreshDeliveryVisibility(ctx context.Context, branch string) error {
	d, err := s.readDutyRecord(ctx, branch)
	if err != nil {
		return err
	}
	if d.Persist {
		if err := s.RefreshVisibility(ctx, branch); err != nil {
			return err
		}
		if d.ProducerVersion != ownedGroupProducer {
			return nil
		}
	}
	if d.ProducerVersion != ownedGroupProducer || d.ActionCut == nil {
		return ErrUnprepared
	}
	return s.transaction(ctx, func(tx context.Context) error {
		current, err := s.readDutyRecord(tx, branch)
		if err != nil {
			return err
		}
		if current.Hidden || !current.PushPending {
			return nil
		}
		deniedSet := map[string]bool{}
		for _, h := range current.VisibilityDenied {
			deniedSet[h] = true
		}
		for start := 0; start < len(current.Recipients); start += 64 {
			page := current.Recipients[start:min(start+64, len(current.Recipients))]
			denied, err := deliveryprofile.CompareBranchVisibilityCut(tx, s.db, s.epoch, s.activationSHA, *current.ActionCut, current.CID, page, current.Seq, current.StoredPeerSeq, false)
			if err != nil {
				return err
			}
			for uid, value := range denied {
				if value {
					deniedSet[hash([]byte(uid))] = true
				}
			}
		}
		all := len(current.Recipients) > 0
		for _, uid := range current.Recipients {
			all = all && deniedSet[hash([]byte(uid))]
		}
		exact := bson.M{"_id": current.ID, "revision": current.Revision, "payload_sha256": current.PayloadSHA}
		if all {
			return s.retirePayload(tx, exact)
		}
		if len(deniedSet) == len(current.VisibilityDenied) {
			return nil
		}
		values := make([]string, 0, len(deniedSet))
		for h := range deniedSet {
			values = append(values, h)
		}
		changed, err := s.db.Collection(Effects).UpdateOne(tx, exact, bson.M{"$set": bson.M{"visibility_denied": values}, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (s *Store) validateSilentOnlineCall(ctx context.Context, d deliveryDuty, call OnlineCall) error {
	if d.Persist || d.Seq != 0 || d.ProducerVersion != ownedGroupProducer || d.ActionCut == nil {
		return ErrInvalid
	}
	end := min(d.OnlineCursor+64, d.RecipientCount)
	if d.OnlineCursor >= end {
		return ErrConflict
	}
	page := map[string]bool{}
	for _, uid := range d.Recipients[d.OnlineCursor:end] {
		page[uid] = true
	}
	seen := map[string]bool{}
	for _, uid := range call.Recipients {
		if !page[uid] || seen[uid] {
			return ErrConflict
		}
		seen[uid] = true
		if err := s.validateSilentRecipient(ctx, d, uid, mongo.SessionFromContext(ctx) != nil); err != nil {
			return err
		}
	}
	return nil
}

// Called by the concrete gateway adapter AFTER configured callbacks/discovery
// and before its first gateway RPC. Known TX commit is attempt start. A crash
// after this commit leaves dispatching and can never replay Seq0 automatically.
func (s *Store) AcquireOwnedSilentPhysical(ctx context.Context, call OnlineCall) error {
	if call.Message == nil || call.Message.Seq != 0 {
		return ErrInvalid
	}
	return s.AcquireOwnedActionPhysical(ctx, call)
}
func (s *Store) AcquireOwnedActionPhysical(ctx context.Context, call OnlineCall) error {
	if s == nil || ctx == nil || call.Message == nil || call.Message.Seq < 0 {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx context.Context) error {
		if err := s.ValidateKafkaOnlineCall(tx, call); err != nil {
			return err
		}
		d, err := s.readDutyRecord(tx, call.BranchID)
		if err != nil {
			return err
		}
		if d.ProducerVersion != ownedGroupProducer || d.ActionCut == nil || d.OnlineInstance != s.instanceID || d.VendorRevision >= math.MaxInt64-1 {
			return ErrOnlineUnconfirmed
		}
		for _, uid := range call.Recipients {
			if err = s.validateSilentRecipient(tx, d, uid, true); err != nil {
				return err
			}
		}
		if d.Persist {
			filter := bson.M{"_id": d.ID, "revision": d.Revision, "online_claim": call.ClaimID, "online_instance": s.instanceID}
			if d.VendorRevision == 0 {
				filter["$or"] = bson.A{bson.M{"vendor_revision": int64(0)}, bson.M{"vendor_revision": bson.M{"$exists": false}}}
			} else {
				filter["vendor_revision"] = d.VendorRevision
			}
			changed, err := s.db.Collection(Effects).UpdateOne(tx, filter, bson.M{"$inc": bson.M{"vendor_revision": int64(1)}})
			if err != nil {
				return err
			}
			if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
				return ErrConflict
			}
			return nil
		}
		if d.SilentState != "prepared" {
			return ErrOnlineUnconfirmed
		}
		changed, err := s.db.Collection(Effects).UpdateOne(tx, bson.M{"_id": d.ID, "revision": d.Revision, "online_claim": call.ClaimID, "online_instance": s.instanceID, "silent_state": "prepared"}, bson.M{"$set": bson.M{"silent_state": "dispatching"}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
}
func (s *Store) recoverOwnedSilentOnline(ctx context.Context, branch string, transport KafkaOnlineTransport) error {
	if err := s.refreshDeliveryVisibility(ctx, branch); err != nil {
		return err
	}
	var d deliveryDuty
	var call OnlineCall
	var targets []string
	claim, err := uuid.NewRandom()
	if err != nil {
		return ErrUnprepared
	}
	err = s.transaction(ctx, func(tx context.Context) error {
		var err error
		d, err = s.readDuty(tx, branch)
		if err != nil {
			return err
		}
		if !d.OnlinePending {
			return nil
		}
		if d.Persist || d.Seq != 0 || d.Hidden || d.MaxPending || d.SenderReadPending || d.OnlineAttempts >= math.MaxInt64-1 {
			return ErrConflict
		}
		if d.SilentState == "dispatching" || d.SilentState == "unconfirmed" {
			return ErrOnlineUnconfirmed
		}
		if d.SilentState != "" && d.SilentState != "prepared" && d.SilentState != "pending-not-dispatched" && d.SilentState != "pending-confirmed" {
			return ErrConflict
		}
		if d.OnlineClaim != "" && time.Now().Before(d.OnlineLeaseUntil) {
			return ErrOnlineBusy
		}
		end := min(d.OnlineCursor+64, d.RecipientCount)
		if d.OnlineCursor >= end {
			return ErrConflict
		}
		page := d.Recipients[d.OnlineCursor:end]
		targets = nil
		for _, uid := range page {
			if validSHA(d.OnlineACKs[hash([]byte(uid))]) {
				continue
			}
			if err := s.validateSilentRecipient(tx, d, uid, true); err != nil {
				if errors.Is(err, ErrConflict) {
					if d.OnlineACKs == nil {
						d.OnlineACKs = map[string]string{}
					}
					d.OnlineACKs[hash([]byte(uid))] = hash([]byte("owned silent recipient permanently suppressed"))
					continue
				}
				return err
			}
			targets = append(targets, uid)
		}
		var message sdkws.MsgData
		if proto.Unmarshal(d.Payload, &message) != nil || message.Seq != 0 {
			return ErrConflict
		}
		changed, err := s.db.Collection(Effects).UpdateOne(tx, bson.M{"_id": d.ID, "revision": d.Revision, "payload_sha256": d.PayloadSHA, "online_pending": true}, bson.M{"$set": bson.M{"online_claim": claim.String(), "online_instance": s.instanceID, "online_lease_until": time.Now().UTC().Add(15 * time.Second), "silent_state": "prepared", "online_acks": d.OnlineACKs}, "$inc": bson.M{"revision": int64(1), "online_attempts": int64(1)}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			return ErrConflict
		}
		d.Revision++
		d.OnlineClaim = claim.String()
		d.OnlineInstance = s.instanceID
		call = OnlineCall{BranchID: branch, ConversationID: d.CID, Message: &message, Recipients: append([]string(nil), targets...), PolicySHA256: d.PolicySHA, ClaimID: claim.String(), ClaimRevision: d.Revision}
		return nil
	})
	if err != nil {
		return err
	}
	if !d.OnlinePending {
		return nil
	}
	report := OnlineReport{}
	if len(targets) > 0 {
		report, err = transport.Deliver(ctx, call)
	}
	return s.finishOwnedSilentOnline(ctx, d, targets, report, err)
}
func (s *Store) finishOwnedSilentOnline(ctx context.Context, d deliveryDuty, targets []string, report OnlineReport, transportErr error) error {
	current, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	pending := false
	err := s.transaction(current, func(tx context.Context) error {
		row, err := s.readDutyRecord(tx, d.BranchID)
		if err != nil {
			return err
		}
		if row.Revision != d.Revision || row.OnlineClaim != d.OnlineClaim || row.OnlineInstance != s.instanceID {
			return ErrConflict
		}
		allowed := map[string]bool{}
		for _, uid := range targets {
			allowed[uid] = true
		}
		acks := row.OnlineACKs
		if acks == nil {
			acks = map[string]string{}
		}
		seen := map[string]bool{}
		for _, ack := range report.ACKs {
			if !report.DispatchStarted || !allowed[ack.UserID] || seen[ack.UserID] || !validSHA(ack.ResponseSHA256) {
				return ErrConflict
			}
			seen[ack.UserID] = true
			acks[hash([]byte(ack.UserID))] = ack.ResponseSHA256
		}
		set := bson.M{"online_acks": acks}
		unset := bson.M{}
		routeSeen := map[string]bool{}
		for _, route := range report.Routes {
			if !report.DispatchStarted || !allowed[route.UserID] || routeSeen[route.UserID] || !validSHA(route.ResponseSHA256) {
				return ErrConflict
			}
			routeSeen[route.UserID] = true
			set["offline_routing."+hash([]byte(route.UserID))] = offlineRouteProof{ResponseSHA256: route.ResponseSHA256, Required: !route.OnlineAccepted && route.UserID != row.Sender, BackgroundWebPC: route.BackgroundWebPC}
		}
		for uid := range seen {
			if !routeSeen[uid] {
				return ErrConflict
			}
		}
		end := min(row.OnlineCursor+64, row.RecipientCount)
		all := true
		for _, uid := range row.Recipients[row.OnlineCursor:end] {
			if !validSHA(acks[hash([]byte(uid))]) {
				all = false
			}
		}
		if all {
			set["online_cursor"] = end
			unset = onlineClaimFields()
			unset["online_acks"] = ""
			delete(set, "online_acks")
			set["silent_state"] = ""
			if end == row.RecipientCount {
				set["online_pending"] = false
				set["push_pending"] = row.OfflinePending
				if !row.OfflinePending {
					set["state"] = "done"
					unset["payload"] = ""
					unset["recipients"] = ""
				}
			}
		} else if !report.DispatchStarted {
			// Only the trusted concrete adapter's explicit0gateway dispatch report can
			// classify known non-application; an arbitrary network error never does.
			set["silent_state"] = "pending-not-dispatched"
			unset = onlineClaimFields()
			pending = true
		} else {
			set["silent_state"] = "unconfirmed"
			pending = true
		}
		changed, err := s.db.Collection(Effects).UpdateOne(tx, bson.M{"_id": row.ID, "revision": row.Revision, "online_claim": d.OnlineClaim, "online_instance": s.instanceID}, bson.M{"$set": set, "$unset": unset, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return errors.Join(ErrOnlineUnconfirmed, err)
	}
	if pending || transportErr != nil {
		return errors.Join(ErrOnlineUnconfirmed, transportErr)
	}
	return nil
}

type OwnedSilentSummary struct {
	BranchID, State, PolicySHA256, PayloadSHA256, ACKsSHA256, ClaimID, InstanceID string
	Revision, Cursor                                                              int64
}

func (s *Store) InspectOwnedSilent(ctx context.Context, branch string) (OwnedSilentSummary, error) {
	if s == nil || ctx == nil {
		return OwnedSilentSummary{}, ErrInvalid
	}
	d, err := s.readDutyRecord(ctx, branch)
	if err != nil {
		return OwnedSilentSummary{}, err
	}
	if d.ProducerVersion != ownedGroupProducer || d.Persist || d.Seq != 0 {
		return OwnedSilentSummary{}, ErrInvalid
	}
	body, _ := json.Marshal(d.OnlineACKs)
	return OwnedSilentSummary{BranchID: branch, State: d.SilentState, PolicySHA256: d.PolicySHA, PayloadSHA256: d.PayloadSHA, ACKsSHA256: hash(body), ClaimID: d.OnlineClaim, InstanceID: d.OnlineInstance, Revision: d.Revision, Cursor: d.OnlineCursor}, nil
}
func (s *Store) ConfirmOwnedSilentNotApplied(ctx context.Context, expected OwnedSilentSummary, evidence string, drain KafkaAttemptDrainProof) error {
	if !validSHA(evidence) || drain.InstanceID != expected.InstanceID || !validSHA(drain.EvidenceSHA256) {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx context.Context) error {
		actual, err := s.InspectOwnedSilent(tx, expected.BranchID)
		if err != nil {
			return err
		}
		if actual != expected || actual.State != "dispatching" && actual.State != "unconfirmed" {
			return ErrConflict
		}
		proof, _ := json.Marshal([]any{"not-applied", evidence, drain})
		changed, err := s.db.Collection(Effects).UpdateOne(tx, bson.M{"_id": key(s.epoch, "branch", expected.BranchID), "revision": expected.Revision, "silent_state": expected.State, "online_claim": expected.ClaimID, "online_instance": expected.InstanceID}, bson.M{"$set": bson.M{"silent_state": "pending-confirmed", "silent_confirmation_sha256": hash(proof)}, "$unset": onlineClaimFields(), "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
}

// A definitive per-recipient gateway outcome may settle an unknown Seq0 page.
// No RPC is sent here. Missing/foreign/duplicate receipts fail the whole CAS;
// old ACKs cannot be overwritten and new ACKs must include their routing proof.
func (s *Store) ConfirmOwnedSilentApplied(ctx context.Context, expected OwnedSilentSummary, report OnlineReport, evidence string, drain KafkaAttemptDrainProof) error {
	if s == nil || ctx == nil || !validSHA(evidence) || drain.InstanceID != expected.InstanceID || !validSHA(drain.EvidenceSHA256) || !report.DispatchStarted {
		return ErrInvalid
	}
	return s.transaction(ctx, func(tx context.Context) error {
		actual, err := s.InspectOwnedSilent(tx, expected.BranchID)
		if err != nil {
			return err
		}
		if actual != expected || actual.State != "dispatching" && actual.State != "unconfirmed" || actual.Revision >= math.MaxInt64-1 {
			return ErrConflict
		}
		row, err := s.readDutyRecord(tx, expected.BranchID)
		if err != nil {
			return err
		}
		if !row.OnlinePending || row.Hidden || len(row.Payload) == 0 || row.OnlineCursor >= row.RecipientCount {
			return ErrConflict
		}
		end := min(row.OnlineCursor+64, row.RecipientCount)
		wanted := map[string]bool{}
		acks := row.OnlineACKs
		if acks == nil {
			acks = map[string]string{}
		}
		for _, uid := range row.Recipients[row.OnlineCursor:end] {
			if !validSHA(acks[hash([]byte(uid))]) {
				wanted[uid] = true
			}
		}
		if len(report.ACKs) != len(wanted) || len(report.Routes) != len(wanted) {
			return ErrInvalid
		}
		seen := map[string]bool{}
		routes := map[string]offlineRouteProof{}
		for _, ack := range report.ACKs {
			if !wanted[ack.UserID] || seen[ack.UserID] || !validSHA(ack.ResponseSHA256) {
				return ErrInvalid
			}
			seen[ack.UserID] = true
			acks[hash([]byte(ack.UserID))] = ack.ResponseSHA256
		}
		for _, route := range report.Routes {
			if !wanted[route.UserID] || routes[route.UserID].ResponseSHA256 != "" || !validSHA(route.ResponseSHA256) {
				return ErrInvalid
			}
			routes[route.UserID] = offlineRouteProof{ResponseSHA256: route.ResponseSHA256, Required: !route.OnlineAccepted && route.UserID != row.Sender, BackgroundWebPC: route.BackgroundWebPC}
		}
		proof, _ := json.Marshal([]any{"applied-original-seq0", expected, evidence, drain, report})
		set := bson.M{"online_cursor": end, "silent_state": "", "silent_confirmation_sha256": hash(proof)}
		for uid, route := range routes {
			set["offline_routing."+hash([]byte(uid))] = route
		}
		unset := onlineClaimFields()
		unset["online_acks"] = ""
		if end == row.RecipientCount {
			set["online_pending"] = false
			set["push_pending"] = row.OfflinePending
			if !row.OfflinePending {
				set["state"] = "done"
				unset["payload"] = ""
				unset["recipients"] = ""
			}
		}
		changed, err := s.db.Collection(Effects).UpdateOne(tx, bson.M{"_id": row.ID, "revision": row.Revision, "silent_state": expected.State, "online_claim": expected.ClaimID, "online_instance": expected.InstanceID}, bson.M{"$set": set, "$unset": unset, "$inc": bson.M{"revision": int64(1)}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
}
