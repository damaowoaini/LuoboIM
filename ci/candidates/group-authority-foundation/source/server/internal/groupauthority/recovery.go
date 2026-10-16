package groupauthority

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
)

const (
	cacheBatchTimeout     = 5 * time.Second
	cacheRecoveryInterval = 2 * time.Second
)

// RecoveryStatus carries no group/user/key/error contents. It is best-effort
// observability only: dropped observations never acknowledge a durable job.
type RecoveryStatus struct{ Pending bool }

// RunCacheRecovery blocks in its caller's owned goroutine and never starts a
// detached one. The service MUST supply its cancellable lifecycle context and
// wait for this method to exit on shutdown. context.Background is rejected.
// Every batch is at most 64 jobs/5s, followed by a cancellable 2s wait. Failed or
// unknown cache/DB results remain durable; this loop replays invalidation only.
// GroupRPC/startrpc lifecycle plumbing is not yet connected in this candidate.
func (s *Store) RunCacheRecovery(ctx context.Context, apply func(context.Context, string) error, observations chan<- RecoveryStatus) error {
	if s == nil || s.db == nil || ctx == nil || ctx.Done() == nil || apply == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnavailable
	}
	return recoverCache(ctx, func(current context.Context) error { return s.FlushPending(current, 64, apply) }, observations)
}

func recoverCache(ctx context.Context, batch func(context.Context) error, observations chan<- RecoveryStatus) error {
	if ctx == nil || ctx.Done() == nil || batch == nil {
		return ErrUnavailable
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		current, cancel := context.WithTimeout(ctx, cacheBatchTimeout)
		err := batch(current)
		cancel()
		select {
		case observations <- RecoveryStatus{Pending: err != nil}:
		default:
		}
		timer := time.NewTimer(cacheRecoveryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
