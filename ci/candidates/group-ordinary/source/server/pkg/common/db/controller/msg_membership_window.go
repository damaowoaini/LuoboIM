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

type invalidGroupHistoryReader struct{}

func (invalidGroupHistoryReader) ReadGroupHistoryUpper(context.Context, string, string) (bool, int64, error) {
	return false, 0, errs.ErrArgs.Wrap("invalid group history reader binding")
}

func selectGroupHistoryReader(readers []GroupHistoryWindowReader) GroupHistoryWindowReader {
	if len(readers) == 0 { return nil }
	if len(readers) != 1 { return invalidGroupHistoryReader{} }
	return readers[0]
}

func (db *commonMsgDatabase) groupHistoryMaximum(ctx context.Context, owner, cid string, maximum int64) (int64, bool, error) {
	// n_ notification tips keep their own legacy Seq0/history contract. They
	// must never borrow an sg_ membership window, even for the same group.
	if db.groupHistory == nil || !strings.HasPrefix(cid, "sg_") { return maximum, false, nil }
	closed, upper, err := db.groupHistory.ReadGroupHistoryUpper(ctx, cid, owner)
	if err != nil { return 0, false, err }
	if upper < 0 || !closed && upper != 0 { return 0, false, errs.ErrData.Wrap("invalid group history upper bound") }
	if closed && upper < maximum { return upper, true, nil }
	return maximum, closed, nil
}
