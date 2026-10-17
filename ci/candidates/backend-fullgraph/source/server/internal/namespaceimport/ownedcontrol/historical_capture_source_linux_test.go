//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/sdkws"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
)

// This fixture tests REAL private FS/capture/reduction/page/Merkle/signature
// code. PageSource and completed exclusion are synthetic ONLY in this file;
// it does not prove a stopped service, credential denial, Kafka drain or Ready.
// Production constructor still requires actual LiveSource + OwnedAgent.
type materializerPageFixture struct {
	id             namespaceinventory.Identity
	rows           map[string][]namespaceinventory.MongoRow
	heads          []namespaceinventory.RedisRow
	calls          int
	catalogPresent map[string]bool
}

var materializerFixtureCollections = []string{"msg", "conversation", "group", "group_member", "group_request", "group_membership_history", "message_visibility_floors", "message_delivery_sequences_v1", "message_delivery_receipts_v1", "message_delivery_effects_v1", "group_authority_effects", "luobo_storage_identity"}

func (f *materializerPageFixture) Identity(ctx context.Context) (namespaceinventory.Identity, error) {
	return f.id, ctx.Err()
}
func (f *materializerPageFixture) Page(ctx context.Context, c namespaceinventory.PageCursor, l namespaceinventory.Limits) (namespaceinventory.Page, error) {
	p := namespaceinventory.Page{Before: c, After: c}
	if e := ctx.Err(); e != nil {
		return p, e
	}
	f.calls++
	if c.Phase == "mongo" {
		name := materializerFixtureCollections[c.Collection]
		next := func() namespaceinventory.PageCursor {
			if c.Collection+1 == len(materializerFixtureCollections) {
				return namespaceinventory.PageCursor{Phase: "redis"}
			}
			return namespaceinventory.PageCursor{Phase: "mongo", Collection: c.Collection + 1}
		}
		rows := f.rows[name]
		if !c.CatalogRead {
			v := bson.D{{Key: "name", Value: name}, {Key: "observedAbsent", Value: true}}
			if len(rows) > 0 || f.catalogPresent[name] {
				v = bson.D{{Key: "name", Value: name}, {Key: "type", Value: "collection"}}
				p.After.CatalogRead = true
			} else {
				p.After = next()
			}
			raw, _ := bson.Marshal(v)
			p.Mongo = []namespaceinventory.MongoRow{{Collection: "@catalog", KeySHA: sha([]byte(name)), Raw: raw}}
			return p, nil
		}
		at := 0
		if len(c.AfterID) > 0 {
			at = int(bson.Raw(c.AfterID).Lookup("v").Int64())
		}
		if at < 0 || at > len(rows) {
			return p, namespaceinventory.ErrInvalid
		}
		end := min(at+l.Rows, len(rows))
		p.Mongo = rows[at:end]
		if end == len(rows) {
			p.After = next()
		} else {
			p.After.AfterID, _ = bson.Marshal(bson.M{"v": int64(end)})
		}
		return p, nil
	}
	if c.Prefix == 0 && int(c.RedisCursor) < len(f.heads) {
		p.Redis = []namespaceinventory.RedisRow{f.heads[int(c.RedisCursor)]}
		p.After.RedisCursor++
		if int(p.After.RedisCursor) == len(f.heads) {
			p.After = namespaceinventory.PageCursor{Phase: "redis", Prefix: 1}
		}
	} else if c.Prefix == 3 {
		p.After = namespaceinventory.PageCursor{Phase: "done"}
	} else {
		p.After = namespaceinventory.PageCursor{Phase: "redis", Prefix: c.Prefix + 1}
	}
	return p, nil
}

type materializerMissingTrust struct{}

func (materializerMissingTrust) DeriveOriginalHistorySegments(context.Context, namespaceimport.Header, namespaceimport.Inventory, string, string, string, namespaceimport.OriginalHistoryRecordReader) ([]namespaceimport.OwnerHistorySegment, error) {
	return nil, &namespaceimport.OriginalHistoryProofPending{Reason: "source-trust-unavailable"}
}
func (materializerMissingTrust) VerifyOriginalHistorySegment(context.Context, namespaceimport.Header, namespaceimport.Inventory, namespaceimport.OwnerHistorySegment, namespaceimport.OriginalHistoryRecordReader) error {
	return namespaceimport.ErrUntrusted
}
func materializerSourceFixture(t *testing.T, before ...func(*materializerPageFixture)) (*HistoricalCaptureSource, *materializerPageFixture, namespaceimport.FenceObservation) {
	t.Helper()
	h := namespaceimport.Header{Tenant: "materializer-test", Database: "materializer_test", StorageInstance: "11111111-1111-4111-8111-111111111111", ReplicaSet: "test-rs", MongoProcess: strings.Repeat("b", 24), MongoTopology: 1, RedisRunID: strings.Repeat("a", 40), RedisDB: 15, KafkaClusterID: "test-cluster", Epoch: "22222222-2222-4222-8222-222222222222", ActivationSHA256: historicalJSONSHA("target-act"), MigrationID: "33333333-3333-4333-8333-333333333333", DeploymentPolicySHA256: historicalJSONSHA("deployment")}
	return materializerSourceFixtureWithHeader(t, h, nil, before...)
}
func materializerSourceFixtureWithHeader(t *testing.T, h namespaceimport.Header, prepare func(*AdmissionHold, *materializerPageFixture) error, before ...func(*materializerPageFixture)) (*HistoricalCaptureSource, *materializerPageFixture, namespaceimport.FenceObservation) {
	t.Helper()
	return materializerSourceFixtureWithCaptureMode(t, h, prepare, nil, before...)
}

// Only the Controller composition defers the original raw-capture block. The
// default fixture still executes that same block synchronously at its original
// position, with the same background context and registered cleanup order.
func materializerSourceFixtureWithCaptureMode(t *testing.T, h namespaceimport.Header, prepare func(*AdmissionHold, *materializerPageFixture) error, deferRaw func(func(context.Context) error), before ...func(*materializerPageFixture)) (*HistoricalCaptureSource, *materializerPageFixture, namespaceimport.FenceObservation) {
	t.Helper()
	ctx := context.Background()
	top := t.TempDir()
	if e := os.Chmod(top, 0700); e != nil {
		t.Fatal(e)
	}
	launcherDir := filepath.Join(top, "launcher")
	if e := os.Mkdir(launcherDir, 0700); e != nil {
		t.Fatal(e)
	}
	l, e := OpenOwnedLauncher(launcherDir, h.SHA256())
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	hold, e := l.Hold(ctx)
	if e != nil {
		t.Fatal(e)
	}
	hold.exclusionSHA = historicalJSONSHA("synthetic-only-completed-exclusion")
	hold.exclusionAt = time.Now().UTC()
	old := "44444444-4444-4444-8444-444444444444"
	gid := "sg_group_with_under"
	created := time.Date(2026, 10, 17, 10, 0, 0, 0, time.UTC)
	act, seed := historicalJSONSHA("old-act"), historicalJSONSHA("old-seed")
	f := &materializerPageFixture{id: namespaceinventory.Identity{Binding: namespaceinventory.Binding{Tenant: h.Tenant, Database: h.Database, StorageInstance: h.StorageInstance, ReplicaSet: h.ReplicaSet, RedisRunID: h.RedisRunID, RedisDB: h.RedisDB}, MongoProcess: h.MongoProcess, MongoTopology: h.MongoTopology, RedisReplication: strings.Repeat("c", 40), TenantAttested: true}, rows: map[string][]namespaceinventory.MongoRow{}}
	add := func(collection string, v any) {
		raw := rawHistorical(t, v)
		id := raw.Lookup("_id").StringValue()
		f.rows[collection] = append(f.rows[collection], namespaceinventory.MongoRow{Collection: collection, KeySHA: historicalJSONSHA(id), Raw: raw})
	}
	groupClaim := originalFreshClaim{ID: "permanent-group-origin:" + sha([]byte(gid)), Kind: "permanent-new-group-namespace-v1", GroupID: gid, Branch: "group-origin", Epoch: old, ActivationSHA256: act, CommandID: "55555555-5555-4555-8555-555555555555", InputSHA256: historicalJSONSHA("create-input"), CutoverSHA256: historicalJSONSHA("old-cut"), CreatedAt: created}
	add("message_delivery_sequences_v1", groupClaim)
	for _, normal := range []bool{true, false} {
		cid := msgprocessor.GetConversationIDBySessionType(constant.SuperGroupChatType, gid)
		branch := "normal"
		maximum := int64(10)
		if !normal {
			cid = msgprocessor.GetNotificationConversationID(constant.SuperGroupChatType, gid)
			branch = "notification"
			maximum = 4
		}
		claim := groupClaim
		claim.ID = "permanent-cid:" + sha([]byte(cid))
		claim.ConversationID = cid
		claim.Branch = branch
		add("message_delivery_sequences_v1", claim)
		d, e := factualDescribe(&sdkws.MsgData{SendID: gid, RecvID: gid, GroupID: gid, SessionType: constant.SuperGroupChatType, Options: map[string]bool{constant.IsNotNotification: normal}})
		if e != nil {
			t.Fatal(e)
		}
		add("message_delivery_sequences_v1", bson.M{"_id": "permanent-origin-principals:" + sha([]byte(cid)), "kind": "permanent-origin-principals-v1", "conversation_id": cid, "origin_kind": "group", "origin_sha256": d.SHA256, "origin_ids": bson.A{gid}, "claim_sha256": historicalJSONSHA(claim), "created_epoch": old, "created_activation_sha256": act, "seed_evidence_sha256": seed, "created_at": created})
		add("message_delivery_sequences_v1", bson.M{"_id": historicalSequenceKey(old, "counter", cid), "epoch": old, "conversation_id": cid, "maximum": maximum, "revision": int64(1), "seed_maximum": int64(0), "seed_evidence_sha256": seed})
		upper := int64(5)
		if !normal {
			upper = 2
		}
		add("message_delivery_sequences_v1", bson.M{"_id": historicalSequenceKey(old, "membership-window", cid+"\x00owner"), "kind": "membership-window-v1", "epoch": old, "conversation_id": cid, "group_id": gid, "user_id": "owner", "last_command": "66666666-6666-4666-8666-666666666666", "seed_evidence_sha256": seed, "lower": int64(2), "upper": upper, "revision": int64(1), "group_revision": int64(1), "closed": true})
		f.heads = append(f.heads, namespaceinventory.RedisRow{Key: "MAX_SEQ:" + cid, Type: "string", Dump: []byte("synthetic-dump-" + branch), RawHead: strconv.FormatInt(maximum, 10), PTTL: -1})
		if normal {
			add("message_delivery_sequences_v1", bson.M{"_id": "unproved-older-segment", "kind": "namespace-owner-live-history-v1", "epoch": old, "conversation_id": cid, "user_id": "former-owner"})
		}
	}
	add("group", bson.M{"_id": "group-row", "group_id": gid, "status": int32(constant.GroupOk)})
	add("group_member", bson.M{"_id": "owner-row", "group_id": gid, "user_id": "owner", "role_level": int32(constant.GroupOwner)})
	add("group_member", bson.M{"_id": "member-row", "group_id": gid, "user_id": "new-member", "role_level": int32(constant.GroupOrdinaryUsers)})
	for _, mutate := range before {
		mutate(f)
	}
	if prepare != nil {
		if e := prepare(hold, f); e != nil {
			t.Fatal("composition fixture metadata preparation failed", e)
		}
	}
	rawDir := filepath.Join(top, "raw")
	captureRaw := func(ctx context.Context) (*HistoricalMaterializer, error) {
		limits := namespaceinventory.DefaultLimits()
		limits.Rows = 1
		summary, e := namespaceinventory.BeginResumableCapture(ctx, f, f.id.Binding, rawDir, limits, namespaceinventory.ProgressBudget{Steps: 1, SourcePages: 1})
		if e != nil {
			return nil, e
		}
		for n := 0; !summary.RecordedTwoPassComplete; n++ {
			if n > 2048 {
				return nil, errors.New("fixture capture did not converge")
			}
			summary, e = namespaceinventory.ResumeResumableCapture(ctx, f, f.id.Binding, rawDir, namespaceinventory.ProgressBudget{Steps: 4, SourcePages: 2})
			if e != nil {
				return nil, e
			}
		}
		snapshot, e := namespaceinventory.OpenImportSnapshot(ctx, f, rawDir, f.id.Binding)
		if e != nil {
			return nil, e
		}
		derived := filepath.Join(top, "derived")
		m, e := NewHistoricalMaterializer(ctx, hold, snapshot, f, derived, h.SHA256(), historicalJSONSHA("history-policy"), false, factualDescribe)
		if e != nil {
			return nil, e
		}
		return m, nil
	}
	var m *HistoricalMaterializer
	if deferRaw == nil {
		m, e = captureRaw(ctx)
		if e != nil {
			t.Fatal(e)
		}
	}
	trustPath := filepath.Join(top, "trust.json")
	trust, _ := json.Marshal(struct {
		Kind   string   `json:"kind"`
		Target string   `json:"target_header_sha256"`
		Epochs []string `json:"epochs"`
	}{"reviewed-original-history-source-trust-v1", h.SHA256(), []string{}})
	if e = os.WriteFile(trustPath, trust, 0600); e != nil {
		t.Fatal(e)
	}
	connectionPath := filepath.Join(top, "connection.json")
	connection, _ := json.Marshal(namespaceinventory.PrivateConnection{Version: 1, Binding: f.id.Binding, MongoURI: "mongodb://127.0.0.1:1/" + h.Database + "?replicaSet=" + h.ReplicaSet, RedisURL: "redis://127.0.0.1:1/15"})
	if e = os.WriteFile(connectionPath, connection, 0600); e != nil {
		t.Fatal(e)
	}
	key := ed25519.NewKeyFromSeed(make([]byte, ed25519.SeedSize))
	source := &HistoricalCaptureSource{agent: &OwnedAgent{header: h, hold: hold}, header: h, live: f, describe: factualDescribe, options: HistoricalCaptureOptions{ConnectionPath: connectionPath, RawDirectory: rawDir, DerivedDirectory: filepath.Join(top, "derived"), PolicySHA256: historicalJSONSHA("history-policy"), OriginalHistoryTrustPath: trustPath, OriginalHistoryTrustSHA256: sha(trust), CaptureAgent: "synthetic-private-capture", CaptureKeyID: "test-key", CapturePrivateKey: key, CapturePublicKey: key.Public().(ed25519.PublicKey), PageBytes: 1 << 20, PageRows: 1}, producer: materializerMissingTrust{}, verifier: materializerMissingTrust{}, materializer: m, descriptorSHA: historicalJSONSHA("test-source-descriptor")}
	if deferRaw != nil {
		deferRaw(func(captureCtx context.Context) error {
			if source.materializer != nil {
				return ErrOwnedEvidence
			}
			// The caller holds the exact original Acquire result before reaching
			// this block. No snapshot clock or original fence is rewritten.
			materializer, captureErr := captureRaw(captureCtx)
			if captureErr != nil {
				return captureErr
			}
			source.materializer = materializer
			return nil
		})
	}
	fence := namespaceimport.FenceObservation{HeaderSHA256: h.SHA256(), LeaseID: hold.LeaseID(), AdmissionGuardSHA256: hold.RecordSHA256(), ExclusionMode: "held-until-explicit-release"}
	return source, f, fence
}
func finishMaterializedSource(t *testing.T, s *HistoricalCaptureSource, f namespaceimport.FenceObservation) namespaceimport.Inventory {
	t.Helper()
	for n := 0; n < 12000; n++ {
		s.agent.hold.exclusionAt = time.Now().UTC()
		i, e := s.Capture(context.Background(), s.header, f)
		if e == nil {
			return i
		}
		if !errors.Is(e, namespaceimport.ErrPending) {
			t.Fatalf("capture phase error: %v", e)
		}
	}
	t.Fatal("bounded source did not finish")
	return namespaceimport.Inventory{}
}
func pageProofRoot(p namespaceimport.CapturedPage) string {
	sum := historicalJSONSHA([]string{"namespace-import-page-leaf-v1", p.Data.Identity().SHA256})
	for _, v := range p.Proof {
		if v.Left {
			sum = historicalJSONSHA([]string{"namespace-import-page-node-v1", v.SHA256, sum})
		} else {
			sum = historicalJSONSHA([]string{"namespace-import-page-node-v1", sum, v.SHA256})
		}
	}
	return sum
}
func TestHistoricalSourceProducesSignedPagesWithoutInventingMissingHistory(t *testing.T) {
	s, f, fence := materializerSourceFixture(t)
	ctx := context.Background()
	calls := f.calls
	rawBefore := s.materializer.snapshot.Fingerprint()
	t.Run("pristine descriptor crash recovery and changed source options", func(t *testing.T) {
		s.materializer = nil
		if e := s.openDerived(ctx, false); e != nil {
			t.Fatal("pristine exclusive head could not complete descriptor", e)
		}
		original := s.descriptorSHA
		s.materializer = nil
		s.descriptorSHA = historicalJSONSHA("changed-source-options")
		if e := s.openDerived(ctx, false); e == nil {
			t.Fatal("resume accepted changed descriptor")
		}
		s.descriptorSHA = original
		if e := s.openDerived(ctx, false); e != nil {
			t.Fatal("exact descriptor could not resume", e)
		}
	})
	i := finishMaterializedSource(t, s, fence)
	t.Run("canonical bodies original base future and missing-proof disposition", func(t *testing.T) {
		if i.OriginalTrust != "UNTRUSTED" || i.Counts.Origins != 2 || i.Counts.Windows != 2 || i.Counts.Quarantines != 0 || i.ProspectiveCount != 4 || i.OwnerHistorySegmentCount != 0 {
			t.Fatal("known namespace lost, historical gap filled, or current roster became a base", i.Counts)
		}
		unsigned := i
		unsigned.Signature = nil
		b, _ := json.Marshal(unsigned)
		if !ed25519.Verify(s.options.CapturePublicKey, b, i.Signature) {
			t.Fatal("inventory was not signed over exact canonical body")
		}
		previous := ""
		origins, windows, prospectives, closures := 0, 0, 0, 0
		for index := uint64(0); index < i.PageCount; index++ {
			p, e := s.ReadPage(ctx, i, index)
			if e != nil || p.Data.Index != index || p.Data.PreviousSHA256 != previous || pageProofRoot(p) != i.PagesRootSHA256 {
				t.Fatal("page chain/private body/proof differs", e)
			}
			previous = p.Data.Identity().SHA256
			origins += len(p.Data.Origins)
			windows += len(p.Data.OwnerWindows)
			prospectives += len(p.Data.ProspectiveAdmissions)
			closures += len(p.Data.CoverageClosures)
			for _, w := range p.Data.OwnerWindows {
				if !w.Closed || w.OwnerID != "owner" || w.Lower != 2 || w.Upper != 5 && w.Upper != 2 {
					t.Fatal("historical base changed")
				}
			}
			for _, a := range p.Data.ProspectiveAdmissions {
				if a.Admission.Lower != a.Admission.SeedMaximum+1 || a.Admission.ValidateShape() != nil {
					t.Fatal("future initial bound changed")
				}
			}
		}
		if origins != 2 || windows != 2 || prospectives != 4 || closures != 6 {
			t.Fatal("actual logical output count mismatch", origins, windows, prospectives, closures)
		}
		pending, e := s.ReadHistoryProofDispositions(ctx, i, "", 1)
		if e != nil || len(pending.Rows) != 1 || pending.Total != 1 || pending.Rows[0].Reason != "source-trust-unavailable" || pending.Rows[0].OwnerID != "former-owner" || len(pending.Rows[0].Evidence) == 0 {
			t.Fatal("missing old proof silently dropped", e)
		}
		if f.calls != calls || s.materializer.snapshot.Fingerprint() != rawBefore {
			t.Fatal("derived pages recaptured original source or renewed raw cut")
		}
	})
	t.Run("completed workspace reopens at exact page and original capture time", func(t *testing.T) {
		old := s.materializer
		next, e := NewHistoricalMaterializer(ctx, old.hold, old.snapshot, f, s.options.DerivedDirectory, s.header.SHA256(), old.policySHA, true, old.describe)
		if e != nil {
			t.Fatal(e)
		}
		s.materializer = next
		again, e := s.finalInventory(ctx)
		if e != nil || historicalJSONSHA(again) != historicalJSONSHA(i) {
			t.Fatal("reopen changed signed capture", e)
		}
		if _, e = s.ReadPage(ctx, i, i.PageCount-1); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("caller-mutated inventory and foreign index fail", func(t *testing.T) {
		bad := i
		bad.ProspectiveCount++
		if _, e := s.ReadPage(ctx, bad, 0); e == nil {
			t.Fatal("foreign binding accepted")
		}
		if _, e := s.ReadPage(ctx, i, i.PageCount); e == nil {
			t.Fatal("out-of-range page accepted")
		}
	})
	t.Run("reviewed source trust cannot change mid-capture or resume", func(t *testing.T) {
		path := s.options.OriginalHistoryTrustPath
		old, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, append(append([]byte(nil), old...), ' '), 0600); e != nil {
			t.Fatal(e)
		}
		if e = s.Revalidate(ctx, s.header, i); e == nil {
			t.Fatal("old processed rows mixed with changed trust")
		}
		if e = os.WriteFile(path, old, 0600); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("live identity restart and lost exclusion still reject", func(t *testing.T) {
		f.id.MongoTopology++
		if _, e := s.ReadPage(ctx, i, 0); e == nil {
			t.Fatal("restart reused old pages")
		}
		f.id.MongoTopology--
		old := s.agent.hold.exclusionSHA
		s.agent.hold.exclusionSHA = ""
		if e := s.materializer.hold.ValidateExclusion(); e == nil {
			t.Fatal("lost exclusion still admissible")
		}
		s.agent.hold.exclusionSHA = old
	})
}

func TestHistoricalClosurePagesConsumeExactly64DespiteWitnessByteBudget(t *testing.T) {
	s, _, _ := materializerSourceFixture(t)
	ctx := context.Background()
	m := s.materializer
	kind, scope := "closure-byte-budget-fixture", "sg_test"
	set := namespaceinventory.DerivedSetID(kind, scope)
	payload := strings.Repeat("x", 48<<10)
	for start := 0; start < 65; start += 16 {
		st, expected, e := m.workspace.Checkpoint(ctx)
		if e != nil {
			t.Fatal(e)
		}
		changes := []namespaceinventory.DerivedMutation{}
		for at := start; at < min(start+16, 65); at++ {
			body, _ := json.Marshal(struct {
				Index int    `json:"index"`
				Value string `json:"value"`
			}{at, payload})
			changes = append(changes, namespaceinventory.DerivedMutation{SetID: set, Key: historicalJSONSHA(at), Value: body})
		}
		if _, _, e = m.workspace.Commit(ctx, expected, st.Phase, st.Cursor, st.Subcursor, changes); e != nil {
			t.Fatal(e)
		}
	}
	for n := 0; n < 512; n++ {
		s.agent.hold.exclusionAt = time.Now().UTC()
		_, done, e := m.advanceTree(ctx, kind, scope)
		if e != nil {
			t.Fatal(e)
		}
		if done {
			break
		}
		if n == 511 {
			t.Fatal("bounded fixture Merkle did not finish")
		}
	}
	v := materializedBodyState{}
	t.Run("first page consumes64 actual sorted leaves without truncating", func(t *testing.T) {
		page, next, processed, done, e := s.closurePage(ctx, v, "owners", scope, kind, scope)
		if e != nil || done || processed != 64 || next == "" || len(page.CoverageClosures) != 1 || page.CoverageClosures[0].PreviousCursor != "" {
			t.Fatal("valid large witness set stalled", processed, e)
		}
		v.LeafCursor = next
		v.LeafProcessed = processed
	})
	t.Run("continuation uses real Store receipt id cursor and closes last leaf", func(t *testing.T) {
		page, next, processed, done, e := s.closurePage(ctx, v, "owners", scope, kind, scope)
		if e != nil || !done || processed != 65 || next <= v.LeafCursor || page.CoverageClosures[0].PreviousCursor != sourceClosureCursor(s.header, "owners", scope, v.LeafCursor) {
			t.Fatal("closure cursor omitted or repeated an actual leaf", processed, e)
		}
	})
	t.Run("inconsistent checkpoint is not used as source authority", func(t *testing.T) {
		bad := v
		bad.LeafProcessed = 0
		if _, _, _, _, e := s.closurePage(ctx, bad, "owners", scope, kind, scope); e == nil {
			t.Fatal("foreign progress accepted")
		}
	})
}

func TestHistoricalAmbiguousCurrentRosterCannotDiscardProvenBaseOrAdmitFuture(t *testing.T) {
	s, _, fence := materializerSourceFixture(t, func(f *materializerPageFixture) {
		raw := rawHistorical(t, bson.M{"_id": "second-owner-row", "group_id": "sg_group_with_under", "user_id": "owner", "role_level": int32(constant.GroupOrdinaryUsers)})
		f.rows["group_member"] = append(f.rows["group_member"], namespaceinventory.MongoRow{Collection: "group_member", KeySHA: historicalJSONSHA("second-owner-row"), Raw: raw})
	})
	i := finishMaterializedSource(t, s, fence)
	if i.Counts.Origins != 2 || i.Counts.Windows != 2 || i.Counts.Quarantines != 0 || i.ProspectiveCount != 0 {
		t.Fatal("ambiguous roster widened future, lost independently proved history, or globally stalled", i.Counts)
	}
	pending, e := s.ReadHistoryProofDispositions(context.Background(), i, "", 1)
	if e != nil || pending.Total != 1 {
		t.Fatal("old missing-chain audit disappeared", e)
	}
}

func TestHistoricalUnattributedEvidenceIsInspectableAndCannotSilentlyPublish(t *testing.T) {
	s, _, fence := materializerSourceFixture(t, func(f *materializerPageFixture) {
		raw := rawHistorical(t, bson.M{"_id": "unattributed-group-row", "status": int32(constant.GroupOk)})
		f.rows["group"] = append(f.rows["group"], namespaceinventory.MongoRow{Collection: "group", KeySHA: historicalJSONSHA("unattributed-group-row"), Raw: raw})
	})
	rejected := false
	for step := 0; step < 12000; step++ {
		s.agent.hold.exclusionAt = time.Now().UTC()
		_, e := s.Capture(context.Background(), s.header, fence)
		if errors.Is(e, namespaceimport.ErrUntrusted) {
			rejected = true
			break
		}
		if e == nil || !errors.Is(e, namespaceimport.ErrPending) {
			t.Fatal("unattributed source became success or failed before its explicit disposition", e)
		}
	}
	if !rejected {
		t.Fatal("unknown source was never rejected")
	}
	progress, e := s.InspectProgress(context.Background())
	if e != nil || progress.OriginalTrust != "UNTRUSTED" || progress.UnattributedRows != 1 || progress.Checkpoint.Phase == "complete" {
		t.Fatal("unknown source count was silently dropped", e)
	}
	rows, _, _, e := s.ReadUnattributedEvidence(context.Background(), "", 1)
	if e != nil || len(rows) != 1 || rows[0].CID != "" || rows[0].Kind != "unresolved-source-row" || rows[0].Source.ObjectSHA256 == "" {
		t.Fatal("original supplementation evidence unavailable", e)
	}
	if _, e = s.finalInventory(context.Background()); e == nil {
		t.Fatal("unknown CID received a signed inventory")
	}
}
