package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const NamespaceCutoverID = "schema:group-namespace-cutover-v1"

var ErrNamespaceWritersUnbound = errors.New("permanent namespace admission is not bound to every allocator and direct notification writer")

// This is a consumed immutable operator result, NOT a runtime constructor,
// a missing-row fallback or a set of Ready booleans. This slice intentionally
// has NO writer for it. The producer must inventory+claim ALL historical CIDs
// (including direct n_, deleted/orphan/group IDs), prove the one global old
// writer/Kafka drain and old-binary exclusion, then publish atomically. The
// complete historical importer/manifest verifier remains a deployment blocker.
type NamespaceCutover struct {
	ID                   string    `bson:"_id"`
	Kind                 string    `bson:"kind"`
	Database             string    `bson:"database"`
	Epoch                string    `bson:"epoch"`
	ActivationSHA256     string    `bson:"activation_sha256"`
	MigrationID          string    `bson:"migration_id"`
	State                string    `bson:"state"`
	ManifestSHA256       string    `bson:"manifest_sha256"`
	MongoInventorySHA256 string    `bson:"mongo_inventory_sha256"`
	RedisInventorySHA256 string    `bson:"redis_inventory_sha256"`
	DrainSHA256          string    `bson:"drain_sha256"`
	OldBinaryFenceSHA256 string    `bson:"old_binary_fence_sha256"`
	NamespaceCount       int64     `bson:"namespace_count"`
	ClaimedCount         int64     `bson:"claimed_count"`
	RegisteredAt         time.Time `bson:"registered_at"`
}

func (s *Store) namespaceCutover(ctx context.Context) (NamespaceCutover, string, error) {
	var raw bson.Raw
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": NamespaceCutoverID}).Decode(&raw); err != nil {
		return NamespaceCutover{}, "", errors.Join(ErrUnprepared, err)
	}
	if !exactFields(raw, []string{"_id", "kind", "database", "epoch", "activation_sha256", "migration_id", "state", "manifest_sha256", "mongo_inventory_sha256", "redis_inventory_sha256", "drain_sha256", "old_binary_fence_sha256"}, []string{"namespace_count", "claimed_count"}, nil, "registered_at") {
		return NamespaceCutover{}, "", ErrUnprepared
	}
	var row NamespaceCutover
	if bson.Unmarshal(raw, &row) != nil || row.ID != NamespaceCutoverID || row.Kind != "immutable-namespace-cutover-v1" || row.Database != s.db.Name() || row.Epoch != s.epoch || row.ActivationSHA256 != s.activationSHA || !validUUID(row.MigrationID) || row.State != "all-inventoried-namespaces-permanently-claimed" || row.NamespaceCount < 0 || row.NamespaceCount != row.ClaimedCount || row.RegisteredAt.IsZero() {
		return row, "", ErrUnprepared
	}
	for _, value := range []string{row.ManifestSHA256, row.MongoInventorySHA256, row.RedisInventorySHA256, row.DrainSHA256, row.OldBinaryFenceSHA256} {
		if !validSHA(value) {
			return row, "", ErrUnprepared
		}
	}
	body, err := json.Marshal(row)
	if err != nil {
		return row, "", err
	}
	return row, hash(body), nil
}

// Explicit base-store readiness does not manufacture membership/reader Ready.
// It can be used by migration check/plan before owner windows/readers are ready;
// real service startup still retains its existing required reader gates.
func (s *Store) CheckNewGroupSeedReady(ctx context.Context) error {
	if s == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnprepared
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckReady(current); err != nil {
		return err
	}
	_, _, err := s.namespaceCutover(current)
	return err
}

// Component readiness is deliberately insufficient for runtime admission. The
// existing generic allocator and direct n_<UID> writers do not yet consume the
// permanent claim's origin/kind. A registered migration cannot certify those
// future writes. No configuration flag, schema row or operator plan bypasses
// this hard fence; remove it only with the complete writer/reader successor.
func (s *Store) CheckNewGroupSeedRuntimeReady(ctx context.Context) error {
	if err := s.CheckNewGroupSeedReady(ctx); err != nil {
		return err
	}
	return errors.Join(ErrUnprepared, ErrNamespaceWritersUnbound)
}

func groupNamespacePair(gid string) (string, string, error) {
	if !identity(gid) {
		return "", "", ErrInvalid
	}
	normal := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, gid)
	notice := msgprocessor.GetNotificationConversationID(constant.SuperGroupChatType, gid)
	if normal == "" || notice == "" || normal == notice || msgprocessor.GetNotificationConversationIDByConversationID(normal) != notice {
		return "", "", ErrInvalid
	}
	return normal, notice, nil
}
func namespaceClaimID(cid string) string   { return "permanent-cid:" + hash([]byte(cid)) }
func groupOriginClaimID(gid string) string { return "permanent-group-origin:" + hash([]byte(gid)) }

type groupNamespaceClaim struct {
	ID               string    `bson:"_id"`
	Kind             string    `bson:"kind"`
	ConversationID   string    `bson:"conversation_id"`
	GroupID          string    `bson:"group_id"`
	Branch           string    `bson:"branch"`
	Epoch            string    `bson:"epoch"`
	ActivationSHA256 string    `bson:"activation_sha256"`
	CommandID        string    `bson:"command_id"`
	InputSHA256      string    `bson:"input_sha256"`
	CutoverSHA256    string    `bson:"cutover_sha256"`
	CreatedAt        time.Time `bson:"created_at"`
}

// IDs do not include epoch. Deleting group rows, closing owner windows or
// switching activation never makes these names available again. No runtime
// delete/update API for claims is provided by this slice.
func claimRows(info groupauthority.NewGroupCreationInfo, normal, notice, cutover string) []groupNamespaceClaim {
	rows := []groupNamespaceClaim{
		{ID: groupOriginClaimID(info.GroupID), Branch: "group-origin"},
		{ID: namespaceClaimID(normal), ConversationID: normal, Branch: "normal"},
		{ID: namespaceClaimID(notice), ConversationID: notice, Branch: "notification"},
	}
	for i := range rows {
		rows[i].Kind, rows[i].GroupID = "permanent-new-group-namespace-v1", info.GroupID
		rows[i].Epoch, rows[i].ActivationSHA256 = info.Epoch, info.ActivationSHA256
		rows[i].CommandID, rows[i].InputSHA256 = info.ID, info.InputSHA256
		rows[i].CutoverSHA256, rows[i].CreatedAt = cutover, info.CreatedAt
	}
	return rows
}

func absent(ctx context.Context, collection *mongo.Collection, filter bson.M) error {
	err := collection.FindOne(ctx, filter).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err == nil {
		return ErrConflict
	}
	return err
}

// Defensive checks complement (never replace) the completed global inventory.
// They occur BEFORE the real GroupMember writer's Joined(upsert), so new
// membership-history rows cannot be mistaken for proof of old empty history.
func (s *Store) absentGroupNamespaces(ctx context.Context, gid, normal, notice string) error {
	ids := []string{normal, notice, msgprocessor.GetConversationIDBySessionType(constant.GroupChatType, gid)}
	for _, name := range []string{Sequences, Receipts, Effects, "message_visibility_floors"} {
		filter := bson.M{"$or": bson.A{bson.M{"conversation_id": bson.M{"$in": ids}}, bson.M{"branches.conversation_id": bson.M{"$in": ids}}, bson.M{"group_id": gid}}}
		if err := absent(ctx, s.db.Collection(name), filter); err != nil {
			return err
		}
	}
	for _, cid := range ids {
		// Match the full legacy doc_id namespace, not a visible/pruned max.
		if err := absent(ctx, s.db.Collection("msg"), bson.M{"doc_id": bson.M{"$regex": "^" + regexp.QuoteMeta(cid) + ":"}}); err != nil {
			return err
		}
	}
	for _, name := range []string{"group_member", "group_request", "group_membership_history"} {
		if err := absent(ctx, s.db.Collection(name), bson.M{"group_id": gid}); err != nil {
			return err
		}
	}
	for _, id := range []string{groupOriginClaimID(gid), namespaceClaimID(normal), namespaceClaimID(notice)} {
		if err := absent(ctx, s.db.Collection(Sequences), bson.M{"_id": id}); err != nil {
			return err
		}
	}
	return nil
}

type NewGroupSeedReservation struct {
	store                      *Store
	intent                     *groupauthority.NewGroupCreationIntent
	info                       groupauthority.NewGroupCreationInfo
	normal, notice, cutoverSHA string
}

func (s *Store) NewGroupCreationCommand(ctx context.Context, owner string, group *relation.GroupModel, members []*relation.GroupMemberModel) (context.Context, groupauthority.NewGroupCreationInfo, error) {
	if s == nil || s.db == nil {
		return nil, groupauthority.NewGroupCreationInfo{}, ErrUnprepared
	}
	return groupauthority.NewGroupCreationCommand(ctx, s.epoch, s.activationSHA, owner, group, members)
}

// Real CreateGroup calls this inside its existing strong Create transaction,
// before the legacy group/member writer. A read-only operator plan cannot mint
// its private live same-attempt CreationIntent. No Redis operation occurs here.
func (s *Store) BeginNewGroupSeed(ctx context.Context, gid string) (*NewGroupSeedReservation, error) {
	if s == nil || ctx == nil {
		return nil, ErrUnprepared
	}
	if err := s.requireTX(ctx); err != nil {
		return nil, err
	}
	intent, err := groupauthority.CaptureNewGroupCreationIntent(ctx, s.db, gid)
	if err != nil {
		return nil, err
	}
	info, err := intent.Inspect(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if info.Epoch != s.epoch || info.ActivationSHA256 != s.activationSHA {
		return nil, ErrConflict
	}
	_, cutover, err := s.namespaceCutover(ctx)
	if err != nil {
		return nil, err
	}
	normal, notice, err := groupNamespacePair(gid)
	if err != nil {
		return nil, err
	}
	if err := s.absentGroupNamespaces(ctx, gid, normal, notice); err != nil {
		return nil, err
	}
	for _, claim := range claimRows(info, normal, notice, cutover) {
		if _, err := s.db.Collection(Sequences).InsertOne(ctx, claim); err != nil {
			return nil, err
		}
	}
	return &NewGroupSeedReservation{store: s, intent: intent, info: info, normal: normal, notice: notice, cutoverSHA: cutover}, nil
}

type NewGroupSeedReceipt struct {
	ID               string    `bson:"_id"`
	Kind             string    `bson:"kind"`
	Epoch            string    `bson:"epoch"`
	ActivationSHA256 string    `bson:"activation_sha256"`
	CommandID        string    `bson:"command_id"`
	InputSHA256      string    `bson:"input_sha256"`
	GroupID          string    `bson:"group_id"`
	Actor            string    `bson:"actor"`
	Owner            string    `bson:"owner"`
	NormalCID        string    `bson:"normal_cid"`
	NotificationCID  string    `bson:"notification_cid"`
	GroupSHA256      string    `bson:"group_sha256"`
	MembersSHA256    string    `bson:"members_sha256"`
	EvidenceSHA256   string    `bson:"evidence_sha256"`
	CutoverSHA256    string    `bson:"cutover_sha256"`
	MemberCount      int64     `bson:"member_count"`
	CreatedAt        time.Time `bson:"created_at"`
}

func seedReceipt(info groupauthority.NewGroupCreationInfo, normal, notice, cutover, evidence string) NewGroupSeedReceipt {
	return NewGroupSeedReceipt{ID: key(info.Epoch, "new-group-seed", info.ID), Kind: "new-group-seed-pair-v1", Epoch: info.Epoch, ActivationSHA256: info.ActivationSHA256, CommandID: info.ID, InputSHA256: info.InputSHA256, GroupID: info.GroupID, Actor: info.Actor, Owner: info.Owner, NormalCID: normal, NotificationCID: notice, GroupSHA256: info.GroupSHA256, MembersSHA256: info.MembersSHA256, EvidenceSHA256: evidence, CutoverSHA256: cutover, MemberCount: int64(len(info.Members)), CreatedAt: info.CreatedAt}
}

// Finish follows the ACKed real group/member writes. Both counters, initial
// owner windows, permanent claims and immutable create receipt commit together.
// A repeated call in this SAME active attempt verifies existing rows read-only;
// after unknown commit use InspectNewGroupSeed, never rerun a client mutation.
func (s *Store) FinishNewGroupSeed(ctx context.Context, reservation *NewGroupSeedReservation) (NewGroupSeedReceipt, error) {
	if s == nil || reservation == nil || reservation.store != s || ctx == nil {
		return NewGroupSeedReceipt{}, ErrInvalid
	}
	if err := s.requireTX(ctx); err != nil {
		return NewGroupSeedReceipt{}, err
	}
	info, err := reservation.intent.Inspect(ctx, s.db)
	if err != nil || info.ID != reservation.info.ID || info.InputSHA256 != reservation.info.InputSHA256 {
		return NewGroupSeedReceipt{}, errors.Join(ErrConflict, err)
	}
	proof, err := groupauthority.CaptureNewGroupSeed(ctx, s.db, info.GroupID)
	if err != nil {
		return NewGroupSeedReceipt{}, err
	}
	actual, err := proof.Inspect(ctx, s.db)
	if err != nil || actual.InputSHA256 != info.InputSHA256 {
		return NewGroupSeedReceipt{}, errors.Join(ErrConflict, err)
	}
	_, cutover, err := s.namespaceCutover(ctx)
	if err != nil || cutover != reservation.cutoverSHA {
		return NewGroupSeedReceipt{}, errors.Join(ErrUnprepared, err)
	}
	claims := claimRows(info, reservation.normal, reservation.notice, cutover)
	for _, expected := range claims {
		var raw bson.Raw
		var current groupNamespaceClaim
		if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": expected.ID}).Decode(&raw); err != nil {
			return NewGroupSeedReceipt{}, err
		}
		if !exactFields(raw, []string{"_id", "kind", "conversation_id", "group_id", "branch", "epoch", "activation_sha256", "command_id", "input_sha256", "cutover_sha256"}, nil, nil, "created_at") || bson.Unmarshal(raw, &current) != nil {
			return NewGroupSeedReceipt{}, ErrConflict
		}
		a, _ := json.Marshal(expected)
		b, _ := json.Marshal(current)
		if string(a) != string(b) {
			return NewGroupSeedReceipt{}, ErrConflict
		}
	}
	body, err := json.Marshal([]any{info, cutover, reservation.normal, reservation.notice})
	if err != nil {
		return NewGroupSeedReceipt{}, err
	}
	evidence := hash(body)
	expected := seedReceipt(info, reservation.normal, reservation.notice, cutover, evidence)
	var old bson.Raw
	err = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": expected.ID}).Decode(&old)
	if err == nil {
		row, err := s.decodeNewGroupSeed(old)
		if err != nil {
			return NewGroupSeedReceipt{}, err
		}
		a, _ := json.Marshal(expected)
		b, _ := json.Marshal(row)
		if string(a) != string(b) {
			return NewGroupSeedReceipt{}, ErrConflict
		}
		if err := s.verifySeedState(ctx, row, info.Members); err != nil {
			return NewGroupSeedReceipt{}, err
		}
		return row, nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return NewGroupSeedReceipt{}, err
	}
	for _, cid := range []string{reservation.normal, reservation.notice} {
		if err := s.InitializeSequence(ctx, cid, Seed{MongoKnown: true, RedisKnown: true, EvidenceSHA256: evidence}); err != nil {
			return NewGroupSeedReceipt{}, err
		}
	}
	for _, member := range info.Members {
		window := MembershipWindow{ID: key(s.epoch, "membership-window", reservation.normal+"\x00"+member.UserID), Kind: "membership-window-v1", Epoch: s.epoch, ConversationID: reservation.normal, GroupID: info.GroupID, UserID: member.UserID, Revision: 1, GroupRevision: 1, LastCommand: info.ID, SeedEvidenceSHA256: evidence}
		if !s.validMembershipWindow(window, reservation.normal, member.UserID) {
			return NewGroupSeedReceipt{}, ErrInvalid
		}
		if _, err := s.db.Collection(Sequences).InsertOne(ctx, window); err != nil {
			return NewGroupSeedReceipt{}, err
		}
	}
	if _, err := s.db.Collection(Receipts).InsertOne(ctx, expected); err != nil {
		return NewGroupSeedReceipt{}, err
	}
	if err := s.verifySeedState(ctx, expected, info.Members); err != nil {
		return NewGroupSeedReceipt{}, err
	}
	return expected, nil
}

func (s *Store) verifySeedState(ctx context.Context, receipt NewGroupSeedReceipt, members []groupauthority.InitialGroupMember) error {
	for _, cid := range []string{receipt.NormalCID, receipt.NotificationCID} {
		var raw bson.Raw
		var current counter
		if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", cid)}).Decode(&raw); err != nil {
			return err
		}
		if !exactFields(raw, []string{"_id", "epoch", "conversation_id", "seed_evidence_sha256"}, []string{"maximum", "revision", "seed_maximum"}, nil, "") || bson.Unmarshal(raw, &current) != nil {
			return ErrConflict
		}
		if current.Epoch != s.epoch || current.CID != cid || current.Maximum < 0 || current.Maximum >= math.MaxInt64-1 || current.SeedMaximum != 0 || current.SeedEvidenceSHA != receipt.EvidenceSHA256 || current.Revision < 1 || current.Revision >= math.MaxInt64-1 {
			return ErrConflict
		}
	}
	for _, member := range members {
		var raw bson.Raw
		if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "membership-window", receipt.NormalCID+"\x00"+member.UserID)}).Decode(&raw); err != nil {
			return err
		}
		var window MembershipWindow
		if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"upper", "revision", "group_revision"}, []string{"closed"}, "") || bson.Unmarshal(raw, &window) != nil || !s.validMembershipWindow(window, receipt.NormalCID, member.UserID) || window.GroupID != receipt.GroupID || window.SeedEvidenceSHA256 != receipt.EvidenceSHA256 || window.GroupRevision != 1 || window.Revision != 1 || window.LastCommand != receipt.CommandID || window.Closed {
			return ErrConflict
		}
	}
	return nil
}

func (s *Store) decodeNewGroupSeed(raw bson.Raw) (NewGroupSeedReceipt, error) {
	if !exactFields(raw, []string{"_id", "kind", "epoch", "activation_sha256", "command_id", "input_sha256", "group_id", "actor", "owner", "normal_cid", "notification_cid", "group_sha256", "members_sha256", "evidence_sha256", "cutover_sha256"}, []string{"member_count"}, nil, "created_at") {
		return NewGroupSeedReceipt{}, ErrConflict
	}
	var row NewGroupSeedReceipt
	if bson.Unmarshal(raw, &row) != nil {
		return row, ErrConflict
	}
	normal, notice, err := groupNamespacePair(row.GroupID)
	if err != nil || row.Kind != "new-group-seed-pair-v1" || row.Epoch != s.epoch || row.ActivationSHA256 != s.activationSHA || !validUUID(row.CommandID) || row.ID != key(s.epoch, "new-group-seed", row.CommandID) || row.NormalCID != normal || row.NotificationCID != notice || !identity(row.Owner) || !identity(row.Actor) || row.MemberCount < 1 || row.CreatedAt.IsZero() {
		return row, ErrConflict
	}
	for _, value := range []string{row.InputSHA256, row.GroupSHA256, row.MembersSHA256, row.EvidenceSHA256, row.CutoverSHA256} {
		if !validSHA(value) {
			return row, ErrConflict
		}
	}
	return row, nil
}

// Unknown outcomes are inspected by the exact stable server command. A missing
// receipt is not rollback proof; inspection never creates/reset counters/claims.
func (s *Store) InspectNewGroupSeed(ctx context.Context, command string) (NewGroupSeedReceipt, error) {
	if s == nil || ctx == nil || !validUUID(command) || mongo.SessionFromContext(ctx) != nil {
		return NewGroupSeedReceipt{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.CheckNewGroupSeedReady(current); err != nil {
		return NewGroupSeedReceipt{}, err
	}
	var raw bson.Raw
	if err := s.db.Collection(Receipts).FindOne(current, bson.M{"_id": key(s.epoch, "new-group-seed", command)}).Decode(&raw); err != nil {
		return NewGroupSeedReceipt{}, errors.Join(ErrCommitUnconfirmed, err)
	}
	return s.decodeNewGroupSeed(raw)
}

// Operator check/plan is read-only and grants no CreationIntent. Its availability
// observation can be stale immediately; Apply is ONLY Begin/actual scoped model
// write/Finish inside the real CreateGroup transaction, never this returned plan.
// There is deliberately no "write Ready" or "seed existing group as zero" API.
type NewGroupNamespacePlan struct {
	GroupID, NormalCID, NotificationCID, CutoverSHA256 string
	State                                              string
}

func (s *Store) PlanNewGroupNamespaces(ctx context.Context, gid string) (NewGroupNamespacePlan, error) {
	if s == nil || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return NewGroupNamespacePlan{}, ErrInvalid
	}
	normal, notice, err := groupNamespacePair(gid)
	if err != nil {
		return NewGroupNamespacePlan{}, err
	}
	current, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := s.CheckNewGroupSeedReady(current); err != nil {
		return NewGroupNamespacePlan{}, err
	}
	result := NewGroupNamespacePlan{GroupID: gid, NormalCID: normal, NotificationCID: notice, State: "read-only-unclaimed-observation-not-apply-authority"}
	err = s.transaction(current, func(tx context.Context) error {
		if err := s.requireTX(tx); err != nil {
			return err
		}
		_, cutover, err := s.namespaceCutover(tx)
		if err != nil {
			return err
		}
		if err := absent(tx, s.db.Collection("group"), bson.M{"group_id": gid}); err != nil {
			return err
		}
		if err := s.absentGroupNamespaces(tx, gid, normal, notice); err != nil {
			return err
		}
		result.CutoverSHA256 = cutover
		return nil
	})
	return result, err
}
