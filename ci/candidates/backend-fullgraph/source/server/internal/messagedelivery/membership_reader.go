package messagedelivery

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	HistoryWindowModeEnv       = "LUOBO_GROUP_HISTORY_WINDOW_V1"
	HistoryWindowEpochEnv      = "LUOBO_GROUP_HISTORY_WINDOW_EPOCH"
	HistoryWindowActivationEnv = "LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256"
)

// NewMembershipBoundStore opts this INSTANCE into the additional membership
// schema gate. It performs no reads, DDL, seed, legacy-owner inference or
// activation. A deployment still needs the explicit drained allocator and
// owner-window migration; a marker is not evidence that these steps happened.
func NewMembershipBoundStore(db *mongo.Database, epoch, activationSHA string) (*Store, error) {
	s, err := NewStore(db, epoch, activationSHA)
	if err != nil {
		return nil, err
	}
	s.membershipRequired = true
	return s, nil
}

// CheckLegacyMsgTransferHistoryMode is the real old writer's startup fence.
// A half-on reader flag must not leave a legacy Redis allocator serving the
// same CID as the shared Mongo allocator. This slice has no managed ordinary
// intake replacement, so required mode is deliberately not activatable yet.
func CheckLegacyMsgTransferHistoryMode() error {
	mode, epoch, activation := os.Getenv(HistoryWindowModeEnv), os.Getenv(HistoryWindowEpochEnv), os.Getenv(HistoryWindowActivationEnv)
	if (mode == "" || mode == "0") && epoch == "" && activation == "" {
		return nil
	}
	return ErrUnprepared
}

type HistoryWindowReader struct{ store *Store }

// Explicitly bound runtime factories capture immutable settings once. Legacy
// mode is preserved only when ALL three settings are absent (or mode=0).
// Invalid/half-on settings fail service initialization; no lazy fallback.
func NewHistoryWindowReaderFromEnvironment(db *mongo.Database) (*HistoryWindowReader, error) {
	mode, epoch, activation := os.Getenv(HistoryWindowModeEnv), os.Getenv(HistoryWindowEpochEnv), os.Getenv(HistoryWindowActivationEnv)
	if (mode == "" || mode == "0") && epoch == "" && activation == "" {
		return nil, nil
	}
	if mode != "required" {
		return nil, ErrUnprepared
	}
	s, err := NewMembershipBoundStore(db, epoch, activation)
	if err != nil {
		return nil, err
	}
	return &HistoryWindowReader{store: s}, nil
}

// Called by every real required-mode startup factory before exposing its
// reader/controller. Construction still performs no I/O or implicit DDL.
func (r *HistoryWindowReader) CheckReady(ctx context.Context) error {
	if ctx == nil {
		return ErrUnprepared
	}
	if r == nil {
		return nil
	} // Explicit complete legacy-mode environment only.
	if r.store == nil {
		return ErrUnprepared
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.store.CheckReady(current)
}

func (r *HistoryWindowReader) ReadGroupHistoryUpper(ctx context.Context, cid, owner string) (bool, int64, error) {
	return r.ReadNamespaceHistoryUpper(ctx, cid, owner)
}

func groupWindowCID(cid string) bool {
	for _, prefix := range []string{"sg_", "g_", "n_"} {
		if strings.HasPrefix(cid, prefix) && identity(strings.TrimPrefix(cid, prefix)) {
			return true
		}
	}
	return false
}

// A page-sized authority read, not a group-capacity limit. The caller pages its
// frozen recipients. Missing, duplicate, foreign or malformed rows fail closed;
// no legacy owner fallback or implicit open-window creation exists. Shared
// prospective proof reads stay inside this bounded page and the SAME read TX.
func (s *Store) ReadMembershipWindows(ctx context.Context, cid string, owners []string) (map[string]MembershipWindow, error) {
	if s == nil || ctx == nil {
		return nil, ErrInvalid
	}
	var result map[string]MembershipWindow
	read := func(current context.Context) error {
		stored, e := s.readStoredMembershipWindows(current, cid, owners)
		if e != nil {
			return e
		}
		d, e := s.readNamespacePrincipals(current, cid)
		if e != nil {
			return e
		}
		result = make(map[string]MembershipWindow, len(stored))
		for uid, w := range stored {
			next, e := s.prospectiveMemberWindow(current, d, w)
			if e != nil {
				return e
			}
			result[uid] = next
		}
		return nil
	}
	if mongo.SessionFromContext(ctx) != nil {
		e := read(ctx)
		return result, e
	}
	if e := s.CheckReady(ctx); e != nil {
		return nil, e
	}
	e := s.transaction(ctx, read)
	return result, e
}
func (s *Store) readStoredMembershipWindows(ctx context.Context, cid string, owners []string) (map[string]MembershipWindow, error) {
	if s == nil || s.db == nil || ctx == nil || !groupWindowCID(cid) || len(owners) < 1 || len(owners) > 64 {
		return nil, ErrInvalid
	}
	wanted := make(map[string]bool, len(owners))
	ids := make([]string, 0, len(owners))
	for _, uid := range owners {
		if !identity(uid) || wanted[uid] {
			return nil, ErrInvalid
		}
		wanted[uid] = true
		ids = append(ids, key(s.epoch, "membership-window", cid+"\x00"+uid))
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if mongo.SessionFromContext(current) == nil {
		if err := s.CheckReady(current); err != nil {
			return nil, err
		}
	} else if err := s.requireTX(current); err != nil {
		return nil, err
	}
	if err := s.checkMembershipSchema(current); err != nil {
		return nil, err
	}
	origin, err := s.readNamespacePrincipals(current, cid)
	if err != nil {
		return nil, err
	}
	if origin.Kind != "group" {
		return nil, ErrConflict
	}
	cursor, err := s.db.Collection(Sequences).Find(current, bson.M{"_id": bson.M{"$in": ids}}, options.Find().SetLimit(int64(len(owners)+1)))
	if err != nil {
		return nil, err
	}
	var rows []bson.Raw
	err = cursor.All(current, &rows)
	closeErr := cursor.Close(current)
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	if len(rows) != len(owners) {
		return nil, ErrUnprepared
	}
	result := make(map[string]MembershipWindow, len(rows))
	for _, raw := range rows {
		if !exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "group_id", "user_id", "last_command", "seed_evidence_sha256"}, []string{"lower", "upper", "revision", "group_revision"}, []string{"closed"}, "") {
			return nil, ErrConflict
		}
		var w MembershipWindow
		if bson.Unmarshal(raw, &w) != nil || !wanted[w.UserID] || result[w.UserID].ID != "" || !s.validMembershipWindow(w, cid, w.UserID) || w.GroupID != origin.GroupID {
			return nil, ErrConflict
		}
		result[w.UserID] = w
	}
	return result, nil
}

func (s *Store) membershipPage(ctx context.Context, cid string, owners []string, seq int64) (map[string]bool, error) {
	denied := make(map[string]bool)
	if !s.membershipRequired || len(owners) == 0 {
		return denied, nil
	}
	if ctx == nil || len(owners) > 64 {
		return nil, ErrInvalid
	}
	if mongo.SessionFromContext(ctx) == nil {
		if err := s.CheckReady(ctx); err != nil {
			return nil, err
		}
		err := s.transaction(ctx, func(tx context.Context) error {
			var err error
			denied, err = s.membershipPage(tx, cid, owners, seq)
			return err
		})
		if err != nil {
			return nil, err
		}
		return denied, nil
	}
	if err := s.requireTX(ctx); err != nil {
		return nil, err
	}
	origin, err := s.readNamespacePrincipals(ctx, cid)
	if err != nil {
		return nil, err
	}
	if origin.Kind != "group" {
		return denied, nil
	}
	windows, err := s.ReadMembershipWindows(ctx, cid, owners)
	if err != nil {
		return nil, err
	}
	for _, uid := range owners {
		if !windows[uid].AllowsSequence(seq, 0) {
			denied[uid] = true
		}
	}
	if seq > 0 {
		keys := []memberNoticeKey{}
		for _, uid := range owners {
			w := windows[uid]
			if denied[uid] && w.Closed && seq >= w.Lower {
				keys = append(keys, memberNoticeKey{Owner: uid, Seq: seq})
			}
		}
		if len(keys) > 0 {
			allowed, err := s.readNoticeEntitlements(ctx, cid, keys)
			if err != nil {
				return nil, err
			}
			for _, e := range allowed {
				delete(denied, e.UserID)
			}
		}
	}
	return denied, nil
}
