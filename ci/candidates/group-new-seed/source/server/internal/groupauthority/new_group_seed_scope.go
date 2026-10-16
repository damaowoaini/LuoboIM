package groupauthority

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/mcontext"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/pkg/authverify"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type creationCommandKey struct{}
type creationCommand struct {
	info                 NewGroupCreationInfo
	groupSHA, membersSHA string
}

type InitialGroupMember struct {
	UserID string
	Role   int32
}

// Internal server command, never a request operationID or a client replay key.
// The persisted receipt contains identities/hashes, not received profile fields.
type NewGroupCreationInfo struct {
	ID, Epoch, ActivationSHA256, Actor, Owner, GroupID string
	InputSHA256, GroupSHA256, MembersSHA256            string
	Members                                            []InitialGroupMember
	GroupRevision                                      int64
	CreatedAt                                          time.Time
}

func creationHash(value any) (string, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// BSON roundtrip matches actual stored millisecond times while excluding _id.
// No revision supplied by a caller is accepted as authority.
func creationRows(group *relation.GroupModel, input []*relation.GroupMemberModel, owner string) (relation.GroupModel, []relation.GroupMemberModel, []InitialGroupMember, error) {
	if group == nil || !noticeIdentity(group.GroupID) || !noticeIdentity(owner) || len(input) == 0 || group.GroupType != constant.WorkingGroup || group.Status == constant.GroupStatusDismissed {
		return relation.GroupModel{}, nil, nil, ErrInvalidScope
	}
	var g relation.GroupModel
	body, err := bson.Marshal(group)
	if err != nil || bson.Unmarshal(body, &g) != nil {
		return g, nil, nil, ErrInvalidScope
	}
	members := make([]relation.GroupMemberModel, 0, len(input))
	seen, owners := map[string]bool{}, 0
	for _, row := range input {
		if row == nil || row.GroupID != g.GroupID || !noticeIdentity(row.UserID) || seen[row.UserID] {
			return g, nil, nil, ErrInvalidScope
		}
		seen[row.UserID] = true
		switch row.RoleLevel {
		case constant.GroupOwner:
			if row.UserID != owner {
				return g, nil, nil, ErrInvalidScope
			}
			owners++
		case constant.GroupAdmin, constant.GroupOrdinaryUsers:
			if row.UserID == owner {
				return g, nil, nil, ErrInvalidScope
			}
		default:
			return g, nil, nil, ErrInvalidScope
		}
		var member relation.GroupMemberModel
		body, err = bson.Marshal(row)
		if err != nil || bson.Unmarshal(body, &member) != nil {
			return g, nil, nil, ErrInvalidScope
		}
		members = append(members, member)
	}
	if owners != 1 {
		return g, nil, nil, ErrInvalidScope
	}
	sort.Slice(members, func(i, j int) bool { return members[i].UserID < members[j].UserID })
	identities := make([]InitialGroupMember, len(members))
	for i, m := range members {
		identities[i] = InitialGroupMember{UserID: m.UserID, Role: m.RoleLevel}
	}
	return g, members, identities, nil
}

// Capture once after existing before-callback authorization and before the
// first Mongo attempt. Callback/retry cannot change owner, data or command ID.
func NewGroupCreationCommand(ctx context.Context, epoch, activation, owner string, group *relation.GroupModel, members []*relation.GroupMemberModel) (context.Context, NewGroupCreationInfo, error) {
	if ctx == nil || !canonicalUUID(epoch) || len(activation) != 64 || !noticeIdentity(mcontext.GetOpUserID(ctx)) || ctx.Value(creationCommandKey{}) != nil {
		return nil, NewGroupCreationInfo{}, ErrInvalidScope
	}
	decoded, err := hex.DecodeString(activation)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != activation {
		return nil, NewGroupCreationInfo{}, ErrInvalidScope
	}
	if err := authverify.CheckAccessV3(ctx, owner); err != nil {
		return nil, NewGroupCreationInfo{}, err
	}
	g, rows, ids, err := creationRows(group, members, owner)
	if err != nil {
		return nil, NewGroupCreationInfo{}, err
	}
	groupSHA, err := creationHash(g)
	if err != nil {
		return nil, NewGroupCreationInfo{}, err
	}
	membersSHA, err := creationHash(rows)
	if err != nil {
		return nil, NewGroupCreationInfo{}, err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, NewGroupCreationInfo{}, ErrUnavailable
	}
	info := NewGroupCreationInfo{ID: id.String(), Epoch: epoch, ActivationSHA256: activation, Actor: mcontext.GetOpUserID(ctx), Owner: owner, GroupID: g.GroupID, GroupSHA256: groupSHA, MembersSHA256: membersSHA, Members: ids, GroupRevision: 1, CreatedAt: time.Now().UTC().Truncate(time.Millisecond)}
	info.InputSHA256, err = creationHash(info)
	if err != nil {
		return nil, NewGroupCreationInfo{}, err
	}
	command := &creationCommand{info: info, groupSHA: groupSHA, membersSHA: membersSHA}
	// Return a separate slice; mutating a returned info cannot modify authority.
	result := info
	result.Members = append([]InitialGroupMember(nil), info.Members...)
	return context.WithValue(ctx, creationCommandKey{}, command), result, nil
}

// Only an active Create:true scope whose group was absent at its fence can
// mint this capability. It expires on every abandoned/committed TX attempt.
// No constructor accepts a claimed empty CID or an arbitrary current roster.
type NewGroupSeedScope struct {
	scope   *mutationScope
	command *creationCommand
}

func boundCreation(ctx context.Context, db *mongo.Database, scope *mutationScope, command *creationCommand) error {
	if ctx == nil || scope == nil || command == nil || ctx.Value(scopeKey{}) != scope || ctx.Value(creationCommandKey{}) != command {
		return ErrInvalidScope
	}
	if err := requireCreation(ctx, db, command.info.GroupID); err != nil {
		return err
	}
	if command.info.Actor != mcontext.GetOpUserID(ctx) || len(scope.targets) != 1 {
		return ErrInvalidScope
	}
	if err := authverify.CheckAccessV3(ctx, command.info.Owner); err != nil {
		return err
	}
	target := scope.targets[command.info.GroupID]
	if target.all || len(target.members) != len(command.info.Members) {
		return ErrInvalidScope
	}
	for _, member := range command.info.Members {
		if !target.members[member.UserID] {
			return ErrInvalidScope
		}
	}
	return nil
}

// The pre-write capability exists only after the real Create fence observed no
// group row. It grants reservation of namespaces, not creation success or reads.
type NewGroupCreationIntent struct {
	scope   *mutationScope
	command *creationCommand
}

func CaptureNewGroupCreationIntent(ctx context.Context, db *mongo.Database, gid string) (*NewGroupCreationIntent, error) {
	if ctx == nil {
		return nil, ErrInvalidScope
	}
	command, ok := ctx.Value(creationCommandKey{}).(*creationCommand)
	scope, active := ctx.Value(scopeKey{}).(*mutationScope)
	if !ok || !active || command == nil || command.info.GroupID != gid {
		return nil, ErrInvalidScope
	}
	if err := boundCreation(ctx, db, scope, command); err != nil {
		return nil, err
	}
	err := db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Err()
	if err == nil {
		return nil, ErrConflict
	}
	if err != mongo.ErrNoDocuments {
		return nil, err
	}
	return &NewGroupCreationIntent{scope: scope, command: command}, nil
}

func (c *NewGroupCreationIntent) Inspect(ctx context.Context, db *mongo.Database) (NewGroupCreationInfo, error) {
	if c == nil {
		return NewGroupCreationInfo{}, ErrInvalidScope
	}
	if err := boundCreation(ctx, db, c.scope, c.command); err != nil {
		return NewGroupCreationInfo{}, err
	}
	info := c.command.info
	info.Members = append([]InitialGroupMember(nil), info.Members...)
	return info, nil
}

func (c *NewGroupSeedScope) Inspect(ctx context.Context, db *mongo.Database) (NewGroupCreationInfo, error) {
	if c == nil {
		return NewGroupCreationInfo{}, ErrInvalidScope
	}
	if err := boundCreation(ctx, db, c.scope, c.command); err != nil {
		return NewGroupCreationInfo{}, err
	}
	command := c.command
	var raw bson.Raw
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": command.info.GroupID}).Decode(&raw); err != nil {
		return NewGroupCreationInfo{}, err
	}
	rev, exists, err := revision(raw)
	if err != nil || !exists || rev != 1 {
		return NewGroupCreationInfo{}, ErrInvalidRevision
	}
	var group relation.GroupModel
	if bson.Unmarshal(raw, &group) != nil {
		return NewGroupCreationInfo{}, ErrInvalidScope
	}
	cursor, err := db.Collection("group_member").Find(ctx, bson.M{"group_id": command.info.GroupID}, options.Find().SetSort(bson.D{{Key: "user_id", Value: 1}}))
	if err != nil {
		return NewGroupCreationInfo{}, err
	}
	rows := make([]*relation.GroupMemberModel, 0, len(command.info.Members))
	for cursor.Next(ctx) {
		var member relation.GroupMemberModel
		if err = cursor.Decode(&member); err != nil {
			break
		}
		if len(rows) >= len(command.info.Members) {
			err = ErrInvalidScope
			break
		}
		rows = append(rows, &member)
	}
	if err == nil {
		err = cursor.Err()
	}
	closeErr := cursor.Close(ctx)
	if err != nil {
		return NewGroupCreationInfo{}, err
	}
	if closeErr != nil {
		return NewGroupCreationInfo{}, closeErr
	}
	g, members, _, err := creationRows(&group, rows, command.info.Owner)
	if err != nil || len(members) != len(command.info.Members) {
		return NewGroupCreationInfo{}, ErrInvalidScope
	}
	gSHA, err := creationHash(g)
	if err != nil {
		return NewGroupCreationInfo{}, err
	}
	mSHA, err := creationHash(members)
	if err != nil {
		return NewGroupCreationInfo{}, err
	}
	if gSHA != command.groupSHA || mSHA != command.membersSHA {
		return NewGroupCreationInfo{}, ErrConflict
	}
	result := command.info
	result.Members = append([]InitialGroupMember(nil), result.Members...)
	return result, nil
}

func CaptureNewGroupSeed(ctx context.Context, db *mongo.Database, gid string) (*NewGroupSeedScope, error) {
	if ctx == nil || db == nil {
		return nil, ErrInvalidScope
	}
	command, ok := ctx.Value(creationCommandKey{}).(*creationCommand)
	scope, active := ctx.Value(scopeKey{}).(*mutationScope)
	if !ok || !active || command == nil || scope == nil || command.info.GroupID != gid {
		return nil, ErrInvalidScope
	}
	proof := &NewGroupSeedScope{scope: scope, command: command}
	if _, err := proof.Inspect(ctx, db); err != nil {
		return nil, err
	}
	return proof, nil
}
