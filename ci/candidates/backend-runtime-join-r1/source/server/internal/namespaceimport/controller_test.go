package namespaceimport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// These are controller contract tests. They do not attest to a real deployment,
// Mongo/Redis snapshot, Kafka drain or historical owner policy.
type memoryJournal struct {
	state State
	found bool
}

func (j *memoryJournal) Load(context.Context) (State, bool, error) {
	return clone(j.state), j.found, nil
}
func (j *memoryJournal) CompareAndSwap(_ context.Context, old string, next State) error {
	if j.found && digest(j.state) != old || !j.found && old != "" {
		return ErrConflict
	}
	j.state = clone(next)
	j.found = true
	return nil
}

type fixtureControl struct {
	key    ed25519.PrivateKey
	config Config
	lease  string
	mutate func(*FenceObservation)
	calls  int
}

func (f *fixtureControl) answer(r ProbeRequest) (FenceObservation, error) {
	f.calls++
	now := time.Now().UTC()
	past := now.Add(-10 * time.Second)
	o := FenceObservation{HeaderSHA256: r.Header.SHA256(), Nonce: r.Nonce, LeaseID: f.lease, Generation: 1, KeyID: f.config.ProbeKeyID, IssuedAt: now, ValidUntil: now.Add(time.Minute), ExclusionMode: "held-until-explicit-release", AdmissionGuardRecord: "synthetic-control-record", AdmissionGuardSHA256: digest("guard")}
	for _, w := range f.config.Writers {
		o.Joined = append(o.Joined, JoinedWriter{w, "stop-record", "join-record", past})
	}
	for _, c := range f.config.Credentials {
		o.Revoked = append(o.Revoked, RevokedCredential{c, "revoke-record", 1, "revoked"})
	}
	for _, p := range f.config.KafkaPartitions {
		o.Drained = append(o.Drained, DrainedPartition{p, 1, 9, 9, 9, past.Add(time.Second), past.Add(2 * time.Second), "broker-record"})
	}
	if f.mutate != nil {
		f.mutate(&o)
	}
	b, _ := json.Marshal(o.signingValue())
	o.Signature = ed25519.Sign(f.key, b)
	return o, nil
}
func (f *fixtureControl) Acquire(_ context.Context, r ProbeRequest) (FenceObservation, error) {
	return f.answer(r)
}
func (f *fixtureControl) Observe(_ context.Context, r ProbeRequest) (FenceObservation, error) {
	return f.answer(r)
}

type fixtureSource struct {
	key       ed25519.PrivateKey
	config    Config
	inventory Inventory
	page      PageData
	drift     bool
}

func syntheticRefs() []SourceRef {
	return []SourceRef{{digest("raw-object"), digest("exact-record"), "raw-bson-slot", 1}}
}
func syntheticOrigin(kind, cid, gid string, ids []string) OriginRecord {
	return OriginRecord{NamespaceOriginDescriptor{cid, kind, ids, gid, digest([]any{kind, ids})}, Head{"present", 91, syntheticRefs()}, Head{"present", 95, syntheticRefs()}, syntheticRefs(), digest("seed")}
}
func (f *fixtureSource) Capture(_ context.Context, h Header, fence FenceObservation) (Inventory, error) {
	i := Inventory{HeaderSHA256: h.SHA256(), LeaseID: fence.LeaseID, CaptureID: "synthetic-capture", CaptureAgent: f.config.CaptureAgent, CapturedAt: time.Now().UTC(), OriginalTrust: "UNTRUSTED", MongoRootSHA256: digest("mongo"), RedisRootSHA256: digest("redis"), RawRootSHA256: digest("raw-root"), PageCount: 1, Counts: f.page.Counts(), CoverageCount: uint64(len(f.page.Origins)), KeyID: f.config.CaptureKeyID}
	f.page.HeaderSHA256 = h.SHA256()
	f.page.InventoryBindingSHA256 = i.BindingSHA256()
	f.page.RawSourceRootSHA256 = i.RawRootSHA256
	installSyntheticCoverage(&i, &f.page)
	i.PagesRootSHA256 = digest([]string{"namespace-import-page-leaf-v1", digest(f.page)})
	b, _ := json.Marshal(i.signingValue())
	i.Signature = ed25519.Sign(f.key, b)
	f.inventory = i
	return clone(i), nil
}
func (f *fixtureSource) ReadPage(_ context.Context, _ Inventory, index uint64) (CapturedPage, error) {
	if index != 0 {
		return CapturedPage{}, ErrInvalid
	}
	return CapturedPage{Data: clone(f.page)}, nil
}
func (f *fixtureSource) Revalidate(context.Context, Header, Inventory) error {
	if f.drift {
		return ErrConflict
	}
	return nil
}

type fixtureOrigins struct {
	reject bool
	seen   []string
}

func (f *fixtureOrigins) ValidateOrigin(_ context.Context, _ Header, _ Inventory, r OriginRecord) error {
	if f.reject {
		return ErrUntrusted
	}
	f.seen = append(f.seen, r.Origin.CID)
	return nil
}
func (f *fixtureOrigins) ValidateWindow(_ context.Context, _ Header, _ Inventory, w OwnerWindow) error {
	if f.reject {
		return ErrUntrusted
	}
	f.seen = append(f.seen, w.CID+"/"+w.OwnerID)
	return nil
}
func (f *fixtureOrigins) ValidateQuarantine(_ context.Context, _ Header, _ Inventory, q Quarantine) error {
	if f.reject {
		return ErrUntrusted
	}
	f.seen = append(f.seen, q.CID)
	return nil
}

type fixtureWriter struct {
	header           Header
	pages            map[string]PageReceipt
	cutover          CutoverReceipt
	calls, publishes int
	lostACK          bool
	unknownAbsent    bool
	lastPermit       *Permit
	lastPage         PageData
}

func (f *fixtureWriter) CheckTarget(context.Context, Header) error { return nil }
func (f *fixtureWriter) ApplyPage(_ context.Context, p *Permit, v VerifiedPage) (PageReceipt, error) {
	data := v.Data()
	if err := p.ValidatePage(f.header, v); err != nil {
		return PageReceipt{}, err
	}
	f.calls++
	f.lastPermit = p
	f.lastPage = data
	r := PageReceipt{HeaderSHA256: data.HeaderSHA256, InventorySHA256: v.InventorySHA256(), Page: data.Identity(), Cumulative: data.Counts()}
	r.ReceiptSHA256 = ReceiptDigest(r)
	if !f.unknownAbsent {
		f.pages[r.Page.SHA256] = r
	}
	if f.lostACK || f.unknownAbsent {
		return PageReceipt{}, ErrCommitUnconfirmed
	}
	return r, nil
}
func (f *fixtureWriter) InspectPage(_ context.Context, _ Header, p PageIdentity) (PageInspection, error) {
	r, ok := f.pages[p.SHA256]
	return PageInspection{ok, r}, nil
}
func (f *fixtureWriter) PublishCutover(_ context.Context, p *Permit, v VerifiedManifest) (CutoverReceipt, error) {
	if err := p.ValidateManifest(f.header, v); err != nil {
		return CutoverReceipt{}, err
	}
	f.publishes++
	r := CutoverReceipt{HeaderSHA256: v.data.HeaderSHA256, ManifestSHA256: v.SHA256()}
	r.ReceiptSHA256 = CutoverReceiptDigest(r)
	f.cutover = r
	return r, nil
}
func (f *fixtureWriter) InspectCutover(context.Context, Header) (CutoverInspection, error) {
	return CutoverInspection{f.cutover.ReceiptSHA256 != "", f.cutover}, nil
}

type controllerFixture struct {
	controller *Controller
	config     Config
	source     *fixtureSource
	control    *fixtureControl
	writer     *fixtureWriter
	origins    *fixtureOrigins
	journal    *memoryJournal
}

func newFixture(t *testing.T) controllerFixture {
	t.Helper()
	pk, sk, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cp, cs, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := Config{Header: Header{Tenant: "synthetic-tenant", Database: "synthetic-db", StorageInstance: "storage-uuid", ReplicaSet: "synthetic-rs", MongoProcess: "process-id", MongoTopology: 1, RedisRunID: "redis-run-id", RedisDB: 15, KafkaClusterID: "kafka-cluster", Epoch: "11111111-1111-4111-8111-111111111111", ActivationSHA256: digest("activation"), MigrationID: "22222222-2222-4222-8222-222222222222"}, Writers: []WriterIdentity{{"synthetic-msgtransfer", "incarnation-1", digest("binary"), digest("source")}}, Credentials: []CredentialIdentity{{"legacy-writer", "credential-1", "version-1"}}, KafkaPartitions: []KafkaPartitionIdentity{{"producer-to-allocation", "synthetic-redis", "synthetic-group", 0}, {"allocation-to-persistence", "synthetic-mongo", "synthetic-group", 0}}, ProbeKeyID: "probe-key", ProbePublicKey: pk, CaptureKeyID: "capture-key", CapturePublicKey: cp, CaptureAgent: "synthetic-capture-agent", OperationTimeout: 10 * time.Second, PermitLifetime: time.Second, PageBytes: 64 * 1024, PageRows: 16}
	c.Header.DeploymentPolicySHA256 = DeploymentPolicySHA256(c)
	s := &fixtureSource{key: cs, config: c, page: PageData{Origins: []OriginRecord{syntheticOrigin("single", "si_a_b", "", []string{"a", "b"})}}}
	p := &fixtureControl{key: sk, config: c, lease: "synthetic-held-lease"}
	w := &fixtureWriter{header: c.Header, pages: map[string]PageReceipt{}}
	j := &memoryJournal{}
	o := &fixtureOrigins{}
	controller, err := NewController(c, w, s, o, p, j)
	if err != nil {
		t.Fatal(err)
	}
	return controllerFixture{controller, c, s, p, w, o, j}
}
func beginCaptured(t *testing.T, f controllerFixture) {
	t.Helper()
	if _, err := f.controller.Begin(context.Background()); err != nil {
		t.Fatal(err)
	}
	s, err := f.controller.Step(context.Background())
	if err != nil || s.Phase != "captured" {
		t.Fatalf("capture: %v phase=%s", err, s.Phase)
	}
}

func TestImportRejectsInventedOrIncompleteControlEvidence(t *testing.T) {
	for _, name := range []string{"wrong-nonce", "not-joined", "one-plane", "moving-end", "credential-not-revoked", "wrong-target", "expired", "automatic-release"} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			f.control.mutate = func(o *FenceObservation) {
				switch name {
				case "wrong-nonce":
					o.Nonce = "other"
				case "not-joined":
					o.Joined = nil
				case "one-plane":
					o.Drained = o.Drained[:1]
				case "moving-end":
					o.Drained[0].CurrentEnd++
				case "credential-not-revoked":
					o.Revoked[0].State = "active"
				case "wrong-target":
					o.HeaderSHA256 = digest("other-db")
				case "expired":
					o.ValidUntil = time.Now().Add(-time.Second)
				case "automatic-release":
					o.ExclusionMode = "expires-and-reopens"
				}
			}
			if _, err := f.controller.Begin(context.Background()); !errors.Is(err, ErrUntrusted) {
				t.Fatalf("wanted untrusted: %v", err)
			}
			if f.writer.calls != 0 || f.journal.found {
				t.Fatal("unauthenticated state or write admitted")
			}
		})
	}
}
func TestImportLostCommitACKUsesExactInspectWithoutReplay(t *testing.T) {
	f := newFixture(t)
	beginCaptured(t, f)
	f.writer.lostACK = true
	s, err := f.controller.Step(context.Background())
	if !errors.Is(err, ErrCommitUnconfirmed) || s.Phase != "pending-page" || f.writer.calls != 1 {
		t.Fatalf("lost ACK not retained: %v", err)
	}
	if f.writer.lastPermit.active.Load() {
		t.Fatal("returned permit still active")
	}
	s, err = f.controller.Step(context.Background())
	if err != nil || s.NextPage != 1 || f.writer.calls != 1 {
		t.Fatalf("inspect replayed business: %v", err)
	}
	s, err = f.controller.Step(context.Background())
	if err != nil || s.Phase != "published" || f.writer.publishes != 1 {
		t.Fatalf("publish: %v", err)
	}
	if _, err = f.controller.Step(context.Background()); !errors.Is(err, ErrFinished) || f.writer.publishes != 1 {
		t.Fatal("published cutover was rewritten")
	}
}
func TestImportUnknownAbsentReceiptNeverReplaysOrRecaptures(t *testing.T) {
	f := newFixture(t)
	beginCaptured(t, f)
	f.writer.unknownAbsent = true
	s, err := f.controller.Step(context.Background())
	if !errors.Is(err, ErrCommitUnconfirmed) {
		t.Fatal(err)
	}
	capture := s.Inventory.SHA256()
	for i := 0; i < 2; i++ {
		s, err = f.controller.Step(context.Background())
		if !errors.Is(err, ErrCommitUnconfirmed) || s.Inventory.SHA256() != capture || f.writer.calls != 1 {
			t.Fatal("absence minted a retry or new cut")
		}
	}
	n := NonApplication{HeaderSHA256: s.Header.SHA256(), LeaseID: s.Fence.LeaseID, AttemptID: s.PendingAttemptID, ValueSHA256: s.PendingPage.SHA256, OriginalSession: "session", Outcome: "majority-known-aborted-session-ended", DatabaseOutcomeRecord: "actual-proof-required", SessionTerminatedAt: time.Now(), KeyID: f.config.ProbeKeyID}
	if _, err = f.controller.ConfirmNotApplied(context.Background(), n); !errors.Is(err, ErrUntrusted) {
		t.Fatal("unsigned operator JSON cleared pending")
	}
}
func TestImportExplicitNotAppliedRequiresExactOriginalAttemptProof(t *testing.T) {
	f := newFixture(t)
	beginCaptured(t, f)
	f.writer.unknownAbsent = true
	s, err := f.controller.Step(context.Background())
	if !errors.Is(err, ErrCommitUnconfirmed) {
		t.Fatal(err)
	}
	n := NonApplication{HeaderSHA256: s.Header.SHA256(), LeaseID: s.Fence.LeaseID, AttemptID: s.PendingAttemptID, ValueSHA256: s.PendingPage.SHA256, OriginalSession: "ended-original-session", Outcome: "majority-known-aborted-session-ended", DatabaseOutcomeRecord: "synthetic-controlled-DB-outcome-record", SessionTerminatedAt: time.Now().UTC(), KeyID: f.config.ProbeKeyID}
	sign := func(v NonApplication) NonApplication {
		b, _ := json.Marshal(v.signingValue())
		v.Signature = ed25519.Sign(f.control.key, b)
		return v
	}
	wrong := clone(n)
	wrong.AttemptID = "a-different-attempt"
	if _, err = f.controller.ConfirmNotApplied(context.Background(), sign(wrong)); !errors.Is(err, ErrUntrusted) {
		t.Fatal("other attempt cleared unknown")
	}
	n = sign(n)
	s, err = f.controller.ConfirmNotApplied(context.Background(), n)
	if err != nil || s.Phase != "captured" || s.LastOutcome == nil || s.LastOutcomeSHA256 != digest(n) || len(s.LastOutcome.Signature) != ed25519.SignatureSize || f.writer.calls != 1 {
		t.Fatal("confirmed non-application did not preserve audit proof")
	}
	f.writer.unknownAbsent = false
	s, err = f.controller.Step(context.Background())
	if err != nil || s.NextPage != 1 || f.writer.calls != 2 {
		t.Fatal("explicit proven non-application could not resume exact page")
	}
}
func TestImportOriginEvidenceFailureNeverBecomesAWriterCapability(t *testing.T) {
	f := newFixture(t)
	beginCaptured(t, f)
	f.origins.reject = true
	if _, err := f.controller.Step(context.Background()); !errors.Is(err, ErrUntrusted) || f.writer.calls != 0 || f.writer.publishes != 0 {
		t.Fatal("unverified raw origin admitted")
	}
	s, err := f.controller.Inspect(context.Background())
	if err != nil || s.Phase != "captured" || s.NextPage != 0 {
		t.Fatal("rejection changed imported state")
	}
}
func TestImportPageCopiesAndSourceDriftCannotMintPermit(t *testing.T) {
	f := newFixture(t)
	beginCaptured(t, f)
	s, err := f.controller.Inspect(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.controller.verifyPage(context.Background(), s, CapturedPage{Data: f.source.page})
	if err != nil {
		t.Fatal(err)
	}
	copy := v.Data()
	copy.Origins[0].Origin.OriginIDs[0] = "foreign"
	if v.Data().Origins[0].Origin.OriginIDs[0] != "a" {
		t.Fatal("verified page aliased caller memory")
	}
	f.source.drift = true
	if _, err = f.controller.Step(context.Background()); !errors.Is(err, ErrConflict) || f.writer.calls != 0 {
		t.Fatal("source drift admitted")
	}
}
func TestImportKeepsSeparateNotificationBoundsAndQuarantinesMissingHistory(t *testing.T) {
	f := newFixture(t)
	sg := syntheticOrigin("group", "sg_group_under_score", "group_under_score", []string{"group_under_score"})
	n := syntheticOrigin("group", "n_group_under_score", "group_under_score", []string{"group_under_score"})
	n.MongoHead.Value = 7
	n.RedisHead.Value = 8
	f.source.page = PageData{Origins: []OriginRecord{sg, n}, OwnerWindows: []OwnerWindow{{"sg_group_under_score", sg.Origin.SHA256, "group_under_score", "removed-owner", 0, 91, true, syntheticRefs()}, {"n_group_under_score", n.Origin.SHA256, "group_under_score", "removed-owner", 0, 7, true, syntheticRefs()}}, Quarantines: []Quarantine{{"n_ambiguous_erased", "historical-owner-boundary-missing", syntheticRefs()}}}
	beginCaptured(t, f)
	s, err := f.controller.Step(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.Cumulative != (Counts{2, 2, 1}) || f.writer.lastPage.OwnerWindows[1].Upper != 7 || f.writer.lastPage.OwnerWindows[0].Upper != 91 || len(f.writer.lastPage.Quarantines) != 1 {
		t.Fatal("domain or quarantine lost")
	}
	if _, err = f.controller.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (f *fixtureOrigins) ValidateCoverage(context.Context, Header, Inventory, OriginRecord, OriginCoverage) error {
	if f.reject {
		return ErrUntrusted
	}
	return nil
}
func (f *fixtureOrigins) ValidateEffectiveBoundary(context.Context, Header, Inventory, EffectiveOwnerBoundary, OriginCoverage) error {
	if f.reject {
		return ErrUntrusted
	}
	return nil
}
