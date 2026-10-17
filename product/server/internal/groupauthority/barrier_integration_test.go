package groupauthority

import (
	"context"
	"errors"
	"sync"
	"testing"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
)

type referenceBarrierKey struct{}

// Shared before/after schedule: the delayed request's first actual write is
// paused by its client-side command monitor. In before this is the disjoint
// target member write after the RPC-style permission read; in after it is the
// common group revision CAS before final authorization. The owner commits and
// ACKs revocation before this paused command is released in both phases.
func TestGroupAuthorityBarrierRevocationACKPreventsDelayedWrite(t *testing.T) {
	paused, release := make(chan struct{}), make(chan struct{})
	var pauseOnce, releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, started *event.CommandStartedEvent) {
		if ctx.Value(referenceBarrierKey{}) != true || started.CommandName != "update" {
			return
		}
		collection, ok := started.Command.Lookup("update").StringValueOK()
		if !ok || collection != "group" && collection != "group_member" {
			return
		}
		pauseOnce.Do(func() {
			close(paused)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}}
	ctx, store, db := authorityMongo(t, monitor)
	seedAuthority(t, ctx, db, "g")
	done := make(chan error, 1)
	exited := make(chan struct{})
	t.Cleanup(func() {
		resume()
		select {
		case <-exited:
		case <-ctx.Done():
			t.Error("delayed fixture did not terminate")
		}
	})
	go func() {
		defer close(exited)
		_, err := barrierMutation(store, context.WithValue(ctx, referenceBarrierKey{}, true), []Target{{GroupID: "g", Members: []string{"member"}}}, func(current context.Context) error {
			level, err := role(current, db, "g", "admin")
			if err != nil {
				return err
			}
			if level != 60 {
				return errFixtureDenied
			}
			return nil
		}, func(current context.Context) error {
			// Exact same raw fixture write in both variants. Low-level production
			// model scope guards are a later integration gate, not bypassed here.
			_, err := db.Collection("group_member").UpdateOne(current, bson.M{"group_id": "g", "user_id": "member"}, bson.M{"$set": bson.M{"nickname": "stale-intent"}})
			return err
		})
		done <- err
	}()
	select {
	case <-paused:
	case <-ctx.Done():
		t.Fatal("fixture setup: delayed first write not observed")
	}
	revocation, err := store.Mutation(ctx, []Target{{GroupID: "g", Members: []string{"admin"}}}, func(current context.Context) error {
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
	if err != nil || revocation == nil {
		resume()
		t.Fatal("fixture setup: owner revocation did not commit", err)
	}
	resume()
	var delayed error
	select {
	case delayed = <-done:
	case <-ctx.Done():
		t.Fatal("fixture setup: delayed request incomplete")
	}
	level, err := role(ctx, db, "g", "admin")
	if err != nil {
		t.Fatal("fixture setup: revoked role read failed", err)
	}
	var target bson.M
	if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": "g", "user_id": "member"}).Decode(&target); err != nil {
		t.Fatal("fixture setup: target read failed", err)
	}
	staleWrite := target["nickname"] == "stale-intent"
	denied := errors.Is(delayed, errFixtureDenied)
	if level != 20 || staleWrite || !denied {
		// Cloud before requires precisely this assertion and these state flags,
		// never a panic, compile failure, timeout or fixture setup rejection.
		t.Fatalf("revocation ACK did not fence delayed helper: revokedRole=%d staleWrite=%t permissionDenied=%t", level, staleWrite, denied)
	}
}

func TestGroupAuthorityBarrierPreservesAuthorizedWrite(t *testing.T) {
	ctx, store, db := authorityMongo(t, nil)
	seedAuthority(t, ctx, db, "g")
	for _, actor := range []string{"admin", "owner"} {
		t.Run(actor, func(t *testing.T) {
			commit, err := barrierMutation(store, ctx, []Target{{GroupID: "g", Members: []string{"member"}}}, func(current context.Context) error {
				level, err := role(current, db, "g", actor)
				if err != nil {
					return err
				}
				if level != 60 && level != 100 {
					return errFixtureDenied
				}
				return nil
			}, func(current context.Context) error {
				_, err := db.Collection("group_member").UpdateOne(current, bson.M{"group_id": "g", "user_id": "member"}, bson.M{"$set": bson.M{"nickname": "authorized-" + actor}})
				return err
			})
			if err != nil || commit == nil {
				t.Fatal("legitimate helper mutation failed", err)
			}
			var target bson.M
			if err := db.Collection("group_member").FindOne(ctx, bson.M{"group_id": "g", "user_id": "member"}).Decode(&target); err != nil || target["nickname"] != "authorized-"+actor {
				t.Fatal("legitimate helper mutation did not commit", err)
			}
		})
	}
}
