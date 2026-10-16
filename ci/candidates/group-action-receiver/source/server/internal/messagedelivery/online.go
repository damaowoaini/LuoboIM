package messagedelivery

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

var (
	ErrOnlineBusy        = errors.New("reliable online delivery in progress")
	ErrOnlineUnconfirmed = errors.New("reliable online delivery unconfirmed")
)

// OnlineReport ACKs mean actual matching gateway responses, not that a person
// read a message or an offline vendor delivered an alert. Partial/unknown ACKs
// never count as all recipients done. Only a trusted internal adapter supplies
// them; caller metadata is not an ACK factory.
type OnlineACK struct{ UserID, ResponseSHA256 string }

// Routing is distinct from a successful socket write. Only a complete exact
// row from every applicable gateway can establish an offline candidate. A
// failed/unknown RPC supplies neither a routing proof nor a recipient ACK.
type OnlineRouteResult struct {
	UserID, ResponseSHA256 string
	OnlineAccepted         bool
	BackgroundWebPC        bool
}
type OnlineReport struct {
	DispatchStarted bool
	ACKs            []OnlineACK
	Routes          []OnlineRouteResult
}
type OnlineCall struct {
	BranchID, ConversationID string
	Message                  *sdkws.MsgData
	Recipients               []string
	PolicySHA256             string
	ClaimID                  string
	ClaimRevision            int64
}
type OnlineTransport interface {
	Deliver(context.Context, OnlineCall) (OnlineReport, error)
}
type VisibilityReader interface {
	GetMsgBySeqs(context.Context, string, string, []int64) (int64, int64, []*sdkws.MsgData, error)
}

type onlineAttempt struct {
	store   *Store
	duty    deliveryDuty
	claim   string
	targets []string
}

func (s *Store) beginOnline(ctx context.Context, branchID string) (*onlineAttempt, error) {
	if err := s.CheckReady(ctx); err != nil {
		return nil, err
	}
	d, err := s.readDuty(ctx, branchID)
	if err != nil {
		return nil, err
	}
	if !d.OnlinePending {
		return nil, nil
	}
	// Seq0 retries are NOT safe on the legacy SDK. Only the explicit reliable
	// group-effect policy (or an ordinary persistent message) may enter here.
	if !d.Persist || d.Seq < 1 || d.Hidden || d.MaxPending || d.SenderReadPending || d.OnlineAttempts >= math.MaxInt64 {
		return nil, ErrUnprepared
	}
	now := time.Now().UTC()
	if d.OnlineClaim != "" && now.Before(d.OnlineLeaseUntil) {
		return nil, ErrOnlineBusy
	}
	end := min(d.OnlineCursor+64, d.RecipientCount)
	if d.OnlineCursor >= end {
		return nil, ErrConflict
	}
	page := append([]string(nil), d.Recipients[d.OnlineCursor:end]...)
	wantACK := map[string]bool{}
	for _, uid := range page {
		wantACK[hash([]byte(uid))] = true
	}
	for uidHash, ack := range d.OnlineACKs {
		if !wantACK[uidHash] || !validSHA(ack) {
			return nil, ErrConflict
		}
	}
	claim, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrUnprepared
	}
	filter := bson.M{"_id": d.ID, "epoch": s.epoch, "revision": d.Revision, "payload_sha256": d.PayloadSHA, "online_pending": true, "online_cursor": d.OnlineCursor}
	// Missing cursor is the only initial legacy shape of this NEW collection.
	if d.OnlineCursor == 0 {
		filter["$or"] = bson.A{bson.M{"online_cursor": 0}, bson.M{"online_cursor": bson.M{"$exists": false}}}
		delete(filter, "online_cursor")
	}
	res, err := s.db.Collection(Effects).UpdateOne(ctx, filter, bson.M{"$set": bson.M{"online_claim": claim.String(), "online_instance": s.instanceID, "online_lease_until": now.Add(15 * time.Second)}, "$inc": bson.M{"revision": int64(1), "online_attempts": int64(1)}})
	if err != nil {
		return nil, ErrOnlineUnconfirmed
	}
	if res.MatchedCount != 1 || res.ModifiedCount != 1 {
		return nil, ErrOnlineBusy
	}
	d.Revision++
	d.OnlineAttempts++
	d.OnlineClaim = claim.String()
	d.OnlineInstance = s.instanceID
	return &onlineAttempt{store: s, duty: d, claim: claim.String(), targets: page}, nil
}

// RecoverOnline is independent of the old Kafka push age filter. Frozen
// message time/IDs/recipients remain unchanged even when recovery is >10s late.
// A lease/restart can reattempt the SAME positive-seq page: server allocation
// and SDK seq suppression provide logical dedup, not physical exactly-once WS.
// Offline vendors have separate durable obligations and are never called here.
func (s *Store) RecoverOnline(ctx context.Context, branchID string, reader VisibilityReader, transport OnlineTransport) error {
	if ctx == nil || reader == nil || transport == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.RefreshVisibility(current, branchID); err != nil {
		return err
	}
	a, err := s.beginOnline(current, branchID)
	if err != nil || a == nil {
		return err
	}
	var message sdkws.MsgData
	if proto.Unmarshal(a.duty.Payload, &message) != nil || message.Seq != a.duty.Seq {
		return a.finish(current, OnlineReport{}, ErrConflict)
	}
	known := map[string]string{}
	for k, v := range a.duty.OnlineACKs {
		known[k] = v
	}
	var targets []string
	for _, uid := range a.targets {
		denied := false
		for _, h := range a.duty.VisibilityDenied {
			if h == hash([]byte(uid)) {
				denied = true
			}
		}
		if denied {
			known[hash([]byte(uid))] = hash([]byte("durable-visibility-suppressed\x00" + uid + "\x00" + a.duty.PayloadSHA))
			continue
		}
		if _, ok := known[hash([]byte(uid))]; ok {
			continue
		}
		_, _, visible, err := reader.GetMsgBySeqs(current, uid, a.duty.CID, []int64{a.duty.Seq})
		if err != nil {
			return a.finish(current, OnlineReport{}, err)
		}
		found := false
		for _, m := range visible {
			if m == nil {
				continue
			}
			stored, currentMessage := proto.Clone(m).(*sdkws.MsgData), proto.Clone(&message).(*sdkws.MsgData)
			stored.IsRead = false
			currentMessage.IsRead = false
			if proto.Equal(stored, currentMessage) {
				found = true
			}
		}
		if !found {
			known[hash([]byte(uid))] = hash([]byte("authoritative-visibility-suppressed\x00" + uid + "\x00" + a.duty.PayloadSHA))
			continue
		}
		targets = append(targets, uid)
	}
	var report OnlineReport
	if len(targets) > 0 {
		if err := a.checkOnlineClaim(current, targets); err != nil {
			return a.finish(current, OnlineReport{}, err)
		}
		call := OnlineCall{BranchID: branchID, ConversationID: a.duty.CID, Message: proto.Clone(&message).(*sdkws.MsgData), Recipients: append([]string(nil), targets...), PolicySHA256: a.duty.PolicySHA, ClaimID: a.claim, ClaimRevision: a.duty.Revision}
		report, err = transport.Deliver(current, call)
	}
	// The transport cannot acknowledge a recipient outside this exact attempt.
	allowed := map[string]bool{}
	for _, uid := range targets {
		allowed[uid] = true
	}
	seen := map[string]bool{}
	for _, ack := range report.ACKs {
		if !report.DispatchStarted || !allowed[ack.UserID] || seen[ack.UserID] || !validSHA(ack.ResponseSHA256) {
			return a.finish(current, OnlineReport{}, ErrConflict)
		}
		seen[ack.UserID] = true
		known[hash([]byte(ack.UserID))] = ack.ResponseSHA256
	}
	routeSeen := map[string]bool{}
	for _, route := range report.Routes {
		if !report.DispatchStarted || !allowed[route.UserID] || routeSeen[route.UserID] || !validSHA(route.ResponseSHA256) {
			return a.finish(current, OnlineReport{}, ErrConflict)
		}
		routeSeen[route.UserID] = true
	}
	merged := OnlineReport{DispatchStarted: report.DispatchStarted, Routes: report.Routes}
	for _, uid := range a.targets {
		if ack, ok := known[hash([]byte(uid))]; ok {
			merged.ACKs = append(merged.ACKs, OnlineACK{UserID: uid, ResponseSHA256: ack})
		}
	}
	return a.finish(current, merged, err)
}

func (a *onlineAttempt) finish(parent context.Context, report OnlineReport, deliveryErr error) error {
	if a == nil || a.store == nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	acks := map[string]string{}
	for k, v := range a.duty.OnlineACKs {
		acks[k] = v
	}
	for _, ack := range report.ACKs {
		acks[hash([]byte(ack.UserID))] = ack.ResponseSHA256
	}
	all := true
	for _, uid := range a.targets {
		if !validSHA(acks[hash([]byte(uid))]) {
			all = false
		}
	}
	set := bson.M{"online_acks": acks, "online_last_unconfirmed": deliveryErr != nil || !all}
	unset := bson.M{"online_claim": "", "online_instance": "", "online_lease_until": ""}
	if a.duty.ProducerVersion == "ordinary-kafka-v1" || a.duty.ProducerVersion == ownedGroupProducer {
		// Per-field updates preserve routing decisions from earlier pages.
		// Never turn a missing route report into a negative online result.
		for _, route := range report.Routes {
			set["offline_routing."+hash([]byte(route.UserID))] = offlineRouteProof{ResponseSHA256: route.ResponseSHA256, Required: !route.OnlineAccepted && route.UserID != a.duty.Sender, BackgroundWebPC: route.BackgroundWebPC}
		}
	}
	if all {
		cursor := a.duty.OnlineCursor + int64(len(a.targets))
		set["online_cursor"] = cursor
		unset["online_acks"] = ""
		delete(set, "online_acks")
		if cursor == a.duty.RecipientCount {
			set["online_pending"] = false
			set["push_pending"] = a.duty.OfflinePending
			if !a.duty.OfflinePending && !a.duty.MaxPending && !a.duty.SenderReadPending {
				set["state"] = "done"
				unset["payload"] = ""
				unset["recipients"] = ""
			}
		}
	}
	res, err := a.store.db.Collection(Effects).UpdateOne(current, bson.M{"_id": a.duty.ID, "epoch": a.store.epoch, "revision": a.duty.Revision, "payload_sha256": a.duty.PayloadSHA, "online_claim": a.claim, "online_instance": a.store.instanceID}, bson.M{"$set": set, "$unset": unset, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return ErrOnlineUnconfirmed
	}
	if res.MatchedCount != 1 || res.ModifiedCount != 1 {
		return ErrConflict
	}
	if deliveryErr != nil || !all {
		return errors.Join(ErrOnlineUnconfirmed, deliveryErr)
	}
	return nil
}
