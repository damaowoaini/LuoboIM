package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"sync/atomic"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// One original branch/configuration has one callback, independently of the
// worker's64-recipient delivery pages. The original source/history is immutable.
type KafkaOfflineCallbackInput struct {
	Binding, FailureMode, OperationID, InputSHA256 string
	Message                                        *sdkws.MsgData
	Users                                          []string
}
type KafkaOfflineCallbackEngine interface {
	PolicyFingerprint() string
	CheckReady(context.Context) error
	ExecuteOffline(context.Context, KafkaOfflineCall) error
	Close() error
}
type KafkaOfflinePreparingTransport interface {
	KafkaOfflineTransport
	PrepareOffline(context.Context, KafkaOfflineCall) error
}
type offlineCallbackRow struct {
	PreviousSnapshot string   `bson:"previous_snapshot_sha256,omitempty" json:"PreviousSnapshot,omitempty"`
	ID               string   `bson:"_id"`
	Kind             string   `bson:"kind"`
	Epoch            string   `bson:"epoch"`
	Branch           string   `bson:"branch_id"`
	Policy           string   `bson:"policy_sha256"`
	Binding          string   `bson:"binding"`
	InputSHA         string   `bson:"input_sha256"`
	BodySHA          string   `bson:"body_sha256"`
	OutputSHA        string   `bson:"output_sha256"`
	Mode             string   `bson:"failure_mode"`
	State            string   `bson:"state"`
	Revision         int64    `bson:"revision"`
	Attempt          string   `bson:"attempt_id"`
	Instance         string   `bson:"instance_id"`
	Allowed          []string `bson:"allowed_user_sha256"`
	Defaults         []string `bson:"default_user_sha256"`
	Selected         []string `bson:"selected_user_sha256"`
	Info             []byte   `bson:"offline_info,omitempty"`
	ReplaceInfo      bool     `bson:"replace_info"`
	Erased           bool     `bson:"output_erased"`
	OutcomeSHA       string   `bson:"outcome_sha256"`
}

func offlineHashes(users []string) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, hash([]byte(u)))
	}
	sort.Strings(out)
	return out
}
func exactSHASet(values []string) bool {
	for i, v := range values {
		if !validSHA(v) || i > 0 && v <= values[i-1] {
			return false
		}
	}
	return true
}
func containsSHA(values []string, value string) bool {
	i := sort.SearchStrings(values, value)
	return i < len(values) && values[i] == value
}
func offlineOutputSHA(selected []string, replace bool, info []byte) string {
	b, _ := json.Marshal([]any{selected, replace, info})
	return hash(b)
}
func validOfflineCallbackRow(r offlineCallbackRow) bool {
	if r.Kind != "kafka-offline-callback-v1" || !validUUID(r.Epoch) || !validUUID(r.Branch) || !validSHA(r.Policy) || !validSHA(r.Binding) || !validSHA(r.InputSHA) || !validSHA(r.BodySHA) || !validUUID(r.Attempt) || !validUUID(r.Instance) || r.Revision < 1 || r.Revision >= math.MaxInt64 || !exactSHASet(r.Allowed) || !exactSHASet(r.Defaults) || !exactSHASet(r.Selected) {
		return false
	}
	if r.PreviousSnapshot != "" && !validSHA(r.PreviousSnapshot) {
		return false
	}
	if !r.Erased && (!r.ReplaceInfo && len(r.Info) > 0 || len(r.Info) > 4<<20) {
		return false
	}
	if r.Mode != "default-stop" && r.Mode != "explicit-stop" && r.Mode != "continue" {
		return false
	}
	for _, u := range append(append([]string(nil), r.Defaults...), r.Selected...) {
		if !containsSHA(r.Allowed, u) {
			return false
		}
	}
	switch r.State {
	case "dispatching":
		return r.OutputSHA == "" && r.OutcomeSHA == ""
	case "pending-confirmed", "pending-not-dispatched", "unconfirmed", "rejected":
		return validSHA(r.OutcomeSHA) && r.OutputSHA == ""
	case "acknowledged", "continued-unknown":
		if r.State == "continued-unknown" && r.Mode != "continue" {
			return false
		}
		return validSHA(r.OutcomeSHA) && validSHA(r.OutputSHA) && (r.Erased || r.OutputSHA == offlineOutputSHA(r.Selected, r.ReplaceInfo, r.Info))
	default:
		return false
	}
}
func (s *Store) offlineCallbackRow(ctx context.Context, d deliveryDuty) (offlineCallbackRow, error) {
	var r offlineCallbackRow
	id := key(s.epoch, "kafka-offline-callback", d.BranchID)
	if err := s.db.Collection(Effects).FindOne(ctx, bson.M{"_id": id}).Decode(&r); err != nil {
		return r, err
	}
	if !d.OfflineCallbackFrozen {
		return r, ErrConflict
	}
	p, err := s.readKafkaPolicy(ctx, d.PolicySHA)
	if err != nil {
		return r, err
	}
	if !validOfflineCallbackRow(r) || r.ID != id || r.Epoch != s.epoch || r.Branch != d.BranchID || r.Policy != d.PolicySHA || r.Binding != p.OfflinePushBinding || r.Mode != p.OfflineFailureMode {
		return r, ErrConflict
	}
	return r, nil
}
func (s *Store) offlineCallbackInput(ctx context.Context, d deliveryDuty) (KafkaOfflineCallbackInput, []string, error) {
	var out KafkaOfflineCallbackInput
	p, err := s.readKafkaPolicy(ctx, d.PolicySHA)
	if err != nil {
		return out, nil, err
	}
	if p.OfflinePushBinding == "" {
		return out, nil, nil
	}
	if d.Hidden || !d.Persist || d.Seq < 1 || d.OnlinePending || !d.OfflinePending {
		return out, nil, ErrConflict
	}
	var m sdkws.MsgData
	if proto.Unmarshal(d.Payload, &m) != nil {
		return out, nil, ErrConflict
	}
	allowed := make([]string, 0, len(d.Recipients))
	defaults := make([]string, 0, len(d.Recipients))
	// Read the whole original frozen roster in bounded pages, in the SAME TX as
	// the final parent write fence. This is read permission, never writer scope.
	for start := 0; start < len(d.Recipients); start += 64 {
		page := d.Recipients[start:min(start+64, len(d.Recipients))]
		denied, err := s.membershipPage(ctx, d.CID, page, d.Seq)
		if err != nil {
			return out, nil, err
		}
		for _, u := range page {
			h := hash([]byte(u))
			hidden := denied[u]
			for _, v := range d.VisibilityDenied {
				if v == h {
					hidden = true
				}
			}
			if hidden {
				continue
			}
			allowed = append(allowed, u)
			route, ok := d.OfflineRouting[h]
			if !ok {
				return out, nil, ErrConflict
			}
			if route.Required {
				defaults = append(defaults, u)
			}
		}
	}
	b, _ := json.Marshal([]any{d.Epoch, d.BranchID, d.PayloadSHA, d.PolicySHA, p.OfflinePushBinding, offlineHashes(allowed), offlineHashes(defaults)})
	return KafkaOfflineCallbackInput{Binding: p.OfflinePushBinding, FailureMode: p.OfflineFailureMode, OperationID: d.SourceOperationID, InputSHA256: hash(b), Message: proto.Clone(&m).(*sdkws.MsgData), Users: defaults}, offlineHashes(allowed), nil
}

// A known-zero-HTTP retry may only NARROW the first frozen read cap. It
// cannot regain a formerly denied member after leave/rejoin or a visibility
// cut. Unknown/dispatching outcomes do not get a new snapshot or request.
func (s *Store) preparedOfflineCallbackInput(ctx context.Context, d deliveryDuty) (KafkaOfflineCallbackInput, []string, error) {
	input, allowed, err := s.offlineCallbackInput(ctx, d)
	if err != nil || input.Binding == "" {
		return input, allowed, err
	}
	old, err := s.offlineCallbackRow(ctx, d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if d.OfflineCallbackFrozen {
			return input, nil, ErrConflict
		}
		return input, allowed, nil
	}
	if err != nil {
		return input, nil, err
	}
	if old.Erased {
		return input, nil, ErrConflict
	}
	switch old.State {
	case "pending-not-dispatched", "pending-confirmed":
		narrowed := make([]string, 0, len(allowed))
		for _, h := range allowed {
			if containsSHA(old.Allowed, h) {
				narrowed = append(narrowed, h)
			}
		}
		users := make([]string, 0, len(input.Users))
		for _, u := range input.Users {
			h := hash([]byte(u))
			if containsSHA(old.Defaults, h) && containsSHA(narrowed, h) {
				users = append(users, u)
			}
		}
		input.Users = users
		body, _ := json.Marshal([]any{d.Epoch, d.BranchID, d.PayloadSHA, d.PolicySHA, input.Binding, narrowed, offlineHashes(users)})
		input.InputSHA256 = hash(body)
		return input, narrowed, nil
	case "acknowledged", "continued-unknown":
		return input, allowed, nil
	case "rejected":
		return input, nil, ErrKafkaPushCallbackRejected
	default:
		return input, nil, ErrKafkaPushCallbackUnconfirmed
	}
}

// Durable minimal audit chain: every old zero-HTTP request identity is retained
// in the same transaction as its replacement, without body/Info/user plaintext.
func (s *Store) archiveOfflineCallbackSnapshot(ctx context.Context, old offlineCallbackRow) (string, error) {
	summary := summarizeOfflineCallback(old)
	body, _ := json.Marshal([]any{"kafka-offline-callback-snapshot-v1", s.epoch, old.Branch, summary})
	receiptSHA := hash(body)
	_, err := s.db.Collection(Effects).InsertOne(ctx, bson.M{"_id": key(s.epoch, "kafka-offline-callback-snapshot", receiptSHA), "kind": "kafka-offline-callback-snapshot-v1", "epoch": s.epoch, "branch_id": old.Branch, "snapshot_sha256": receiptSHA, "previous_snapshot_sha256": old.PreviousSnapshot, "summary": summary})
	return receiptSHA, err
}

func (s *Store) KafkaOfflineCallbackInput(ctx context.Context, call KafkaOfflineCall) (KafkaOfflineCallbackInput, error) {
	d, err := s.boundKafkaOfflineDuty(ctx, call)
	if err != nil {
		return KafkaOfflineCallbackInput{}, err
	}
	// The first snapshot is only request preparation. Begin repeats it inside
	// the strong transaction; a changed snapshot grants no physical permission.
	input, _, err := s.preparedOfflineCallbackInput(ctx, d)
	return input, err
}

type KafkaOfflineCallbackDispatch struct {
	store *Store
	row   offlineCallbackRow
	began atomic.Bool
}

func (s *Store) BeginKafkaOfflineCallback(ctx context.Context, call KafkaOfflineCall, inputSHA, bodySHA string) (*KafkaOfflineCallbackDispatch, error) {
	if s == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || !validSHA(inputSHA) || !validSHA(bodySHA) {
		return nil, ErrInvalid
	}
	attempt, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrUnprepared
	}
	var claim offlineCallbackRow
	grant := false
	err = s.transaction(ctx, func(tx context.Context) error {
		grant = false
		d, err := s.boundKafkaOfflineDuty(tx, call)
		if err != nil {
			return err
		}
		input, allowed, err := s.preparedOfflineCallbackInput(tx, d)
		if err != nil {
			return err
		}
		if input.Binding == "" {
			return nil
		}
		id := key(s.epoch, "kafka-offline-callback", d.BranchID)
		old, err := s.offlineCallbackRow(tx, d)
		if err == nil {
			switch old.State {
			case "acknowledged", "continued-unknown":
				if old.BodySHA != bodySHA || old.InputSHA != inputSHA {
					return ErrConflict
				}
				return nil
			case "rejected":
				return ErrKafkaPushCallbackRejected
			case "pending-confirmed", "pending-not-dispatched":
			default:
				return ErrKafkaPushCallbackUnconfirmed
			}
		} else if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		} else if d.OfflineCallbackFrozen {
			return ErrConflict
		}
		if input.InputSHA256 != inputSHA || d.CallbackRevision >= math.MaxInt64-1 {
			return ErrConflict
		}
		parent := bson.M{"_id": d.ID, "epoch": s.epoch, "revision": d.Revision, "offline_claim": call.ClaimID, "hidden": false, "payload_sha256": d.PayloadSHA}
		if d.CallbackRevision == 0 {
			parent["$or"] = bson.A{bson.M{"callback_revision": int64(0)}, bson.M{"callback_revision": bson.M{"$exists": false}}}
		} else {
			parent["callback_revision"] = d.CallbackRevision
		}
		changed, err := s.db.Collection(Effects).UpdateOne(tx, parent, bson.M{"$set": bson.M{"offline_callback_frozen": true}, "$inc": bson.M{"callback_revision": int64(1)}})
		if err != nil {
			return err
		}
		if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
			return ErrConflict
		}
		claim = offlineCallbackRow{ID: id, Kind: "kafka-offline-callback-v1", Epoch: s.epoch, Branch: d.BranchID, Policy: d.PolicySHA, Binding: input.Binding, InputSHA: inputSHA, BodySHA: bodySHA, Mode: input.FailureMode, State: "dispatching", Revision: 1, Attempt: attempt.String(), Instance: s.instanceID, Allowed: allowed, Defaults: offlineHashes(input.Users)}
		if old.ID != "" {
			// Reserve one revision for replacing the request and one for its
			// terminal/zero-HTTP result; never grant a permit that cannot settle.
			if old.Revision >= math.MaxInt64-2 {
				return ErrConflict
			}
			if old.State != "pending-confirmed" && old.State != "pending-not-dispatched" {
				return ErrConflict
			}
			priorSHA, err := s.archiveOfflineCallbackSnapshot(tx, old)
			if err != nil {
				return err
			}
			claim.PreviousSnapshot = priorSHA
			claim.Revision = old.Revision + 1
			changed, err = s.db.Collection(Effects).ReplaceOne(tx, bson.M{"_id": id, "revision": old.Revision, "state": old.State, "attempt_id": old.Attempt}, claim)
			if err != nil {
				return err
			}
			if changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
				return ErrConflict
			}
		} else {
			if _, err = s.db.Collection(Effects).InsertOne(tx, claim); err != nil {
				return err
			}
		}
		if len(input.Users) == 0 {
			selected, replace, body, _ := offlineDecision(claim, nil, nil)
			_, err := s.db.Collection(Effects).UpdateOne(tx, bson.M{"_id": id, "revision": claim.Revision, "state": "dispatching"}, bson.M{"$set": bson.M{"state": "acknowledged", "outcome_sha256": hash([]byte("no original offline candidates")), "output_sha256": offlineOutputSHA(selected, replace, body), "selected_user_sha256": selected}, "$inc": bson.M{"revision": int64(1)}})
			return err
		}
		grant = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !grant {
		return nil, nil
	}
	return &KafkaOfflineCallbackDispatch{store: s, row: claim}, nil
}

// The KNOWN Begin commit is the attempt start. The one-shot local gate never
// grants a second HTTP.Do. A Clear before Begin commits conflicts its parent TX.
func (d *KafkaOfflineCallbackDispatch) BeforeHTTP(ctx context.Context, call KafkaOfflineCall, body []byte) error {
	if d == nil || d.store == nil || hash(body) != d.row.BodySHA {
		return ErrInvalid
	}
	if _, err := d.store.boundKafkaOfflineDuty(ctx, call); err != nil {
		return err
	}
	var row offlineCallbackRow
	if err := d.store.db.Collection(Effects).FindOne(ctx, bson.M{"_id": d.row.ID}).Decode(&row); err != nil {
		return err
	}
	if !validOfflineCallbackRow(row) || row.Revision != d.row.Revision || row.Attempt != d.row.Attempt || row.State != "dispatching" || row.BodySHA != d.row.BodySHA || row.Erased {
		return ErrConflict
	}
	if !d.began.CompareAndSwap(false, true) {
		return ErrConflict
	}
	return nil
}
func (d *KafkaOfflineCallbackDispatch) NotDispatched(parent context.Context) error {
	if d == nil || d.store == nil || parent == nil || d.began.Load() {
		return ErrKafkaPushCallbackUnconfirmed
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	changed, err := d.store.db.Collection(Effects).UpdateOne(ctx, bson.M{"_id": d.row.ID, "revision": d.row.Revision, "state": "dispatching", "attempt_id": d.row.Attempt, "instance_id": d.row.Instance}, bson.M{"$set": bson.M{"state": "pending-not-dispatched", "outcome_sha256": hash([]byte("owned adapter proved no HTTP.Do"))}, "$inc": bson.M{"revision": int64(1)}})
	if err != nil || changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
		return ErrKafkaPushCallbackUnconfirmed
	}
	return nil
}
func offlineDecision(r offlineCallbackRow, users []string, info *sdkws.OfflinePushInfo) ([]string, bool, []byte, error) {
	selected := append([]string(nil), r.Defaults...)
	if len(users) > len(r.Allowed) {
		return nil, false, nil, ErrInvalid
	}
	for _, user := range users {
		if !identity(user) {
			return nil, false, nil, ErrInvalid
		}
	}
	if len(users) > 0 {
		selected = offlineHashes(users)
		if !exactSHASet(selected) {
			return nil, false, nil, ErrInvalid
		}
	}
	for _, h := range selected {
		if !containsSHA(r.Allowed, h) {
			return nil, false, nil, ErrInvalid
		}
	}
	var body []byte
	var err error
	if info != nil {
		if proto.Size(info) > 4<<20 {
			return nil, false, nil, ErrInvalid
		}
		body, err = proto.MarshalOptions{Deterministic: true}.Marshal(info)
		if err != nil || len(body) > 4<<20 {
			return nil, false, nil, ErrInvalid
		}
	}
	return selected, info != nil, body, nil
}
func (d *KafkaOfflineCallbackDispatch) Finish(parent context.Context, outcome, outcomeSHA string, users []string, info *sdkws.OfflinePushInfo) error {
	if d == nil || d.store == nil || parent == nil || !d.began.Load() || !validSHA(outcomeSHA) {
		return ErrInvalid
	}
	selected, replace, body, err := offlineDecision(d.row, users, info)
	if err != nil {
		return err
	}
	state := ""
	switch outcome {
	case "acknowledged":
		state = "acknowledged"
	case "rejected":
		state = "rejected"
	case "unknown":
		state = "unconfirmed"
		if d.row.Mode == "continue" {
			state = "continued-unknown"
			selected, replace, body, _ = offlineDecision(d.row, nil, nil)
		}
	default:
		return ErrInvalid
	}
	set := bson.M{"state": state, "outcome_sha256": outcomeSHA}
	if state == "acknowledged" || state == "continued-unknown" {
		set["selected_user_sha256"] = selected
		set["replace_info"] = replace
		set["offline_info"] = body
		set["output_sha256"] = offlineOutputSHA(selected, replace, body)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancel()
	changed, err := d.store.db.Collection(Effects).UpdateOne(ctx, bson.M{"_id": d.row.ID, "revision": d.row.Revision, "state": "dispatching", "attempt_id": d.row.Attempt, "output_erased": false}, bson.M{"$set": set, "$inc": bson.M{"revision": int64(1)}})
	if err != nil || changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
		return ErrKafkaPushCallbackUnconfirmed
	}
	if state == "unconfirmed" {
		return ErrKafkaPushCallbackUnconfirmed
	}
	if state == "rejected" {
		return ErrKafkaPushCallbackRejected
	}
	return nil
}
func (s *Store) offlineCallbackDecision(ctx context.Context, d deliveryDuty) (offlineCallbackRow, error) {
	p, err := s.readKafkaPolicy(ctx, d.PolicySHA)
	if err != nil {
		return offlineCallbackRow{}, err
	}
	if p.OfflinePushBinding == "" {
		return offlineCallbackRow{}, nil
	}
	r, err := s.offlineCallbackRow(ctx, d)
	if err != nil {
		return r, err
	}
	if r.Erased {
		return r, ErrConflict
	}
	switch r.State {
	case "acknowledged", "continued-unknown":
		return r, nil
	case "rejected":
		return r, ErrKafkaPushCallbackRejected
	default:
		return r, ErrKafkaPushCallbackUnconfirmed
	}
}
func (s *Store) RequireKafkaOfflineCallback(ctx context.Context, call KafkaOfflineCall, user string) error {
	d, err := s.boundKafkaOfflineDuty(ctx, call)
	if err != nil {
		return err
	}
	r, err := s.offlineCallbackDecision(ctx, d)
	if err != nil {
		return err
	}
	if r.ID != "" && !containsSHA(r.Selected, hash([]byte(user))) {
		return ErrConflict
	}
	return nil
}
func (s *Store) kafkaOfflineSelection(ctx context.Context, call KafkaOfflineCall, user string, original bool, originalProof string) (bool, string, string, error) {
	d, err := s.boundKafkaOfflineDuty(ctx, call)
	if err != nil {
		return false, "", "", err
	}
	r, err := s.offlineCallbackDecision(ctx, d)
	if errors.Is(err, ErrKafkaPushCallbackRejected) {
		return false, r.OutcomeSHA, "frozen-callback-decision", nil
	}
	if err != nil {
		return false, "", "", err
	}
	if r.ID == "" {
		return original, originalProof, "frozen-routing-or-visibility", nil
	}
	return containsSHA(r.Selected, hash([]byte(user))), r.OutputSHA, "frozen-callback-decision", nil
}
func (s *Store) KafkaOfflineProviderMessage(ctx context.Context, call KafkaOfflineCall) (*sdkws.MsgData, error) {
	d, err := s.boundKafkaOfflineDuty(ctx, call)
	if err != nil {
		return nil, err
	}
	r, err := s.offlineCallbackDecision(ctx, d)
	if err != nil {
		return nil, err
	}
	m := proto.Clone(call.Message).(*sdkws.MsgData)
	if r.ID != "" && r.ReplaceInfo {
		var info sdkws.OfflinePushInfo
		if proto.Unmarshal(r.Info, &info) != nil {
			return nil, ErrConflict
		}
		m.OfflinePushInfo = &info
	}
	return m, nil
}

// Child response data is private and must be erased by the same transaction
// that hides the parent. Minimal identity/request/outcome hashes remain.
func (s *Store) eraseOfflineCallback(ctx context.Context, branch string) error {
	_, err := s.db.Collection(Effects).UpdateOne(ctx, bson.M{"_id": key(s.epoch, "kafka-offline-callback", branch), "epoch": s.epoch}, bson.M{"$set": bson.M{"output_erased": true}, "$unset": bson.M{"offline_info": ""}})
	return err
}

func (s *Store) KafkaOfflineCallbackSettled(ctx context.Context, call KafkaOfflineCall) (bool, error) {
	d, err := s.boundKafkaOfflineDuty(ctx, call)
	if err != nil {
		return false, err
	}
	p, err := s.readKafkaPolicy(ctx, d.PolicySHA)
	if err != nil {
		return false, err
	}
	if p.OfflinePushBinding == "" {
		return true, nil
	}
	r, err := s.offlineCallbackRow(ctx, d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if d.OfflineCallbackFrozen {
			return false, ErrConflict
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if r.Erased {
		return false, ErrConflict
	}
	switch r.State {
	case "acknowledged", "continued-unknown":
		return true, nil
	case "pending-confirmed", "pending-not-dispatched":
		return false, nil
	case "rejected":
		return false, ErrKafkaPushCallbackRejected
	default:
		return false, ErrKafkaPushCallbackUnconfirmed
	}
}

type KafkaOfflineCallbackSummary struct {
	BranchID, PolicySHA256, Binding, InputSHA256, BodySHA256, OutputSHA256, State, AttemptID, InstanceID, OutcomeSHA256, RecordSHA256 string
	Revision                                                                                                                          int64
	PreviousSnapshotSHA256                                                                                                            string `json:"PreviousSnapshotSHA256,omitempty"`
	Erased                                                                                                                            bool
}
type KafkaOfflineCallbackConfirmation struct {
	Decision, EvidenceSHA256, ResponseSHA256 string
	Users                                    []string
	Info                                     *sdkws.OfflinePushInfo
	Drain                                    KafkaAttemptDrainProof
}

func summarizeOfflineCallback(r offlineCallbackRow) KafkaOfflineCallbackSummary {
	b, _ := json.Marshal(r)
	return KafkaOfflineCallbackSummary{BranchID: r.Branch, PolicySHA256: r.Policy, Binding: r.Binding, InputSHA256: r.InputSHA, BodySHA256: r.BodySHA, OutputSHA256: r.OutputSHA, State: r.State, AttemptID: r.Attempt, InstanceID: r.Instance, OutcomeSHA256: r.OutcomeSHA, RecordSHA256: hash(b), Revision: r.Revision, Erased: r.Erased, PreviousSnapshotSHA256: r.PreviousSnapshot}
}
func (s *Store) InspectKafkaOfflineCallback(ctx context.Context, branch string) (KafkaOfflineCallbackSummary, error) {
	if s == nil || ctx == nil || !validUUID(branch) {
		return KafkaOfflineCallbackSummary{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return KafkaOfflineCallbackSummary{}, err
	}
	d, err := s.readDutyRecord(current, branch)
	if err != nil {
		return KafkaOfflineCallbackSummary{}, err
	}
	r, err := s.offlineCallbackRow(current, d)
	if err != nil {
		return KafkaOfflineCallbackSummary{}, err
	}
	return summarizeOfflineCallback(r), nil
}

// One read of the original record supplies BOTH the exact expected digest and
// the CAS version. Do not inspect-old/read-new and borrow the newer revision.
func (s *Store) ConfirmKafkaOfflineCallback(ctx context.Context, expected KafkaOfflineCallbackSummary, proof KafkaOfflineCallbackConfirmation) error {
	if s == nil || ctx == nil || !validUUID(expected.BranchID) || !validSHA(expected.RecordSHA256) || !validSHA(proof.EvidenceSHA256) || proof.Drain.InstanceID != expected.InstanceID || !validSHA(proof.Drain.EvidenceSHA256) {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckKafkaReady(current); err != nil {
		return err
	}
	d, err := s.readDutyRecord(current, expected.BranchID)
	if err != nil {
		return err
	}
	r, err := s.offlineCallbackRow(current, d)
	if err != nil {
		return err
	}
	if summarizeOfflineCallback(r) != expected || r.Erased || r.State != "dispatching" && r.State != "unconfirmed" && r.State != "continued-unknown" {
		return ErrConflict
	}
	set := bson.M{}
	state := ""
	switch proof.Decision {
	case "applied":
		if !validSHA(proof.ResponseSHA256) {
			return ErrInvalid
		}
		selected, replace, info, err := offlineDecision(r, proof.Users, proof.Info)
		if err != nil {
			return err
		}
		if r.State == "continued-unknown" && offlineOutputSHA(selected, replace, info) != r.OutputSHA {
			return ErrConflict
		}
		state = "acknowledged"
		set["selected_user_sha256"] = selected
		set["replace_info"] = replace
		set["offline_info"] = info
		set["output_sha256"] = offlineOutputSHA(selected, replace, info)
	case "not-applied":
		if proof.ResponseSHA256 != "" || len(proof.Users) > 0 || proof.Info != nil {
			return ErrInvalid
		}
		if r.State == "continued-unknown" {
			return ErrConflict
		} // Original continuation may already have vendor effects.
		state = "pending-confirmed"
	case "rejected":
		if r.State == "continued-unknown" {
			return ErrConflict
		}
		if proof.ResponseSHA256 != "" || len(proof.Users) > 0 || proof.Info != nil {
			return ErrInvalid
		}
		state = "rejected"
	default:
		return ErrInvalid
	}
	b, _ := json.Marshal([]any{proof.Decision, proof.EvidenceSHA256, proof.ResponseSHA256, proof.Drain})
	set["state"] = state
	set["outcome_sha256"] = hash(b)
	changed, err := s.db.Collection(Effects).UpdateOne(current, bson.M{"_id": r.ID, "epoch": s.epoch, "revision": r.Revision, "state": r.State, "attempt_id": r.Attempt, "instance_id": r.Instance, "body_sha256": r.BodySHA, "output_erased": false}, bson.M{"$set": set, "$inc": bson.M{"revision": int64(1)}})
	if err != nil || changed.MatchedCount != 1 || changed.ModifiedCount != 1 {
		return ErrKafkaPushCallbackUnconfirmed
	}
	return nil
}
