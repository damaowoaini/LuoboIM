package groupauthority

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

type barrierKey struct{}

var errFixtureDenied = errors.New("fixture permission denied")

// This is a real Mongo transaction barrier, not a mocked role getter. A driver
// monitor pauses only this request's first revision CAS after its snapshot read;
// another scoped transaction commits revocation before the delayed CAS reaches
// Mongo. No failCommand/global server configuration or real group is used.
func TestGroupAuthorityMongoRevocationWinsBeforeDelayedCAS(t *testing.T) {
	paused, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var attempts atomic.Int32
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, started *event.CommandStartedEvent) {
		if ctx.Value(barrierKey{}) != true || started.CommandName != "update" {
			return
		}
		collection, ok := started.Command.Lookup("update").StringValueOK()
		if !ok || collection != "group" {
			return
		}
		attempts.Add(1)
		once.Do(func() {
			close(paused)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}}
	ctx, store, db := authorityMongo(t, monitor)
	seedAuthority(t, ctx, db, "g")
	var releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(resume)
	done := make(chan error, 1)
	finished := make(chan struct{})
	t.Cleanup(func() {
		resume()
		select {
		case <-finished:
		case <-ctx.Done():
			t.Error("delayed fixture goroutine did not exit")
		}
	})
	go func() {
		defer close(finished)
		_, err := store.Mutation(context.WithValue(ctx, barrierKey{}, true), []Target{{GroupID: "g", Members: []string{"member"}}}, func(current context.Context) error {
			level, err := role(current, db, "g", "admin")
			if err != nil {
				return err
			}
			if level != 60 {
				return errFixtureDenied
			}
			return nil
		}, func(current context.Context) error {
			return updateMember(current, db, "g", "member", bson.M{"nickname": "forbidden-late-edit"})
		})
		done <- err
	}()
	select {
	case <-paused:
	case <-ctx.Done():
		t.Fatal("delayed transaction never reached CAS")
	}
	commit, err := store.Mutation(ctx, []Target{{GroupID: "g", Members: []string{"admin"}}}, func(current context.Context) error {
		level, err := role(current, db, "g", "owner")
		if err != nil {
			return err
		}
		if level != 100 {
			return errFixtureDenied
		}
		return nil
	}, func(current context.Context) error {
		return updateMember(current, db, "g", "admin", bson.M{"role_level": int32(20)})
	})
	if err != nil || commit == nil {
		resume()
		t.Fatal("revocation did not commit", err)
	}
	resume()
	select {
	case err := <-done:
		if !errors.Is(err, errFixtureDenied) {
			t.Fatal("revoked delayed writer was not denied", err)
		}
	case <-ctx.Done():
		t.Fatal("delayed transaction did not terminate")
	}
	if attempts.Load() < 2 {
		t.Fatal("Mongo conflict did not cause a fresh transaction attempt")
	}
	var member bson.M
	if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": "g", "user_id": "member"}).Decode(&member); err != nil || member["nickname"] != nil {
		t.Fatal("revoked writer changed target", err)
	}
	var group struct {
		Revision int64 `bson:"authz_revision"`
	}
	if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": "g"}).Decode(&group); err != nil || group.Revision != 2 {
		t.Fatal("aborted revision leaked", err)
	}
}

func TestGroupAuthorityMongoCrossGroupBatchRollback(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	seedAuthority(t, ctx, db, "a", "z")
	stop := errors.New("fixture second write failed")
	_, err := store.Mutation(ctx, []Target{{GroupID: "z", Members: []string{"member"}}, {GroupID: "a", Members: []string{"member"}}}, func(context.Context) error { return nil }, func(current context.Context) error {
		if err := updateMember(current, db, "a", "member", bson.M{"nickname": "must-rollback"}); err != nil {
			return err
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal("batch failure lost", err)
	}
	for _, id := range []string{"a", "z"} {
		var group struct {
			Revision int64 `bson:"authz_revision"`
		}
		if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": id}).Decode(&group); err != nil || group.Revision != 1 {
			t.Fatal("partial fence committed", err)
		}
	}
	var member bson.M
	if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": "a", "user_id": "member"}).Decode(&member); err != nil || member["nickname"] != nil {
		t.Fatal("partial member write committed", err)
	}
	if n, err := db.Collection(InvalidationsCollection).CountDocuments(ctx, bson.M{}); err != nil || n != 0 {
		t.Fatal("aborted batch left effects", err)
	}
}

func TestGroupAuthorityMongoScopeIsExactAndExpires(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	seedAuthority(t, ctx, db, "g")
	var captured context.Context
	_, err := store.Mutation(ctx, []Target{{GroupID: "g", Members: []string{"member"}}}, func(current context.Context) error { captured = current; return nil }, func(current context.Context) error {
		if Require(current, db, "g") != nil || RequireMembers(current, db, "g", []string{"member"}) != nil {
			return errors.New("current scope rejected")
		}
		if !errors.Is(RequireMembers(current, db, "g", []string{"foreign"}), ErrInvalidScope) || !errors.Is(RequireAllMembers(current, db, "g"), ErrInvalidScope) {
			return errors.New("member/whole-group scope widened")
		}
		if !errors.Is(Require(current, db, "foreign"), ErrInvalidScope) || !errors.Is(Require(current, db.Client().Database(db.Name()+"_foreign"), "g"), ErrInvalidScope) {
			return errors.New("foreign writer accepted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(Require(captured, db, "g"), ErrInvalidScope) {
		t.Fatal("retained scope authorized a post-commit write")
	}
	if !errors.Is(Require(ctx, db, "g"), ErrInvalidScope) {
		t.Fatal("ordinary context authorized a writer")
	}
}

func TestGroupAuthorityMongoExternalSessionsNeverStartIndependentTransaction(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	seedAuthority(t, ctx, db, "g")
	for _, strong := range []bool{false, true} {
		name := "weak"
		if strong {
			name = "strong-without-private-scope"
		}
		t.Run(name, func(t *testing.T) {
			session, err := db.Client().StartSession()
			if err != nil {
				t.Fatal(err)
			}
			defer session.EndSession(ctx)
			options := options.Transaction()
			if strong {
				options.SetReadConcern(readconcern.Snapshot()).SetReadPreference(readpref.Primary()).SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true)))
			}
			if err := session.StartTransaction(options); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := session.AbortTransaction(ctx); err != nil {
					t.Error("fixture transaction abort unconfirmed", err)
				}
			}()
			called := false
			current := mongo.NewSessionContext(ctx, session)
			_, err = store.Mutation(current, []Target{{GroupID: "g"}}, func(context.Context) error { called = true; return nil }, func(context.Context) error { called = true; return nil })
			if !errors.Is(err, ErrUnavailable) || called {
				t.Fatal("external transaction replaced/committed independently", err)
			}
		})
	}
	session, err := db.Client().StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.EndSession(ctx)
	if _, err := store.Mutation(mongo.NewSessionContext(ctx, session), []Target{{GroupID: "g"}}, func(context.Context) error { return nil }, func(context.Context) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatal("bare session accepted", err)
	}
}

func TestGroupAuthorityMongoRetryEffectsAreDurableAndPostCommit(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	seedAuthority(t, ctx, db, "g")
	var attempts int
	commit, err := store.Mutation(ctx, []Target{{GroupID: "g", Members: []string{"member"}}}, func(context.Context) error { return nil }, func(current context.Context) error {
		attempts++
		if attempts == 1 {
			current.Value(scopeKey{}).(*mutationScope).cacheKeys["GROUP_MEMBER_INFO:g-aborted"] = struct{}{}
		}
		if attempts == 1 {
			return mongo.CommandError{Code: 112, Message: "synthetic transaction retry", Labels: []string{"TransientTransactionError"}}
		}
		return updateMember(current, db, "g", "member", bson.M{"nickname": "committed"})
	})
	if err != nil || commit == nil || attempts != 2 {
		t.Fatal("retry did not commit", err)
	}
	var job invalidation
	if err := db.Collection(InvalidationsCollection).FindOne(ctx, bson.M{}).Decode(&job); err != nil || len(job.Keys) != 9 {
		t.Fatal("wrong durable target key set", err)
	}
	for _, key := range job.Keys {
		if key == "GROUP_MEMBER_INFO:g-aborted" {
			t.Fatal("aborted attempt leaked cache obligation")
		}
	}
	calls := 0
	err = commit.Flush(ctx, func(current context.Context, key string) error {
		calls++
		if mongo.SessionFromContext(current) != nil || !cacheKey(key) {
			t.Fatal("side effect ran inside retry session or wrong namespace")
		}
		return errors.New("synthetic cache unavailable")
	})
	if !errors.Is(err, ErrCacheSyncPending) || calls != 1 {
		t.Fatal("committed cache failure hidden", err)
	}
	if n, err := db.Collection(InvalidationsCollection).CountDocuments(ctx, bson.M{}); err != nil || n != 1 {
		t.Fatal("cache retry obligation lost", err)
	}
	restarted, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.FlushPending(ctx, 64, func(current context.Context, key string) error {
		if mongo.SessionFromContext(current) != nil || !cacheKey(key) {
			return ErrInvalidScope
		}
		return nil
	}); err != nil {
		t.Fatal("durable restart recovery failed", err)
	}
	if n, err := db.Collection(InvalidationsCollection).CountDocuments(ctx, bson.M{}); err != nil || n != 0 {
		t.Fatal("acknowledged cache job retained", err)
	}
}

func TestGroupAuthorityMongoCreationAndLegacyRevision(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	for _, id := range []string{"legacy", "new"} {
		if id == "legacy" {
			if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": id}); err != nil {
				t.Fatal(err)
			}
		}
		commit, err := store.Mutation(ctx, []Target{{GroupID: id, Create: id == "new"}}, func(context.Context) error { return nil }, func(current context.Context) error {
			if id == "legacy" {
				return nil
			}
			doc, err := NewGroupDocument(current, db, bson.M{"group_id": id, RevisionField: int64(999)})
			if err != nil {
				return err
			}
			_, err = db.Collection("group").InsertOne(current, doc)
			return err
		})
		if err != nil || commit == nil {
			t.Fatal("valid creation/legacy fence rejected", err)
		}
		var group struct {
			Revision int64 `bson:"authz_revision"`
		}
		if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": id}).Decode(&group); err != nil || group.Revision != 1 {
			t.Fatal("server revision not initialized", err)
		}
	}
	if _, err := store.Mutation(ctx, []Target{{GroupID: "new", Create: true}}, func(context.Context) error { return nil }, func(context.Context) error { return nil }); !errors.Is(err, ErrConflict) {
		t.Fatal("create mode reused existing group", err)
	}
	if _, err := store.Mutation(ctx, []Target{{GroupID: "unknown"}}, func(context.Context) error { return nil }, func(context.Context) error { return nil }); !errors.Is(err, ErrMissing) {
		t.Fatal("missing old group silently recreated", err)
	}
}

func TestGroupAuthorityMongoReadinessNeverPreparesMissingOutbox(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	seedAuthority(t, ctx, db, "g")
	if err := db.Collection(InvalidationsCollection).Drop(ctx); err != nil {
		t.Fatal(err)
	}
	called := false
	if _, err := store.Mutation(ctx, []Target{{GroupID: "g"}}, func(context.Context) error { called = true; return nil }, func(context.Context) error { called = true; return nil }); !errors.Is(err, ErrUnavailable) || called {
		t.Fatal("missing outbox did not fail closed", err)
	}
	names, err := db.ListCollectionNames(ctx, bson.M{"name": InvalidationsCollection})
	if err != nil || len(names) != 0 {
		t.Fatal("runtime silently prepared outbox", err)
	}
}

func TestGroupAuthorityMongoOutboxRejectsAutoDiscardingCollections(t *testing.T) {
	for _, kind := range []string{"capped", "validator", "view"} {
		t.Run(kind, func(t *testing.T) {
			ctx, store, db := authorityMongo(t, nil)
			seedAuthority(t, ctx, db, "g")
			if err := db.Collection(InvalidationsCollection).Drop(ctx); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "capped":
				err = db.CreateCollection(ctx, InvalidationsCollection, options.CreateCollection().SetCapped(true).SetSizeInBytes(4096))
			case "validator":
				err = db.CreateCollection(ctx, InvalidationsCollection, options.CreateCollection().SetValidator(bson.M{"kind": bson.M{"$type": "string"}}))
			case "view":
				err = db.CreateView(ctx, InvalidationsCollection, "group", mongo.Pipeline{})
			}
			if err != nil {
				t.Fatal("explicit isolated fixture setup failed", err)
			}
			called := false
			_, err = store.Mutation(ctx, []Target{{GroupID: "g"}}, func(context.Context) error { called = true; return nil }, func(context.Context) error { called = true; return nil })
			if !errors.Is(err, ErrUnavailable) || called {
				t.Fatal("unsafe outbox policy admitted a mutation", err)
			}
			var group struct {
				Revision int64 `bson:"authz_revision"`
			}
			if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": "g"}).Decode(&group); err != nil || group.Revision != 1 {
				t.Fatal("readiness failure changed group revision", err)
			}
		})
	}
}

func TestGroupAuthorityMongoWholeGroupScopeUsesOnlyActualMembers(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	seedAuthority(t, ctx, db, "g", "other")
	if _, err := db.Collection("group_member").InsertOne(ctx, bson.M{"group_id": "other", "user_id": "foreign", "role_level": int32(20)}); err != nil {
		t.Fatal(err)
	}
	commit, err := store.Mutation(ctx, []Target{{GroupID: "g", AllMembers: true}}, func(context.Context) error { return nil }, func(current context.Context) error {
		if err := RequireAllMembers(current, db, "g"); err != nil {
			return err
		}
		if err := RequireMembers(current, db, "g", []string{"owner", "admin", "member"}); err != nil {
			return err
		}
		if !errors.Is(RequireMembers(current, db, "g", []string{"foreign"}), ErrInvalidScope) {
			return errors.New("foreign group member entered whole-group scope")
		}
		return nil
	})
	if err != nil || commit == nil {
		t.Fatal("valid whole-group scope failed", err)
	}
	var job invalidation
	if err := db.Collection(InvalidationsCollection).FindOne(ctx, bson.M{}).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if len(job.Keys) != 13 {
		t.Fatal("whole-group invalidation omitted actual members")
	}
	for _, key := range job.Keys {
		if key == "JOIN_GROUPS_KEY:foreign" || key == "GROUP_INFO:other" || key == "GROUP_MEMBER_INFO:g-foreign" {
			t.Fatal("whole-group scope invalidated another group's keys")
		}
	}
}

// A real Mongo commit is acknowledged underneath, then its result is replaced
// once at this private driver-session boundary. This is controlled lost-ACK
// recovery testing, not a claim to have dropped actual network packets.
type lostAuthorityCommitACK struct {
	mongo.Session
	mongo.XSession
	commits int
	aborts  int
	cancel  context.CancelFunc
}

func (s *lostAuthorityCommitACK) CommitTransaction(ctx context.Context) error {
	s.commits++
	err := s.Session.CommitTransaction(ctx)
	if err == nil && s.commits == 1 {
		if s.cancel != nil {
			s.cancel()
		}
		return mongo.CommandError{Code: 251, Message: "synthetic lost commit ACK", Labels: []string{"UnknownTransactionCommitResult", "TransientTransactionError"}}
	}
	return err
}
func (s *lostAuthorityCommitACK) AbortTransaction(ctx context.Context) error {
	s.aborts++
	return s.Session.AbortTransaction(ctx)
}

func TestGroupAuthorityMongoAcknowledgedCommitLostAtBoundaryNeverReplaysWrite(t *testing.T) {
	for _, cancelAfterCommit := range []bool{false, true} {
		name := "recover-same-commit"
		if cancelAfterCommit {
			name = "cancel-remains-unconfirmed"
		}
		t.Run(name, func(t *testing.T) {
			var commandMu sync.Mutex
			var workCommands, commitCommands []bson.Raw
			monitor := &event.CommandMonitor{Started: func(_ context.Context, started *event.CommandStartedEvent) {
				isWork := started.CommandName == "update" && started.Command.Lookup("update").StringValue() == "group_member"
				if !isWork && started.CommandName != "commitTransaction" {
					return
				}
				commandMu.Lock()
				defer commandMu.Unlock()
				raw := append(bson.Raw(nil), started.Command...)
				if isWork {
					workCommands = append(workCommands, raw)
				} else {
					commitCommands = append(commitCommands, raw)
				}
			}}
			ctx, store, db := authorityMongo(t, monitor)
			seedAuthority(t, ctx, db, "g")
			current, cancel := context.WithCancel(ctx)
			defer cancel()
			raw, err := db.Client().StartSession()
			if err != nil {
				t.Fatal(err)
			}
			session := &lostAuthorityCommitACK{Session: raw, XSession: raw.(mongo.XSession)}
			if cancelAfterCommit {
				session.cancel = cancel
			}
			writes := 0
			err = store.transactionSession(current, session, func(tx context.Context) error {
				// Keep commit interception outside the driver's context slot;
				// its concrete-session extractor must receive the same raw.
				tx = mongo.NewSessionContext(tx, raw)
				if err := strongSession(tx, db.Client()); err != nil {
					return err
				}
				writes++
				_, err := db.Collection("group_member").UpdateOne(tx, bson.M{"group_id": "g", "user_id": "member"}, bson.M{"$inc": bson.M{"probe_writes": int64(1)}})
				return err
			})
			if cancelAfterCommit {
				if !errors.Is(err, ErrCommitUnconfirmed) || !errors.Is(err, context.Canceled) || session.commits != 1 {
					t.Fatal("unknown cancelled commit classified as rollback/success", err)
				}
			} else if err != nil || session.commits != 2 {
				t.Fatal("same commit recovery failed", err)
			}
			commandMu.Lock()
			observedWorks := append([]bson.Raw(nil), workCommands...)
			observedCommits := append([]bson.Raw(nil), commitCommands...)
			commandMu.Unlock()
			if len(observedWorks) != 1 || len(observedCommits) != session.commits {
				t.Fatal("wire command count differs from one work/same commit retries")
			}
			automatic, hasAutomatic := observedWorks[0].Lookup("autocommit").BooleanOK()
			number, hasNumber := observedWorks[0].Lookup("txnNumber").Int64OK()
			if !hasAutomatic || automatic || !hasNumber || observedWorks[0].Lookup("lsid").Type == 0 {
				t.Fatal("actual Mongo work is not in transaction")
			}
			for _, command := range observedCommits {
				commitNumber, ok := command.Lookup("txnNumber").Int64OK()
				if !ok || commitNumber != number || !command.Lookup("lsid").Equal(observedWorks[0].Lookup("lsid")) {
					t.Fatal("actual commit does not bind work transaction")
				}
			}
			var row struct {
				Writes int64 `bson:"probe_writes"`
			}
			if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": "g", "user_id": "member"}).Decode(&row); err != nil || row.Writes != 1 || writes != 1 || session.aborts != 0 || db.Client().NumberSessionsInProgress() != 0 {
				t.Fatal("committed business write replayed/aborted or session leaked", err)
			}
		})
	}
}

func TestGroupAuthorityMongoCacheRecoveryCancellationRetainsDurableJob(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	seedAuthority(t, ctx, db, "g")
	commit, err := store.Mutation(ctx, []Target{{GroupID: "g", Members: []string{"member"}}}, func(context.Context) error { return nil }, func(tx context.Context) error {
		return updateMember(tx, db, "g", "member", bson.M{"nickname": "committed"})
	})
	if err != nil || commit == nil {
		t.Fatal("fixture commit failed", err)
	}
	lifecycle, cancel := context.WithCancel(ctx)
	defer cancel()
	calls := 0
	err = store.RunCacheRecovery(lifecycle, func(current context.Context, key string) error {
		calls++
		deadline, ok := current.Deadline()
		if !ok || time.Until(deadline) > cacheBatchTimeout || mongo.SessionFromContext(current) != nil {
			t.Fatal("cache worker lost bounded postcommit context")
		}
		cancel()
		<-current.Done()
		return current.Err()
	}, nil)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("worker failed to stop/called another key", err)
	}
	if n, err := db.Collection(InvalidationsCollection).CountDocuments(ctx, bson.M{}); err != nil || n != 1 {
		t.Fatal("cancelled worker acknowledged incomplete invalidations", err)
	}
	if err := store.FlushPending(ctx, 64, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal("subsequent owned recovery failed", err)
	}
	if n, err := db.Collection(InvalidationsCollection).CountDocuments(ctx, bson.M{}); err != nil || n != 0 {
		t.Fatal("completed cache job not acknowledged", err)
	}
}
