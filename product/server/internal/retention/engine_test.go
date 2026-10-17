package retention

import (
	"context"
	"errors"
	"testing"
)

type repositoryFixture struct {
	sourceFixture
	rows          []Policy
	before, saved Cursor
	requested     int
	commits       []Plan
	commitError   error
}

func (f *repositoryFixture) Cursor(context.Context) (Cursor, error) { return f.before, nil }
func (f *repositoryFixture) Page(_ context.Context, c Cursor, n int) ([]Policy, error) {
	if c != f.before {
		return nil, errors.New("cursor mismatch")
	}
	f.requested = n
	return f.rows, nil
}
func (f *repositoryFixture) SaveCursor(_ context.Context, before, next Cursor) error {
	if before != f.before {
		return errors.New("cursor mismatch")
	}
	f.saved = next
	return nil
}
func (f *repositoryFixture) Floor(context.Context, Policy) (int64, error) { return 1, nil }
func (f *repositoryFixture) Commit(_ context.Context, p Plan) error {
	f.commits = append(f.commits, p)
	return f.commitError
}

func TestRetentionRunStableCursorAdvancesPastRejectedPolicyAndStaleSnapshot(t *testing.T) {
	valid := validPolicy()
	invalid := valid
	invalid.Owner = "later"
	invalid.Enabled = false
	f := &repositoryFixture{rows: []Policy{valid, invalid}, commitError: ErrPolicyChanged, sourceFixture: sourceFixture{slots: []Slot{{Seq: 1, Known: true, SendTime: 1, Document: "doc"}}}}
	summary, err := Run(context.Background(), f, testNow)
	if err != nil || summary.Visited != 2 || summary.Committed != 0 || summary.Skipped != 2 || len(f.commits) != 1 || f.requested != 50 {
		t.Fatal("bounded/stale scan accounting incorrect")
	}
	if f.saved.Owner != "later" || f.saved.Conversation != invalid.Conversation {
		t.Fatal("failed row stalled global cursor")
	}
}

func TestRetentionRunReadErrorCannotReachCommitAndEmptyPageWrapsCursor(t *testing.T) {
	f := &repositoryFixture{rows: []Policy{validPolicy()}, sourceFixture: sourceFixture{err: errors.New("synthetic storage failure")}}
	summary, err := Run(context.Background(), f, testNow)
	if err != nil || summary.Failed != 1 || len(f.commits) != 0 {
		t.Fatal("read failure reached commit")
	}
	f = &repositoryFixture{before: Cursor{Owner: "last", Conversation: "last", Version: 7}}
	if _, err = Run(context.Background(), f, testNow); err != nil || f.saved.Owner != "" || f.saved.Conversation != "" {
		t.Fatal("complete cursor did not wrap")
	}
}
