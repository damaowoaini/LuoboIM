package groupauthority

import "context"

// BEFORE REFERENCE ONLY, NEVER PRODUCTION: faithfully represent the current
// RPC's final permission read outside the subsequent writer transaction. Even
// making that writer transaction stronger cannot prevent this disjoint race.
// It does not invoke the old controller or claim a runtime exploit. The same
// real Mongo schedule/assertions and positive control run in both phases.
func barrierMutation(store *Store, ctx context.Context, targets []Target, authorize, write func(context.Context) error) (*Commit, error) {
	if err := authorize(ctx); err != nil {
		return nil, err
	}
	if err := store.transaction(ctx, write); err != nil {
		return nil, err
	}
	return &Commit{store: store}, nil
}
