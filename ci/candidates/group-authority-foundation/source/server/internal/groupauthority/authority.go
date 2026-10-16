// Package groupauthority serializes each group's permission-sensitive writes.
// This candidate is not wired into a running service. Wire compatibility is
// unchanged: the revision and scope are server-only storage metadata.
package groupauthority

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/mongo"
)

const RevisionField = "authz_revision"
const InvalidationsCollection = "group_authz_cache_invalidations"

var (
	ErrUnavailable       = errors.New("group authority requires a strong Mongo transaction")
	ErrInvalidScope      = errors.New("group mutation scope does not match writer")
	ErrInvalidRevision   = errors.New("group authority revision is invalid")
	ErrConflict          = errors.New("group authority state changed")
	ErrMissing           = errors.New("group authority document is missing")
	ErrCacheSyncPending  = errors.New("group write committed; cache synchronization pending")
	ErrCommitUnconfirmed = errors.New("group transaction commit is unconfirmed; do not replay")
	ErrRetryLimit        = errors.New("group transaction retry budget exhausted")
)

// Target names an existing group, or an exact group to be created by this
// transaction. Creation is an explicit operation; a missing old group never
// silently becomes a new group. Targets are sorted/deduplicated before locking.
type Target struct {
	GroupID string
	Create  bool
	// Exact immutable request members, checked by final action authorization.
	// AllMembers is reserved for a whole-group operation such as dismiss-all;
	// its actual member set is read inside this transaction after authorization.
	Members    []string
	AllMembers bool
}

type Store struct {
	db *mongo.Database
}

// NewStore does no I/O or DDL. Runtime/catalog preparation is explicit.
func NewStore(db *mongo.Database) (*Store, error) {
	if db == nil || db.Client() == nil || db.Name() == "" {
		return nil, ErrUnavailable
	}
	return &Store{db: db}, nil
}

type scopeKey struct{}
type targetScope struct {
	create  bool
	all     bool
	members map[string]bool
}
type mutationScope struct {
	store     *Store
	active    atomic.Bool
	targets   map[string]*targetScope
	cacheKeys map[string]struct{}
}

func normalizeTargets(input []Target) ([]Target, error) {
	if len(input) == 0 {
		return nil, ErrInvalidScope
	}
	seen := make(map[string]*targetScope, len(input))
	for _, target := range input {
		if target.GroupID == "" || len(target.GroupID) > 256 || strings.TrimSpace(target.GroupID) != target.GroupID || target.Create && target.AllMembers {
			return nil, ErrInvalidScope
		}
		current := seen[target.GroupID]
		if current == nil {
			current = &targetScope{create: target.Create, all: target.AllMembers, members: make(map[string]bool)}
			seen[target.GroupID] = current
		} else if current.create != target.Create || current.all != target.AllMembers {
			return nil, ErrInvalidScope
		}
		for _, id := range target.Members {
			if id == "" || len(id) > 256 || strings.TrimSpace(id) != id {
				return nil, ErrInvalidScope
			}
			current.members[id] = true
		}
	}
	result := make([]Target, 0, len(seen))
	for id, current := range seen {
		target := Target{GroupID: id, Create: current.create, AllMembers: current.all}
		for userID := range current.members {
			target.Members = append(target.Members, userID)
		}
		sort.Strings(target.Members)
		result = append(result, target)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GroupID < result[j].GroupID })
	return result, nil
}

// Require is the low-level writer guard. A session, opuser, or a second Store
// with the same names cannot create this private, live scope. Every participating
// group/member/request model writer must call it before accessing Mongo.
func Require(ctx context.Context, db *mongo.Database, groupID string) error {
	if ctx == nil {
		return ErrInvalidScope
	}
	scope, ok := ctx.Value(scopeKey{}).(*mutationScope)
	if !ok || scope == nil || !scope.active.Load() || scope.store == nil || db == nil || db.Client() != scope.store.db.Client() || db.Name() != scope.store.db.Name() {
		return ErrInvalidScope
	}
	if _, ok := scope.targets[groupID]; !ok {
		return ErrInvalidScope
	}
	return strongSession(ctx, scope.store.db.Client())
}

func requireCreation(ctx context.Context, db *mongo.Database, groupID string) error {
	if err := Require(ctx, db, groupID); err != nil {
		return err
	}
	scope := ctx.Value(scopeKey{}).(*mutationScope)
	if !scope.targets[groupID].create {
		return ErrInvalidScope
	}
	return nil
}

// Commit is returned only after the driver has acknowledged commit. On an
// unknown commit outcome no caller gets this capability; durable cache jobs
// remain discoverable if that transaction actually committed.
type Commit struct {
	store     *Store
	cacheTask string
}

// Mutation takes the actual group-document write conflicts before re-reading
// authorization. Both callbacks must contain only Mongo work in this session.
// External before-callbacks run once before this method; notification/cache/
// after-callback code must run only after the returned Commit is non-nil.
func (s *Store) Mutation(ctx context.Context, targets []Target, authorize, write func(context.Context) error) (*Commit, error) {
	if s == nil || s.db == nil || ctx == nil || authorize == nil || write == nil || mongo.SessionFromContext(ctx) != nil {
		return nil, ErrUnavailable
	}
	// Covers readiness and every transaction/commit retry even when the RPC
	// caller supplied no deadline. A shorter parent deadline still wins.
	ctx, cancel := context.WithTimeout(ctx, mutationTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ordered, err := normalizeTargets(targets)
	if err != nil {
		return nil, err
	}
	if err := s.CheckReady(ctx); err != nil {
		return nil, err
	}
	id := uuid.NewString() // Stable across driver transaction retries, no payload idempotency claim.
	created := time.Now().UTC()
	var finalTask string
	err = s.transaction(ctx, func(current context.Context) error {
		finalTask = "" // Discard abandoned attempt effects on every retry.
		scope := &mutationScope{store: s, targets: make(map[string]*targetScope), cacheKeys: make(map[string]struct{})}
		scope.active.Store(true)
		defer scope.active.Store(false)
		for _, target := range ordered {
			if err := s.fence(current, target); err != nil {
				return err
			}
			members := make(map[string]bool, len(target.Members))
			for _, id := range target.Members {
				members[id] = true
			}
			scope.targets[target.GroupID] = &targetScope{create: target.Create, all: target.AllMembers, members: members}
		}
		current = context.WithValue(current, scopeKey{}, scope)
		if err := authorize(current); err != nil {
			return err
		}
		if err := scope.prepareInvalidations(current); err != nil {
			return err
		}
		if err := write(current); err != nil {
			return err
		}
		for _, target := range ordered {
			if target.Create {
				if err := s.checkCreated(current, target.GroupID); err != nil {
					return err
				}
			}
		}
		if len(scope.cacheKeys) == 0 {
			return nil
		}
		keys := make([]string, 0, len(scope.cacheKeys))
		for key := range scope.cacheKeys {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		if err := s.insertInvalidation(current, id, created, keys); err != nil {
			return err
		}
		finalTask = id
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Commit{store: s, cacheTask: finalTask}, nil
}

// RequireMembers protects member/request writes in addition to the group fence.
// An exact group scope does not authorize arbitrary UIDs. Whole-group deletion
// uses RequireAllMembers explicitly, never an empty slice that widens a query.
func RequireMembers(ctx context.Context, db *mongo.Database, groupID string, userIDs []string) error {
	if err := Require(ctx, db, groupID); err != nil {
		return err
	}
	if len(userIDs) == 0 {
		return ErrInvalidScope
	}
	target := ctx.Value(scopeKey{}).(*mutationScope).targets[groupID]
	for _, id := range userIDs {
		if !target.members[id] {
			return ErrInvalidScope
		}
	}
	return nil
}

func RequireAllMembers(ctx context.Context, db *mongo.Database, groupID string) error {
	if err := Require(ctx, db, groupID); err != nil {
		return err
	}
	if !ctx.Value(scopeKey{}).(*mutationScope).targets[groupID].all {
		return ErrInvalidScope
	}
	return nil
}
