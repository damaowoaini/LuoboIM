package messagedelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type notificationFactsFixture struct {
	ctx             context.Context
	store           *Store
	db              *mongo.Database
	source, command string
	normal          bool
}

func newNotificationFactsFixture(t *testing.T, normal, persist bool, drop *commitReplyDropper) notificationFactsFixture {
	t.Helper()
	var monitor *event.CommandMonitor
	var dialer options.ContextDialer
	if drop != nil {
		monitor = drop.monitor()
		dialer = drop
	}
	ctx, authority, base, db, groups, members := originMongoFixture(t, monitor, dialer)
	const gid = "facts_under"
	if _, _, err := applyNewGroupSeed(ctx, authority, base, groups, members, gid, nil); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{Sequences, Receipts} {
		if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": MembershipSchemaID, "version": int32(1), "epoch": base.epoch, "activation_sha256": base.activationSHA, "legacy_message_writers_retired": true, "legacy_group_writers_retired": true, "owner_bounds_seeded": true, "readers_bound": true}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	// Explicit owned fixture catalog preparation, matching the existing real
	// group-effects fixtures. Runtime Queue/CheckReady never creates this schema.
	if err := db.CreateCollection(ctx, groupeffects.Collection); err != nil {
		t.Fatal("facts fixture effects collection preparation failed", err)
	}
	if _, err := db.Collection(groupeffects.Collection).InsertOne(ctx, bson.M{"_id": groupeffects.SchemaID, "kind": "group-effects-schema-v1", "version": int32(1)}); err != nil {
		t.Fatal("facts fixture effects schema preparation failed", err)
	}
	effects, err := groupeffects.NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := effects.CheckReady(ctx); err != nil {
		t.Fatal("facts fixture effects readiness failed", err)
	}
	profile := deliveryprofile.Group{Version: "owned-group-effect-v1", RiskAction: "group-message", Transport: deliveryprofile.Transport{Version: "group-effect-v1", Epoch: s.epoch, ActivationSHA256: s.activationSHA, ConfigurationSHA256: hash([]byte("synthetic complete policy")), Discovery: "zookeeper", Vendor: "none"}}
	if _, err := profile.Fingerprint(); err != nil {
		t.Fatal(err)
	}
	ordinary := int32(constant.GroupOrdinaryUsers)
	commandCtx, command, err := groupauthority.NewRoleEditCommand(context.WithValue(ctx, constant.OpUserID, "owner"), s.epoch, []groupauthority.RoleEdit{{GroupID: gid, UserID: "member", Role: &ordinary}})
	if err != nil {
		t.Fatal(err)
	}
	var source string
	_, err = authority.Mutation(commandCtx, []groupauthority.Target{{GroupID: gid, Members: []string{"member"}}}, func(context.Context) error { return nil }, func(tx context.Context) error {
		snapshot, err := groupauthority.BeginRoleNotice(tx, db, gid)
		if err != nil {
			return err
		}
		// Same-value ordinary role is an existing legal command; no fake cap/row.
		if err = snapshot.Finish(tx, db); err != nil {
			return err
		}
		message := &sdkws.MsgData{SendID: "owner", RecvID: gid, GroupID: gid, SessionType: constant.SuperGroupChatType, MsgFrom: constant.SysMsgType, ContentType: constant.GroupMemberSetToOrdinaryUserNotification, Content: []byte(`{"synthetic":true}`), Options: msgprocessor.NewOptions(msgprocessor.WithNotNotification(false), msgprocessor.WithHistory(persist), msgprocessor.WithSendMsg(normal), msgprocessor.WithOfflinePush(false))}
		operation := uuid.NewSHA1(uuid.MustParse(command.ID), []byte("synthetic actual role notice")).String()
		frozen, err := groupeffects.NewNotification(operation, 0, gid, "owner", message, command.CreatedAt)
		if err != nil {
			return err
		}
		source = frozen.ID
		return effects.QueueRoleNotification(tx, snapshot, "member", frozen, groupeffects.ActionDeliveryPolicy{Owned: &profile, Epoch: s.epoch, ConfigurationSHA256: hash([]byte("synthetic frozen policy"))}, groupeffects.RoleEffectOrder{CommandID: command.ID, Epoch: s.epoch})
	})
	if err != nil {
		t.Fatal(err)
	}
	return notificationFactsFixture{ctx: ctx, store: s, db: db, source: source, command: command.ID, normal: normal}
}
func acceptNotificationFacts(f notificationFactsFixture, tail func(context.Context, Receipt) error) (Receipt, error) {
	var result Receipt
	err := f.store.transaction(f.ctx, func(tx context.Context) error {
		in, err := f.store.ownedActionInput(tx, f.source)
		if err != nil {
			return err
		}
		result, err = f.store.acceptOwnedAction(tx, in)
		if err != nil {
			return err
		}
		if tail != nil {
			return tail(tx, result)
		}
		return nil
	})
	if err != nil {
		return Receipt{}, err
	}
	return result, nil
}
func loadNotificationFacts(t *testing.T, f notificationFactsFixture, branch string) (notificationFacts, map[string][]byte) {
	t.Helper()
	var h notificationFacts
	if err := f.db.Collection(Receipts).FindOne(f.ctx, bson.M{"_id": notificationFactsID(f.store.epoch, branch)}).Decode(&h); err != nil {
		t.Fatal(err)
	}
	original := h.SHA256
	h.SHA256 = ""
	raw, err := json.Marshal(h)
	h.SHA256 = original
	if err != nil || hash(raw) != original || !h.FactsOnly {
		t.Fatal("facts header changed or was called authority")
	}
	blobs := map[string][]byte{}
	for _, b := range h.Blobs {
		var full bytes.Buffer
		for n := int64(0); n < b.Pages; n++ {
			var p notificationFactsPage
			if err := f.db.Collection(Receipts).FindOne(f.ctx, bson.M{"_id": notificationFactsPageID(f.store.epoch, branch, b.Label, n)}).Decode(&p); err != nil {
				t.Fatal(err)
			}
			if p.Kind != ownedNotificationFactsPageKind || p.HeaderID != h.ID || p.HeaderSHA != original || p.Number != n || p.Label != b.Label || len(p.Data) > ownedNotificationFactsPageBytes || hash(p.Data) != p.SHA256 {
				t.Fatal("page detached from original facts")
			}
			full.Write(p.Data)
		}
		if factBlob(b.Label, full.Bytes()) != b {
			t.Fatal("original raw blob lost across pages")
		}
		blobs[b.Label] = full.Bytes()
	}
	return h, blobs
}

type notificationFactsProjection struct{}

func (notificationFactsProjection) RaiseMaximum(context.Context, string, int64) error { return nil }
func (notificationFactsProjection) RaiseSenderRead(context.Context, string, string, int64) error {
	return nil
}

func TestOwnedNotificationFactsCommitSurviveTerminalCleanupAndOriginalReceipt(t *testing.T) {
	for _, normal := range []bool{false, true} {
		name := "frozen-head"
		if normal {
			name = "accepted-normal"
		}
		t.Run(name, func(t *testing.T) {
			f := newNotificationFactsFixture(t, normal, true, nil)
			r, err := acceptNotificationFacts(f, nil)
			if err != nil {
				t.Fatal(err)
			}
			var branch AcceptedBranch
			for _, b := range r.Branches {
				if b.ConversationID == "n_facts_under" {
					branch = b
				}
			}
			if branch.Seq != 1 || !branch.Persist {
				t.Fatal("actual positive n_ branch missing")
			}
			h, blobs := loadNotificationFacts(t, f, branch.ID)
			if h.CommandID != f.command || h.SourceID != f.source || h.SourceOperationID == h.CommandID || h.CID != "n_facts_under" || h.NormalCID != "sg_facts_under" || h.GroupID != "facts_under" || h.OriginKind != "group" || h.Database != f.db.Name() || h.Epoch != f.store.epoch || h.Activation != f.store.activationSHA || h.Actor != "owner" {
				t.Fatal("facts borrowed wrong source/namespace/command")
			}
			expectedKind, expectedPosition := "original-frozen-head", int64(0)
			if normal {
				expectedKind, expectedPosition = "accepted-normal-sequence", 1
			}
			if h.NormalPositionKind != expectedKind || h.NormalPosition != expectedPosition {
				t.Fatal("n_ seq confused with normal original position")
			}
			var recipients []string
			if json.Unmarshal(blobs["branch-recipients-json"], &recipients) != nil || len(recipients) != 2 || recipients[0] != "member" || recipients[1] != "owner" {
				t.Fatal("exact branch recipients lost")
			}
			var cut deliveryprofile.VisibilityCut
			if json.Unmarshal(blobs["original-cut-json"], &cut) != nil || !cut.Valid() || cut.SourceHead != 0 || cut.Notification.ConversationID != "n_facts_under" {
				t.Fatal("original cut refreshed or lost")
			}
			for _, label := range []string{"normal-claim-bson", "notification-claim-bson", "group-origin-claim-bson"} {
				if bson.Raw(blobs[label]).Validate() != nil {
					t.Fatal("original raw claim missing")
				}
			}
			originalHeader, _ := bson.Marshal(h)
			// Simulate only the already-tested transport ACK flags. NO gateway/vendor
			// delivery is claimed. The production Project below performs real cleanup.
			updated, err := f.db.Collection(Effects).UpdateOne(f.ctx, bson.M{"_id": key(f.store.epoch, "branch", branch.ID), "source_id": f.source}, bson.M{"$set": bson.M{"push_pending": false, "online_pending": false, "offline_pending": false, "online_cursor": h.RecipientCount}})
			if err != nil || updated.MatchedCount != 1 {
				t.Fatal("terminal fixture boundary missed", err)
			}
			err = func() error {
				current, cancel := context.WithTimeout(f.ctx, 5*time.Second)
				defer cancel()
				originalDuty, e := f.store.readDutyRecord(current, branch.ID)
				if e != nil {
					return e
				}
				scoped, _, e := f.store.actionRecovery(current, branch.ID, originalDuty.PolicySHA)
				if e != nil {
					return e
				}
				return f.store.Project(scoped, branch.ID, notificationFactsProjection{})
			}()
			if err != nil {
				t.Fatal(err)
			}
			var duty bson.Raw
			if err = f.db.Collection(Effects).FindOne(f.ctx, bson.M{"_id": key(f.store.epoch, "branch", branch.ID)}).Decode(&duty); err != nil {
				t.Fatal(err)
			}
			if _, err := duty.LookupErr("payload"); err == nil {
				t.Fatal("production terminal cleanup retained plaintext")
			}
			if _, err := duty.LookupErr("recipients"); err == nil {
				t.Fatal("production terminal cleanup retained recipients")
			}
			restarted, err := NewMembershipBoundStore(f.db, f.store.epoch, f.store.activationSHA)
			if err != nil {
				t.Fatal(err)
			}
			var again Receipt
			var found bool
			if err = restarted.transaction(f.ctx, func(tx context.Context) error {
				var e error
				again, found, e = restarted.ownedAccepted(tx, f.source)
				return e
			}); err != nil || !found || again.InputSHA256 != r.InputSHA256 {
				t.Fatal("original committed receipt not resolved", err)
			}
			after, _ := loadNotificationFacts(t, f, branch.ID)
			afterHeader, _ := bson.Marshal(after)
			if !bytes.Equal(originalHeader, afterHeader) {
				t.Fatal("receipt-first restart rewrote non-authority facts")
			}
		})
	}
}

func TestOwnedNotificationFactsFailureRollsBackSlotCounterAndSourceReceipt(t *testing.T) {
	for _, kind := range []string{"abort-after-facts", "foreign-page-collision"} {
		t.Run(kind, func(t *testing.T) {
			f := newNotificationFactsFixture(t, false, true, nil)
			var input ownedActionInput
			if err := f.store.transaction(f.ctx, func(tx context.Context) error {
				var e error
				input, e = f.store.ownedActionInput(tx, f.source)
				return e
			}); err != nil {
				t.Fatal(err)
			}
			plans, err := groupeffects.PlanNotificationBranches(input.frozen)
			if err != nil || len(plans) != 1 {
				t.Fatal("fixture branch", err)
			}
			firstPage := notificationFactsPageID(f.store.epoch, plans[0].ID, "original-cut-json", 0)
			if kind == "foreign-page-collision" {
				if _, err = f.db.Collection(Receipts).InsertOne(f.ctx, bson.M{"_id": firstPage, "foreign": true}); err != nil {
					t.Fatal(err)
				}
			}
			sentinel := errors.New("synthetic rollback after real facts")
			_, err = acceptNotificationFacts(f, func(context.Context, Receipt) error {
				if kind == "abort-after-facts" {
					return sentinel
				}
				return nil
			})
			if kind == "abort-after-facts" && !errors.Is(err, sentinel) || kind == "foreign-page-collision" && !mongo.IsDuplicateKeyError(err) {
				t.Fatal("expected actual transaction failure absent", err)
			}
			for _, filter := range []bson.M{{"_id": notificationFactsID(f.store.epoch, plans[0].ID)}, {"_id": key(f.store.epoch, "source", f.source)}, {"header_id": notificationFactsID(f.store.epoch, plans[0].ID)}} {
				if n, e := f.db.Collection(Receipts).CountDocuments(f.ctx, filter); e != nil || n != 0 {
					t.Fatal("aborted facts/source leaked", e)
				}
			}
			if n, e := f.db.Collection("msg").CountDocuments(f.ctx, bson.M{"doc_id": "n_facts_under:0"}); e != nil || n != 0 {
				t.Fatal("aborted facts left a stored message", e)
			}
			var c counter
			if e := f.db.Collection(Sequences).FindOne(f.ctx, bson.M{"_id": key(f.store.epoch, "sequence", "n_facts_under")}).Decode(&c); e != nil || c.Maximum != 0 {
				t.Fatal("aborted facts advanced counter", e)
			}
		})
	}
}
func TestOwnedNotificationFactsDoNotTurnSeq0IntoPersistedHistory(t *testing.T) {
	f := newNotificationFactsFixture(t, false, false, nil)
	r, err := acceptNotificationFacts(f, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Branches) != 1 || r.Branches[0].Seq != 0 || r.Branches[0].Persist {
		t.Fatal("silent configuration changed")
	}
	if n, e := f.db.Collection(Receipts).CountDocuments(f.ctx, bson.M{"kind": ownedNotificationFactsKind}); e != nil || n != 0 {
		t.Fatal("Seq0 falsely got a positive-history fact", e)
	}
}

func TestOwnedNotificationFactsLostCommitACKResolvesWithoutBackfillOrReexecution(t *testing.T) {
	drop := &commitReplyDropper{all: true, requests: map[int32]bool{}}
	f := newNotificationFactsFixture(t, false, true, drop)
	bounded, cancel := context.WithTimeout(context.WithValue(f.ctx, deliveryDropCommitKey{}, true), 3*time.Second)
	marked := f
	marked.ctx = bounded
	result, err := acceptNotificationFacts(marked, nil)
	cancel()
	if !errors.Is(err, ErrCommitUnconfirmed) || result.ID != "" {
		t.Fatal("unknown commit was advertised as confirmed", err)
	}
	drop.mu.Lock()
	wire := append([]deliveryWireFact(nil), drop.facts...)
	drop.mu.Unlock()
	if drop.dropped.Load() < 1 || len(wire) < 2 {
		t.Fatal("real Mongo commit ACK was not lost")
	}
	first := wire[0]
	for _, fact := range wire {
		if !fact.transaction || fact.txn != first.txn || fact.lsidSHA != first.lsidSHA {
			t.Fatal("unknown commit reexecuted business work in another transaction")
		}
	}
	restarted, err := NewMembershipBoundStore(f.db, f.store.epoch, f.store.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	var resolved Receipt
	var found bool
	if err = restarted.transaction(f.ctx, func(tx context.Context) error {
		var e error
		resolved, found, e = restarted.ownedAccepted(tx, f.source)
		return e
	}); err != nil || !found || len(resolved.Branches) != 1 || resolved.Branches[0].Seq != 1 {
		t.Fatal("original source receipt unavailable after unknown ACK", err)
	}
	h, _ := loadNotificationFacts(t, f, resolved.Branches[0].ID)
	before, _ := bson.Marshal(h)
	if n, e := f.db.Collection(Receipts).CountDocuments(f.ctx, bson.M{"kind": ownedNotificationFactsKind, "source_id": f.source}); e != nil || n != 1 {
		t.Fatal("unknown commit duplicated facts", e)
	}
	var c counter
	if e := f.db.Collection(Sequences).FindOne(f.ctx, bson.M{"_id": key(f.store.epoch, "sequence", "n_facts_under")}).Decode(&c); e != nil || c.Maximum != 1 {
		t.Fatal("unknown commit duplicated allocation", e)
	}
	// Only inspect the old receipt twice. No fresh source/cut/roster is captured.
	if err = restarted.transaction(f.ctx, func(tx context.Context) error {
		_, ok, e := restarted.ownedAccepted(tx, f.source)
		if e != nil {
			return e
		}
		if !ok {
			return ErrConflict
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := loadNotificationFacts(t, f, resolved.Branches[0].ID)
	body, _ := bson.Marshal(after)
	if !bytes.Equal(before, body) {
		t.Fatal("inspect repaired/reinterpreted old facts")
	}
}
