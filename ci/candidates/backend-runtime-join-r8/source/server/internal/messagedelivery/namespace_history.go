package messagedelivery

import (
	"context"
	"errors"
	"go.mongodb.org/mongo-driver/mongo"
	"math"
	"time"
)

var ErrNamespaceHistoryNotOwned = errors.New("namespace history does not belong to owner")

func (r *HistoryWindowReader) ManagedNamespaceHistory() bool { return r != nil }

func (r *HistoryWindowReader) HasNamespaceHistoryAccess(ctx context.Context, cid, owner string) (bool, error) {
	if r == nil {
		return false, nil
	}
	_, _, err := r.ReadNamespaceHistoryUpper(ctx, cid, owner)
	if errors.Is(err, ErrNamespaceHistoryNotOwned) {
		return false, nil
	}
	return err == nil, err
}

// The authenticated owner is still checked by the original RPC/controller.
// This authority is additional: a notification CID can have either a group
// or a direct origin, which its spelling alone cannot distinguish.
type namespaceHistoryPolicy struct {
	origin   NamespaceOriginDescriptor
	owner    string
	window   *MembershipWindow
	direct   *directHistoryPolicy
	segments *ownerHistorySegments
}

func (p namespaceHistoryPolicy) valid() bool {
	if !identity(p.owner) || validateNamespaceDescriptor(p.origin) != nil {
		return false
	}
	if p.origin.Kind == "group" {
		return p.window != nil && membershipWindowShape(*p.window) && p.window.ConversationID == p.origin.CID && p.window.GroupID == p.origin.GroupID && p.window.UserID == p.owner
	}
	if p.window != nil {
		return false
	}
	return p.direct != nil && p.direct.valid(p.origin, p.owner)
}

func (p namespaceHistoryPolicy) allows(seq int64) bool {
	if !p.valid() || seq < 1 || seq == math.MaxInt64 {
		return false
	}
	if p.direct != nil {
		return p.direct.allows(seq)
	}
	if p.segments != nil {
		return p.segments.allows(seq)
	}
	return p.window != nil && p.window.AllowsSequence(seq, 0)
}

func (s *Store) namespaceHistoryPolicy(ctx context.Context, cid, owner string) (namespaceHistoryPolicy, error) {
	if !identity(cid) || !identity(owner) {
		return namespaceHistoryPolicy{}, ErrInvalid
	}
	if err := s.requireTX(ctx); err != nil {
		return namespaceHistoryPolicy{}, err
	}
	if err := s.checkMembershipSchema(ctx); err != nil {
		return namespaceHistoryPolicy{}, err
	}
	origin, err := s.readNamespacePrincipals(ctx, cid)
	if err != nil {
		return namespaceHistoryPolicy{}, err
	}
	if err := s.requireNamespaceCounter(ctx, cid); err != nil {
		return namespaceHistoryPolicy{}, err
	}
	p := namespaceHistoryPolicy{origin: origin, owner: owner}
	if origin.Kind == "group" {
		w, err := s.readStoredMembershipWindow(ctx, cid, owner)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				// Only a complete import makes a missing historical-owner row a
				// definite non-owner. Partial/unknown preparation stays an error.
				if _, _, cutErr := s.namespaceCutover(ctx); cutErr != nil {
					return namespaceHistoryPolicy{}, cutErr
				}
				extra, extraErr := s.additionalHistoryUpper(ctx, origin, owner)
				if extraErr != nil {
					return namespaceHistoryPolicy{}, extraErr
				}
				if extra == 0 {
					return namespaceHistoryPolicy{}, ErrNamespaceHistoryNotOwned
				}
				// A private imported interval may be the owner's only proved old range.
				// The virtual closed-empty window is not a live member/delivery grant.
				cut, cutSHA, e := s.namespaceCutover(ctx)
				if e != nil {
					return namespaceHistoryPolicy{}, e
				}
				w = MembershipWindow{ID: key(s.epoch, "membership-window", cid+"\x00"+owner), Kind: "membership-window-v1", Epoch: s.epoch, ConversationID: cid, GroupID: origin.GroupID, UserID: owner, Closed: true, Revision: 1, GroupRevision: 1, LastCommand: cut.MigrationID, SeedEvidenceSHA256: cutSHA}
			} else {
				return namespaceHistoryPolicy{}, err
			}
		}
		p.window = &w
		p.segments, err = s.groupOwnerHistory(ctx, origin, w)
		if err != nil {
			return namespaceHistoryPolicy{}, err
		}
	} else {
		direct, err := s.directHistoryPolicy(ctx, origin, owner)
		if err != nil {
			return namespaceHistoryPolicy{}, err
		}
		p.direct = &direct
	}
	if !p.valid() {
		return namespaceHistoryPolicy{}, ErrNamespaceHistoryNotOwned
	}
	return p, nil
}

func (r *HistoryWindowReader) ReadNamespaceHistoryUpper(ctx context.Context, cid, owner string) (bool, int64, error) {
	if ctx == nil {
		return false, 0, ErrInvalid
	}
	if r == nil {
		return false, 0, nil
	} // Fully explicit legacy factory binding.
	if r.store == nil || !r.store.membershipRequired {
		return false, 0, ErrUnprepared
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.store.CheckReady(current); err != nil {
		return false, 0, err
	}
	var policy namespaceHistoryPolicy
	var noticeUpper, additionalUpper int64
	err := r.store.transaction(current, func(tx context.Context) error {
		var err error
		policy, err = r.store.namespaceHistoryPolicy(tx, cid, owner)
		if err != nil {
			return err
		}
		additionalUpper, err = r.store.additionalHistoryUpper(tx, policy.origin, owner)
		if err != nil {
			return err
		}
		noticeUpper = 0
		if policy.window != nil && policy.window.Closed {
			noticeUpper, err = r.store.noticeQueryUpper(tx, cid, owner)
		}
		return err
	})
	if err != nil {
		return false, 0, err
	}
	if policy.direct != nil {
		return true, max(policy.direct.upper(), additionalUpper), nil
	}
	if policy.segments != nil {
		return true, max(policy.segments.upper(), noticeUpper, additionalUpper), nil
	}
	if policy.window == nil || !policy.window.Closed {
		return false, 0, nil
	}
	return true, max(policy.window.Upper, noticeUpper), nil
}

// A page is a read budget, never a group or conversation capacity limit. The
// controller pages all requested positive sequences and rejects foreign keys.
// Seq0 is an online-only notification and never becomes history by this hook.
func (r *HistoryWindowReader) FilterNamespaceHistorySequences(ctx context.Context, cid, owner string, seqs []int64) (map[int64]bool, error) {
	if ctx == nil || len(seqs) < 1 || len(seqs) > 64 {
		return nil, ErrInvalid
	}
	allowed := make(map[int64]bool, len(seqs))
	seen := make(map[int64]bool, len(seqs))
	for _, seq := range seqs {
		if seq < 1 || seq == math.MaxInt64 || seen[seq] {
			return nil, ErrInvalid
		}
		seen[seq] = true
	}
	if r == nil {
		for _, seq := range seqs {
			allowed[seq] = true
		}
		return allowed, nil
	}
	if r.store == nil || !r.store.membershipRequired {
		return nil, ErrUnprepared
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := r.store.CheckReady(current); err != nil {
		return nil, err
	}
	err := r.store.transaction(current, func(tx context.Context) error {
		p, err := r.store.namespaceHistoryPolicy(tx, cid, owner)
		if err != nil {
			return err
		}
		// The callback can be retried only after a known aborted read-only TX.
		// Never retain values from a discarded snapshot.
		for seq := range allowed {
			delete(allowed, seq)
		}
		for _, seq := range seqs {
			if p.allows(seq) {
				allowed[seq] = true
			}
		}
		floor := int64(0)
		if p.direct != nil {
			floor = max(p.direct.floor, p.direct.historicalFloor)
		} else if p.segments != nil {
			floor = max(p.segments.Floor, p.segments.HistoricalFloor)
		}
		extra, err := r.store.readAdditionalHistory(tx, p.origin, owner, seqs, floor)
		if err != nil {
			return err
		}
		for seq := range extra {
			allowed[seq] = true
		}
		if p.window != nil && p.window.Closed {
			keys := []memberNoticeKey{}
			for _, seq := range seqs {
				if !allowed[seq] && seq >= p.window.Lower && (p.segments == nil || seq >= max(p.segments.Floor, p.segments.HistoricalFloor)) {
					keys = append(keys, memberNoticeKey{Owner: owner, Seq: seq})
				}
			}
			if len(keys) > 0 {
				rows, err := r.store.readNoticeEntitlements(tx, cid, keys)
				if err != nil {
					return err
				}
				for _, e := range rows {
					allowed[e.Seq] = true
				}
			}
		}
		if err := r.store.filterGroupHistoryScopes(tx, p.origin, owner, allowed); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return allowed, nil
}
