package messagedelivery

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	HistoryWindowModeEnv = "LUOBO_GROUP_HISTORY_WINDOW_V1"
	HistoryWindowEpochEnv = "LUOBO_GROUP_HISTORY_WINDOW_EPOCH"
	HistoryWindowActivationEnv = "LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256"
)

// NewMembershipBoundStore opts this INSTANCE into the additional membership
// schema gate. It performs no reads, DDL, seed, legacy-owner inference or
// activation. A deployment still needs the explicit drained allocator and
// owner-window migration; a marker is not evidence that these steps happened.
func NewMembershipBoundStore(db *mongo.Database, epoch, activationSHA string) (*Store, error) {
	s, err := NewStore(db, epoch, activationSHA)
	if err != nil { return nil, err }
	s.membershipRequired = true
	return s, nil
}

// CheckLegacyMsgTransferHistoryMode is the real old writer's startup fence.
// A half-on reader flag must not leave a legacy Redis allocator serving the
// same CID as the shared Mongo allocator. This slice has no managed ordinary
// intake replacement, so required mode is deliberately not activatable yet.
func CheckLegacyMsgTransferHistoryMode() error {
 mode, epoch, activation := os.Getenv(HistoryWindowModeEnv), os.Getenv(HistoryWindowEpochEnv), os.Getenv(HistoryWindowActivationEnv)
 if (mode == "" || mode == "0") && epoch == "" && activation == "" { return nil }
 return ErrUnprepared
}

type HistoryWindowReader struct { store *Store }

// Explicitly bound runtime factories capture immutable settings once. Legacy
// mode is preserved only when ALL three settings are absent (or mode=0).
// Invalid/half-on settings fail service initialization; no lazy fallback.
func NewHistoryWindowReaderFromEnvironment(db *mongo.Database) (*HistoryWindowReader, error) {
	mode, epoch, activation := os.Getenv(HistoryWindowModeEnv), os.Getenv(HistoryWindowEpochEnv), os.Getenv(HistoryWindowActivationEnv)
	if (mode == "" || mode == "0") && epoch == "" && activation == "" { return nil, nil }
	if mode != "required" { return nil, ErrUnprepared }
	s, err := NewMembershipBoundStore(db, epoch, activation)
	if err != nil { return nil, err }
	return &HistoryWindowReader{store: s}, nil
}

func (r *HistoryWindowReader) ReadGroupHistoryUpper(ctx context.Context, cid, owner string) (bool, int64, error) {
	// A typed nil is the explicit legacy-mode adapter supplied by factories.
	if r == nil { return false, 0, nil }
	if r.store == nil { return false, 0, ErrUnprepared }
	w, err := r.store.ReadMembershipWindow(ctx, cid, owner)
	if err != nil { return false, 0, err }
	return w.Closed, w.Upper, nil
}

func groupWindowCID(cid string) bool {
	gid := strings.TrimPrefix(cid, "sg_")
	return strings.HasPrefix(cid, "sg_") && identity(gid) && msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, gid) == cid
}

// A page-sized authority read, not a group-capacity limit. The caller pages its
// frozen recipients. Missing, duplicate, foreign or malformed rows fail closed;
// no per-owner N-query fallback and no implicit open-window creation exists.
func (s *Store) ReadMembershipWindows(ctx context.Context, cid string, owners []string) (map[string]MembershipWindow, error) {
	if s == nil || s.db == nil || ctx == nil || !groupWindowCID(cid) || len(owners) < 1 || len(owners) > 64 { return nil, ErrInvalid }
	wanted := make(map[string]bool, len(owners))
	ids := make([]string, 0, len(owners))
	for _, uid := range owners {
		if !identity(uid) || wanted[uid] { return nil, ErrInvalid }
		wanted[uid] = true
		ids = append(ids, key(s.epoch, "membership-window", cid+"\x00"+uid))
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if mongo.SessionFromContext(current) == nil {
		if err := s.CheckReady(current); err != nil { return nil, err }
	} else if err := s.requireTX(current); err != nil { return nil, err }
	if err := s.checkMembershipSchema(current); err != nil { return nil, err }
	cursor, err := s.db.Collection(Sequences).Find(current, bson.M{"_id": bson.M{"$in": ids}}, options.Find().SetLimit(int64(len(owners)+1)))
	if err != nil { return nil, err }
	var rows []bson.Raw
	err = cursor.All(current, &rows)
	closeErr := cursor.Close(current)
	if err != nil || closeErr != nil { return nil, errors.Join(err, closeErr) }
	if len(rows) != len(owners) { return nil, ErrUnprepared }
	result := make(map[string]MembershipWindow, len(rows))
	for _, raw := range rows {
		if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"upper", "revision", "group_revision"}, []string{"closed"}, "") { return nil, ErrConflict }
		var w MembershipWindow
		if bson.Unmarshal(raw, &w) != nil || !wanted[w.UserID] || result[w.UserID].ID != "" || !s.validMembershipWindow(w, cid, w.UserID) { return nil, ErrConflict }
		result[w.UserID] = w
	}
	return result, nil
}

func (s *Store) membershipPage(ctx context.Context, cid string, owners []string, seq int64) (map[string]bool, error) {
	denied := make(map[string]bool)
	if !s.membershipRequired || !strings.HasPrefix(cid, "sg_") || len(owners) == 0 { return denied, nil }
	windows, err := s.ReadMembershipWindows(ctx, cid, owners)
	if err != nil { return nil, err }
	for _, uid := range owners {
		if !windows[uid].AllowsSequence(seq, 0) { denied[uid] = true }
	}
	return denied, nil
}
