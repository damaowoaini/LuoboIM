package unrelation

import (
	"context"
	"go.mongodb.org/mongo-driver/mongo"
)

// A visibility restriction is monotonic: empty Owners means all readers. Use
// EITHER exact Sequences OR the exclusive floor Below, never an unbounded filter.
// This carries no authorization: only the existing already-authorized writers
// construct it; the durable coordinator verifies its DB/epoch/transaction.
type PendingVisibilityChange struct {
	ConversationID string
	Owners         []string
	Sequences      []int64
	Below          int64
	// FloorOnly is set only by explicit internal retention callers. It never
	// comes from wire input and must not reinterpret a user Clear.
	FloorOnly bool
}

// MutateMessageVisibility must commit the existing source mutation and pending
// payload retirement in the SAME strong transaction. It never calls Redis,
// callbacks, push or creates an index. A configured adapter cannot degrade to
// raw writes when its catalog/session/epoch is unavailable.
type PendingDeliveryVisibility interface {
	MutateMessageVisibility(context.Context, *mongo.Database, []PendingVisibilityChange, func(context.Context) error) error
}

// RetentionFloorContext marks only the existing bounded retention floor write.
// The ordinary floor/Clear API retains its generation-revoking semantics.
type retentionFloorContextKey struct{}

func WithRetentionFloorOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, retentionFloorContextKey{}, true)
}
func IsRetentionFloorOnly(ctx context.Context) bool {
	v, _ := ctx.Value(retentionFloorContextKey{}).(bool)
	return v
}
