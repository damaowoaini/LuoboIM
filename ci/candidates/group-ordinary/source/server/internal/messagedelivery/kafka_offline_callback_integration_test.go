package messagedelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/event"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"google.golang.org/protobuf/proto"
)

type offlineCallbackFixtureAdapter struct {
	store                *Store
	fingerprint, binding string
	prepare              func(context.Context, KafkaOfflineCall) error
	provider             func(context.Context, KafkaOfflineCall, string, *KafkaAliasDispatch) error
}

func (a *offlineCallbackFixtureAdapter) PolicyFingerprint() string { return a.fingerprint }
func (a *offlineCallbackFixtureAdapter) CheckReady(ctx context.Context) error {
	return a.store.CheckKafkaReady(ctx)
}
func (a *offlineCallbackFixtureAdapter) PrepareOffline(ctx context.Context, call KafkaOfflineCall) error {
	return a.prepare(ctx, call)
}
func (a *offlineCallbackFixtureAdapter) DeliverOffline(ctx context.Context, call KafkaOfflineCall) error {
	for _, user := range call.DispatchRecipients {
		request := KafkaAliasRequest{Vendor: "getui", Binding: a.binding, Protocol: "getui-alias-v2", RequestID: strings.ReplaceAll(uuid.NewString(), "-", ""), BodySHA256: hash([]byte("synthetic provider request"))}
		d, err := a.store.BeginKafkaAliasVendor(ctx, call, user, request)
		if err != nil {
			return err
		}
		if d != nil {
			if err := a.provider(ctx, call, user, d); err != nil {
				return err
			}
		}
	}
	return nil
}

// Reuses the existing fixed loopback/nonce DB claim+checked cleanup. No
// callback/vendor network is opened by these real Mongo boundary tests.
func offlineCallbackFixture(t *testing.T, mode string, monitor *event.CommandMonitor, dialer options.ContextDialer, extras ...int) (context.Context, *Store, *mongo.Database, string, *sdkws.MsgData, *offlineCallbackFixtureAdapter) {
	t.Helper()
	ctx, authority, base, db := offlineCallbackR1MongoTransport(t, monitor, dialer)
	if _, err := db.Collection("group").InsertOne(ctx, bson.M{"group_id": "g", "status": int32(constant.GroupOk), "group_type": int32(constant.WorkingGroup), groupauthority.RevisionField: int64(1)}); err != nil {
		t.Fatal(err)
	}
	roster := []string{"member", "owner"}
	roles := map[string]int32{"owner": constant.GroupOwner, "member": constant.GroupOrdinaryUsers}
	if len(extras) > 0 {
		for i := 0; i < extras[0]; i++ {
			u := fmt.Sprintf("extra-%03d", i)
			roster = append(roster, u)
			roles[u] = constant.GroupOrdinaryUsers
		}
	}
	for user, role := range roles {
		if _, err := db.Collection("group_member").InsertOne(ctx, bson.M{"group_id": "g", "user_id": user, "role_level": role, "join_time": time.Unix(1, 0).UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{Sequences, Receipts} {
		if _, err := db.Collection(name).InsertOne(ctx, bson.M{"_id": MembershipSchemaID, "version": int32(1), "epoch": base.epoch, "activation_sha256": base.activationSHA, "legacy_message_writers_retired": true, "legacy_group_writers_retired": true, "owner_bounds_seeded": true, "readers_bound": true}); err != nil {
			t.Fatal(err)
		}
	}
	for _, user := range roster {
		w := boundaryWindowFixture()
		w.Epoch, w.UserID = base.epoch, user
		w.ID = key(base.epoch, "membership-window", "sg_g\x00"+user)
		if _, err := db.Collection(Sequences).InsertOne(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	initializeSequence(t, ctx, authority, base, "sg_g", 0)
	s, err := NewMembershipBoundStore(db, base.epoch, base.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, constant.OpUserID, "owner")
	record := kafkaFixtureRecord(t, 301)
	var message sdkws.MsgData
	if proto.Unmarshal(record.Body, &message) != nil {
		t.Fatal("source")
	}
	message.Options = msgprocessor.WithOptions(message.Options, msgprocessor.WithOfflinePush(true))
	record.Body, err = proto.MarshalOptions{Deterministic: true}.Marshal(&message)
	if err != nil {
		t.Fatal(err)
	}
	policy, _ := kafkaPolicyFixture(t, s)
	policy.Vendor = "getui"
	policy.VendorBinding = hash([]byte("synthetic alias binding"))
	policy.OfflinePushBinding = hash([]byte("synthetic callback binding"))
	policy.OfflineFailureMode = mode
	fingerprint, err := policy.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := s.AcceptKafkaRecord(ctx, record, fingerprint, policy)
	if err != nil || len(accepted.Branches) != 1 {
		t.Fatal("source acceptance", err)
	}
	branch := accepted.Branches[0].ID
	message.Seq = accepted.Branches[0].Seq
	for page := 0; page < (len(roster)+63)/64; page++ {
		if err := s.RecoverKafkaOnline(ctx, branch, &staleBodyReader{message: &message}, discardProjection{}, &kafkaOnlineFixtureTransport{policy: fingerprint}); err != nil {
			t.Fatal(err)
		}
	}
	return ctx, s, db, branch, &message, &offlineCallbackFixtureAdapter{store: s, fingerprint: fingerprint, binding: policy.VendorBinding}
}
func callbackFixtureDispatch(ctx context.Context, s *Store, call KafkaOfflineCall) (*KafkaOfflineCallbackDispatch, []byte, error) {
	settled, err := s.KafkaOfflineCallbackSettled(ctx, call)
	if err != nil || settled {
		return nil, nil, err
	}
	input, err := s.KafkaOfflineCallbackInput(ctx, call)
	if err != nil {
		return nil, nil, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, nil, err
	}
	d, err := s.BeginKafkaOfflineCallback(ctx, call, input.InputSHA256, hash(body))
	return d, body, err
}
func TestKafkaOfflineCallbackMongoActualWorkerConsumesOneFrozenDecisionAndErasesPrivateOutput(t *testing.T) {
	ctx, s, db, branch, message, a := offlineCallbackFixture(t, "default-stop", nil, nil)
	callbacks, posts := 0, 0
	a.prepare = func(ctx context.Context, call KafkaOfflineCall) error {
		d, body, err := callbackFixtureDispatch(ctx, s, call)
		if err != nil || d == nil {
			return err
		}
		if err := d.BeforeHTTP(ctx, call, body); err != nil {
			return err
		}
		callbacks++
		return d.Finish(ctx, "acknowledged", hash([]byte("synthetic response")), []string{"member", "owner"}, &sdkws.OfflinePushInfo{Title: "changed title"})
	}
	a.provider = func(ctx context.Context, call KafkaOfflineCall, user string, d *KafkaAliasDispatch) error {
		effective, err := s.KafkaOfflineProviderMessage(ctx, call)
		if err != nil || effective.OfflinePushInfo == nil || effective.OfflinePushInfo.Title != "changed title" || !proto.Equal(call.Message, message) {
			t.Fatal("provider did not use exact callback info without rewriting original", err)
		}
		if err := d.BeforeProvider(ctx, call, user, d.row.Input.BodySHA256); err != nil {
			return err
		}
		posts++
		return d.Finish(ctx, KafkaAliasReport{PhysicalAttemptStarted: true, ProviderAccepted: true, ProviderReceiptSHA256: hash([]byte("synthetic provider ACK")), OutcomeSHA256: hash([]byte("synthetic outcome"))})
	}
	if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err != nil || callbacks != 1 || posts != 2 {
		t.Fatal("actual page lost legal in-roster expansion or repeated callback", err)
	}
	var row offlineCallbackRow
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"_id": key(s.epoch, "kafka-offline-callback", branch)}).Decode(&row); err != nil || !row.Erased || len(row.Info) > 0 || row.State != "acknowledged" {
		t.Fatal("settled callback retained private output", err)
	}
	var maximum counter
	if err := db.Collection(Sequences).FindOne(ctx, bson.M{"_id": key(s.epoch, "sequence", "sg_g")}).Decode(&maximum); err != nil || maximum.Maximum != 1 {
		t.Fatal("callback allocated another history sequence", err)
	}
}
func TestKafkaOfflineCallbackMongoUnknownRestartAndOperatorCASNeverReplayOriginalHTTP(t *testing.T) {
	ctx, s, db, branch, message, a := offlineCallbackFixture(t, "default-stop", nil, nil)
	calls := 0
	a.prepare = func(ctx context.Context, call KafkaOfflineCall) error {
		d, body, err := callbackFixtureDispatch(ctx, a.store, call)
		if err != nil || d == nil {
			return err
		}
		if err := d.BeforeHTTP(ctx, call, body); err != nil {
			return err
		}
		calls++
		return d.Finish(ctx, "unknown", hash([]byte("synthetic lost response")), nil, nil)
	}
	a.provider = func(context.Context, KafkaOfflineCall, string, *KafkaAliasDispatch) error {
		t.Fatal("unknown callback reached supplier")
		return ErrInvalid
	}
	if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil || calls != 1 {
		t.Fatal("unknown callback accepted or not attempted", err)
	}
	restarted, err := NewMembershipBoundStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	a.store = restarted
	if err := restarted.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil || calls != 1 {
		t.Fatal("restart automatically replayed unknown callback", err)
	}
	expected, err := restarted.InspectKafkaOfflineCallback(ctx, branch)
	if err != nil || expected.State != "unconfirmed" {
		t.Fatal(err)
	}
	proof := KafkaOfflineCallbackConfirmation{Decision: "applied", EvidenceSHA256: hash([]byte("synthetic independent endpoint inspection")), ResponseSHA256: hash([]byte("synthetic original response")), Users: []string{"member"}, Drain: KafkaAttemptDrainProof{InstanceID: expected.InstanceID, EvidenceSHA256: hash([]byte("synthetic original joined worker"))}}
	stale := expected
	stale.Revision++
	if err := restarted.ConfirmKafkaOfflineCallback(ctx, stale, proof); !errors.Is(err, ErrConflict) {
		t.Fatal("stale operator borrowed revision", err)
	}
	if err := restarted.ConfirmKafkaOfflineCallback(ctx, expected, proof); err != nil {
		t.Fatal("independent original output not confirmable", err)
	}
	if err := restarted.ConfirmKafkaOfflineCallback(ctx, expected, proof); !errors.Is(err, ErrConflict) {
		t.Fatal("old operator overwrote confirmed output", err)
	}
	current, err := restarted.InspectKafkaOfflineCallback(ctx, branch)
	if err != nil || current.State != "acknowledged" || current.RecordSHA256 == expected.RecordSHA256 || calls != 1 {
		t.Fatal("confirmation caused callback or lost result", err)
	}
	deleted, err := db.Collection(Effects).DeleteOne(ctx, bson.M{"_id": key(s.epoch, "kafka-offline-callback", branch), "kind": "kafka-offline-callback-v1"})
	if err != nil || deleted.DeletedCount != 1 {
		t.Fatal("synthetic receipt-loss fixture not established", err)
	}
	if err := restarted.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); !errors.Is(err, ErrConflict) || calls != 1 {
		t.Fatal("missing child receipt replayed a previously frozen callback", err)
	}
}
func TestKafkaOfflineCallbackMongoContinueAndRejectedAreDistinctDurableDecisions(t *testing.T) {
	for _, outcome := range []string{"unknown", "rejected"} {
		t.Run(outcome, func(t *testing.T) {
			ctx, s, _, branch, message, a := offlineCallbackFixture(t, "continue", nil, nil)
			callbacks, posts := 0, 0
			a.prepare = func(ctx context.Context, call KafkaOfflineCall) error {
				d, body, err := callbackFixtureDispatch(ctx, s, call)
				if err != nil || d == nil {
					return err
				}
				if err := d.BeforeHTTP(ctx, call, body); err != nil {
					return err
				}
				callbacks++
				return d.Finish(ctx, outcome, hash([]byte("synthetic observed outcome")), nil, nil)
			}
			a.provider = func(ctx context.Context, call KafkaOfflineCall, user string, d *KafkaAliasDispatch) error {
				if err := d.BeforeProvider(ctx, call, user, d.row.Input.BodySHA256); err != nil {
					return err
				}
				posts++
				return d.Finish(ctx, KafkaAliasReport{PhysicalAttemptStarted: true, ProviderAccepted: true, ProviderReceiptSHA256: hash([]byte("synthetic provider ACK")), OutcomeSHA256: hash([]byte("synthetic complete"))})
			}
			if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err != nil || callbacks != 1 {
				t.Fatal("known callback decision left perpetual pending page", err)
			}
			wanted := 1
			if outcome == "rejected" {
				wanted = 0
			}
			if posts != wanted {
				t.Fatal("authentication/semantic rejection became failedContinue")
			}
		})
	}
}
func TestKafkaOfflineCallbackMongoForeignResponseNeverGrantsSupplierPermission(t *testing.T) {
	ctx, s, _, branch, message, a := offlineCallbackFixture(t, "continue", nil, nil)
	a.prepare = func(ctx context.Context, call KafkaOfflineCall) error {
		d, body, err := callbackFixtureDispatch(ctx, s, call)
		if err != nil || d == nil {
			return err
		}
		if err := d.BeforeHTTP(ctx, call, body); err != nil {
			return err
		}
		return d.Finish(ctx, "acknowledged", hash([]byte("synthetic foreign list")), []string{"foreign"}, nil)
	}
	a.provider = func(context.Context, KafkaOfflineCall, string, *KafkaAliasDispatch) error {
		t.Fatal("callback invented cross-group read authority")
		return ErrInvalid
	}
	if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil {
		t.Fatal("foreign response accepted")
	}
	summary, err := s.InspectKafkaOfflineCallback(ctx, branch)
	if err != nil || summary.State != "dispatching" {
		t.Fatal("unsafe output was silently continued", err)
	}
}

type offlineCallbackBarrierKey struct{}

func TestKafkaOfflineCallbackMongoClearWinsBeforeAttemptCommit(t *testing.T) {
	arrived, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var once sync.Once
	var paused atomic.Bool
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		if ctx.Value(offlineCallbackBarrierKey{}) != true || e.CommandName != "update" || !bytes.Contains(e.Command, []byte("callback_revision")) {
			return
		}
		if paused.CompareAndSwap(false, true) {
			close(arrived)
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
	}}
	ctx, s, db, branch, message, a := offlineCallbackFixture(t, "default-stop", monitor, nil)
	work, cancel := context.WithCancel(context.WithValue(ctx, offlineCallbackBarrierKey{}, true))
	defer cancel()
	resume := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		cancel()
		resume()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			ctx.Value(sequenceFixtureOwnershipKey{}).(*sequenceFixtureOwnership).dropAllowed.Store(false)
			t.Error("callback worker not drained; retain fixture")
		}
	})
	posts := 0
	a.prepare = func(ctx context.Context, call KafkaOfflineCall) error {
		d, body, err := callbackFixtureDispatch(ctx, s, call)
		if err != nil || d == nil {
			return err
		}
		if err := d.BeforeHTTP(ctx, call, body); err != nil {
			return err
		}
		posts++
		return nil
	}
	a.provider = func(context.Context, KafkaOfflineCall, string, *KafkaAliasDispatch) error {
		t.Fatal("cleared message reached vendor")
		return ErrInvalid
	}
	result := make(chan error, 1)
	go func() {
		defer close(done)
		result <- s.RecoverKafkaOffline(work, branch, &staleBodyReader{message: message}, a)
	}()
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("actual transaction did not reach final parent write")
	}
	if err := physicalPermitClear(ctx, s, db); err != nil {
		t.Fatal(err)
	}
	resume()
	select {
	case err := <-result:
		if err == nil || posts != 0 {
			t.Fatal("Clear committed before callback attempt but old plaintext got a permit", err)
		}
	case <-work.Done():
		t.Fatal("callback did not finish")
	}
	physicalPermitNoPlaintext(t, ctx, db, s, branch)
	if count, err := db.Collection(Effects).CountDocuments(ctx, bson.M{"kind": "kafka-offline-callback-v1"}); err != nil || count != 0 {
		t.Fatal("aborted callback claim survived", err)
	}
}
func TestKafkaOfflineCallbackMongoLostCommitACKDoesNotGrantHTTPAfterRestart(t *testing.T) {
	drop := &commitReplyDropper{requests: map[int32]bool{}, all: true}
	baseMonitor := drop.monitor()
	var factsMu sync.Mutex
	var workFacts []deliveryWireFact
	monitor := &event.CommandMonitor{Started: func(ctx context.Context, e *event.CommandStartedEvent) {
		baseMonitor.Started(ctx, e)
		if ctx.Value(deliveryDropCommitKey{}) != true || e.CommandName != "update" && e.CommandName != "insert" {
			return
		}
		auto, ok := e.Command.Lookup("autocommit").BooleanOK()
		txn, txOK := e.Command.Lookup("txnNumber").Int64OK()
		lsid, lsOK := e.Command.Lookup("lsid").DocumentOK()
		factsMu.Lock()
		workFacts = append(workFacts, deliveryWireFact{command: e.CommandName, transaction: ok && !auto && txOK && lsOK, txn: txn, lsidSHA: hash(lsid)})
		factsMu.Unlock()
	}}
	ctx, s, db, branch, message, a := offlineCallbackFixture(t, "default-stop", monitor, drop)
	attempts, posts := 0, 0
	a.prepare = func(ctx context.Context, call KafkaOfflineCall) error {
		attempts++
		d, body, err := callbackFixtureDispatch(context.WithValue(ctx, deliveryDropCommitKey{}, true), a.store, call)
		if err != nil {
			return err
		}
		if d == nil {
			return nil
		}
		if err := d.BeforeHTTP(ctx, call, body); err != nil {
			return err
		}
		posts++
		return nil
	}
	a.provider = func(context.Context, KafkaOfflineCall, string, *KafkaAliasDispatch) error {
		t.Fatal("unknown claim reached vendor")
		return ErrInvalid
	}
	if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil || posts != 0 || drop.dropped.Load() == 0 {
		t.Fatal("commit reply fault was not exercised or granted HTTP", err)
	}
	factsMu.Lock()
	facts := append([]deliveryWireFact(nil), workFacts...)
	factsMu.Unlock()
	drop.mu.Lock()
	commits := append([]deliveryWireFact(nil), drop.facts...)
	drop.mu.Unlock()
	if len(facts) != 2 || facts[0].command != "update" || facts[1].command != "insert" || len(commits) == 0 {
		t.Fatal("callback attempt did not write exactly one parent fence+child in actual TX")
	}
	for _, f := range append(facts, commits...) {
		if !f.transaction || f.txn != facts[0].txn || f.lsidSHA != facts[0].lsidSHA {
			t.Fatal("commit retry repeated work or lost actual session")
		}
	}
	expected, err := s.InspectKafkaOfflineCallback(ctx, branch)
	if err != nil || expected.State != "dispatching" {
		t.Fatal("actual committed receipt was lost or rewritten", err)
	}
	restarted, err := NewMembershipBoundStore(db, s.epoch, s.activationSHA)
	if err != nil {
		t.Fatal(err)
	}
	a.store = restarted
	if err := restarted.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil || posts != 0 {
		t.Fatal("restart replayed uncertain callback", err)
	}
	got, err := restarted.InspectKafkaOfflineCallback(ctx, branch)
	if err != nil || !reflect.DeepEqual(got, expected) {
		t.Fatal("restart rewrote original claim", err)
	}
	if attempts != 2 {
		t.Fatal("real worker did not inspect original receipt on restart")
	}
}

func TestKafkaOfflineCallbackMongoWholeOriginalCandidateIsFrozenOnceAcrossPages(t *testing.T) {
	ctx, s, _, branch, message, a := offlineCallbackFixture(t, "default-stop", nil, nil, 69)
	callbacks, posts := 0, 0
	a.prepare = func(ctx context.Context, call KafkaOfflineCall) error {
		settled, err := s.KafkaOfflineCallbackSettled(ctx, call)
		if err != nil || settled {
			return err
		}
		input, err := s.KafkaOfflineCallbackInput(ctx, call)
		if err != nil {
			return err
		}
		if len(input.Users) != 70 {
			t.Fatal("callback request was narrowed to a64-recipient delivery page")
		}
		body, err := json.Marshal(input)
		if err != nil {
			return err
		}
		d, err := s.BeginKafkaOfflineCallback(ctx, call, input.InputSHA256, hash(body))
		if err != nil || d == nil {
			return err
		}
		if err := d.BeforeHTTP(ctx, call, body); err != nil {
			return err
		}
		callbacks++
		return d.Finish(ctx, "acknowledged", hash([]byte("synthetic original whole-list response")), nil, nil)
	}
	a.provider = func(ctx context.Context, call KafkaOfflineCall, user string, d *KafkaAliasDispatch) error {
		if err := d.BeforeProvider(ctx, call, user, d.row.Input.BodySHA256); err != nil {
			return err
		}
		posts++
		return d.Finish(ctx, KafkaAliasReport{PhysicalAttemptStarted: true, ProviderAccepted: true, ProviderReceiptSHA256: hash([]byte("synthetic ACK")), OutcomeSHA256: hash([]byte("synthetic outcome"))})
	}
	for i := 0; i < 2; i++ {
		if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err != nil {
			t.Fatal("actual multi-page delivery failed", err)
		}
	}
	if callbacks != 1 || posts != 70 {
		t.Fatal("whole branch callback or original recipient replayed", callbacks, posts)
	}
}

func TestKafkaOfflineCallbackMongoClearAfterAttemptNeverPersistsLatePrivateResponse(t *testing.T) {
	ctx, s, db, branch, message, a := offlineCallbackFixture(t, "default-stop", nil, nil)
	callbacks, posts := 0, 0
	a.prepare = func(work context.Context, call KafkaOfflineCall) error {
		d, body, err := callbackFixtureDispatch(work, s, call)
		if err != nil || d == nil {
			return err
		}
		if err := d.BeforeHTTP(work, call, body); err != nil {
			return err
		}
		callbacks++ // This is already an admitted in-flight callback; Clear cannot retract remote effects.
		if err := physicalPermitClear(ctx, s, db); err != nil {
			return err
		}
		return d.Finish(work, "acknowledged", hash([]byte("synthetic late response")), []string{"member"}, &sdkws.OfflinePushInfo{Title: "synthetic private late result"})
	}
	a.provider = func(context.Context, KafkaOfflineCall, string, *KafkaAliasDispatch) error { posts++; return ErrInvalid }
	if err := s.RecoverKafkaOffline(ctx, branch, &staleBodyReader{message: message}, a); err == nil || callbacks != 1 || posts != 0 {
		t.Fatal("late callback recreated cleared private output or reached provider", err)
	}
	physicalPermitNoPlaintext(t, ctx, db, s, branch)
	var row offlineCallbackRow
	if err := db.Collection(Effects).FindOne(ctx, bson.M{"_id": key(s.epoch, "kafka-offline-callback", branch)}).Decode(&row); err != nil || !row.Erased || len(row.Info) != 0 || row.State != "dispatching" {
		t.Fatal("late response changed original redacted receipt", err)
	}
}
