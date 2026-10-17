//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
)

// This file is compiled ONLY by go test, including its external test package.
// No production constructor, private Permit or private VerifiedPage is exported.
// The PageSource and signed exclusion observations below are SYNTHETIC. Real
// Mongo transactions/custody/receipts are tested; real process/Kafka/credential
// exclusion, capture authenticity and production Ready are not attested here.
type HistoricalMongoCompositionForTest struct {
	Source  *HistoricalCaptureSource
	Hold    *AdmissionHold
	Config  namespaceimport.Config
	control *historicalMongoSyntheticControl
}

// prepare installs exactly the synthetic raw input into the fresh nonce DB,
// invokes the real held-metadata adapter, then returns that actual DB's bounded
// original rows. Capture starts only AFTER this explicit metadata phase.
func NewHistoricalMongoCompositionForTest(ctx context.Context, t *testing.T, h namespaceimport.Header, prepare func(context.Context, namespaceimport.Header, *AdmissionHold, map[string][]bson.Raw) (map[string][]bson.Raw, error)) *HistoricalMongoCompositionForTest {
	t.Helper()
	captureKey := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	probeSeed := make([]byte, ed25519.SeedSize)
	probeSeed[0] = 1
	probeKey := ed25519.NewKeyFromSeed(probeSeed)
	config := namespaceimport.Config{Header: h, Writers: []namespaceimport.WriterIdentity{{Deployment: "synthetic-composition-writer", Incarnation: "synthetic-incarnation", BinarySHA256: historicalJSONSHA("synthetic-binary"), SourceSHA256: historicalJSONSHA("synthetic-source")}}, Credentials: []namespaceimport.CredentialIdentity{{Scope: "synthetic-mongo", CredentialID: "synthetic-retired", Version: "test-v1"}}, KafkaPartitions: []namespaceimport.KafkaPartitionIdentity{{Plane: "producer-to-allocation", Topic: "synthetic-intake", Group: "synthetic-old-group", Partition: 0}, {Plane: "allocation-to-persistence", Topic: "synthetic-store", Group: "synthetic-old-store", Partition: 0}}, ProbeKeyID: "synthetic-probe-key", ProbePublicKey: probeKey.Public().(ed25519.PublicKey), CaptureKeyID: "test-key", CapturePublicKey: captureKey.Public().(ed25519.PublicKey), CaptureAgent: "synthetic-private-capture", OperationTimeout: time.Minute, PermitLifetime: 20 * time.Second, PageBytes: 1 << 20, PageRows: 1}
	config.Header.DeploymentPolicySHA256 = namespaceimport.DeploymentPolicySHA256(config)
	h = config.Header
	var captureRaw func(context.Context) error
	source, _, _ := materializerSourceFixtureWithCaptureMode(t, h, func(hold *AdmissionHold, f *materializerPageFixture) error {
		if prepare == nil {
			return namespaceimport.ErrInvalid
		}
		// 65 distinct historical owners force TWO real 64-leaf owner and boundary
		// closure transactions. They are not added to the current roster/future set.
		normal := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, "sg_group_with_under")
		old := "44444444-4444-4444-8444-444444444444"
		for n := 0; n < 64; n++ {
			uid := fmt.Sprintf("closed-owner-%02d", n)
			raw := rawHistorical(t, bson.M{"_id": historicalSequenceKey(old, "membership-window", normal+"\x00"+uid), "kind": "membership-window-v1", "epoch": old, "conversation_id": normal, "group_id": "sg_group_with_under", "user_id": uid, "last_command": "66666666-6666-4666-8666-666666666666", "seed_evidence_sha256": historicalJSONSHA("old-seed"), "lower": int64(2), "upper": int64(3), "revision": int64(1), "group_revision": int64(1), "closed": true})
			f.rows["message_delivery_sequences_v1"] = append(f.rows["message_delivery_sequences_v1"], namespaceinventory.MongoRow{Collection: "message_delivery_sequences_v1", KeySHA: historicalJSONSHA(raw.Lookup("_id").StringValue()), Raw: raw})
		}
		rawRows := map[string][]bson.Raw{}
		for _, name := range materializerFixtureCollections {
			for _, r := range f.rows[name] {
				rawRows[name] = append(rawRows[name], append(bson.Raw(nil), r.Raw...))
			}
			if rawRows[name] == nil {
				rawRows[name] = []bson.Raw{}
			}
		}
		actual, e := prepare(ctx, h, hold, rawRows)
		if e != nil {
			return e
		}
		if len(actual) != len(materializerFixtureCollections) {
			return namespaceimport.ErrConflict
		}
		f.rows = map[string][]namespaceinventory.MongoRow{}
		f.catalogPresent = map[string]bool{}
		for _, name := range materializerFixtureCollections {
			rows, found := actual[name]
			if !found {
				return namespaceimport.ErrConflict
			}
			f.catalogPresent[name] = true
			for _, raw := range rows {
				id, ok := raw.Lookup("_id").StringValueOK()
				if !ok || id == "" {
					return namespaceimport.ErrInvalid
				}
				f.rows[name] = append(f.rows[name], namespaceinventory.MongoRow{Collection: name, KeySHA: historicalJSONSHA(id), Raw: append(bson.Raw(nil), raw...)})
			}
		}
		return nil
	}, func(run func(context.Context) error) {
		if captureRaw != nil || run == nil {
			t.Fatal("raw capture continuation must be registered exactly once")
		}
		captureRaw = run
	})
	if captureRaw == nil || source.materializer != nil {
		t.Fatal("Controller fixture captured before its original Acquire")
	}
	result := &HistoricalMongoCompositionForTest{Source: source, Hold: source.agent.hold, Config: config}
	result.control = &historicalMongoSyntheticControl{fixture: result, key: probeKey, stoppedAt: time.Now().UTC(), captureRaw: captureRaw}
	return result
}

func (f *HistoricalMongoCompositionForTest) ControlPlaneForTest() namespaceimport.ControlPlane {
	return f.control
}
func (f *HistoricalMongoCompositionForTest) RefreshSyntheticHoldForTest() {
	f.Hold.exclusionAt = time.Now().UTC()
}
func (f *HistoricalMongoCompositionForTest) SnapshotFingerprintForTest() string {
	return f.Source.materializer.snapshot.Fingerprint()
}
func (f *HistoricalMongoCompositionForTest) OriginalTrustForTest() (string, string) {
	return f.Source.options.OriginalHistoryTrustPath, f.Source.options.OriginalHistoryTrustSHA256
}
func (f *HistoricalMongoCompositionForTest) UseOriginalHistoryVerifierForTest(producer namespaceimport.OriginalHistorySegmentProducer, verifier namespaceimport.OriginalHistorySegmentVerifier) {
	f.Source.producer = producer
	f.Source.verifier = verifier
}

type historicalMongoSyntheticControl struct {
	fixture          *HistoricalMongoCompositionForTest
	key              ed25519.PrivateKey
	stoppedAt        time.Time
	captureRaw       func(context.Context) error
	acquireAttempted bool
	acquireFence     namespaceimport.FenceObservation
	acquireFailure   error
}

func (c *historicalMongoSyntheticControl) Acquire(ctx context.Context, r namespaceimport.ProbeRequest) (namespaceimport.FenceObservation, error) {
	// This retained test control may acquire once. Even a failed/ambiguous
	// first attempt cannot obtain a new nonce, cut or snapshot timestamp.
	if c.acquireAttempted {
		if c.acquireFailure != nil {
			return namespaceimport.FenceObservation{}, c.acquireFailure
		}
		return namespaceimport.FenceObservation{}, namespaceimport.ErrConflict
	}
	c.acquireAttempted = true
	fence, e := c.answer(ctx, r)
	if e != nil {
		c.acquireFailure = e
		return namespaceimport.FenceObservation{}, e
	}
	c.acquireFence = fence
	if c.captureRaw == nil || c.fixture.Source.materializer != nil {
		c.acquireFailure = namespaceimport.ErrConflict
		return namespaceimport.FenceObservation{}, c.acquireFailure
	}
	if e = c.captureRaw(ctx); e != nil {
		c.acquireFailure = e
		return namespaceimport.FenceObservation{}, e
	}
	// Observe only the actual immutable clock recorded by BeginCapture. These
	// checks cannot repair/backdate the raw session or manufacture a valid cut.
	capturedAt := c.fixture.Source.materializer.snapshot.Identity().CapturedAt
	if capturedAt.Before(c.acquireFence.IssuedAt) || capturedAt.Before(c.stoppedAt) {
		c.acquireFailure = namespaceimport.ErrUntrusted
		return namespaceimport.FenceObservation{}, c.acquireFailure
	}
	for _, sample := range c.acquireFence.Drained {
		if capturedAt.Before(sample.CurrentSampleAt) {
			c.acquireFailure = namespaceimport.ErrUntrusted
			return namespaceimport.FenceObservation{}, c.acquireFailure
		}
	}
	if e = ctx.Err(); e != nil {
		c.acquireFailure = e
		return namespaceimport.FenceObservation{}, e
	}
	c.captureRaw = nil
	return c.acquireFence, nil
}
func (c *historicalMongoSyntheticControl) Observe(ctx context.Context, r namespaceimport.ProbeRequest) (namespaceimport.FenceObservation, error) {
	return c.answer(ctx, r)
}
func (c *historicalMongoSyntheticControl) answer(ctx context.Context, r namespaceimport.ProbeRequest) (namespaceimport.FenceObservation, error) {
	if ctx.Err() != nil {
		return namespaceimport.FenceObservation{}, ctx.Err()
	}
	f := c.fixture
	f.RefreshSyntheticHoldForTest()
	if r.Header != f.Config.Header || r.LeaseID != "" && r.LeaseID != f.Hold.LeaseID() {
		return namespaceimport.FenceObservation{}, namespaceimport.ErrConflict
	}
	// Record the two synthetic samples in their actual order. The fixture does
	// not claim a real broker drain, and never invents earlier clock values to
	// fit a snapshot that was already captured.
	previousSample := time.Now().UTC()
	sample := time.Now().UTC()
	now := time.Now().UTC()
	if !previousSample.Before(sample) || sample.After(now) {
		return namespaceimport.FenceObservation{}, namespaceimport.ErrUntrusted
	}
	out := namespaceimport.FenceObservation{HeaderSHA256: r.Header.SHA256(), Nonce: r.Nonce, LeaseID: f.Hold.LeaseID(), Generation: 1, KeyID: f.Config.ProbeKeyID, IssuedAt: now, ValidUntil: now.Add(time.Minute), ExclusionMode: "held-until-explicit-release", AdmissionGuardRecord: "synthetic-composition-hold-only", AdmissionGuardSHA256: f.Hold.RecordSHA256()}
	for _, w := range f.Config.Writers {
		out.Joined = append(out.Joined, namespaceimport.JoinedWriter{Identity: w, StopRecord: "synthetic-stop", JoinRecord: "synthetic-join", ProcessExitAt: c.stoppedAt})
	}
	for _, v := range f.Config.Credentials {
		out.Revoked = append(out.Revoked, namespaceimport.RevokedCredential{Identity: v, RevocationRecord: "synthetic-denial", RevocationGeneration: 1, State: "revoked"})
	}
	for _, v := range f.Config.KafkaPartitions {
		out.Drained = append(out.Drained, namespaceimport.DrainedPartition{Identity: v, LeaderEpoch: 1, PreviousEnd: 9, CurrentEnd: 9, CommittedNext: 9, PreviousSampleAt: previousSample, CurrentSampleAt: sample, BrokerObservationRecord: "synthetic-offsets"})
	}
	body, e := json.Marshal(out)
	if e != nil {
		return namespaceimport.FenceObservation{}, e
	}
	out.Signature = ed25519.Sign(c.key, body)
	return out, nil
}
