package groupeffects

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func effectsActor(ctx context.Context) context.Context {
	return context.WithValue(ctx, constant.OpUserID, "member")
}
func prepareQueuedCallback(t *testing.T, ctx context.Context, authority *groupauthority.Store, s *Store, db *mongo.Database, endpoint *Endpoint) Frozen {
	t.Helper()
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", groupauthority.RevisionField: int64(1), "status": int32(0)}); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(&callbackstruct.CallbackQuitGroupReq{CallbackCommand: callbackstruct.CallbackQuitGroupCommand, GroupID: "g", UserID: "member"})
	if err != nil {
		t.Fatal(err)
	}
	binding := digest([]byte("synthetic endpoint config"))
	if endpoint != nil {
		binding = endpoint.Binding()
	}
	f, err := NewCallback(uuid.NewString(), 0, "g", "member", CallbackPayload{Name: AfterQuit, EndpointBinding: binding, Body: body}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = authority.Mutation(effectsActor(ctx), []groupauthority.Target{{GroupID: "g", Members: []string{"member"}}}, func(context.Context) error { return nil }, func(tx context.Context) error { return s.Queue(tx, []Frozen{f}) })
	if err != nil {
		t.Fatal("queued effect did not commit", err)
	}
	return f
}
func TestGroupEffectsMongoBusinessAndObligationCommitOrAbortTogether(t *testing.T) {
	for _, abort := range []bool{true, false} {
		name := "commit"
		if abort {
			name = "abort"
		}
		t.Run(name, func(t *testing.T) {
			ctx, authority, s, db := effectsMongo(t, nil)
			if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", groupauthority.RevisionField: int64(1)}); err != nil {
				t.Fatal(err)
			}
			f := testCallback(t)
			stop := errors.New("synthetic after-queue abort")
			commit, err := authority.Mutation(effectsActor(ctx), []groupauthority.Target{{GroupID: "g", Members: []string{"member"}}}, func(context.Context) error { return nil }, func(tx context.Context) error {
				if _, err := db.Collection("group").UpdateOne(tx, bson.M{"group_id": "g"}, bson.M{"$set": bson.M{"group_name": "changed"}}); err != nil {
					return err
				}
				if err := s.Queue(tx, []Frozen{f}); err != nil {
					return err
				}
				if abort {
					return stop
				}
				return nil
			})
			if abort {
				if !errors.Is(err, stop) || commit != nil {
					t.Fatal("aborted queue reported commit", err)
				}
			} else if err != nil || commit == nil {
				t.Fatal("valid queue failed", err)
			}
			var group bson.M
			if err := db.Collection("group").FindOne(ctx, bson.M{"group_id": "g"}).Decode(&group); err != nil {
				t.Fatal(err)
			}
			count, err := db.Collection(Collection).CountDocuments(ctx, bson.M{"_id": f.ID})
			if err != nil {
				t.Fatal(err)
			}
			if abort && (count != 0 || group["group_name"] != nil) || !abort && (count != 1 || group["group_name"] != "changed") {
				t.Fatal("effect receipt split from business transaction")
			}
		})
	}
}
func TestGroupEffectsMongoRejectsScopeAndLostSchemaWithoutImplicitCreate(t *testing.T) {
	ctx, authority, s, db := effectsMongo(t, nil)
	f := testCallback(t)
	if err := s.Queue(effectsActor(ctx), []Frozen{f}); err == nil {
		t.Fatal("unscoped effect was accepted")
	}
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", groupauthority.RevisionField: int64(1)}); err != nil {
		t.Fatal(err)
	}
	if err := db.Collection(Collection).Drop(ctx); err != nil {
		t.Fatal("owned fixture drop failed", err)
	}
	_, err := authority.Mutation(effectsActor(ctx), []groupauthority.Target{{GroupID: "g", Members: []string{"member"}}}, func(context.Context) error { return nil }, func(tx context.Context) error { return s.Queue(tx, []Frozen{f}) })
	if err == nil {
		t.Fatal("queue recreated an unprepared namespace")
	}
	names, err := db.ListCollectionNames(ctx, bson.M{"name": Collection})
	if err != nil || len(names) != 0 {
		t.Fatal("unprepared effects collection was implicitly created", err)
	}
}
func TestGroupEffectsMongoExactlyOneConcurrentCallbackClaim(t *testing.T) {
	ctx, authority, s, db := effectsMongo(t, nil)
	f := prepareQueuedCallback(t, ctx, authority, s, db, nil)
	var wg sync.WaitGroup
	claims := make(chan *Dispatch, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, e := s.BeginCallback(ctx, f.ID, 1, f.PayloadSHA256)
			if e != nil {
				failures <- e
			} else {
				claims <- d
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(failures)
	if len(claims) != 1 || len(failures) != 7 {
		t.Fatal("callback claim was not exclusive")
	}
	for err := range failures {
		if !errors.Is(err, ErrConflict) {
			t.Fatal("unexpected claim failure", err)
		}
	}
	d := <-claims
	if err := d.Finish(ctx, Unknown, ""); err != nil {
		t.Fatal(err)
	}
	restarted, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.BeginCallback(ctx, f.ID, 3, f.PayloadSHA256); !errors.Is(err, ErrConflict) {
		t.Fatal("unknown callback auto-replayed after process restart", err)
	}
	state, err := restarted.Inspect(ctx, f.ID)
	if err != nil || state.State != Unconfirmed || state.Attempts != 1 {
		t.Fatal("unknown receipt disappeared", err)
	}
}
func TestGroupEffectsMongoExplicitConfirmationPreservesIntentAndDropsDonePayload(t *testing.T) {
	ctx, authority, s, db := effectsMongo(t, nil)
	f := prepareQueuedCallback(t, ctx, authority, s, db, nil)
	first, err := s.BeginCallback(ctx, f.ID, 1, f.PayloadSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Finish(ctx, Unknown, ""); err != nil {
		t.Fatal(err)
	}
	confirmation := Confirmation{ExpectedSenderInstanceID: s.InstanceID(), ID: f.ID, ExpectedRevision: 3, ExpectedPayloadSHA256: f.PayloadSHA256, Decision: "not-applied", EvidenceSHA256: digest([]byte("synthetic remote definitively cancelled")), DrainedEvidenceSHA256: digest([]byte("synthetic exact sender stopped"))}
	summary, err := s.Confirm(ctx, confirmation)
	if err != nil || summary.State != Pending || summary.Revision != 4 || summary.ID != f.ID {
		t.Fatal("explicit recovery changed original intent", err)
	}
	if _, err := s.Confirm(ctx, confirmation); !errors.Is(err, ErrConflict) {
		t.Fatal("confirmation was silently replayed", err)
	}
	second, err := s.BeginCallback(ctx, f.ID, 4, f.PayloadSHA256)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := second.Callback()
	if err != nil {
		t.Fatal(err)
	}
	var old CallbackPayload
	if json.Unmarshal(f.Payload, &old) != nil || string(payload.Body) != string(old.Body) {
		t.Fatal("confirmed retry changed frozen callback bytes")
	}
	if err := first.Finish(ctx, Acknowledged, digest([]byte("late old ACK"))); !errors.Is(err, ErrConflict) {
		t.Fatal("old attempt overwrote a newer confirmation", err)
	}
	if err := second.Finish(ctx, Acknowledged, digest([]byte("new observed ACK"))); err != nil {
		t.Fatal(err)
	}
	var raw bson.M
	if err := db.Collection(Collection).FindOne(ctx, bson.M{"_id": f.ID}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["payload"]; ok {
		t.Fatal("done receipt retained private payload")
	}
	if _, ok := raw["claim"]; ok {
		t.Fatal("done receipt retained mutable attempt claim")
	}
	if summary, err := s.Inspect(ctx, f.ID); err != nil || summary.State != Done || summary.Attempts != 2 || summary.ID != f.ID {
		t.Fatal("recovered callback lost minimal durable outcome", err)
	}
}
