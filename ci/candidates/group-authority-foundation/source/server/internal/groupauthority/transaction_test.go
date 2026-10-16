package groupauthority

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

// This wrapper observes the real pinned driver session, not a replacement
// transaction implementation. The actual command Mock below owns no socket.
type observedAuthoritySession struct {
	mongo.Session
	mongo.XSession
	aborts        int
	ends          int
	abortDeadline time.Duration
	endDeadline   time.Duration
}

func (s *observedAuthoritySession) AbortTransaction(ctx context.Context) error {
	s.aborts++
	if deadline, ok := ctx.Deadline(); ok {
		s.abortDeadline = time.Until(deadline)
	}
	return s.Session.AbortTransaction(ctx)
}
func (s *observedAuthoritySession) EndSession(ctx context.Context) {
	s.ends++
	if deadline, ok := ctx.Deadline(); ok {
		s.endDeadline = time.Until(deadline)
	}
	s.Session.EndSession(ctx)
}

func TestGroupAuthorityDriverTransactionRetryAndUnknownCommitBudget(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock).CreateCollection(false))
	success := func() bson.D {
		return mtest.CreateSuccessResponse(bson.E{Key: "n", Value: int32(1)}, bson.E{Key: "nModified", Value: int32(1)})
	}
	unknown := func(labels ...string) bson.D {
		return mtest.CreateCommandErrorResponse(mtest.CommandError{Code: 251, Name: "NoSuchTransaction", Message: "synthetic commit transport boundary", Labels: labels})
	}
	setup := func(mt *mtest.T) (*Store, *observedAuthoritySession) {
		store, err := NewStore(mt.DB)
		if err != nil {
			mt.Fatal(err)
		}
		raw, err := mt.Client.StartSession()
		if err != nil {
			mt.Fatal(err)
		}
		session := &observedAuthoritySession{Session: raw, XSession: raw.(mongo.XSession)}
		mt.Cleanup(func() {
			if mt.Client.NumberSessionsInProgress() != 0 {
				mt.Error("driver session leaked")
			}
			mt.AddMockResponses(mtest.CreateSuccessResponse())
			end, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()
			if err := mt.Client.Disconnect(end); err != nil {
				mt.Error("Mock client disconnect unconfirmed", err)
			}
		})
		return store, session
	}
	work := func(mt *mtest.T, current context.Context) error {
		deadline, ok := current.Deadline()
		if !ok || time.Until(deadline) > mutationTimeout || time.Until(deadline) <= 0 {
			mt.Fatal("transaction lost its internal deadline")
		}
		_, err := mt.DB.Collection("synthetic").UpdateOne(current, bson.M{"_id": "fixture"}, bson.M{"$inc": bson.M{"n": 1}})
		return err
	}
	commands := func(mt *mtest.T, name string) []bson.Raw {
		var rows []bson.Raw
		for _, e := range mt.GetAllStartedEvents() {
			if e.CommandName == name {
				rows = append(rows, e.Command)
			}
		}
		return rows
	}
	sameCommit := func(mt *mtest.T, want int) {
		rows := commands(mt, "commitTransaction")
		if len(rows) != want {
			mt.Fatalf("commit commands=%d want=%d", len(rows), want)
		}
		for _, row := range rows[1:] {
			if row.Lookup("txnNumber").Int64() != rows[0].Lookup("txnNumber").Int64() || !row.Lookup("lsid").Equal(rows[0].Lookup("lsid")) {
				mt.Fatal("unknown result started another transaction/session")
			}
		}
	}
	mt.Run("transient_work_retries_fresh_transaction", func(mt *mtest.T) {
		store, session := setup(mt)
		mt.AddMockResponses(success(), success(), success(), success()) // write,abort,write,commit
		calls := 0
		err := store.transactionSession(context.Background(), session, func(current context.Context) error {
			calls++
			if err := work(mt, current); err != nil {
				return err
			}
			if calls == 1 {
				return mongo.CommandError{Code: 112, Message: "synthetic conflict", Labels: []string{"TransientTransactionError"}}
			}
			return nil
		})
		if err != nil || calls != 2 || session.aborts != 1 || len(commands(mt, "abortTransaction")) != 1 {
			mt.Fatal("transient work retry mismatch", err)
		}
		writes := commands(mt, "update")
		if len(writes) != 2 || writes[0].Lookup("txnNumber").Int64() >= writes[1].Lookup("txnNumber").Int64() {
			mt.Fatal("retry did not start fresh transaction")
		}
	})
	mt.Run("unknown_plus_transient_retries_only_same_commit", func(mt *mtest.T) {
		store, session := setup(mt)
		mt.AddMockResponses(success(), unknown("UnknownTransactionCommitResult", "TransientTransactionError"), success())
		calls := 0
		err := store.transactionSession(context.Background(), session, func(current context.Context) error { calls++; return work(mt, current) })
		if err != nil || calls != 1 || session.aborts != 0 || len(commands(mt, "abortTransaction")) != 0 {
			mt.Fatal("unknown replayed work/aborted", err)
		}
		sameCommit(mt, 2)
	})
	mt.Run("unknown_attempt_limit_stays_unconfirmed", func(mt *mtest.T) {
		store, session := setup(mt)
		mt.AddMockResponses(success())
		for n := 0; n < commitAttempts; n++ {
			mt.AddMockResponses(unknown("UnknownTransactionCommitResult"))
		}
		calls := 0
		err := store.transactionSession(context.Background(), session, func(current context.Context) error { calls++; return work(mt, current) })
		if !errors.Is(err, ErrCommitUnconfirmed) || !errors.Is(err, ErrRetryLimit) || calls != 1 || session.aborts != 0 || session.ends != 0 || len(commands(mt, "abortTransaction")) != 0 {
			mt.Fatal("unknown limit misclassified/replayed", err)
		}
		sameCommit(mt, commitAttempts)
	})
	mt.Run("definitive_transient_commit_restarts_transaction", func(mt *mtest.T) {
		store, session := setup(mt)
		mt.AddMockResponses(success(), unknown("TransientTransactionError"), success(), success())
		calls := 0
		err := store.transactionSession(context.Background(), session, func(current context.Context) error { calls++; return work(mt, current) })
		writes := commands(mt, "update")
		if err != nil || calls != 2 || len(writes) != 2 || writes[0].Lookup("txnNumber").Int64() >= writes[1].Lookup("txnNumber").Int64() {
			mt.Fatal("definitive transient commit did not refresh auth/write", err)
		}
	})
	mt.Run("transient_attempt_limit_is_finite", func(mt *mtest.T) {
		store, session := setup(mt)
		calls := 0
		err := store.transactionSession(context.Background(), session, func(current context.Context) error {
			calls++
			return mongo.CommandError{Code: 112, Message: "synthetic conflict", Labels: []string{"TransientTransactionError"}}
		})
		if !errors.Is(err, ErrRetryLimit) || errors.Is(err, ErrCommitUnconfirmed) || calls != transactionAttempts || len(commands(mt, "commitTransaction")) != 0 {
			mt.Fatal("transaction retry limit violated", err)
		}
	})
	mt.Run("cancelled_work_has_bounded_independent_cleanup", func(mt *mtest.T) {
		store, session := setup(mt)
		mt.AddMockResponses(success(), success()) // actual write then bounded abort
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := store.transactionSession(ctx, session, func(current context.Context) error {
			deadline, ok := current.Deadline()
			if !ok || time.Until(deadline) > mutationTimeout {
				mt.Fatal("operation deadline missing")
			}
			if err := work(mt, current); err != nil {
				return err
			}
			<-current.Done()
			return current.Err()
		})
		if !errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCommitUnconfirmed) || session.aborts != 1 || session.ends != 1 || session.abortDeadline <= 0 || session.abortDeadline > cleanupTimeout || session.endDeadline <= 0 || session.endDeadline > cleanupTimeout || len(commands(mt, "commitTransaction")) != 0 {
			mt.Fatal("cancel/cleanup contract violated", err)
		}
	})
}
