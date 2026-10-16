package groupeffects

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// Targets are finalized from the already-authorized snapshot inside the group
// business transaction, per branch. An effect's targets cannot be inferred
// from a later cache, and the visible copy need not have the silent roster.
type NotificationTargets struct {
	BranchID string   `bson:"branch_id" json:"branchID"`
	UserIDs  []string `bson:"user_ids" json:"userIDs"`
}
type notificationDeliveryRow struct {
	Receipt         `bson:",inline"`
	ProducerVersion string                `bson:"producer_version"`
	Targets         []NotificationTargets `bson:"delivery_targets"`
	RosterSHA       string                `bson:"delivery_roster_sha256"`
	DeliveryEpoch   string                `bson:"delivery_epoch"`
	InputSHA        string                `bson:"delivery_input_sha256"`
	ReceiptSHA      string                `bson:"delivery_receipt_sha256"`
}

// NotificationDelivery is a private-store-bound read capability. Its public
// snapshots are cloned; callers cannot manufacture a completion capability by
// supplying an effect-like client message or arbitrary opuser metadata.
type NotificationDelivery struct {
	store *Store
	row   notificationDeliveryRow
}

func (d *NotificationDelivery) Snapshot() (Frozen, []NotificationTargets) {
	if d == nil {
		return Frozen{}, nil
	}
	f := d.row.Frozen
	f.Payload = append([]byte(nil), f.Payload...)
	targets := make([]NotificationTargets, len(d.row.Targets))
	for i, t := range d.row.Targets {
		targets[i] = NotificationTargets{BranchID: t.BranchID, UserIDs: append([]string(nil), t.UserIDs...)}
	}
	return f, targets
}
func (d *NotificationDelivery) Accepted() (bool, string, string, string) {
	if d == nil {
		return false, "", "", ""
	}
	return d.row.State == Done, d.row.DeliveryEpoch, d.row.InputSHA, d.row.ReceiptSHA
}

func receiverSession(ctx context.Context, db *mongo.Database) error {
	if ctx == nil || db == nil {
		return ErrUnavailable
	}
	session := mongo.SessionFromContext(ctx)
	active, ok := session.(mongo.XSession)
	if !ok || session.Client() != db.Client() {
		return ErrUnavailable
	}
	x := active.ClientSession()
	if x == nil || !x.TransactionRunning() || x.CurrentRc == nil || x.CurrentRc.Level != "snapshot" || x.CurrentRp == nil || x.CurrentRp.Mode() != readpref.PrimaryMode || x.CurrentWc == nil || x.CurrentWc.Journal == nil || !*x.CurrentWc.Journal {
		return ErrUnavailable
	}
	w, ok := x.CurrentWc.W.(string)
	if !ok || w != "majority" {
		return ErrUnavailable
	}
	if _, ok := ctx.Deadline(); !ok {
		return ErrUnavailable
	}
	return ctx.Err()
}
func canonicalNotificationTargets(plans []NotificationBranch, input []NotificationTargets) ([]NotificationTargets, string, error) {
	if len(input) != len(plans) || len(input) == 0 {
		return nil, "", ErrInvalid
	}
	expected := map[string]bool{}
	for _, p := range plans {
		expected[p.ID] = true
	}
	seen := map[string]bool{}
	out := make([]NotificationTargets, 0, len(input))
	total := 0
	for _, t := range input {
		if !expected[t.BranchID] || seen[t.BranchID] || len(t.UserIDs) > 50000 {
			return nil, "", ErrInvalid
		}
		seen[t.BranchID] = true
		c := NotificationTargets{BranchID: t.BranchID, UserIDs: append([]string(nil), t.UserIDs...)}
		sort.Strings(c.UserIDs)
		for i, uid := range c.UserIDs {
			if !validIdentity(uid) || i > 0 && uid == c.UserIDs[i-1] {
				return nil, "", ErrInvalid
			}
		}
		total += len(c.UserIDs)
		if total > 100000 {
			return nil, "", ErrInvalid
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BranchID < out[j].BranchID })
	body, err := json.Marshal(out)
	if err != nil {
		return nil, "", ErrInvalid
	}
	return out, digest(body), nil
}

// FreezeNotificationTargets joins Queue in the SAME private group mutation.
// It never reads the future group roster. Every target must belong to the
// exact authorized member/request target scope; this is not a raw-key enqueue.
func (s *Store) FreezeNotificationTargets(ctx context.Context, id string, input []NotificationTargets) error {
	if s == nil || !validID(id) {
		return ErrInvalid
	}
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	if err := s.checkSchema(ctx); err != nil {
		return err
	}
	var r Receipt
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&r); err != nil {
		return unavailable(err)
	}
	if r.validate() != nil || r.Kind != Notification || r.State != Pending || r.Claim != "" || r.Revision >= math.MaxInt64 {
		return ErrConflict
	}
	if err := groupauthority.Require(ctx, s.db, r.GroupID); err != nil {
		return err
	}
	plans, err := PlanNotificationBranches(r.Frozen)
	if err != nil {
		return err
	}
	targets, roster, err := canonicalNotificationTargets(plans, input)
	if err != nil {
		return err
	}
	for _, target := range targets {
		if len(target.UserIDs) > 0 {
			if err := groupauthority.RequireMembers(ctx, s.db, r.GroupID, target.UserIDs); err != nil {
				return err
			}
		}
	}
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": id, "kind": Notification, "state": Pending, "revision": r.Revision, "payload_sha256": r.PayloadSHA256, "delivery_targets": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"delivery_targets": targets, "delivery_roster_sha256": roster}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}

func (s *Store) ReadNotificationDelivery(ctx context.Context, id string) (*NotificationDelivery, error) {
	if s == nil || ctx == nil || !validID(id) {
		return nil, ErrInvalid
	}
	if err := s.checkSchema(ctx); err != nil {
		return nil, err
	}
	if err := s.RoleOrderReady(ctx, id); err != nil {
		return nil, err
	}
	var row notificationDeliveryRow
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&row); err != nil {
		return nil, unavailable(err)
	}
	// The old minimal receiver cannot silently skip action-frozen callbacks or
	// offline obligations. A policy-aware owned worker is a required next slice.
	if row.ProducerVersion != "" {
		return nil, ErrUnavailable
	}
	if row.Receipt.validate() != nil || row.Kind != Notification || row.Claim != "" || !validSHA(row.RosterSHA) {
		return nil, ErrConflict
	}
	if row.State == Done {
		if !validID(row.DeliveryEpoch) || !validSHA(row.InputSHA) || !validSHA(row.ReceiptSHA) || len(row.Targets) != 0 || len(row.Payload) != 0 {
			return nil, ErrConflict
		}

		proof, _ := json.Marshal([]string{row.DeliveryEpoch, row.InputSHA, row.ReceiptSHA})
		if row.OutcomeSHA256 != digest(proof) {
			return nil, ErrConflict
		}
	} else {
		if row.State != Pending || row.DeliveryEpoch != "" || row.InputSHA != "" || row.ReceiptSHA != "" {
			return nil, ErrConflict
		}
		plans, err := PlanNotificationBranches(row.Frozen)
		if err != nil {
			return nil, err
		}
		targets, roster, err := canonicalNotificationTargets(plans, row.Targets)
		if err != nil || roster != row.RosterSHA {
			return nil, ErrConflict
		}
		row.Targets = targets
	}
	return &NotificationDelivery{store: s, row: row}, nil
}

// AcknowledgeNotificationDelivery is LOCAL durable acceptance, not a WS/vendor
// ACK. The receiver calls it in its same strong transaction as seq/source/duty/
// receipt persistence. Therefore lost commit ACK cannot strand a second raw
// notification payload after clear/delete, or require replaying the group RPC.
func (s *Store) AcknowledgeNotificationDelivery(ctx context.Context, cap *NotificationDelivery, epoch, inputSHA, receiptSHA string) error {
	if cap == nil || cap.store != s || cap.row.State != Pending || !validID(epoch) || !validSHA(inputSHA) || !validSHA(receiptSHA) {
		return ErrInvalid
	}
	if err := receiverSession(ctx, s.db); err != nil {
		return err
	}
	if err := s.checkSchema(ctx); err != nil {
		return err
	}
	proof, _ := json.Marshal([]string{epoch, inputSHA, receiptSHA})
	result, err := s.collection.UpdateOne(ctx, bson.M{"_id": cap.row.ID, "kind": Notification, "state": Pending, "revision": cap.row.Revision, "payload_sha256": cap.row.PayloadSHA256, "delivery_roster_sha256": cap.row.RosterSHA}, bson.M{"$set": bson.M{"state": Done, "delivery_epoch": epoch, "delivery_input_sha256": inputSHA, "delivery_receipt_sha256": receiptSHA, "decision": "receiver-ack", "outcome_sha256": digest(proof), "updated_at": time.Now().UTC()}, "$unset": bson.M{"payload": "", "delivery_targets": ""}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil {
		return unavailable(err)
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
