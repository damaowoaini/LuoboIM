package messagedelivery

import (
	"context"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"math"
	"sync"
	"sync/atomic"
	"testing"
)

func receiverCutFixture(t *testing.T, monitor *event.CommandMonitor) (context.Context, *Store, *mongo.Database) {
	t.Helper()
	ctx, _, base, db := ownedReceiverMongo(t, monitor, nil)
	for _, name := range []string{Sequences, Receipts} {
		if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": MembershipSchemaID, "version": int32(1), "epoch": base.epoch, "activation_sha256": base.activationSHA, "legacy_message_writers_retired": true, "legacy_group_writers_retired": true, "owner_bounds_seeded": true, "readers_bound": true}); err != nil {
			t.Fatal(err)
		}
	}
	store, err := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	for _, cid := range []string{"sg_g", "n_sg_g"} {
		if err := store.transaction(ctx, func(tx context.Context) error {
			return store.InitializeSequence(tx, cid, Seed{MongoMax: 10, RedisMax: 10, MongoKnown: true, RedisKnown: true, EvidenceSHA256: hash([]byte("owned source seed"))})
		}); err != nil {
			t.Fatal(err)
		}
	}
	window := boundaryWindowFixture()
	window.Epoch = store.epoch
	window.ID = key(store.epoch, "membership-window", "sg_g\x00member")
	if _, err := db.Collection(Sequences).InsertOne(ctx, window); err != nil {
		t.Fatal(err)
	}
	return ctx, store, db
}
func freezeReceiverCut(t *testing.T, ctx context.Context, s *Store) deliveryprofile.VisibilityCut {
	t.Helper()
	var cut deliveryprofile.VisibilityCut
	if err := s.transaction(ctx, func(tx context.Context) error {
		var err error
		cut, err = deliveryprofile.CaptureVisibilityCut(tx, s.db, s.epoch, s.activationSHA, "sg_g", []string{"member"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return cut
}
func cutDenied(t *testing.T, ctx context.Context, s *Store, cut deliveryprofile.VisibilityCut, cid string, seq, normal int64) bool {
	t.Helper()
	var denied map[string]bool
	if err := s.transaction(ctx, func(tx context.Context) error {
		var err error
		denied, err = deliveryprofile.CompareBranchVisibilityCut(tx, s.db, s.epoch, s.activationSHA, cut, cid, []string{"member"}, seq, normal, true)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return denied["member"]
}
func changeCut(t *testing.T, ctx context.Context, s *Store, cid string, below int64, floorOnly bool) {
	t.Helper()
	if err := s.transaction(ctx, func(tx context.Context) error {
		return deliveryprofile.BumpVisibilityCut(tx, s.db, s.epoch, s.activationSHA, cid, nil, below, floorOnly)
	}); err != nil {
		t.Fatal(err)
	}
}
func TestOwnedCutRetentionUsesOriginalHeadOrAcceptedSequenceAndClearRevokes(t *testing.T) {
	ctx, s, _ := receiverCutFixture(t, nil)
	cut := freezeReceiverCut(t, ctx, s)
	if cut.SourceHead != 10 || cut.Notification.SourceHead != 10 {
		t.Fatal("cut did not freeze both actual shared counter heads")
	}
	changeCut(t, ctx, s, "sg_g", 11, true)
	if cutDenied(t, ctx, s, cut, "sg_g", 0, 0) || cutDenied(t, ctx, s, cut, "n_sg_g", 0, 0) {
		t.Fatal("retention removed a new pending notification at head+1")
	}
	changeCut(t, ctx, s, "sg_g", 12, true)
	if !cutDenied(t, ctx, s, cut, "sg_g", 0, 0) || cutDenied(t, ctx, s, cut, "sg_g", 12, 12) || cutDenied(t, ctx, s, cut, "n_sg_g", 0, 12) {
		t.Fatal("retention confused pending source with an accepted normal sequence")
	}
	changeCut(t, ctx, s, "n_sg_g", 13, true)
	if !cutDenied(t, ctx, s, cut, "n_sg_g", 12, 12) || cutDenied(t, ctx, s, cut, "sg_g", 12, 12) {
		t.Fatal("n_ retention crossed CID or ignored actual stored n_ sequence")
	}
	changeCut(t, ctx, s, "sg_g", 12, false)
	if !cutDenied(t, ctx, s, cut, "sg_g", 12, 12) || !cutDenied(t, ctx, s, cut, "n_sg_g", 14, 12) {
		t.Fatal("explicit Clear lost generation revocation")
	}
}
func TestOwnedCutHeadOverflowMissingSeedAndMalformedCounterRefuseWithoutCut(t *testing.T) {
	for _, test := range []struct {
		name    string
		head    int64
		missing bool
	}{{"negative", -1, false}, {"overflow", math.MaxInt64 - 1, false}, {"missing", 0, true}} {
		t.Run(test.name, func(t *testing.T) {
			ctx, s, db := receiverCutFixture(t, nil)
			id := key(s.epoch, "sequence", "sg_g")
			if test.missing {
				if _, err := db.Collection(Sequences).DeleteOne(ctx, bson.M{"_id": id}); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"maximum": test.head}}); err != nil {
					t.Fatal(err)
				}
			}
			err := s.transaction(ctx, func(tx context.Context) error {
				_, err := deliveryprofile.CaptureVisibilityCut(tx, db, s.epoch, s.activationSHA, "sg_g", []string{"member"})
				return err
			})
			if err == nil {
				t.Fatal("invalid source head acquired a cut")
			}
			count, err := db.Collection(Sequences).CountDocuments(ctx, bson.M{"kind": "owned-notification-cut-v1"})
			if err != nil || count != 0 {
				t.Fatal("failed freeze leaked cut rows", err)
			}
		})
	}
}
func TestOwnedCutSharedAllocatorCASRetriesWholeSnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var armed atomic.Bool
	type bypassKey struct{}
	monitor := &event.CommandMonitor{Started: func(current context.Context, e *event.CommandStartedEvent) {
		if current.Value(bypassKey{}) == true {
			return
		}
		if e.CommandName != "update" || !armed.Load() {
			return
		}
		collection, ok := e.Command.Lookup("update").StringValueOK()
		if !ok || collection != Sequences {
			return
		}
		once.Do(func() { close(entered); <-release })
	}}
	ctx, s, db := receiverCutFixture(t, monitor)
	armed.Store(true)
	var cut deliveryprofile.VisibilityCut
	result := make(chan error, 1)
	var calls atomic.Int64
	go func() {
		result <- s.transaction(ctx, func(tx context.Context) error {
			calls.Add(1)
			var err error
			cut, err = deliveryprofile.CaptureVisibilityCut(tx, db, s.epoch, s.activationSHA, "sg_g", []string{"member"})
			return err
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("actual counter CAS barrier not reached")
	}
	// The contender has no transaction and bypasses only this test monitor.
	// It writes the same seeded counter as a committed ordinary allocation.
	r, err := db.Collection(Sequences).UpdateOne(context.WithValue(ctx, bypassKey{}, true), bson.M{"_id": key(s.epoch, "sequence", "sg_g")}, bson.M{"$inc": bson.M{"maximum": int64(1), "revision": int64(1)}})
	close(release)
	if err != nil || r.MatchedCount != 1 {
		t.Fatal("ordinary counter contender did not commit", err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("cut retry did not finish")
	}
	if calls.Load() < 2 || cut.SourceHead != 11 {
		t.Fatal("cut accepted stale shared head without retry")
	}
}
