package controller

import (
	"context"
	"strings"

	"github.com/OpenIMSDK/tools/errs"
)

// GroupHistoryWindowReader only supplies an authenticated owner's upper
// history bound. It grants neither membership nor message-write permission.
// A closed zero is distinct from the legacy max_seq=0 (unbounded) contract.
type GroupHistoryWindowReader interface {
	ReadGroupHistoryUpper(context.Context, string, string) (closed bool, upper int64, err error)
}

// A managed reader resolves the original typed namespace and can authorize
// isolated notices after an owner leaves. Its maximum only bounds a query;
// it must never authorize the intervening sequence gap on its own.
type NamespaceHistoryReader interface {
	ReadNamespaceHistoryUpper(context.Context, string, string) (closed bool, upper int64, err error)
	FilterNamespaceHistorySequences(context.Context, string, string, []int64) (map[int64]bool, error)
}

type namespaceHistoryAccessReader interface {
	ManagedNamespaceHistory() bool
	HasNamespaceHistoryAccess(context.Context, string, string) (bool, error)
}

// RPC preflight distinguishes a fully explicit legacy binding from required
// managed mode. Managed authorization never falls back to parsing n_ names.
func (db *commonMsgDatabase) ManagedNamespaceHistoryAccess(ctx context.Context, owner, cid string) (bool, bool, error) {
	r, ok := db.groupHistory.(namespaceHistoryAccessReader)
	if !ok || !r.ManagedNamespaceHistory() {
		return false, false, nil
	}
	allowed, err := r.HasNamespaceHistoryAccess(ctx, cid, owner)
	return true, allowed, err
}

type invalidGroupHistoryReader struct{}

func (invalidGroupHistoryReader) ReadGroupHistoryUpper(context.Context, string, string) (bool, int64, error) {
	return false, 0, errs.ErrArgs.Wrap("invalid group history reader binding")
}

func (invalidGroupHistoryReader) ManagedNamespaceHistory() bool { return true }
func (invalidGroupHistoryReader) HasNamespaceHistoryAccess(context.Context, string, string) (bool, error) {
	return false, errs.ErrArgs.Wrap("invalid namespace history reader binding")
}
func (invalidGroupHistoryReader) ReadNamespaceHistoryUpper(context.Context, string, string) (bool, int64, error) {
	return false, 0, errs.ErrArgs.Wrap("invalid namespace history reader binding")
}
func (invalidGroupHistoryReader) FilterNamespaceHistorySequences(context.Context, string, string, []int64) (map[int64]bool, error) {
	return nil, errs.ErrArgs.Wrap("invalid namespace history reader binding")
}

func selectGroupHistoryReader(readers []GroupHistoryWindowReader) GroupHistoryWindowReader {
	if len(readers) == 0 {
		return nil
	}
	if len(readers) != 1 {
		return invalidGroupHistoryReader{}
	}
	return readers[0]
}

func (db *commonMsgDatabase) groupHistoryMaximum(ctx context.Context, owner, cid string, maximum int64) (int64, bool, error) {
	if reader, ok := db.groupHistory.(NamespaceHistoryReader); ok {
		closed, upper, err := reader.ReadNamespaceHistoryUpper(ctx, cid, owner)
		if err != nil {
			return 0, false, err
		}
		if upper < 0 || !closed && upper != 0 {
			return 0, false, errs.ErrData.Wrap("invalid namespace history upper bound")
		}
		if closed && upper < maximum {
			return upper, true, nil
		}
		return maximum, closed, nil
	}
	// n_ notification tips keep their own legacy Seq0/history contract. They
	// must never borrow an sg_ membership window, even for the same group.
	if db.groupHistory == nil || !strings.HasPrefix(cid, "sg_") {
		return maximum, false, nil
	}
	closed, upper, err := db.groupHistory.ReadGroupHistoryUpper(ctx, cid, owner)
	if err != nil {
		return 0, false, err
	}
	if upper < 0 || !closed && upper != 0 {
		return 0, false, errs.ErrData.Wrap("invalid group history upper bound")
	}
	if closed && upper < maximum {
		return upper, true, nil
	}
	return maximum, closed, nil
}

func (db *commonMsgDatabase) managedHistorySequences(ctx context.Context, owner, cid string, seqs []int64) (map[int64]bool, bool, error) {
	reader, managed := db.groupHistory.(NamespaceHistoryReader)
	if !managed {
		return nil, false, nil
	}
	wanted := make(map[int64]bool, len(seqs))
	unique := make([]int64, 0, len(seqs))
	for _, seq := range seqs {
		if seq > 0 && !wanted[seq] {
			wanted[seq] = true
			unique = append(unique, seq)
		}
	}
	allowed := make(map[int64]bool, len(unique))
	for offset := 0; offset < len(unique); offset += 64 {
		page := unique[offset:min(offset+64, len(unique))]
		rows, err := reader.FilterNamespaceHistorySequences(ctx, cid, owner, page)
		if err != nil {
			return nil, true, err
		}
		pageWanted := make(map[int64]bool, len(page))
		for _, seq := range page {
			pageWanted[seq] = true
		}
		for seq, permit := range rows {
			if !pageWanted[seq] {
				return nil, true, errs.ErrData.Wrap("foreign namespace history sequence")
			}
			if permit {
				allowed[seq] = true
			}
		}
	}
	return allowed, true, nil
}
