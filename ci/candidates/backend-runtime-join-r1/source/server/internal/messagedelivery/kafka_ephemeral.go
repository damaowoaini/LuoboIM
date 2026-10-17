package messagedelivery

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

// A typed source cut is custody for exactly this ordinary Seq0 branch. It is
// never a group-effect grant and never permits a positive-history read.
func ordinaryEphemeralDuty(d deliveryDuty) bool {
	if d.ProducerVersion != "ordinary-kafka-v1" || d.Persist || d.Seq != 0 || d.OrdinaryCut == nil || !d.OrdinaryCut.Valid() || !validSHA(d.OrdinaryCutSHA) || deliveryprofile.Digest(*d.OrdinaryCut) != d.OrdinaryCutSHA || d.ActionCut != nil || d.ActionProfile != nil || d.MaxPending || d.SenderReadPending || d.CID != d.OrdinaryCut.Branch.ConversationID || int64(len(d.OrdinaryCut.Normal.Owners)) != d.RecipientCount {
		return false
	}
	if d.StoredPeerCID == "" {
		return d.StoredPeerSeq == 0
	}
	// Typed applications must subsequently pass acceptedApplicationBranch against
	// the original source claim and SAME actual Kafka receipt. Generic H+1 stays.
	if d.ApplicationSource != nil {
		return d.ApplicationSource.Valid() && d.StoredPeerCID == d.OrdinaryCut.Normal.ConversationID && d.StoredPeerCID != d.CID && d.StoredPeerSeq > d.OrdinaryCut.Normal.SourceHead
	}
	return d.StoredPeerCID == d.OrdinaryCut.Normal.ConversationID && d.StoredPeerCID != d.CID && d.StoredPeerSeq == d.OrdinaryCut.Normal.SourceHead+1
}
func silentDutySupported(d deliveryDuty) bool {
	return d.ProducerVersion == ownedGroupProducer && d.ActionCut != nil || ordinaryEphemeralDuty(d) || d.ApplicationSource != nil && d.ApplicationSource.Valid()
}
func (s *Store) compareSilentVisibility(ctx context.Context, d deliveryDuty, users []string, fence bool) (map[string]bool, error) {
	if d.ApplicationSource != nil {
		return s.compareApplicationVisibility(ctx, d, users, fence)
	}
	if d.ProducerVersion == ownedGroupProducer && d.ActionCut != nil {
		return s.compareOwnedDutyVisibility(ctx, d, users, fence)
	}
	if !ordinaryEphemeralDuty(d) {
		return nil, ErrUnprepared
	}
	var message sdkws.MsgData
	if proto.Unmarshal(d.Payload, &message) != nil || message.Seq != 0 || !d.OrdinaryCut.Matches(&message) {
		return nil, ErrConflict
	}
	return deliveryprofile.CompareOrdinaryVisibilityCut(ctx, s.db, s.epoch, s.activationSHA, *d.OrdinaryCut, &message, users, d.StoredPeerSeq, fence)
}

func (s *Store) RecoverKafkaEphemeralOnline(ctx context.Context, branch string, transport KafkaOnlineTransport) error {
	if s == nil || !s.membershipRequired || ctx == nil || transport == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return err
	}
	d, err := s.readDutyRecord(current, branch)
	if err != nil {
		return err
	}
	if !ordinaryEphemeralDuty(d) || transport.PolicyFingerprint() != d.PolicySHA {
		return ErrUnprepared
	}
	current = context.WithValue(current, kafkaRecoveryKey{}, kafkaRecoveryScope{store: s, branch: branch, policy: d.PolicySHA})
	// The existing private Seq0 engine owns one frozen page, durable physical
	// start, ACK/routes and the exact original unknown claim. No Project call.
	return s.recoverOwnedSilentOnline(current, branch, transport)
}
func (s *Store) AcquireKafkaEphemeralPhysical(ctx context.Context, call OnlineCall) error {
	if call.Message == nil || call.Message.Seq != 0 {
		return ErrInvalid
	}
	return s.acquireSilentPhysical(ctx, call, true)
}
func (s *Store) RecoverKafkaEphemeralOffline(ctx context.Context, branch string, transport KafkaOfflineTransport) error {
	// Seq0 explicitly has no history reader; the internal mode guard prevents
	// entering the persisted branch with this nil reader.
	return s.recoverOffline(ctx, branch, nil, transport, false, true)
}

func (s *Store) ListKafkaEphemeralOnlinePending(ctx context.Context, after string, limit int64) ([]KafkaOnlinePending, error) {
	return s.listKafkaEphemeralPending(ctx, after, limit, "online_pending")
}
func (s *Store) ListKafkaEphemeralOfflinePending(ctx context.Context, after string, limit int64) ([]KafkaOnlinePending, error) {
	return s.listKafkaEphemeralPending(ctx, after, limit, "offline_pending")
}
func (s *Store) listKafkaEphemeralPending(ctx context.Context, after string, limit int64, kind string) ([]KafkaOnlinePending, error) {
	if s == nil || !s.membershipRequired || ctx == nil || mongo.SessionFromContext(ctx) != nil || limit < 1 || limit > 32 || after != "" && !validSHA(after) || kind != "online_pending" && kind != "offline_pending" {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return nil, err
	}
	filter := bson.M{"epoch": s.epoch, "producer_version": "ordinary-kafka-v1", "persist": false, "state": "ready", kind: true}
	if after != "" {
		filter["_id"] = bson.M{"$gt": after}
	}
	cursor, err := s.db.Collection(Effects).Find(current, filter, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(limit).SetProjection(bson.M{"branch_id": 1}))
	if err != nil {
		return nil, err
	}
	out := []KafkaOnlinePending{}
	for cursor.Next(current) {
		var row struct {
			Branch string `bson:"branch_id"`
		}
		if cursor.Decode(&row) != nil {
			err = ErrConflict
			break
		}
		d, e := s.readDutyRecord(current, row.Branch)
		if e != nil {
			err = e
			break
		}
		if !ordinaryEphemeralDuty(d) || d.Hidden {
			err = ErrUnprepared
			break
		}
		out = append(out, KafkaOnlinePending{BranchID: d.BranchID, PolicySHA256: d.PolicySHA, Cursor: d.ID})
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(current)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	return out, nil
}

func (s *Store) RequiredKafkaOnlinePolicies(ctx context.Context) ([]string, error) {
	return s.requiredKafkaPolicies(ctx, "online_pending")
}
func (s *Store) requiredKafkaPolicies(ctx context.Context, kind string) ([]string, error) {
	if s == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || kind != "online_pending" && kind != "offline_pending" {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return nil, err
	}
	cursor, err := s.db.Collection(Effects).Find(current, bson.M{"epoch": s.epoch, "producer_version": "ordinary-kafka-v1", kind: true}, options.Find().SetBatchSize(128).SetProjection(bson.M{"branch_id": 1}))
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for cursor.Next(current) {
		var row struct {
			Branch string `bson:"branch_id"`
		}
		if cursor.Decode(&row) != nil {
			err = ErrConflict
			break
		}
		d, e := s.readDutyRecord(current, row.Branch)
		if e != nil {
			err = e
			break
		}
		// Old Seq0 rows without a same-TX source cut need a separate original-source
		// migration. Startup never invents a new cut from today's roster/history.
		if d.Hidden || d.State != "ready" || !d.Persist && !ordinaryEphemeralDuty(d) {
			err = ErrUnprepared
			break
		}
		seen[d.PolicySHA] = true
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(current)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	out := make([]string, 0, len(seen))
	for policy := range seen {
		out = append(out, policy)
	}
	sort.Strings(out)
	return out, nil
}

// Operator settlement names remain separate from owned-group capability APIs.
// A local timeout/drain alone is never a definitive recipient outcome.
func (s *Store) InspectKafkaEphemeral(ctx context.Context, branch string) (OwnedSilentSummary, error) {
	return s.inspectSilent(ctx, branch, true)
}
func (s *Store) ConfirmKafkaEphemeralNotApplied(ctx context.Context, expected OwnedSilentSummary, evidence string, drain KafkaAttemptDrainProof) error {
	return s.confirmSilentNotApplied(ctx, expected, evidence, drain, true)
}
func (s *Store) ConfirmKafkaEphemeralApplied(ctx context.Context, expected OwnedSilentSummary, report OnlineReport, evidence string, drain KafkaAttemptDrainProof) error {
	return s.confirmSilentApplied(ctx, expected, report, evidence, drain, true)
}
