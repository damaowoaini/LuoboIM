package messagedelivery

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/table/relation"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
)

func TestMemberAdmissionJointUsesIndependentHeadsAndPreservesActualFloors(t *testing.T) {
	for _, forbid := range []bool{false, true} {
		name := "history-allowed"
		if forbid {
			name = "history-forbidden"
		}
		t.Run(name, func(t *testing.T) {
			ctx, authority, s, db, _ := membershipFixture(t, nil)
			// Exact owned synthetic imported heads, not an inferred group max.
			for cid, maximum := range map[string]int64{"sg_g": 4, "n_g": 9} {
				if _, err := db.Collection(Sequences).UpdateOne(ctx, bson.M{"_id": key(s.epoch, "sequence", cid)}, bson.M{"$set": bson.M{"maximum": maximum, "seed_maximum": maximum}}); err != nil {
					t.Fatal(err)
				}
			}
			driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, s)
			if err != nil {
				t.Fatal(err)
			}
			if err = driver.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "sg_g", UserID: "new-owner", MinSeq: 3}, {ConversationID: "n_g", UserID: "new-owner", MinSeq: 8}}); err != nil {
				t.Fatal(err)
			}
			command := jointAddMember(t, ctx, authority, s, db, "new-owner", forbid, nil)
			for cid, expected := range map[string]int64{"sg_g": 3, "n_g": 8} {
				if forbid {
					if cid == "sg_g" {
						expected = 5
					} else {
						expected = 10
					}
				}
				w, err := s.ReadMembershipWindow(ctx, cid, "new-owner")
				if err != nil || w.Closed || w.Lower != expected || w.LastCommand != command.ID {
					t.Fatal("actual independent lower/head contract differs", cid, w, err)
				}
			}
			owner, err := s.ReadMembershipWindow(ctx, "sg_g", "owner")
			if err != nil || owner.Lower != 0 || owner.LastCommand != "" {
				t.Fatal("new admission reset an existing owner", err)
			}
		})
	}
}

func TestMemberProjectionJointCommitsOnceAndLateTicketsCannotReopenConversation(t *testing.T) {
	for _, mode := range []string{"cache-retry", "leave-rejoin", "privacy-cut"} {
		t.Run(mode, func(t *testing.T) {
			ctx, authority, s, db, _ := membershipFixture(t, nil)
			effects := jointEffects(t, ctx, db)
			jointAddMember(t, ctx, authority, s, db, "new-owner", false, func(tx context.Context, cap *groupauthority.MemberAdmissionScope) error {
				return effects.QueueMemberConversationProjections(tx, cap, s.activationSHA)
			})
			page, err := effects.ListMemberConversationProjections(ctx, "", 8)
			if err != nil || len(page) != 1 {
				t.Fatal("actual NewMembers projection missing", err)
			}
			cacheFailure := errors.New("controlled cache ACK failure")
			calls := 0
			invalidate := func(context.Context, string, string) error {
				calls++
				if calls == 1 && mode == "cache-retry" {
					return cacheFailure
				}
				return nil
			}
			if mode == "leave-rejoin" {
				if err = jointRemoveMember(ctx, authority, s, db, "new-owner"); err != nil {
					t.Fatal(err)
				}
				jointAddMember(t, ctx, authority, s, db, "new-owner", false, nil)
			}
			if mode == "privacy-cut" {
				driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, s)
				if err != nil {
					t.Fatal(err)
				}
				if err = driver.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "sg_g", UserID: "new-owner", MinSeq: 1}}); err != nil {
					t.Fatal(err)
				}
			}
			err = effects.RecoverMemberConversationProjection(ctx, page[0].ID, s, invalidate)
			if mode == "cache-retry" {
				if !errors.Is(err, cacheFailure) {
					t.Fatal("original cache failure lost", err)
				}
				var row relation.ConversationModel
				if err = db.Collection("conversation").FindOne(ctx, bson.M{"owner_user_id": "new-owner", "conversation_id": "sg_g"}).Decode(&row); err != nil || row.MaxSeq != 0 {
					t.Fatal("actual projection not committed", err)
				}
				if _, err = db.Collection("conversation").UpdateOne(ctx, bson.M{"owner_user_id": "new-owner", "conversation_id": "sg_g"}, bson.M{"$set": bson.M{"max_seq": int64(7), "min_seq": int64(3), "recv_msg_opt": int32(2)}}); err != nil {
					t.Fatal(err)
				}
				if err = effects.RecoverMemberConversationProjection(ctx, page[0].ID, s, invalidate); err != nil {
					t.Fatal(err)
				}
				if err = db.Collection("conversation").FindOne(ctx, bson.M{"owner_user_id": "new-owner", "conversation_id": "sg_g"}).Decode(&row); err != nil || row.MaxSeq != 7 || row.MinSeq != 3 || row.RecvMsgOpt != 2 || calls != 2 {
					t.Fatal("known projected retry reopened original conversation", row, err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if n, err := db.Collection("conversation").CountDocuments(ctx, bson.M{"owner_user_id": "new-owner"}); err != nil || n != 0 || calls != 0 {
					t.Fatal("stale admission/private cut recreated conversation", n, err)
				}
			}
			if pending, err := effects.ListMemberConversationProjections(ctx, "", 8); err != nil || len(pending) != 0 {
				t.Fatal("terminal source did not drain", err)
			}
		})
	}
}

type jointProjectionBarrierKey struct{}

func TestMemberProjectionJointSerializesWithConcurrentActualPrivacyCut(t *testing.T) {
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(jointProjectionBarrierKey{}) != true || e.CommandName != "update" {
			return
		}
		name, _ := e.Command.Lookup("update").StringValueOK()
		if name != Sequences {
			return
		}
		once.Do(func() {
			close(paused)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
	}}
	ctx, authority, s, db, _ := membershipFixture(t, monitor)
	effects := jointEffects(t, ctx, db)
	jointAddMember(t, ctx, authority, s, db, "new-owner", false, func(tx context.Context, cap *groupauthority.MemberAdmissionScope) error {
		return effects.QueueMemberConversationProjections(tx, cap, s.activationSHA)
	})
	page, err := effects.ListMemberConversationProjections(ctx, "", 8)
	if err != nil || len(page) != 1 {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		resume()
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		select {
		case <-done:
		case <-end.Done():
			ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false)
			t.Error("owned projection did not join; retain DB")
		}
	})
	result := make(chan error, 1)
	go func() {
		defer close(done)
		result <- effects.RecoverMemberConversationProjection(context.WithValue(ctx, jointProjectionBarrierKey{}, true), page[0].ID, s, func(context.Context, string, string) error {
			return errors.New("revoked source must not invalidate a newly projected conversation")
		})
	}()
	select {
	case <-paused:
	case <-ctx.Done():
		t.Fatal("actual source fence did not reach barrier")
	}
	driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, s)
	if err != nil {
		t.Fatal(err)
	}
	if err = driver.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "sg_g", UserID: "new-owner", MinSeq: 1}}); err != nil {
		t.Fatal(err)
	}
	resume()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual projection transaction did not settle")
	}
	if n, err := db.Collection("conversation").CountDocuments(ctx, bson.M{"owner_user_id": "new-owner"}); err != nil || n != 0 {
		t.Fatal("snapshot write skew resurrected cut conversation", n, err)
	}
}
