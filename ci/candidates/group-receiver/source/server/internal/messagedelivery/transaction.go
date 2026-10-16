// Transaction state machine copied from the independently verified foundation-v2-r2.
// Kept isolated here until the receiver integration can share a reviewed common primitive.
package messagedelivery

import (
	"context"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

const (
	mutationTimeout     = 20 * time.Second
	cleanupTimeout      = 2 * time.Second
	commitTimeout       = 5 * time.Second
	transactionAttempts = 4
	commitAttempts      = 4
)

func strongSession(ctx context.Context, client *mongo.Client) error {
	session := mongo.SessionFromContext(ctx)
	active, ok := session.(mongo.XSession)
	if !ok || session.Client() != client {
		return ErrUnprepared
	}
	state := active.ClientSession()
	if state == nil || !state.TransactionRunning() || state.CurrentRc == nil || state.CurrentRc.Level != "snapshot" || state.CurrentRp == nil || state.CurrentRp.Mode() != readpref.PrimaryMode || state.CurrentWc == nil || state.CurrentWc.Journal == nil || !*state.CurrentWc.Journal {
		return ErrUnprepared
	}
	w, ok := state.CurrentWc.W.(string)
	if !ok || w != "majority" {
		return ErrUnprepared
	}
	return nil
}

func (s *Store) transaction(ctx context.Context, fn func(context.Context) error) error {
	if s == nil || s.db == nil || ctx == nil || fn == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnprepared
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := s.db.Client().StartSession()
	if err != nil {
		return err
	}
	return s.transactionSession(ctx, session, fn)
}

// transactionSession owns this session until its bounded cleanup. Keeping this
// small boundary also allows a real-Mongo test to lose one acknowledged commit
// result without server failpoints or a second business write. It is not public.
func (s *Store) transactionSession(ctx context.Context, session mongo.Session, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, mutationTimeout)
	defer cancel()
	uncertain := false
	defer func() {
		if uncertain {
			// The pinned driver's EndSession would otherwise issue abort after a
			// timed-out commit still marked TransactionInProgress. Never race an
			// unknown commit with abort. Release only local session state; Mongo
			// owns eventual cleanup of a possible unfinished transaction.
			if active, ok := session.(mongo.XSession); ok && active.ClientSession() != nil {
				active.ClientSession().EndSession()
			}
			return
		}
		end, stop := context.WithTimeout(context.Background(), cleanupTimeout)
		defer stop()
		session.EndSession(end)
	}()
	for attempt := 0; attempt < transactionAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		maxCommit := commitTimeout
		opts := options.Transaction().SetReadConcern(readconcern.Snapshot()).SetReadPreference(readpref.Primary()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).SetMaxCommitTime(&maxCommit)
		if err := session.StartTransaction(opts); err != nil {
			return err
		}
		current := mongo.NewSessionContext(ctx, session)
		err := strongSession(current, s.db.Client())
		if err == nil {
			err = fn(current)
		} // Mongo only, no callbacks/cache/notifications.
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			// The function must not perform commit itself. An unexpected unknown
			// result still fails closed and must not trigger abort or replay.
			if hasLabel(err, "UnknownTransactionCommitResult") {
				uncertain = true
				return errors.Join(ErrCommitUnconfirmed, err)
			}
			abortSession(session)
			if hasLabel(err, "TransientTransactionError") && ctx.Err() == nil {
				if attempt+1 < transactionAttempts {
					continue
				}
				return errors.Join(ErrRetryLimit, err)
			}
			return err
		}
		retry, unknown, err := commitSession(ctx, session)
		uncertain = unknown
		if !retry {
			return err
		}
		// Only an explicit transient commit result with no previous unknown
		// may restart authorization/write in a fresh transaction.
		if attempt+1 == transactionAttempts {
			return errors.Join(ErrRetryLimit, err)
		}
	}
	return ErrRetryLimit
}

func abortSession(session mongo.Session) {
	abort, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()
	// The driver suppresses server abort errors. This is bounded best effort,
	// not proof of rollback; an uncommitted transaction is never called applied.
	_ = session.AbortTransaction(abort)
}

func hasLabel(err error, label string) bool {
	var labeled interface{ HasErrorLabel(string) bool }
	return errors.As(err, &labeled) && labeled.HasErrorLabel(label)
}

// Unknown always wins over transient. Each retry keeps the same session and
// transaction number, and never calls business code again. The pinned driver's
// WithTransaction uses a background commit context and a 120s retry window;
// explicit CommitTransaction preserves our total deadline instead.
func commitSession(ctx context.Context, session mongo.Session) (retryTransaction, uncertain bool, err error) {
	unknown := false
	for attempt := 0; attempt < commitAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			if unknown {
				return false, true, errors.Join(ErrCommitUnconfirmed, err)
			}
			abortSession(session) // No commit attempt has been dispatched yet.
			return false, false, err
		}
		err = session.CommitTransaction(ctx)
		if err == nil {
			return false, false, nil
		} // Real success ACK wins near deadline.
		if hasLabel(err, "UnknownTransactionCommitResult") {
			unknown = true
			continue
		}
		if unknown {
			return false, true, errors.Join(ErrCommitUnconfirmed, err)
		}
		if hasLabel(err, "TransientTransactionError") && ctx.Err() == nil {
			return true, false, err
		}
		// A dispatched commit that failed/cancelled without a definitive transient
		// result is conservatively unknown, never "rolled back".
		return false, true, errors.Join(ErrCommitUnconfirmed, err)
	}
	return false, true, errors.Join(ErrCommitUnconfirmed, ErrRetryLimit, err)
}
