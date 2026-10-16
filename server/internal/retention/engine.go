package retention

import (
	"context"
	"time"
)

type Repository interface {
	Source
	Cursor(context.Context) (Cursor, error)
	Page(context.Context, Cursor, int) ([]Policy, error)
	SaveCursor(context.Context, Cursor, Cursor) error
	Floor(context.Context, Policy) (int64, error)
	Commit(context.Context, Plan) error
}

type Summary struct{ Visited, Committed, Skipped, Failed int }

// Run visits one stable owner/CID page. Failed rows are revisited on the next
// cursor pass, and cannot block all later owners indefinitely.
func Run(ctx context.Context, store Repository, now time.Time) (Summary, error) {
	var summary Summary
	before, err := store.Cursor(ctx)
	if err != nil {
		return summary, err
	}
	rows, err := store.Page(ctx, before, 50)
	if err != nil {
		return summary, err
	}
	next := Cursor{}
	for _, p := range rows {
		if err = ctx.Err(); err != nil {
			return summary, err
		}
		next.Owner, next.Conversation = p.Owner, p.Conversation
		summary.Visited++
		if !p.Eligible() {
			summary.Skipped++
			continue
		}
		floor, err := store.Floor(ctx, p)
		if err != nil {
			summary.Failed++
			continue
		}
		plan, err := BuildPlan(ctx, store, p, floor, now, MaximumChunk)
		if err != nil {
			summary.Failed++
			continue
		}
		if err = store.Commit(ctx, plan); err != nil {
			if isStale(err) {
				summary.Skipped++
			} else {
				summary.Failed++
			}
			continue
		}
		summary.Committed++
	}
	return summary, store.SaveCursor(ctx, before, next)
}
