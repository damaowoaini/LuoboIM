package messagedelivery

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/event"
)

// Real scoped removal -> production ordered queue -> SAME actual receiver
// acceptance/receipt TX. Callback/network adapters are outside this storage
// test; no fabricated pending/entitlement row substitutes for those producers.
func TestMemberRemovalJointOnlyActualNoticeSequencePassesClosedHistoryWindow(t *testing.T) {
	for _, item := range []struct {
		name             string
		persist, visible bool
	}{{"silent-only", false, false}, {"persisted-tip", true, false}, {"visible-clone-and-tip", true, true}} {
		t.Run(item.name, func(t *testing.T) {
			ctx, s, db, _, frozen, _, plans := removalVisibilityFixture(t, groupauthority.NoticeKick, item.persist, item.visible)
			actor := context.WithValue(ctx, constant.OpUserID, "owner")
			for _, plan := range plans {
				// An unrelated message can land AFTER removal but BEFORE its delayed
				// notice. Its positive sequence must not be authorized by that notice.
				options := msgprocessor.NewOptions(msgprocessor.WithHistory(true), msgprocessor.WithNotNotification(plan.ConversationID == "sg_g"))
				message := &sdkws.MsgData{SendID: "owner", RecvID: "g", GroupID: "g", SessionType: constant.SuperGroupChatType, MsgFrom: constant.UserMsgType, ContentType: constant.Text, ClientMsgID: uuid.NewString(), ServerMsgID: uuid.NewString(), CreateTime: 10, SendTime: 10, Content: []byte("intervening owned gap"), Options: options}
				receipt, err := s.AcceptCommitted(actor, originEnvelope(t, message, "owner"))
				if err != nil || len(receipt.Branches) != 1 || receipt.Branches[0].Seq != 1 {
					t.Fatal("actual intervening allocator failed", err)
				}
			}
			var accepted Receipt
			if err := s.transaction(ctx, func(tx context.Context) error {
				in, err := s.ownedActionInput(tx, frozen.ID)
				if err != nil {
					return err
				}
				accepted, err = s.acceptOwnedAction(tx, in)
				return err
			}); err != nil {
				t.Fatal("actual original receiver acceptance failed", err)
			}
			reader := &HistoryWindowReader{store: s}
			for _, branch := range accepted.Branches {
				w, err := s.ReadMembershipWindow(ctx, branch.ConversationID, "member")
				if err != nil || !w.Closed || w.Upper != 0 {
					t.Fatal("async notice raised scalar upper", w, err)
				}
				allowed, err := reader.FilterNamespaceHistorySequences(ctx, branch.ConversationID, "member", []int64{1, 2, 3})
				if err != nil {
					t.Fatal(err)
				}
				if allowed[1] || allowed[3] {
					t.Fatal("single notice authorized post-removal interval", allowed)
				}
				if branch.Persist {
					if branch.Seq != 2 || !allowed[2] {
						t.Fatal("exact real notice entitlement missing", branch, allowed)
					}
				} else {
					if branch.Seq != 0 || allowed[2] {
						t.Fatal("Seq0 created positive history authority", branch, allowed)
					}
				}
			}
			// Original acceptance is idempotent; there is no next sequence or second
			// grant after its receipt has been committed.
			if err := s.transaction(ctx, func(tx context.Context) error {
				in, err := s.ownedActionInput(tx, frozen.ID)
				if err != nil {
					return err
				}
				again, ok, err := s.ownedAccepted(tx, in.frozen.ID)
				if err != nil || !ok || again.SourceID != accepted.SourceID {
					return ErrConflict
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			_ = db // DB ownership and cleanup stay with the shared original fixture.
		})
	}
}

type jointNoticeBarrierKey struct{}

func TestMemberRemovalJointConcurrentClearCannotMintLateNoticeEntitlement(t *testing.T) {
	paused, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	resume := func() { releaseOnce.Do(func() { close(release) }) }
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(jointNoticeBarrierKey{}) != true || e.CommandName != "update" {
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
	ctx, s, db, _, frozen, _, _ := removalVisibilityFixtureMonitored(t, groupauthority.NoticeKick, true, monitor)
	t.Cleanup(func() {
		resume()
		end, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		select {
		case <-done:
		case <-end.Done():
			ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false)
			t.Error("owned notice transaction did not join; retain DB")
		}
	})
	result := make(chan error, 1)
	go func() {
		defer close(done)
		result <- s.transaction(context.WithValue(ctx, jointNoticeBarrierKey{}, true), func(tx context.Context) error {
			in, err := s.ownedActionInput(tx, frozen.ID)
			if err != nil {
				return err
			}
			_, err = s.acceptOwnedAction(tx, in)
			return err
		})
	}()
	select {
	case <-paused:
	case <-ctx.Done():
		t.Fatal("actual notice acceptance did not reach shared source fence")
	}
	driver, err := unrelation.NewMsgMongoDriverWithDeliveryVisibility(db, s)
	if err != nil {
		t.Fatal(err)
	}
	if err = driver.AdvanceMessageVisibility(ctx, []table.MessageVisibilityFloor{{ConversationID: "n_g", UserID: "member", MinSeq: 1}}); err != nil {
		t.Fatal(err)
	}
	resume()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("actual notice transaction did not settle")
	}
	reader := &HistoryWindowReader{store: s}
	allowed, err := reader.FilterNamespaceHistorySequences(ctx, "n_g", "member", []int64{1, 2})
	if err != nil || allowed[1] || allowed[2] {
		t.Fatal("concurrent privacy cut minted removed-owner history", allowed, err)
	}
	w, err := s.ReadMembershipWindow(ctx, "n_g", "member")
	if err != nil || !w.Closed || w.Upper != 0 {
		t.Fatal("privacy race widened removed owner window", err)
	}
}
