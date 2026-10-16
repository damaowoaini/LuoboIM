package groupauthority

import "context"

// Cloud after adapter. Only this test-only function is replaced for the before
// phase. The production Store, fixture barriers, positive controls and assertions
// remain identical. This is helper evidence, not old runtime RPC execution.
func barrierMutation(store *Store, ctx context.Context, targets []Target, authorize, write func(context.Context) error) (*Commit, error) {
	return store.Mutation(ctx, targets, authorize, write)
}
