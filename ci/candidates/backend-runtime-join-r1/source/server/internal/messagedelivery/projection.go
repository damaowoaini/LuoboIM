package messagedelivery

import (
	"context"
	"math"
	"strconv"
	"time"

	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// The runtime must supply its owned bounded Redis client (context timeouts,
// finite socket/pool deadlines, no unbounded retries). This adapter writes no
// plaintext MESSAGE_CACHE; already committed Mongo remains the body authority.
type RedisProjection struct{ client redis.UniversalClient }

func NewRedisProjection(client redis.UniversalClient) (*RedisProjection, error) {
	if client == nil {
		return nil, ErrInvalid
	}
	return &RedisProjection{client: client}, nil
}

const raiseProjectionScript = `
local function canonical(v)
 if type(v) ~= 'string' or not string.match(v,'^%d+$') then return nil end
 if #v > 1 and string.sub(v,1,1) == '0' then return nil end
 if #v > 19 or (#v == 19 and v > '9223372036854775807') then return nil end
 return v
end
local value=canonical(ARGV[1]); if not value then return redis.error_reply('invalid sequence projection') end
local old=redis.call('GET',KEYS[1])
if old then
 old=canonical(old);if not old then return redis.error_reply('invalid stored sequence projection') end
 if #old > #value or (#old == #value and old >= value) then return 0 end
end
redis.call('SET',KEYS[1],value);return 1
`

func (p *RedisProjection) raise(ctx context.Context, key string, value int64) error {
	if p == nil || p.client == nil || ctx == nil || value < 0 {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return p.client.Eval(current, raiseProjectionScript, []string{key}, strconv.FormatInt(value, 10)).Err()
}
func (p *RedisProjection) RaiseMaximum(ctx context.Context, cid string, value int64) error {
	if !identity(cid) {
		return ErrInvalid
	}
	return p.raise(ctx, "MAX_SEQ:"+cid, value)
}
func (p *RedisProjection) RaiseSenderRead(ctx context.Context, uid, cid string, value int64) error {
	if !identity(uid) || !identity(cid) {
		return ErrInvalid
	}
	return p.raise(ctx, "HAS_READ_SEQ:"+uid+":"+cid, value)
}

type Projection interface {
	RaiseMaximum(context.Context, string, int64) error
	RaiseSenderRead(context.Context, string, string, int64) error
}

type offlineRouteProof struct {
	ResponseSHA256  string `bson:"response_sha256"`
	Required        bool   `bson:"required"`
	BackgroundWebPC bool   `bson:"background_web_pc"`
}

type deliveryDuty struct {
	ApplicationSource     *groupeffects.MemberApplicationAcceptedBinding `bson:"member_application_source"`
	NamespaceOriginKind   string                                         `bson:"namespace_origin_kind"`
	NamespaceOriginSHA256 string                                         `bson:"namespace_origin_sha256"`
	ActionProfile         *deliveryprofile.Group                         `bson:"action_profile"`
	ActionProfileSHA      string                                         `bson:"action_profile_sha256"`
	ActionProducer        string                                         `bson:"action_producer"`
	ActionCut             *deliveryprofile.VisibilityCut                 `bson:"owned_visibility"`
	RemovalCut            *deliveryprofile.MemberRemovalVisibilityCut    `bson:"owned_removal_visibility"`
	StoredPeerCID         string                                         `bson:"stored_peer_cid"`
	StoredPeerSeq         int64                                          `bson:"stored_peer_seq"`
	SilentState           string                                         `bson:"silent_state"`
	OrdinaryCut           *deliveryprofile.OrdinaryVisibilityCut         `bson:"ordinary_visibility"`
	OrdinaryCutSHA        string                                         `bson:"ordinary_visibility_sha256"`

	OfflineCallbackFrozen bool                         `bson:"offline_callback_frozen"`
	CallbackRevision      int64                        `bson:"callback_revision"`
	VendorRevision        int64                        `bson:"vendor_revision"`
	OfflineCursor         int64                        `bson:"offline_cursor"`
	OfflineSettled        int64                        `bson:"offline_settled"`
	OfflineAttempts       int64                        `bson:"offline_attempts"`
	OfflineClaim          string                       `bson:"offline_claim"`
	OfflineInstance       string                       `bson:"offline_instance"`
	OfflineLeaseUntil     time.Time                    `bson:"offline_lease_until"`
	SourceOperationID     string                       `bson:"source_operation_id"`
	OfflineRouting        map[string]offlineRouteProof `bson:"offline_routing"`
	ProducerVersion       string                       `bson:"producer_version"`
	PolicySHA             string                       `bson:"policy_sha256"`
	ID                    string                       `bson:"_id"`
	Epoch                 string                       `bson:"epoch"`
	SourceID              string                       `bson:"source_id"`
	BranchID              string                       `bson:"branch_id"`
	CID                   string                       `bson:"conversation_id"`
	InputSHA              string                       `bson:"input_sha256"`
	PayloadSHA            string                       `bson:"payload_sha256"`
	Payload               []byte                       `bson:"payload"`
	Recipients            []string                     `bson:"recipients"`
	Sender                string                       `bson:"sender_id"`
	Seq                   int64                        `bson:"seq"`
	Persist               bool                         `bson:"persist"`
	Hidden                bool                         `bson:"hidden"`
	MaxPending            bool                         `bson:"max_pending"`
	SenderReadPending     bool                         `bson:"sender_read_pending"`
	PushPending           bool                         `bson:"push_pending"`
	State                 string                       `bson:"state"`
	Revision              int64                        `bson:"revision"`
	OnlineCursor          int64                        `bson:"online_cursor"`
	OnlineClaim           string                       `bson:"online_claim"`
	OnlineInstance        string                       `bson:"online_instance"`
	OnlineLeaseUntil      time.Time                    `bson:"online_lease_until"`
	OnlineAttempts        int64                        `bson:"online_attempts"`
	RecipientCount        int64                        `bson:"recipient_count"`
	OnlinePending         bool                         `bson:"online_pending"`
	OfflinePending        bool                         `bson:"offline_pending"`
	OnlineACKs            map[string]string            `bson:"online_acks"`
	VisibilityDenied      []string                     `bson:"visibility_denied"`
}

func (s *Store) readDuty(ctx context.Context, branchID string) (deliveryDuty, error) {
	d, err := s.readDutyRecord(ctx, branchID)
	if err != nil {
		return deliveryDuty{}, err
	}
	if err := s.requireDutyNamespace(ctx, d); err != nil {
		return deliveryDuty{}, err
	}
	// The old dispatch/project worker cannot consume a policy-aware duty.
	if d.ProducerVersion != "" || d.PolicySHA != "" {
		if !s.kafkaRecoveryAuthorized(ctx, d) {
			return deliveryDuty{}, ErrUnprepared
		}
	}
	return d, nil
}

// Visibility mutation needs to parse and redact every recognized duty even
// while delivery remains fenced. Parsing a frozen policy is NOT dispatch
// authority, and must not block clear/delete when a new producer row exists.
func (s *Store) readDutyRecord(ctx context.Context, branchID string) (deliveryDuty, error) {
	var d deliveryDuty
	if s == nil || s.db == nil || !validUUID(branchID) {
		return d, ErrInvalid
	}
	if err := s.db.Collection(Effects).FindOne(ctx, bson.M{"_id": key(s.epoch, "branch", branchID)}).Decode(&d); err != nil {
		return d, err
	}
	if d.ProducerVersion != "" || d.PolicySHA != "" {
		if d.ProducerVersion != "ordinary-kafka-v1" && d.ProducerVersion != ownedGroupProducer || !validSHA(d.PolicySHA) {
			return deliveryDuty{}, ErrUnprepared
		}
		p, err := s.readKafkaPolicy(ctx, d.PolicySHA)
		if err != nil {
			return deliveryDuty{}, err
		}
		if d.ProducerVersion == ownedGroupProducer {
			if d.ActionProfile == nil || d.ActionCut == nil || !d.ActionCut.Valid() || d.ActionProducer != "group-role-v1" && d.ActionProducer != "group-action-v1" {
				return deliveryDuty{}, ErrConflict
			}
			actual, err := d.ActionProfile.Fingerprint()
			if err != nil || actual != d.ActionProfileSHA || p.Version != ownedGroupProducer || KafkaDeliveryPolicy(d.ActionProfile.Transport) != p {
				return deliveryDuty{}, ErrConflict
			}
		} else if p.Version != "ordinary-kafka-v1" {
			return deliveryDuty{}, ErrConflict
		}
	}
	if d.RemovalCut != nil {
		if d.ProducerVersion != ownedGroupProducer || d.ActionCut == nil || !d.RemovalCut.Valid() || d.RemovalCut.EffectID != d.SourceID || d.RemovalCut.ProfileSHA256 != d.ActionProfileSHA || deliveryprofile.Digest(d.RemovalCut.Base) != deliveryprofile.Digest(*d.ActionCut) {
			return deliveryDuty{}, ErrConflict
		}
		plan, ok := d.RemovalCut.Branches[d.BranchID]
		if !ok || plan.CID != d.CID || plan.Persist != d.Persist {
			return deliveryDuty{}, ErrConflict
		}
	}
	if d.ApplicationSource != nil {
		if d.ProducerVersion != "ordinary-kafka-v1" || d.ActionCut != nil || d.ActionProfile != nil {
			return deliveryDuty{}, ErrConflict
		}
		if _, err := s.acceptedApplicationBranch(ctx, d); err != nil {
			return deliveryDuty{}, err
		}
	} else if d.OrdinaryCut != nil || d.OrdinaryCutSHA != "" {
		if !ordinaryEphemeralDuty(d) {
			return deliveryDuty{}, ErrConflict
		}
		if len(d.Payload) > 0 {
			var message sdkws.MsgData
			if proto.Unmarshal(d.Payload, &message) != nil || message.Seq != 0 || !d.OrdinaryCut.Matches(&message) || d.CID != d.OrdinaryCut.Branch.ConversationID || message.SendID != d.Sender {
				return deliveryDuty{}, ErrConflict
			}
		}
		for _, user := range d.Recipients {
			if _, ok := d.OrdinaryCut.Normal.Owners[hash([]byte(user))]; !ok {
				return deliveryDuty{}, ErrConflict
			}
		}
	}
	if d.CallbackRevision < 0 || d.CallbackRevision >= math.MaxInt64 || d.SourceOperationID != "" && !identity(d.SourceOperationID) {
		return deliveryDuty{}, ErrConflict
	}
	if d.VendorRevision < 0 || d.VendorRevision >= math.MaxInt64 || d.OfflineCursor < 0 || d.OfflineCursor > d.RecipientCount || d.OfflineSettled < 0 || d.OfflineSettled > d.RecipientCount || d.OfflineAttempts < 0 {
		return deliveryDuty{}, ErrConflict
	}
	if d.OfflineClaim != "" && (!validUUID(d.OfflineClaim) || !validUUID(d.OfflineInstance) || d.OfflineLeaseUntil.IsZero()) || d.OfflineClaim == "" && (d.OfflineInstance != "" || !d.OfflineLeaseUntil.IsZero()) {
		return deliveryDuty{}, ErrConflict
	}
	if d.ID != key(s.epoch, "branch", branchID) || d.Epoch != s.epoch || d.BranchID != branchID || !validUUID(d.SourceID) || !identity(d.CID) || !identity(d.Sender) || !validSHA(d.InputSHA) || !validSHA(d.PayloadSHA) || d.Revision < 1 || d.Revision >= math.MaxInt64 || d.Persist && d.Seq < 1 || !d.Persist && d.Seq != 0 || d.Hidden && d.PushPending || d.OnlineCursor < 0 || d.OnlineCursor > d.RecipientCount || d.RecipientCount < 0 || d.RecipientCount > 50000 && d.ProducerVersion != ownedGroupProducer || d.State != "ready" && d.State != "done" {
		return deliveryDuty{}, ErrConflict
	}
	if len(d.Payload) > 0 && hash(d.Payload) != d.PayloadSHA {
		return deliveryDuty{}, ErrConflict
	}
	if d.PushPending && (d.ProducerVersion == "" && !d.Persist || len(d.Payload) == 0 || len(d.Recipients) == 0) {
		return deliveryDuty{}, ErrUnprepared
	}
	if d.OnlinePending && !d.PushPending || d.OfflinePending && !d.PushPending || len(d.Recipients) > 0 && int64(len(d.Recipients)) != d.RecipientCount {
		return deliveryDuty{}, ErrConflict
	}
	if d.OnlineClaim != "" && (!validUUID(d.OnlineClaim) || !validUUID(d.OnlineInstance) || d.OnlineLeaseUntil.IsZero()) || d.OnlineClaim == "" && (d.OnlineInstance != "" || !d.OnlineLeaseUntil.IsZero()) {
		return deliveryDuty{}, ErrConflict
	}
	for uidHash, route := range d.OfflineRouting {
		if !validSHA(uidHash) || !validSHA(route.ResponseSHA256) {
			return deliveryDuty{}, ErrConflict
		}
	}
	for i, uid := range d.Recipients {
		if !identity(uid) || i > 0 && uid <= d.Recipients[i-1] {
			return deliveryDuty{}, ErrConflict
		}
	}
	return d, nil
}

// Project is safely repeatable even after a partial Redis/Mongo ACK loss. It
// raises only exact derived keys; it neither assigns seq nor rehydrates content.
// The sender read floor is THIS message's seq, never a later global maximum.
func (s *Store) Project(ctx context.Context, branchID string, projection Projection) error {
	if ctx == nil || projection == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return err
	}
	d, err := s.readDuty(current, branchID)
	if err != nil {
		return err
	}
	if !d.MaxPending && !d.SenderReadPending {
		return nil
	}
	if !d.Persist {
		return ErrConflict
	}
	var c counter
	if err := s.db.Collection(Sequences).FindOne(current, bson.M{"_id": key(s.epoch, "sequence", d.CID)}).Decode(&c); err != nil {
		return err
	}
	if c.ID != key(s.epoch, "sequence", d.CID) || c.Epoch != s.epoch || c.CID != d.CID || c.Maximum < d.Seq || c.Maximum < c.SeedMaximum || c.SeedMaximum < 0 || c.Revision < 1 || c.Revision >= math.MaxInt64 || !validSHA(c.SeedEvidenceSHA) {
		return ErrConflict
	}
	if d.MaxPending {
		if err := projection.RaiseMaximum(current, d.CID, c.Maximum); err != nil {
			return err
		}
	}
	if d.SenderReadPending {
		if err := projection.RaiseSenderRead(current, d.Sender, d.CID, d.Seq); err != nil {
			return err
		}
	}
	set := bson.M{"max_pending": false, "sender_read_pending": false}
	update := bson.M{"$set": set, "$inc": bson.M{"revision": int64(1)}}
	if !d.PushPending {
		set["state"] = "done"
		update["$unset"] = bson.M{"payload": "", "recipients": ""}
	}
	res, err := s.db.Collection(Effects).UpdateOne(current, bson.M{"_id": d.ID, "epoch": s.epoch, "revision": d.Revision, "payload_sha256": d.PayloadSHA}, update)
	if err != nil {
		return ErrCommitUnconfirmed
	}
	if res.MatchedCount != 1 || res.ModifiedCount != 1 {
		return ErrConflict
	}
	return nil
}
