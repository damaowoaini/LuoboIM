package namespaceimport

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"time"
)

// State contains no message bodies or credentials. The file implementation is
// private and crash durable. It is a recovery journal, NOT a storage authority.
type State struct {
	Revision                    uint64           `json:"revision"`
	Header                      Header           `json:"header"`
	Phase                       string           `json:"phase"`
	Fence                       FenceObservation `json:"fence"`
	CaptureFence                FenceObservation `json:"capture_fence"`
	Inventory                   Inventory        `json:"inventory"`
	NextPage                    uint64           `json:"next_page"`
	LastPageSHA256              string           `json:"last_page_sha256"`
	Cumulative                  Counts           `json:"cumulative"`
	AppliedProspectives         uint64           `json:"applied_prospectives,omitempty"`
	AppliedOwnerHistorySegments uint64           `json:"applied_owner_history_segments,omitempty"`
	PendingPage                 PageIdentity     `json:"pending_page"`
	PendingManifestSHA256       string           `json:"pending_manifest_sha256"`
	PendingAttemptID            string           `json:"pending_attempt_id"`
	PendingSince                time.Time        `json:"pending_since"`
	LastOutcomeSHA256           string           `json:"last_outcome_sha256"`
	LastOutcome                 *NonApplication  `json:"last_outcome,omitempty"`
	Cutover                     CutoverReceipt   `json:"cutover"`
}
type Journal interface {
	Load(context.Context) (State, bool, error)
	// expected is the complete previous State hash, or empty for first create.
	// Persist before business dispatch; ambiguity/failure must not erase pending.
	CompareAndSwap(context.Context, string, State) error
}
type Controller struct {
	mu      sync.Mutex
	config  Config
	writer  Writer
	source  InventorySource
	origins OriginValidator
	control ControlPlane
	journal Journal
}

func NewController(config Config, writer Writer, source InventorySource, origins OriginValidator, control ControlPlane, journal Journal) (*Controller, error) {
	if _, ok := origins.(CoverageValidator); !ok {
		return nil, ErrCoverageIncomplete
	}
	config = clone(config)
	if config.validate() != nil || portNil(writer) || portNil(source) || portNil(origins) || portNil(control) || portNil(journal) {
		return nil, ErrInvalid
	}
	return &Controller{config: config, writer: writer, source: source, origins: origins, control: control, journal: journal}, nil
}
func (c *Controller) operation(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, c.config.OperationTimeout)
}
func (c *Controller) load(ctx context.Context) (State, bool, error) {
	s, found, err := c.journal.Load(ctx)
	if err != nil || !found {
		return s, found, err
	}
	if s.Header != c.config.Header || s.Revision == 0 {
		return State{}, false, ErrConflict
	}
	switch s.Phase {
	case "fenced", "captured", "pending-page", "pending-cutover", "published":
	default:
		return State{}, false, ErrConflict
	}
	return clone(s), true, nil
}
func (c *Controller) save(ctx context.Context, old State, next State, first bool) error {
	expected := ""
	if !first {
		expected = digest(old)
	}
	if old.Revision == ^uint64(0) {
		return ErrInvalid
	}
	next.Revision = old.Revision + 1
	return c.journal.CompareAndSwap(ctx, expected, clone(next))
}
func (c *Controller) observe(ctx context.Context, s State) (FenceObservation, error) {
	n, err := nonce()
	if err != nil {
		return FenceObservation{}, err
	}
	r := ProbeRequest{Header: c.config.Header, Nonce: n, LeaseID: s.Fence.LeaseID, MinimumGeneration: s.Fence.Generation}
	f, err := c.control.Observe(ctx, r)
	if err != nil {
		return FenceObservation{}, err
	}
	f = clone(f)
	if err = c.config.verifyFence(f, r, time.Now()); err != nil {
		return FenceObservation{}, err
	}
	// The deployment/credential hold must not disappear between pages, on timeout
	// or controller restart. No Controller method automatically releases it.
	return f, nil
}

// Begin performs actual target check -> controlled stop/drain/exclusion ->
// durable fenced state -> fresh capture under that same hold. It does not write
// Mongo namespace state. Reopening an existing journal never acquires a new cut.
func (c *Controller) Begin(ctx context.Context) (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := c.operation(ctx)
	defer cancel()
	s, found, err := c.load(ctx)
	if err != nil {
		return State{}, err
	}
	if found {
		return s, nil
	}
	if err = c.writer.CheckTarget(ctx, c.config.Header); err != nil {
		return State{}, err
	}
	n, err := nonce()
	if err != nil {
		return State{}, err
	}
	request := ProbeRequest{Header: c.config.Header, Nonce: n}
	f, err := c.control.Acquire(ctx, request)
	if err != nil {
		return State{}, err
	}
	f = clone(f)
	if err = c.config.verifyFence(f, request, time.Now()); err != nil {
		return State{}, err
	}
	next := State{Header: c.config.Header, Phase: "fenced", Fence: f}
	if err = c.save(ctx, State{}, next, true); err != nil {
		return State{}, err
	}
	return c.loadExisting(ctx)
}
func (c *Controller) loadExisting(ctx context.Context) (State, error) {
	s, found, err := c.load(ctx)
	if err != nil {
		return State{}, err
	}
	if !found {
		return State{}, ErrConflict
	}
	return s, nil
}

// Step advances at most one capture/page/publish operation. It has no unbounded
// loop over history and no network calls inside Writer's transaction retries.
// Captured inventory, page IDs and the migration identity never change on resume.
func (c *Controller) Step(ctx context.Context) (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := c.operation(ctx)
	defer cancel()
	s, err := c.loadExisting(ctx)
	if err != nil {
		return State{}, err
	}
	if s.Phase == "published" {
		return s, ErrFinished
	}
	if err = c.writer.CheckTarget(ctx, c.config.Header); err != nil {
		return s, err
	}
	f, err := c.observe(ctx, s)
	if err != nil {
		return s, err
	}
	if s.Phase == "fenced" {
		i, err := c.source.Capture(ctx, c.config.Header, clone(f))
		if err != nil {
			return s, err
		}
		i = clone(i)
		if err = c.config.verifyInventory(i, s.Fence); err != nil {
			return s, err
		}
		if err = c.source.Revalidate(ctx, c.config.Header, clone(i)); err != nil {
			return s, err
		}
		next := clone(s)
		next.Inventory = i
		next.Fence = f
		next.CaptureFence = s.Fence
		next.Phase = "captured"
		if err = c.save(ctx, s, next, false); err != nil {
			return s, err
		}
		return c.loadExisting(ctx)
	}
	if err = c.config.verifyInventory(s.Inventory, s.CaptureFence); err != nil {
		return s, err
	}
	if err = c.source.Revalidate(ctx, c.config.Header, clone(s.Inventory)); err != nil {
		return s, err
	}
	if s.Phase == "pending-page" {
		return c.inspectPage(ctx, s)
	}
	if s.Phase == "pending-cutover" {
		return c.inspectCutover(ctx, s)
	}
	// A journal is not proof that the previous page really committed. Recheck its
	// exact durable receipt before consuming local counters or dispatching a child.
	if s.NextPage > 0 {
		p, err := c.source.ReadPage(ctx, clone(s.Inventory), s.NextPage-1)
		if err != nil {
			return s, err
		}
		id := p.Data.Identity()
		if id.SHA256 != s.LastPageSHA256 {
			return s, ErrConflict
		}
		got, err := c.writer.InspectPage(ctx, c.config.Header, id)
		if err != nil {
			return s, err
		}
		if !got.Found || !validReceipt(got.Receipt, c.config.Header.SHA256(), s.Inventory.SHA256(), id, s.Cumulative) || got.Receipt.CumulativeProspectives != s.AppliedProspectives || got.Receipt.CumulativeOwnerHistorySegments != s.AppliedOwnerHistorySegments {
			return s, ErrCommitUnconfirmed
		}
	}
	if s.NextPage == s.Inventory.PageCount {
		return c.publish(ctx, s, f)
	}
	if s.NextPage > s.Inventory.PageCount {
		return s, ErrConflict
	}
	captured, err := c.source.ReadPage(ctx, clone(s.Inventory), s.NextPage)
	if err != nil {
		return s, err
	}
	v, err := c.verifyPage(ctx, s, captured)
	if err != nil {
		return s, err
	}
	// Evidence validation may take time; recheck the frozen source then obtain a
	// fresh authenticated held-fence observation just before granting the permit.
	if err = c.source.Revalidate(ctx, c.config.Header, clone(s.Inventory)); err != nil {
		return s, err
	}
	f, err = c.observe(ctx, s)
	if err != nil {
		return s, err
	}
	attempt, err := nonce()
	if err != nil {
		return s, err
	}
	next := clone(s)
	next.Phase = "pending-page"
	next.PendingPage = v.data.Identity()
	next.PendingAttemptID = attempt
	next.PendingSince = time.Now().UTC()
	next.Fence = f
	if err = c.save(ctx, s, next, false); err != nil {
		return s, err
	}
	next, err = c.loadExisting(ctx)
	if err != nil {
		return s, err
	}
	p := newPermit(c.config, f, s.Inventory.SHA256(), v.SHA256())
	p.attemptID = attempt
	receipt, writeErr := c.applyPageOnce(ctx, p, v)
	if writeErr != nil {
		return next, errors.Join(ErrCommitUnconfirmed, writeErr)
	}
	return c.acceptPage(ctx, next, receipt)
}
func (c *Controller) applyPageOnce(ctx context.Context, p *Permit, v VerifiedPage) (PageReceipt, error) {
	defer p.active.Store(false)
	return c.writer.ApplyPage(ctx, p, v)
}
func (c *Controller) publishOnce(ctx context.Context, p *Permit, v VerifiedManifest) (CutoverReceipt, error) {
	defer p.active.Store(false)
	return c.writer.PublishCutover(ctx, p, v)
}
func validReceipt(r PageReceipt, h, inventory string, id PageIdentity, counts Counts) bool {
	sha := r.ReceiptSHA256
	r.ReceiptSHA256 = ""
	return r.HeaderSHA256 == h && r.InventorySHA256 == inventory && r.Page == id && r.Cumulative == counts && validSHA(sha) && digest(r) == sha
}

// ReceiptDigest supplies the exact neutral canonical shape for Store. A digest
// is integrity only; receipt existence is read through the actual Writer.
func ReceiptDigest(r PageReceipt) string           { r.ReceiptSHA256 = ""; return digest(r) }
func CutoverReceiptDigest(r CutoverReceipt) string { r.ReceiptSHA256 = ""; return digest(r) }
func (c *Controller) acceptPage(ctx context.Context, s State, receipt PageReceipt) (State, error) {
	p, err := c.source.ReadPage(ctx, clone(s.Inventory), s.PendingPage.Index)
	if err != nil {
		return s, err
	}
	v, err := c.verifyPage(ctx, s, p)
	if err != nil {
		return s, err
	}
	if v.data.Identity() != s.PendingPage {
		return s, ErrConflict
	}
	counts, err := s.Cumulative.add(v.data.Counts())
	if err != nil {
		return s, err
	}
	prospectives := uint64(len(v.data.ProspectiveAdmissions))
	if ^uint64(0)-s.AppliedProspectives < prospectives {
		return s, ErrInvalid
	}
	prospectives += s.AppliedProspectives
	historySegments := uint64(len(v.data.OwnerHistorySegments))
	if ^uint64(0)-s.AppliedOwnerHistorySegments < historySegments {
		return s, ErrInvalid
	}
	historySegments += s.AppliedOwnerHistorySegments
	if !validReceipt(receipt, c.config.Header.SHA256(), s.Inventory.SHA256(), s.PendingPage, counts) || receipt.CumulativeProspectives != prospectives || receipt.CumulativeOwnerHistorySegments != historySegments {
		return s, ErrConflict
	}
	next := clone(s)
	next.Phase = "captured"
	next.NextPage++
	next.LastPageSHA256 = s.PendingPage.SHA256
	next.Cumulative = counts
	next.AppliedProspectives = prospectives
	next.AppliedOwnerHistorySegments = historySegments
	next.PendingPage = PageIdentity{}
	next.PendingAttemptID = ""
	next.PendingSince = time.Time{}
	if err = c.save(ctx, s, next, false); err != nil {
		return s, err
	}
	return c.loadExisting(ctx)
}
func (c *Controller) inspectPage(ctx context.Context, s State) (State, error) {
	r, err := c.writer.InspectPage(ctx, c.config.Header, s.PendingPage)
	if err != nil {
		return s, err
	}
	if !r.Found {
		return s, ErrCommitUnconfirmed
	}
	return c.acceptPage(ctx, s, r.Receipt)
}
func manifestFor(s State) ManifestData {
	return ManifestData{HeaderSHA256: s.Header.SHA256(), InventorySHA256: s.Inventory.SHA256(), FinalPageSHA256: s.LastPageSHA256, PageCount: s.NextPage, Counts: s.Cumulative, CoverageSetRootSHA256: s.Inventory.CoverageSetRootSHA256, CoverageCount: s.Inventory.CoverageCount, ProspectiveSetRootSHA256: s.Inventory.ProspectiveSetRootSHA256, ProspectiveCount: s.AppliedProspectives, OwnerHistorySegmentSetRootSHA256: s.Inventory.OwnerHistorySegmentSetRootSHA256, OwnerHistorySegmentCount: s.AppliedOwnerHistorySegments}
}
func (c *Controller) publish(ctx context.Context, s State, f FenceObservation) (State, error) {
	if s.AppliedOwnerHistorySegments != s.Inventory.OwnerHistorySegmentCount || s.Inventory.OwnerHistorySegmentCount > 0 && !validSHA(s.Inventory.OwnerHistorySegmentSetRootSHA256) || s.AppliedProspectives != s.Inventory.ProspectiveCount || s.Inventory.ProspectiveCount > 0 && !validSHA(s.Inventory.ProspectiveSetRootSHA256) || s.Cumulative != s.Inventory.Counts || s.Inventory.CoverageCount != s.Cumulative.Origins || !validSHA(s.Inventory.CoverageSetRootSHA256) {
		return s, ErrConflict
	}
	m := manifestFor(s)
	v := VerifiedManifest{data: m, sha256: digest(m), inventory: clone(s.Inventory)}
	attempt, err := nonce()
	if err != nil {
		return s, err
	}
	next := clone(s)
	next.Phase = "pending-cutover"
	next.PendingManifestSHA256 = v.sha256
	next.PendingAttemptID = attempt
	next.PendingSince = time.Now().UTC()
	next.Fence = f
	if err = c.save(ctx, s, next, false); err != nil {
		return s, err
	}
	next, err = c.loadExisting(ctx)
	if err != nil {
		return s, err
	}
	p := newPermit(c.config, f, s.Inventory.SHA256(), v.sha256)
	p.attemptID = attempt
	r, writeErr := c.publishOnce(ctx, p, v)
	if writeErr != nil {
		return next, errors.Join(ErrCommitUnconfirmed, writeErr)
	}
	return c.acceptCutover(ctx, next, r)
}
func (c *Controller) acceptCutover(ctx context.Context, s State, r CutoverReceipt) (State, error) {
	if r.HeaderSHA256 != c.config.Header.SHA256() || r.ManifestSHA256 != s.PendingManifestSHA256 || !validSHA(r.ReceiptSHA256) || CutoverReceiptDigest(r) != r.ReceiptSHA256 {
		return s, ErrConflict
	}
	next := clone(s)
	next.Phase = "published"
	next.Cutover = r
	next.PendingManifestSHA256 = ""
	next.PendingAttemptID = ""
	next.PendingSince = time.Time{}
	if err := c.save(ctx, s, next, false); err != nil {
		return s, err
	}
	return c.loadExisting(ctx)
}
func (c *Controller) inspectCutover(ctx context.Context, s State) (State, error) {
	r, err := c.writer.InspectCutover(ctx, c.config.Header)
	if err != nil {
		return s, err
	}
	if !r.Found {
		return s, ErrCommitUnconfirmed
	}
	return c.acceptCutover(ctx, s, r.Receipt)
}
func (c *Controller) Inspect(ctx context.Context) (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := c.operation(ctx)
	defer cancel()
	return c.loadExisting(ctx)
}

// NonApplication is an explicit controlled DB-outcome confirmation, not an
// "absent receipt" inference. The original session must be stopped and proven
// unable to commit later; signature authority is the pinned deployment agent.
type NonApplication struct {
	HeaderSHA256          string    `json:"header_sha256"`
	LeaseID               string    `json:"lease_id"`
	AttemptID             string    `json:"attempt_id"`
	ValueSHA256           string    `json:"value_sha256"`
	OriginalSession       string    `json:"original_session"`
	Outcome               string    `json:"outcome"`
	DatabaseOutcomeRecord string    `json:"database_outcome_record"`
	SessionTerminatedAt   time.Time `json:"session_terminated_at"`
	KeyID                 string    `json:"key_id"`
	Signature             []byte    `json:"signature"`
}

func (n NonApplication) signingValue() NonApplication { n.Signature = nil; return n }
func (c *Controller) ConfirmNotApplied(ctx context.Context, n NonApplication) (State, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := c.operation(ctx)
	defer cancel()
	s, err := c.loadExisting(ctx)
	if err != nil {
		return State{}, err
	}
	if s.Phase != "pending-page" && s.Phase != "pending-cutover" {
		return s, ErrPending
	}
	value := s.PendingPage.SHA256
	if s.Phase == "pending-cutover" {
		value = s.PendingManifestSHA256
	}
	if n.HeaderSHA256 != s.Header.SHA256() || n.LeaseID != s.Fence.LeaseID || n.AttemptID != s.PendingAttemptID || n.ValueSHA256 != value || !identity(n.OriginalSession) || n.Outcome != "majority-known-aborted-session-ended" || !identity(n.DatabaseOutcomeRecord) || n.SessionTerminatedAt.Before(s.PendingSince) || n.SessionTerminatedAt.After(time.Now()) || n.KeyID != c.config.ProbeKeyID || !verifySignature(ed25519.PublicKey(c.config.ProbePublicKey), n.signingValue(), n.Signature) {
		return s, ErrUntrusted
	}
	if _, err = c.observe(ctx, s); err != nil {
		return s, err
	}
	if s.Phase == "pending-page" {
		r, err := c.writer.InspectPage(ctx, s.Header, s.PendingPage)
		if err != nil {
			return s, err
		}
		if r.Found {
			return s, ErrConflict
		}
	} else {
		r, err := c.writer.InspectCutover(ctx, s.Header)
		if err != nil {
			return s, err
		}
		if r.Found {
			return s, ErrConflict
		}
	}
	next := clone(s)
	next.Phase = "captured"
	next.LastOutcomeSHA256 = digest(n)
	outcome := clone(n)
	next.LastOutcome = &outcome
	next.PendingPage = PageIdentity{}
	next.PendingManifestSHA256 = ""
	next.PendingAttemptID = ""
	next.PendingSince = time.Time{}
	if err = c.save(ctx, s, next, false); err != nil {
		return s, err
	}
	return c.loadExisting(ctx)
}
