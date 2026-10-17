package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"regexp"
	"sort"
	"sync/atomic"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Namespace ownership is NOT message/role/recipient authorization. Callers keep
// their original final access checks. CID strings alone cannot identify owners:
// group collision_peer and direct(collision,peer) both use n_collision_peer.
type messageOrigin struct{ Kind, SHA256, CID, GroupID string }

func messageNamespace(message *sdkws.MsgData) (messageOrigin, error) {
	if message == nil || !identity(message.SendID) || !identity(message.RecvID) {
		return messageOrigin{}, ErrInvalid
	}
	cid := msgprocessor.GetConversationIDByMsg(message)
	if !identity(cid) {
		return messageOrigin{}, ErrInvalid
	}
	kind, ids, gid := "", []string(nil), ""
	switch message.SessionType {
	case constant.SuperGroupChatType, constant.GroupChatType:
		if !identity(message.GroupID) || message.RecvID != message.GroupID {
			return messageOrigin{}, ErrInvalid
		}
		kind, ids, gid = "group", []string{message.GroupID}, message.GroupID
	case constant.SingleChatType:
		if message.GroupID != "" {
			return messageOrigin{}, ErrInvalid
		}
		kind, ids = "single", []string{message.SendID, message.RecvID}
		sort.Strings(ids)
	case constant.NotificationChatType:
		if message.GroupID != "" {
			return messageOrigin{}, ErrInvalid
		}
		kind, ids = "server-notification", []string{message.SendID, message.RecvID}
	default:
		return messageOrigin{}, ErrInvalid
	}
	body, err := json.Marshal([]any{kind, ids})
	if err != nil {
		return messageOrigin{}, err
	}
	return messageOrigin{Kind: kind, SHA256: hash(body), CID: cid, GroupID: gid}, nil
}

type directNamespaceClaim struct {
	ID                 string    `bson:"_id"`
	Kind               string    `bson:"kind"`
	ConversationID     string    `bson:"conversation_id"`
	OriginKind         string    `bson:"origin_kind"`
	OriginSHA256       string    `bson:"origin_sha256"`
	Epoch              string    `bson:"created_epoch"`
	ActivationSHA256   string    `bson:"created_activation_sha256"`
	SeedEvidenceSHA256 string    `bson:"seed_evidence_sha256"`
	CreatedAt          time.Time `bson:"created_at"`
}

// Exact permanent key and strict row shape; never Decode arbitrary Sequence
// rows as counters and never replace an unknown historical origin by a fresh one.
func (s *Store) namespaceOwner(ctx context.Context, cid string) (messageOrigin, string, error) {
	if s == nil || s.db == nil || ctx == nil || !identity(cid) {
		return messageOrigin{}, "", ErrInvalid
	}
	if err := s.requireNamespaceNotQuarantined(ctx, cid); err != nil {
		return messageOrigin{}, "", err
	}
	var raw bson.Raw
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceClaimID(cid)}).Decode(&raw); err != nil {
		return messageOrigin{}, "", err
	}
	kind, ok := raw.Lookup("kind").StringValueOK()
	if !ok {
		return messageOrigin{}, "", ErrConflict
	}
	switch kind {
	case historicalNamespaceKind:
		return s.historicalNamespaceOwner(ctx, cid, raw)
	case "permanent-new-group-namespace-v1":
		if !exactFields(raw, []string{"_id", "kind", "conversation_id", "group_id", "branch", "epoch", "activation_sha256", "command_id", "input_sha256", "cutover_sha256"}, nil, nil, "created_at") {
			return messageOrigin{}, "", ErrConflict
		}
		var row groupNamespaceClaim
		if bson.Unmarshal(raw, &row) != nil || row.ID != namespaceClaimID(cid) || row.ConversationID != cid || !identity(row.GroupID) || !validUUID(row.Epoch) || !validSHA(row.ActivationSHA256) || !validUUID(row.CommandID) || !validSHA(row.InputSHA256) || !validSHA(row.CutoverSHA256) || row.CreatedAt.IsZero() {
			return messageOrigin{}, "", ErrConflict
		}
		normal, notice, err := groupNamespacePair(row.GroupID)
		if err != nil || row.Branch == "normal" && cid != normal || row.Branch == "notification" && cid != notice || row.Branch != "normal" && row.Branch != "notification" {
			return messageOrigin{}, "", ErrConflict
		}
		var originRaw bson.Raw
		if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": groupOriginClaimID(row.GroupID)}).Decode(&originRaw); err != nil {
			return messageOrigin{}, "", err
		}
		if !exactFields(originRaw, []string{"_id", "kind", "conversation_id", "group_id", "branch", "epoch", "activation_sha256", "command_id", "input_sha256", "cutover_sha256"}, nil, nil, "created_at") {
			return messageOrigin{}, "", ErrConflict
		}
		var origin groupNamespaceClaim
		if bson.Unmarshal(originRaw, &origin) != nil {
			return messageOrigin{}, "", ErrConflict
		}
		expected := row
		expected.ID, expected.ConversationID, expected.Branch = groupOriginClaimID(row.GroupID), "", "group-origin"
		a, _ := json.Marshal(expected)
		b, _ := json.Marshal(origin)
		if string(a) != string(b) {
			return messageOrigin{}, "", ErrConflict
		}
		body, _ := json.Marshal([]any{"group", []string{row.GroupID}})
		return messageOrigin{Kind: "group", SHA256: hash(body), CID: cid, GroupID: row.GroupID}, "", nil
	case "permanent-message-origin-v1":
		if !exactFields(raw, []string{"_id", "kind", "conversation_id", "origin_kind", "origin_sha256", "created_epoch", "created_activation_sha256", "seed_evidence_sha256"}, nil, nil, "created_at") {
			return messageOrigin{}, "", ErrConflict
		}
		var row directNamespaceClaim
		if bson.Unmarshal(raw, &row) != nil || row.ID != namespaceClaimID(cid) || row.ConversationID != cid || row.OriginKind != "single" && row.OriginKind != "server-notification" || !validSHA(row.OriginSHA256) || !validUUID(row.Epoch) || !validSHA(row.ActivationSHA256) || !validSHA(row.SeedEvidenceSHA256) || row.CreatedAt.IsZero() {
			return messageOrigin{}, "", ErrConflict
		}
		return messageOrigin{Kind: row.OriginKind, SHA256: row.OriginSHA256, CID: cid}, row.SeedEvidenceSHA256, nil
	default:
		return messageOrigin{}, "", ErrUnprepared
	}
}

func (s *Store) requireNamespace(ctx context.Context, expected messageOrigin) error {
	actual, _, err := s.namespaceOwner(ctx, expected.CID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return errors.Join(ErrUnprepared, err)
	}
	if err != nil {
		return err
	}
	if actual.Kind != expected.Kind || actual.SHA256 != expected.SHA256 {
		return ErrConflict
	}
	// The CID spelling and an irreversible origin hash cannot identify the
	// original participants for later notification-history authorization.
	// Every managed writer consumes the same permanent principal binding.
	if _, err := s.readNamespacePrincipals(ctx, expected.CID); err != nil {
		return err
	}
	return nil
}

// The exact _id query cannot decode permanent claims/schema rows as counters.
// Existing origin ownership does not permit zero-seeding a new activation.
func (s *Store) requireNamespaceCounter(ctx context.Context, cid string) error {
	var raw bson.Raw
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", cid)}).Decode(&raw); err != nil {
		return errors.Join(ErrUnprepared, err)
	}
	if !exactFields(raw, []string{"_id", "epoch", "conversation_id", "seed_evidence_sha256"}, []string{"maximum", "revision", "seed_maximum"}, nil, "") {
		return ErrUnprepared
	}
	var row counter
	if bson.Unmarshal(raw, &row) != nil || row.ID != key(s.epoch, "sequence", cid) || row.CID != cid || row.Epoch != s.epoch || !validSHA(row.SeedEvidenceSHA) || row.SeedMaximum < 0 || row.Maximum < row.SeedMaximum || row.Maximum >= math.MaxInt64 || row.Revision < 1 || row.Revision >= math.MaxInt64 {
		return ErrUnprepared
	}
	return nil
}

func legacyNamespaceDocumentFilter(cid string) bson.M {
	return bson.M{"doc_id": bson.M{"$regex": "^" + regexp.QuoteMeta(cid) + ":"}}
}

// This legacy/generic adapter has no durable source capability. It must use
// the original actor and same-TX authoritative roster, never arbitrary IDs.
func (s *Store) authorizeNamespaceEnvelope(ctx context.Context, input Envelope) error {
	actor := mcontext.GetOpUserID(ctx)
	for _, branch := range input.Branches {
		var message sdkws.MsgData
		if proto.Unmarshal(branch.Payload, &message) != nil {
			return ErrInvalid
		}
		if err := authverify.CheckAccessV3(ctx, message.SendID); err != nil {
			return err
		}
		if message.MsgFrom != constant.UserMsgType || message.ContentType >= constant.NotificationBegin || message.SessionType == constant.NotificationChatType {
			if err := authverify.CheckAdmin(ctx); err != nil {
				return err
			}
		}
		roster, err := s.freezeKafkaRoster(ctx, &message, actor)
		if err != nil {
			return err
		}
		allowed := make(map[string]bool, len(roster.users))
		for _, uid := range roster.users {
			allowed[uid] = true
		}
		for _, uid := range branch.Recipients {
			if !allowed[uid] {
				return ErrConflict
			}
		}
	}
	return nil
}

type namespaceSeedKey struct{}
type namespaceSeedProof struct {
	store         *Store
	session       mongo.Session
	cid, evidence string
	active        atomic.Bool
}

// Called only after private real Create proof, or after inserting a fresh direct
// claim with completed-cutover+raw namespace absence in this SAME transaction.
func (s *Store) seedOwnedNamespace(ctx context.Context, cid, evidence string) error {
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	if _, _, err := s.namespaceOwner(ctx, cid); err != nil {
		return err
	}
	proof := &namespaceSeedProof{store: s, session: mongo.SessionFromContext(ctx), cid: cid, evidence: evidence}
	proof.active.Store(true)
	defer proof.active.Store(false)
	return s.InitializeSequence(context.WithValue(ctx, namespaceSeedKey{}, proof), cid, Seed{MongoKnown: true, RedisKnown: true, EvidenceSHA256: evidence})
}

func (s *Store) requireNamespaceSeed(ctx context.Context, cid, evidence string) error {
	proof, ok := ctx.Value(namespaceSeedKey{}).(*namespaceSeedProof)
	if !ok || proof == nil || !proof.active.Load() || proof.store != s || proof.session != mongo.SessionFromContext(ctx) || proof.cid != cid || proof.evidence != evidence {
		return ErrUnprepared
	}
	return nil
}

type namespaceInputKey struct{}
type namespaceInputProof struct {
	store   *Store
	session mongo.Session
	digest  string
	active  atomic.Bool
}

// Internal adapters call this only AFTER their original authorization/source
// cap validation. The private proof expires before leaving this call. Namespace
// ownership does not authorize a new group member, callback or recipient list.
func (s *Store) acceptSourceEnvelope(ctx context.Context, envelope Envelope, owned bool, denied map[string][]string) (Receipt, error) {
	if err := s.requireTX(ctx); err != nil {
		return Receipt{}, err
	}
	_, digest, err := freezeEnvelopeMode(envelope, owned)
	if err != nil {
		return Receipt{}, err
	}
	proof := &namespaceInputProof{store: s, session: mongo.SessionFromContext(ctx), digest: digest}
	proof.active.Store(true)
	defer proof.active.Store(false)
	return s.acceptWithSuppression(context.WithValue(ctx, namespaceInputKey{}, proof), envelope, owned, denied)
}

func (s *Store) requireNamespaceInput(ctx context.Context, digest string) error {
	proof, ok := ctx.Value(namespaceInputKey{}).(*namespaceInputProof)
	if !ok || proof == nil || !proof.active.Load() || proof.store != s || proof.session != mongo.SessionFromContext(ctx) || proof.digest != digest {
		return ErrUnprepared
	}
	return nil
}

// Keep old rows parseable for clear/redaction/inspection; actual projection and
// delivery require a retained minimal origin. No erased/legacy payload renews it.
func (s *Store) requireDutyNamespace(ctx context.Context, duty deliveryDuty) error {
	if !identity(duty.CID) || !validSHA(duty.NamespaceOriginSHA256) || duty.NamespaceOriginKind == "" {
		return ErrUnprepared
	}
	return s.requireNamespace(ctx, messageOrigin{CID: duty.CID, Kind: duty.NamespaceOriginKind, SHA256: duty.NamespaceOriginSHA256})
}

type NamespaceEnqueuePermit struct {
	store          *Store
	actor, bodySHA string
}

func (p *NamespaceEnqueuePermit) Check(ctx context.Context, message *sdkws.MsgData) error {
	if p == nil || p.store == nil || ctx == nil || ctx.Err() != nil || mcontext.GetOpUserID(ctx) != p.actor || message == nil {
		return ErrUnprepared
	}
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil || hash(body) != p.bodySHA {
		return ErrConflict
	}
	return nil
}

type enqueueNamespaceReceipt struct {
	ID            string `bson:"_id"`
	Kind          string `bson:"kind"`
	Epoch         string `bson:"epoch"`
	Actor         string `bson:"actor"`
	MessageSHA256 string `bson:"message_sha256"`
	OriginsSHA256 string `bson:"origins_sha256"`
}

// Final authorized producer fields are captured after existing callbacks, once.
// This is NOT a Kafka delivery receipt. It only grants enqueue after a known
// namespace-reservation commit; no external work occurs in transaction retries.
func (s *Store) ReserveMessageNamespaces(ctx context.Context, message *sdkws.MsgData) (*NamespaceEnqueuePermit, error) {
	if s == nil || ctx == nil || message == nil || message.Seq != 0 || !identity(message.ServerMsgID) || mongo.SessionFromContext(ctx) != nil {
		return nil, ErrInvalid
	}
	if err := authverify.CheckAccessV3(ctx, message.SendID); err != nil {
		return nil, err
	}
	if message.MsgFrom != constant.UserMsgType || message.ContentType >= constant.NotificationBegin || message.SessionType == constant.NotificationChatType {
		if err := authverify.CheckAdmin(ctx); err != nil {
			return nil, err
		}
	}
	frozen := proto.Clone(message).(*sdkws.MsgData)
	body, err := proto.MarshalOptions{Deterministic: true}.Marshal(frozen)
	if err != nil {
		return nil, err
	}
	stored, ephemeral, tips, silent, modifiers := msgprocessor.SplitStorageMessages([]*sdkws.MsgData{frozen})
	all := append(append(append(append([]*sdkws.MsgData{}, stored...), ephemeral...), tips...), silent...)
	all = append(all, modifiers...)
	origins := map[string]messageOrigin{}
	principals := map[string]NamespaceOriginDescriptor{}
	for _, m := range all {
		descriptor, err := DescribeNamespace(m)
		if err != nil {
			return nil, err
		}
		// A silent tip still needs the original normal/notification namespace
		// pair for its privacy cut. These are derived from this actual typed
		// message, never from splitting a potentially ambiguous CID string.
		for _, cid := range []string{descriptor.CID, msgprocessor.GetChatConversationIDByMsg(m), msgprocessor.GetNotificationConversationIDByMsg(m)} {
			d := descriptor
			d.CID = cid
			if err := validateNamespaceDescriptor(d); err != nil {
				return nil, err
			}
			origin := messageOrigin{Kind: d.Kind, SHA256: d.SHA256, CID: cid, GroupID: d.GroupID}
			if prior, ok := origins[cid]; ok && (prior.Kind != origin.Kind || prior.SHA256 != origin.SHA256) {
				return nil, ErrConflict
			}
			origins[cid], principals[cid] = origin, d
		}
	}
	if len(origins) == 0 {
		return nil, ErrInvalid
	}
	ids := make([]string, 0, len(origins))
	for cid := range origins {
		ids = append(ids, cid)
	}
	sort.Strings(ids)
	packed, _ := json.Marshal(origins)
	expected := enqueueNamespaceReceipt{ID: key(s.epoch, "namespace-enqueue", frozen.ServerMsgID), Kind: "namespace-enqueue-v1", Epoch: s.epoch, Actor: mcontext.GetOpUserID(ctx), MessageSHA256: hash(body), OriginsSHA256: hash(packed)}
	current, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := s.CheckNewGroupSeedReady(current); err != nil {
		return nil, err
	}
	err = s.transaction(current, func(tx context.Context) error {
		if err := s.requireTX(tx); err != nil {
			return err
		}
		var raw bson.Raw
		err := s.db.Collection(Receipts).FindOne(tx, bson.M{"_id": expected.ID}).Decode(&raw)
		if err == nil {
			if !exactFields(raw, []string{"_id", "kind", "epoch", "actor", "message_sha256", "origins_sha256"}, nil, nil, "") {
				return ErrConflict
			}
			var old enqueueNamespaceReceipt
			if bson.Unmarshal(raw, &old) != nil {
				return ErrConflict
			}
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(expected)
			if string(a) != string(b) {
				return ErrConflict
			}
			for _, cid := range ids {
				if err := s.requireNamespace(tx, origins[cid]); err != nil {
					return err
				}
				if err := s.requireNamespaceCounter(tx, cid); err != nil {
					return err
				}
			}
			return nil
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		if err := s.reserveTypedNamespacePairs(tx, origins, principals); err != nil {
			return err
		}
		_, err = s.db.Collection(Receipts).InsertOne(tx, expected)
		return err
	})
	if err != nil {
		return nil, err
	} // Unknown commit never grants enqueue.
	permit := &NamespaceEnqueuePermit{store: s, actor: expected.Actor, bodySHA: expected.MessageSHA256}
	if err := permit.Check(ctx, message); err != nil {
		return nil, err
	}
	return permit, nil
}

// Both callers have already checked a private source capability or the original
// authenticated enqueue contract. This helper is deliberately not exported.
func (s *Store) reserveTypedNamespacePairs(ctx context.Context, origins map[string]messageOrigin, principals map[string]NamespaceOriginDescriptor) error {
	if err := s.requireTX(ctx); err != nil {
		return err
	}
	if len(origins) == 0 || len(origins) != len(principals) {
		return ErrInvalid
	}
	ids := make([]string, 0, len(origins))
	for cid, origin := range origins {
		d, ok := principals[cid]
		if !ok || d.CID != cid || origin.CID != cid || validateNamespaceDescriptor(d) != nil || origin.Kind != d.Kind || origin.SHA256 != d.SHA256 || origin.GroupID != d.GroupID {
			return ErrConflict
		}
		ids = append(ids, cid)
	}
	sort.Strings(ids)
	_, cutover, err := s.namespaceCutover(ctx)
	if err != nil {
		return err
	}
	for _, cid := range ids {
		origin := origins[cid]
		actual, evidence, err := s.namespaceOwner(ctx, cid)
		if err == nil {
			if actual.Kind != origin.Kind || actual.SHA256 != origin.SHA256 {
				return ErrConflict
			}
			// Compatible existing claim is not proof that a missing epoch
			// counter may be seeded zero. Only THIS new insertion may seed.
			_ = evidence
			if err := s.requireNamespaceCounter(ctx, cid); err != nil {
				return err
			}
			if err := s.recordNamespacePrincipals(ctx, principals[cid]); err != nil {
				return err
			}
			continue
		}
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return err
		}
		if origin.Kind == "group" {
			return ErrUnprepared
		}
		for _, name := range []string{Sequences, Receipts, Effects, "message_visibility_floors"} {
			if err := absent(ctx, s.db.Collection(name), bson.M{"$or": bson.A{bson.M{"conversation_id": cid}, bson.M{"branches.conversation_id": cid}}}); err != nil {
				return err
			}
		}
		if err := absent(ctx, s.db.Collection("msg"), legacyNamespaceDocumentFilter(cid)); err != nil {
			return err
		}
		seedBody, _ := json.Marshal([]string{origin.Kind, origin.SHA256, cid, cutover})
		evidence = hash(seedBody)
		claim := directNamespaceClaim{ID: namespaceClaimID(cid), Kind: "permanent-message-origin-v1", ConversationID: cid, OriginKind: origin.Kind, OriginSHA256: origin.SHA256, Epoch: s.epoch, ActivationSHA256: s.activationSHA, SeedEvidenceSHA256: evidence, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
		if _, err := s.db.Collection(Sequences).InsertOne(ctx, claim); err != nil {
			return err
		}
		if err := s.seedOwnedNamespace(ctx, cid, evidence); err != nil {
			return err
		}
		if err := s.recordNamespacePrincipals(ctx, principals[cid]); err != nil {
			return err
		}
	}
	return nil
}

// Nil is a deliberate legacy mode only when ALL original settings are absent.
// Required startup stays hard fenced; a new claim helper does not certify a
// historical importer, complete writer cutover, or positive-n entitlement reader.
func NewMessageNamespaceAdmissionFromEnvironment(db *mongo.Database) (*Store, error) {
	mode, epoch, activation := os.Getenv(HistoryWindowModeEnv), os.Getenv(HistoryWindowEpochEnv), os.Getenv(HistoryWindowActivationEnv)
	if (mode == "" || mode == "0") && epoch == "" && activation == "" {
		return nil, nil
	}
	if mode != "required" {
		return nil, ErrUnprepared
	}
	s, err := NewStore(db, epoch, activation)
	if err != nil {
		return nil, err
	}
	return s, nil
}
