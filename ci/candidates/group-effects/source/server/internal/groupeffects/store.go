package groupeffects

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"google.golang.org/protobuf/proto"
)

type Store struct {
	instanceID string
	db         *mongo.Database
	collection *mongo.Collection
}

func NewStore(db *mongo.Database) (*Store, error) {
	if db == nil || db.Client() == nil || db.Name() == "" {
		return nil, ErrUnavailable
	}
	primary := db.Client().Database(db.Name(), options.Database().SetReadPreference(readpref.Primary()).SetReadConcern(readconcern.Majority()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))))
	instance, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrUnavailable
	}
	return &Store{db: primary, collection: primary.Collection(Collection), instanceID: instance.String()}, nil
}

// CheckReady is read-only. A missing/unsafe collection is never implicitly
// created by Queue or an operator inspection. It requires an ordinary durable
// collection with exactly the _id_ index; there is no TTL/lease auto-reclaim.
func (s *Store) CheckReady(ctx context.Context) error {
	if s == nil || s.db == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnavailable
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var hello struct {
		Writable bool   `bson:"isWritablePrimary"`
		SetName  string `bson:"setName"`
		Msg      string `bson:"msg"`
		Sessions *int64 `bson:"logicalSessionTimeoutMinutes"`
	}
	if s.db.RunCommand(current, bson.D{{Key: "hello", Value: 1}}).Decode(&hello) != nil || !hello.Writable || hello.Sessions == nil || hello.SetName == "" && hello.Msg != "isdbgrid" {
		return ErrUnavailable
	}
	cursor, err := s.db.ListCollections(current, bson.D{{Key: "name", Value: Collection}}, options.ListCollections().SetNameOnly(false))
	if err != nil {
		return ErrUnavailable
	}
	defer cursor.Close(current)
	count := 0
	for cursor.Next(current) {
		count++
		var row struct {
			Name    string   `bson:"name"`
			Type    string   `bson:"type"`
			Options bson.Raw `bson:"options"`
			Info    *struct {
				ReadOnly *bool `bson:"readOnly"`
			} `bson:"info"`
		}
		if cursor.Decode(&row) != nil || count != 1 || row.Name != Collection || row.Type != "collection" || row.Info == nil || row.Info.ReadOnly == nil || *row.Info.ReadOnly {
			return ErrUnavailable
		}
		fields, err := row.Options.Elements()
		if err != nil || len(fields) != 0 {
			return ErrUnavailable
		}
	}
	if cursor.Err() != nil || cursor.Close(current) != nil || count != 1 {
		return ErrUnavailable
	}
	indexes, err := s.db.RunCommandCursor(current, bson.D{{Key: "listIndexes", Value: Collection}, {Key: "cursor", Value: bson.D{}}})
	if err != nil {
		return ErrUnavailable
	}
	defer indexes.Close(current)
	count = 0
	for indexes.Next(current) {
		count++
		var row struct {
			Name          string   `bson:"name"`
			Key           bson.D   `bson:"key"`
			TTL           any      `bson:"expireAfterSeconds"`
			Partial       bson.Raw `bson:"partialFilterExpression"`
			Sparse        bool     `bson:"sparse"`
			PrepareUnique bool     `bson:"prepareUnique"`
			Collation     bson.Raw `bson:"collation"`
		}
		if indexes.Decode(&row) != nil || count != 1 || row.Name != "_id_" || len(row.Key) != 1 || row.Key[0].Key != "_id" || !one(row.Key[0].Value) || row.TTL != nil || len(row.Partial) != 0 || row.Sparse || row.PrepareUnique || len(row.Collation) != 0 {
			return ErrUnavailable
		}
	}
	if indexes.Err() != nil || indexes.Close(current) != nil || count != 1 {
		return ErrUnavailable
	}
	return s.checkSchema(current)
}

// InstanceID is observability only, not a reusable write capability.
func (s *Store) InstanceID() string {
	if s == nil {
		return ""
	}
	return s.instanceID
}

func one(v any) bool {
	switch n := v.(type) {
	case int32:
		return n == 1
	case int64:
		return n == 1
	case int:
		return n == 1
	case float64:
		return n == 1
	}
	return false
}

const SchemaID = "schema:group-effects-v1"

// This immutable operator-created sentinel is also read INSIDE the business
// transaction. A removed/unprepared namespace cannot be implicitly created by
// InsertMany after a stale startup readiness check.
func (s *Store) checkSchema(ctx context.Context) error {
	var raw bson.Raw
	if err := s.collection.FindOne(ctx, bson.M{"_id": SchemaID}).Decode(&raw); err != nil {
		return unavailable(err)
	}
	elements, err := raw.Elements()
	if err != nil || len(elements) != 3 {
		return ErrUnavailable
	}
	seen := map[string]bool{}
	for _, e := range elements {
		if seen[e.Key()] {
			return ErrUnavailable
		}
		seen[e.Key()] = true
	}
	id, ok := raw.Lookup("_id").StringValueOK()
	if !ok || id != SchemaID {
		return ErrUnavailable
	}
	kind, ok := raw.Lookup("kind").StringValueOK()
	if !ok || kind != "group-effects-schema-v1" {
		return ErrUnavailable
	}
	version, ok := raw.Lookup("version").Int32OK()
	if !ok || version != 1 {
		return ErrUnavailable
	}
	return nil
}

// Queue executes in the same live private groupauthority transaction as the
// business write. It contains no HTTP/Redis/MQ call and never adopts a duplicate
// ID. The caller checks readiness before entering the strong transaction.
func (s *Store) Queue(ctx context.Context, effects []Frozen) error {
	if s == nil || s.db == nil || ctx == nil || len(effects) == 0 || len(effects) > 4096 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	documents := make([]any, 0, len(effects))
	for _, effect := range effects {
		if effect.validate() != nil || seen[effect.ID] || mcontext.GetOpUserID(ctx) != effect.Actor {
			return ErrInvalid
		}
		seen[effect.ID] = true
		if err := groupauthority.Require(ctx, s.db, effect.GroupID); err != nil {
			return err
		}
		if effect.Kind == AfterCallback {
			var payload CallbackPayload
			if json.Unmarshal(effect.Payload, &payload) != nil {
				return ErrInvalid
			}
			_, ids, err := canonicalCallback(effect.GroupID, payload)
			if err != nil {
				return err
			}
			if len(ids) > 0 {
				if err := groupauthority.RequireMembers(ctx, s.db, effect.GroupID, ids); err != nil {
					return err
				}
			}
		} else if effect.Kind == Notification {
			var message sdkws.MsgData
			if proto.Unmarshal(effect.Payload, &message) != nil {
				return ErrInvalid
			}
			// Direct notification recipients must be explicit authorized targets,
			// even if an application recipient is not a current group member.
			if message.SessionType != constant.SuperGroupChatType {
				if err := groupauthority.RequireMembers(ctx, s.db, effect.GroupID, []string{message.RecvID}); err != nil {
					return err
				}
			}
		}
		effect.Payload = append([]byte(nil), effect.Payload...)
		documents = append(documents, Receipt{Frozen: effect, State: Pending, Revision: 1, UpdatedAt: effect.CreatedAt})
	}
	if err := s.checkSchema(ctx); err != nil {
		return err
	}
	_, err := s.collection.InsertMany(ctx, documents)
	if mongo.IsDuplicateKeyError(err) {
		return ErrConflict
	}
	if err != nil {
		return unavailable(err)
	}
	return nil
}

func (r Receipt) validate() error {
	if !validID(r.ID) || !validID(r.OperationID) || !validIdentity(r.GroupID) || !validIdentity(r.Actor) || !validSHA(r.PayloadSHA256) || r.Revision < 1 || r.Revision == math.MaxInt64 || r.Attempts < 0 || r.CreatedAt.IsZero() {
		return ErrInvalid
	}
	if r.Kind != Notification && r.Kind != AfterCallback || r.OutcomeSHA256 != "" && !validSHA(r.OutcomeSHA256) {
		return ErrInvalid
	}
	want, err := effectIdentity(r.OperationID, r.Ordinal, r.GroupID, r.Actor, r.Kind, r.CreatedAt)
	if err != nil || want.ID != r.ID {
		return ErrInvalid
	}
	switch r.State {
	case Pending:
		if r.Claim != "" || r.SenderInstanceID != "" || !r.ClaimedAt.IsZero() {
			return ErrInvalid
		}
	case Dispatching, Unconfirmed:
		if !validID(r.Claim) || !validID(r.SenderInstanceID) || r.Attempts == 0 || r.ClaimedAt.IsZero() {
			return ErrInvalid
		}
	case Done:
		if len(r.Payload) != 0 || r.Claim != "" || !validSHA(r.OutcomeSHA256) || (r.Decision != "receiver-ack" && r.Decision != "operator-confirmed-applied") {
			return ErrInvalid
		}
		return nil
	default:
		return ErrInvalid
	}
	return r.Frozen.validate()
}

func (s *Store) read(ctx context.Context, id string) (Receipt, error) {
	if s == nil || s.collection == nil || !validID(id) {
		return Receipt{}, ErrInvalid
	}
	var r Receipt
	if err := s.collection.FindOne(ctx, bson.M{"_id": id}).Decode(&r); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return r, ErrConflict
		}
		return r, ErrUnavailable
	}
	if r.validate() != nil {
		return Receipt{}, ErrInvalid
	}
	return r, nil
}
func (s *Store) Inspect(ctx context.Context, id string) (Summary, error) {
	if ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return Summary{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return Summary{}, err
	}
	r, err := s.read(current, id)
	if err != nil {
		return Summary{}, err
	}
	return r.Summary(), nil
}

// Dispatch is a non-serializable capability from this exact acknowledged claim.
// It is never reconstructed from a row, metadata, CLI argument or expired lease.
type Dispatch struct {
	store *Store
	row   Receipt
	claim string
}

func (d *Dispatch) Callback() (CallbackPayload, error) {
	if d == nil || d.store == nil || d.row.Kind != AfterCallback || d.row.Frozen.validate() != nil {
		return CallbackPayload{}, ErrInvalid
	}
	var payload CallbackPayload
	if err := json.Unmarshal(d.row.Payload, &payload); err != nil {
		return payload, ErrInvalid
	}
	payload.Body = append([]byte(nil), payload.Body...)
	return payload, nil
}

func (s *Store) BeginCallback(ctx context.Context, id string, revision int64, payloadSHA256 string) (*Dispatch, error) {
	if ctx == nil || mongo.SessionFromContext(ctx) != nil || revision < 1 || revision == math.MaxInt64 || !validSHA(payloadSHA256) {
		return nil, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return nil, err
	}
	r, err := s.read(current, id)
	if err != nil {
		return nil, err
	}
	if r.Kind != AfterCallback || r.State != Pending || r.Revision != revision || r.PayloadSHA256 != payloadSHA256 || r.Attempts == math.MaxInt32 {
		return nil, ErrConflict
	}
	claimID, err := uuid.NewRandom()
	if err != nil {
		return nil, ErrUnavailable
	}
	claim := claimID.String()
	at := time.Now().UTC()
	result, err := s.collection.UpdateOne(current, bson.M{"_id": id, "state": Pending, "revision": revision, "payload_sha256": payloadSHA256, "attempts": r.Attempts}, bson.M{"$set": bson.M{"state": Dispatching, "claim": claim, "claimed_at": at, "updated_at": at, "sender_instance_id": s.instanceID}, "$inc": bson.M{"revision": int64(1), "attempts": int32(1)}, "$unset": bson.M{"outcome_sha256": "", "decision": "", "evidence_sha256": ""}})
	if err != nil {
		return nil, ErrUnconfirmed
	} // No capability on an unknown claim ACK.
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return nil, ErrConflict
	}
	r.State = Dispatching
	r.Claim = claim
	r.SenderInstanceID = s.instanceID
	r.OutcomeSHA256 = ""
	r.Decision = ""
	r.EvidenceSHA256 = ""
	r.ClaimedAt = at
	r.UpdatedAt = at
	r.Revision++
	r.Attempts++
	return &Dispatch{store: s, row: r, claim: claim}, nil
}

type Outcome uint8

const (
	Acknowledged Outcome = iota + 1
	NotDispatched
	Unknown
)

// Finish requires a known attempt capability. A transport error after HTTP.Do
// began is Unknown even for 401/403/500; response codes do not prove no effect.
// Acknowledged means a validated application success, not merely HTTP 200.
func (d *Dispatch) Finish(ctx context.Context, outcome Outcome, responseSHA256 string) error {
	if d == nil || d.store == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil || d.claim == "" || d.row.State != Dispatching {
		return ErrInvalid
	}
	if outcome == NotDispatched && responseSHA256 != "" {
		return ErrInvalid
	}
	if outcome != Acknowledged && outcome != NotDispatched && outcome != Unknown || outcome == Acknowledged && !validSHA(responseSHA256) || responseSHA256 != "" && !validSHA(responseSHA256) {
		return ErrInvalid
	}
	state := Unconfirmed
	set := bson.M{"updated_at": time.Now().UTC()}
	unset := bson.M{}
	switch outcome {
	case Acknowledged:
		state = Done
		set["outcome_sha256"] = responseSHA256
		set["decision"] = "receiver-ack"
		unset["payload"] = ""
		unset["claim"] = ""
	case NotDispatched:
		state = Pending
		unset["claim"] = ""
		unset["sender_instance_id"] = ""
		unset["claimed_at"] = ""
	case Unknown:
		if responseSHA256 != "" {
			set["outcome_sha256"] = responseSHA256
		}
	}
	set["state"] = state
	update := bson.M{"$set": set, "$inc": bson.M{"revision": int64(1)}}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	result, err := d.store.collection.UpdateOne(current, bson.M{"_id": d.row.ID, "state": Dispatching, "revision": d.row.Revision, "claim": d.claim, "sender_instance_id": d.row.SenderInstanceID, "payload_sha256": d.row.PayloadSHA256}, update)
	if err != nil {
		return ErrUnconfirmed
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}

type Confirmation struct {
	ExpectedSenderInstanceID string
	ID                       string
	ExpectedRevision         int64
	ExpectedPayloadSHA256    string
	Decision                 string // applied or not-applied
	EvidenceSHA256           string
	DrainedEvidenceSHA256    string
}

func (c Confirmation) validate() error {
	if !validID(c.ID) || !validID(c.ExpectedSenderInstanceID) || c.ExpectedRevision < 1 || c.ExpectedRevision == math.MaxInt64 || !validSHA(c.ExpectedPayloadSHA256) || !validSHA(c.EvidenceSHA256) || !validSHA(c.DrainedEvidenceSHA256) || (c.Decision != "applied" && c.Decision != "not-applied") {
		return ErrInvalid
	}
	return nil
}

// Confirm is a privileged explicit operator CAS, not an automated result
// detector. The operator must first stop/drain the exact sending worker and
// obtain definitive remote outcome/cancellation evidence. Hashes bind those
// audit artifacts; hashes alone are NOT proof that either fact is true.
func (s *Store) Confirm(ctx context.Context, c Confirmation) (Summary, error) {
	if c.validate() != nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return Summary{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return Summary{}, err
	}
	r, err := s.read(current, c.ID)
	if err != nil {
		return Summary{}, err
	}
	if r.Kind != AfterCallback || (r.State != Unconfirmed && r.State != Dispatching) || r.Revision != c.ExpectedRevision || r.PayloadSHA256 != c.ExpectedPayloadSHA256 || r.SenderInstanceID != c.ExpectedSenderInstanceID {
		return Summary{}, ErrConflict
	}
	set := bson.M{"evidence_sha256": c.EvidenceSHA256, "updated_at": time.Now().UTC()}
	unset := bson.M{"claim": ""}
	if c.Decision == "applied" {
		set["state"] = Done
		set["decision"] = "operator-confirmed-applied"
		set["outcome_sha256"] = c.EvidenceSHA256
		unset["payload"] = ""
	} else {
		set["state"] = Pending
		set["decision"] = "operator-confirmed-not-applied"
		unset["sender_instance_id"] = ""
		unset["claimed_at"] = ""
	}
	update := bson.M{"$set": set, "$unset": unset, "$inc": bson.M{"revision": int64(1)}, "$push": bson.M{"confirmation_history": bson.M{"decision": c.Decision, "evidence_sha256": c.EvidenceSHA256, "drained_evidence_sha256": c.DrainedEvidenceSHA256, "previous_revision": r.Revision, "sender_instance_id": r.SenderInstanceID, "at": time.Now().UTC()}}}
	result, err := s.collection.UpdateOne(current, bson.M{"_id": r.ID, "state": r.State, "revision": r.Revision, "claim": r.Claim, "sender_instance_id": r.SenderInstanceID, "payload_sha256": r.PayloadSHA256}, update)
	if err != nil {
		return Summary{}, ErrUnconfirmed
	}
	if result.MatchedCount != 1 || result.ModifiedCount != 1 {
		return Summary{}, ErrConflict
	}
	// Readback failure never means the confirmation rolled back or may repeat.
	updated, err := s.read(current, r.ID)
	if err != nil {
		return Summary{}, ErrUnconfirmed
	}
	return updated.Summary(), nil
}
