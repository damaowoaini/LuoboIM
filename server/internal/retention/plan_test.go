package retention

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	driversession "go.mongodb.org/mongo-driver/x/mongo/driver/session"
)

var testNow = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)

func validPolicy() Policy {
	return Policy{Owner: "owner", Conversation: "si_owner_peer", Enabled: true, Duration: 30, Revision: 1, Prepared: true}
}

type sourceFixture struct {
	slots []Slot
	err   error
	calls int
}

func (s *sourceFixture) Slots(context.Context, Policy, int64, int) ([]Slot, error) {
	s.calls++
	return s.slots, s.err
}

func TestEligibilityNeverLetsZeroLatestBypassDisabledOrInvalidDuration(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		enabled                        bool
		duration, revision, generation int64
		want                           bool
	}{
		{"disabled_zero_latest", false, 30, 1, 0, false}, {"zero", true, 0, 1, 0, false}, {"negative", true, -1, 1, 0, false},
		{"overflow", true, MaxDurationSeconds + 1, 1, 0, false}, {"not_prepared", true, 30, 0, 0, false},
		{"revision_exhausted", true, 30, math.MaxInt64, 0, false}, {"generation_exhausted", true, 30, 1, math.MaxInt64, false},
		{"enabled_first_scan", true, 30, 1, 0, true}, {"long_valid_legacy_preference", true, 15552001, 1, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := validPolicy()
			p.Enabled = tc.enabled
			p.Duration = tc.duration
			p.Revision = tc.revision
			p.Generation = tc.generation
			if p.Eligible() != tc.want {
				t.Fatalf("eligible=%v want=%v", p.Eligible(), tc.want)
			}
			source := &sourceFixture{}
			_, err := BuildPlan(context.Background(), source, p, 1, testNow, MaximumChunk)
			if tc.want && err != nil {
				t.Fatal(err)
			}
			if !tc.want && (err == nil || source.calls != 0) {
				t.Fatal("invalid policy reached source")
			}
		})
	}
}

func TestPlanUsesSafeContiguousPrefixAndExactSecondsBoundary(t *testing.T) {
	cutoff := testNow.UnixMilli() - 30000
	for _, tc := range []struct {
		name  string
		slots []Slot
		want  int64
	}{
		{"due_at_boundary", []Slot{{Seq: 1, Known: true, SendTime: cutoff, Document: "d"}}, 2},
		{"millisecond_before_due", []Slot{{Seq: 1, Known: true, SendTime: cutoff + 1, Document: "d"}}, 1},
		{"live_before_expired", []Slot{{Seq: 1, Known: true, SendTime: cutoff + 1, Document: "d"}, {Seq: 2, Known: true, SendTime: cutoff - 100, Document: "d"}}, 1},
		{"unknown_before_expired", []Slot{{Seq: 1, Known: false, Document: "d"}, {Seq: 2, Known: true, SendTime: cutoff - 100, Document: "d"}}, 1},
		{"owner_tombstone_prefix", []Slot{{Seq: 1, Known: true, Hidden: true, Document: "d"}, {Seq: 2, Known: true, SendTime: cutoff + 1, Document: "d"}}, 2},
		{"invalid_send_time", []Slot{{Seq: 1, Known: true, SendTime: 0, Document: "d"}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := BuildPlan(context.Background(), &sourceFixture{slots: tc.slots}, validPolicy(), 1, testNow, MaximumChunk)
			if err != nil || plan.ToFloor != tc.want {
				t.Fatalf("floor=%d want=%d error=%v", plan.ToFloor, tc.want, err)
			}
		})
	}
}

func TestPlanReadFailureCannotPublishAccumulatedDestructiveProgress(t *testing.T) {
	source := &sourceFixture{slots: []Slot{{Seq: 1, Known: true, SendTime: 1, Document: "d"}}, err: errors.New("synthetic read failure")}
	plan, err := BuildPlan(context.Background(), source, validPolicy(), 1, testNow, MaximumChunk)
	if err == nil || plan.ToFloor != 0 || len(plan.Sequences) != 0 {
		t.Fatal("partial plan escaped read error")
	}
	_, err = BuildPlan(context.Background(), &sourceFixture{slots: []Slot{{Seq: 2, Known: true, Hidden: true, Document: "d"}}}, validPolicy(), 1, testNow, MaximumChunk)
	if !errors.Is(err, ErrSourceChanged) {
		t.Fatal("out-of-order source accepted")
	}
}

func TestServerPreparationDiscardsCallerRevisionAndProgress(t *testing.T) {
	doc, err := PrepareDocument(bson.M{"owner_user_id": "owner", "conversation_id": "cid", "is_msg_destruct": false, "msg_destruct_time": int64(0), "retention_policy_revision": int64(800), "retention_scan_generation": int64(9), "latest_msg_destruct_time": testNow})
	if err != nil {
		t.Fatal(err)
	}
	if doc["retention_policy_revision"] != int64(1) || doc["retention_scan_generation"] != int64(0) || doc["latest_msg_destruct_time"] != (time.Time{}) {
		t.Fatal("caller metadata was retained")
	}
	for _, bad := range []any{-1, float64(30), "30", true} {
		if _, err := PrepareDocument(bson.M{"is_msg_destruct": true, "msg_destruct_time": bad}); err == nil {
			t.Fatalf("invalid duration type accepted: %T", bad)
		}
	}
	for _, key := range []string{"retention_policy_revision", "retention_scan_generation", "latest_msg_destruct_time"} {
		if CheckExternalFields(map[string]any{key: int64(1)}) == nil {
			t.Fatal("generic map may forge server metadata")
		}
	}
}

func TestCommitRejectsClockRollbackBeforeDatabaseAccess(t *testing.T) {
	s := NewMongoStore(nil)
	s.Now = func() time.Time { return testNow.Add(-time.Second) }
	if err := s.Commit(context.Background(), Plan{Policy: validPolicy(), At: testNow}); !errors.Is(err, ErrPolicyChanged) {
		t.Fatal("clock rollback reached database")
	}
}

func TestReadbackKeepsExplicitFalseZeroAndAbsentDistinct(t *testing.T) {
	for _, tc := range []struct {
		name   string
		doc    bson.M
		absent bool
	}{
		{"absent", bson.M{}, true}, {"explicit_false_zero", bson.M{"is_msg_destruct": false, "msg_destruct_time": int64(0)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := bson.Marshal(tc.doc)
			if err != nil {
				t.Fatal(err)
			}
			flag, err := optionalBool(raw, "is_msg_destruct")
			if err != nil {
				t.Fatal(err)
			}
			duration, err := optionalInt(raw, "msg_destruct_time")
			if err != nil {
				t.Fatal(err)
			}
			if (flag == nil) != tc.absent || (duration == nil) != tc.absent {
				t.Fatal("presence changed")
			}
			encoded, err := json.Marshal(Readback{Enabled: flag, Duration: duration})
			if err != nil {
				t.Fatal(err)
			}
			var value map[string]any
			if err = json.Unmarshal(encoded, &value); err != nil {
				t.Fatal(err)
			}
			if tc.absent {
				if value["isMsgDestruct"] != nil {
					t.Fatal("missing became false")
				}
			} else if value["isMsgDestruct"] != false || value["msgDestructTime"] != float64(0) {
				t.Fatal("false/zero omitted")
			}
		})
	}
	bad, _ := bson.Marshal(bson.M{"is_msg_destruct": 1})
	if _, err := optionalBool(bad, "is_msg_destruct"); err == nil {
		t.Fatal("numeric flag accepted")
	}
}

// A driver-state double checks the joining contract offline. Real durability and
// rollback are separately covered by the explicit replica-set fixture below.
type transactionStateFixture struct {
	mongo.Session
	state  *driversession.Client
	client *mongo.Client
}

func (s *transactionStateFixture) Client() *mongo.Client                { return s.client }
func (s *transactionStateFixture) ClientSession() *driversession.Client { return s.state }

func TestPolicyTransactionNeverReplacesWeakOrForeignOuterSession(t *testing.T) {
	for _, name := range []string{"strong_join", "missing_driver_state", "bare_session", "weak_read", "weak_write", "no_journal", "secondary", "foreign_client", "cleanup_must_own_commit", "unowned_external_policy_scope"} {
		t.Run(name, func(t *testing.T) {
			client := &mongo.Client{}
			state := &driversession.Client{TransactionState: driversession.Starting, CurrentRc: readconcern.Snapshot(), CurrentRp: readpref.Primary(), CurrentWc: writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))}
			current := &transactionStateFixture{client: client, state: state}
			join := true
			switch name {
			case "missing_driver_state":
				current.state = nil
			case "bare_session":
				state.TransactionState = driversession.None
			case "weak_read":
				state.CurrentRc = readconcern.Local()
			case "weak_write":
				state.CurrentWc = writeconcern.New(writeconcern.W(1), writeconcern.J(true))
			case "no_journal":
				state.CurrentWc = writeconcern.New(writeconcern.WMajority())
			case "secondary":
				state.CurrentRp = readpref.Secondary()
			case "foreign_client":
				current.client = &mongo.Client{}
			case "cleanup_must_own_commit":
				join = false
			}
			ctx := mongo.NewSessionContext(context.Background(), current)
			calls := 0
			fn := func(context.Context) error { calls++; return nil }
			var err error
			if name == "unowned_external_policy_scope" {
				err = WithPolicyTransaction(ctx, client, fn)
			} else {
				err = transaction(ctx, client, join, fn)
			}
			want := name == "strong_join"
			if (err == nil) != want || (calls == 1) != want {
				t.Fatalf("joined=%v calls=%d want=%v", err == nil, calls, want)
			}
		})
	}
}

func TestPolicyEffectsWaitForCommitAndReportPendingWithoutFalseRollback(t *testing.T) {
	effects := &commitEffects{}
	ctx := context.WithValue(context.Background(), effectsKey{}, effects)
	calls := 0
	if err := AfterCommit(ctx, func(context.Context) error { calls++; return errors.New("synthetic cache unavailable") }); err != nil {
		t.Fatal(err)
	}
	if err := AfterCommit(ctx, func(context.Context) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("effect ran before commit")
	}
	// An aborted transaction never calls flushEffects; all effects remain inert.
	if !RequiresStrongTransaction(ctx) || RequiresStrongTransaction(context.Background()) {
		t.Fatal("transaction scope lost")
	}
	if err := flushEffects(context.Background(), effects); !errors.Is(err, ErrSyncPending) || calls != 2 {
		t.Fatalf("postcommit pending=%v calls=%d", err, calls)
	}
	ordinary := 0
	if err := AfterCommit(context.Background(), func(context.Context) error { ordinary++; return nil }); err != nil || ordinary != 1 {
		t.Fatal("ordinary nonpolicy side effect changed")
	}
}
