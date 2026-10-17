//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

type HistoricalCaptureOptions struct {
	ConnectionPath             string
	RawDirectory               string
	DerivedDirectory           string
	PolicySHA256               string
	OriginalHistoryTrustPath   string
	OriginalHistoryTrustSHA256 string
	CaptureAgent               string
	CaptureKeyID               string
	CapturePublicKey           ed25519.PublicKey
	CapturePrivateKey          ed25519.PrivateKey
	PageBytes                  int
	PageRows                   int
	ReviewedLegacyAllocation   *LegacyAllocationReviewSpec
}

// This real source is private operator composition. It performs only the owned
// hold/capture/derived-files workflow; it never writes business rows, starts a
// producer, grants history, releases the fence, or changes readiness. Keys and
// reviewed policy are source-owned private bootstrap inputs, never event JSON.
type HistoricalCaptureSource struct {
	mu             sync.Mutex
	agent          *OwnedAgent
	header         namespaceimport.Header
	live           namespaceinventory.PageSource
	describe       DescribeHistoricalNamespace
	options        HistoricalCaptureOptions
	producer       namespaceimport.OriginalHistorySegmentProducer
	verifier       namespaceimport.OriginalHistorySegmentVerifier
	materializer   *HistoricalMaterializer
	descriptorSHA  string
	legacyReviewed bool
}

func NewHistoricalCaptureSource(agent *OwnedAgent, h namespaceimport.Header, live *namespaceinventory.LiveSource, describe DescribeHistoricalNamespace, o HistoricalCaptureOptions, producer namespaceimport.OriginalHistorySegmentProducer, verifier namespaceimport.OriginalHistorySegmentVerifier) (*HistoricalCaptureSource, error) {
	if agent == nil || h.Validate() != nil || agent.header != h || live == nil || describe == nil || historyPortNil(producer) || historyPortNil(verifier) || !isSHA(o.PolicySHA256) || !isSHA(o.OriginalHistoryTrustSHA256) || !historicalIdentity(o.CaptureAgent) || !historicalIdentity(o.CaptureKeyID) || len(o.CapturePrivateKey) != ed25519.PrivateKeySize || len(o.CapturePublicKey) != ed25519.PublicKeySize || !o.CapturePrivateKey.Public().(ed25519.PublicKey).Equal(o.CapturePublicKey) || o.PageBytes < 1024 || o.PageBytes > 8<<20 || o.PageRows < 1 || o.PageRows > 1024 {
		return nil, ErrOwnedEvidence
	}
	for _, p := range []string{o.ConnectionPath, o.RawDirectory, o.DerivedDirectory, o.OriginalHistoryTrustPath} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			return nil, ErrOwnedEvidence
		}
	}
	if o.RawDirectory == o.DerivedDirectory || strings.HasPrefix(o.DerivedDirectory, o.RawDirectory+string(filepath.Separator)) {
		return nil, ErrOwnedEvidence
	}
	// The private connection is parsed by the real inventory client. It cannot be
	// an ambient client/default URI; its declared target must match this header.
	connection, e := namespaceinventory.LoadPrivateConnection(o.ConnectionPath)
	if e != nil {
		return nil, e
	}
	b := connection.Binding
	if b.Tenant != h.Tenant || b.Database != h.Database || b.StorageInstance != h.StorageInstance || b.ReplicaSet != h.ReplicaSet || b.RedisRunID != h.RedisRunID || b.RedisDB != h.RedisDB {
		return nil, ErrOwnedEvidence
	}
	o.CapturePrivateKey = append(ed25519.PrivateKey(nil), o.CapturePrivateKey...)
	o.CapturePublicKey = append(ed25519.PublicKey(nil), o.CapturePublicKey...)
	if o.ReviewedLegacyAllocation != nil {
		raw, e := json.Marshal(o.ReviewedLegacyAllocation)
		if e != nil {
			return nil, e
		}
		var copied LegacyAllocationReviewSpec
		if namespaceinventory.DecodeStrict(raw, &copied) != nil {
			return nil, ErrOwnedEvidence
		}
		for n := range copied.Trees {
			copied.Trees[n].Root = o.ReviewedLegacyAllocation.Trees[n].Root
		}
		o.ReviewedLegacyAllocation = &copied
	}
	descriptor := historicalJSONSHA(struct {
		Header, Policy, Agent, KeyID, OriginalTrust, LegacyReview string
		Public                                                    []byte
		PageBytes, PageRows                                       int
	}{h.SHA256(), o.PolicySHA256, o.CaptureAgent, o.CaptureKeyID, o.OriginalHistoryTrustSHA256, historicalJSONSHA(o.ReviewedLegacyAllocation), o.CapturePublicKey, o.PageBytes, o.PageRows})
	return &HistoricalCaptureSource{agent: agent, header: h, live: live, describe: describe, options: o, producer: producer, verifier: verifier, descriptorSHA: descriptor}, nil
}
func (s *HistoricalCaptureSource) checkReviewedSource() error {
	if s == nil || !isSHA(s.options.OriginalHistoryTrustSHA256) {
		return ErrOwnedEvidence
	}
	body, e := namespaceinventory.ReadPrivateDocument(s.options.OriginalHistoryTrustPath, 1<<20)
	if e != nil {
		return e
	}
	if sha(body) != s.options.OriginalHistoryTrustSHA256 {
		return ErrOwnedEvidence
	}
	return nil
}
func (s *HistoricalCaptureSource) held(ctx context.Context) error {
	if s == nil || ctx == nil || s.agent == nil || s.agent.hold == nil || s.agent.hold.HeaderSHA256() != s.header.SHA256() {
		return ErrOwnedEvidence
	}
	if e := s.agent.hold.ValidateExclusion(); e != nil {
		// Actual bounded OS/process/credential/Kafka re-observation, not a boolean or
		// timestamp refresh from the caller. It renews only the live hold capability.
		hold, e := s.agent.RefreshExclusion(ctx)
		if e != nil || hold != s.agent.hold {
			return ErrOwnedEvidence
		}
	}
	return s.agent.hold.ValidateExclusion()
}
func (s *HistoricalCaptureSource) openDerived(ctx context.Context, capture bool) error {
	if s.materializer != nil {
		if !matchesHeader(s.materializer.snapshot.Identity().Identity, s.header) {
			return ErrOwnedEvidence
		}
		return s.materializer.hold.ValidateExclusion()
	}
	var snapshot *namespaceinventory.ImportSnapshot
	var e error
	if capture {
		live, ok := s.live.(*namespaceinventory.LiveSource)
		if !ok {
			return ErrOwnedEvidence
		}
		snapshot, e = s.agent.CaptureRaw(ctx, s.options.ConnectionPath, s.options.RawDirectory, live, 16)
	} else {
		connection, loadErr := namespaceinventory.LoadPrivateConnection(s.options.ConnectionPath)
		if loadErr != nil {
			return loadErr
		}
		snapshot, e = namespaceinventory.OpenImportSnapshot(ctx, s.live, s.options.RawDirectory, connection.Binding)
	}
	if e != nil {
		return e
	}
	if !matchesHeader(snapshot.Identity().Identity, s.header) {
		return ErrOwnedEvidence
	}
	resume := false
	if info, statErr := os.Lstat(s.options.DerivedDirectory); statErr == nil {
		if !private(info, true) {
			return ErrOwnedEvidence
		}
		resume = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	m, e := NewHistoricalMaterializer(ctx, s.agent.hold, snapshot, s.live, s.options.DerivedDirectory, s.header.SHA256(), s.options.PolicySHA256, resume, s.describe)
	if e != nil {
		return e
	}
	st, expected, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return e
	}
	var stored struct {
		SHA256 string `json:"sha256"`
	}
	_, found, e := m.readDerived(ctx, "source-descriptor", snapshot.Fingerprint(), snapshot.Fingerprint(), &stored)
	if e != nil {
		return e
	}
	if found {
		if stored.SHA256 != s.descriptorSHA {
			return ErrOwnedEvidence
		}
	} else {
		// A crash may occur after the exclusive empty workspace head is durable
		// but before this deterministic descriptor is committed. Only that exact
		// pristine state can finish the first write; any progress without a pinned
		// descriptor rejects, including resumed source/options substitution.
		if resume && (st.Sequence != 1 || st.Phase != "raw" || st.Cursor != "" || st.Subcursor != "" || st.SetsRoot != "" || st.PreviousSHA256 != "") {
			return ErrOwnedEvidence
		}
		stored.SHA256 = s.descriptorSHA
		change, e := derivedUpdate("source-descriptor", snapshot.Fingerprint(), snapshot.Fingerprint(), namespaceinventory.DerivedEntry{}, stored)
		if e != nil {
			return e
		}
		if _, _, e = m.workspace.Commit(ctx, expected, st.Phase, st.Cursor, st.Subcursor, []namespaceinventory.DerivedMutation{change}); e != nil {
			return e
		}
	}
	s.materializer = m
	return nil
}
func (s *HistoricalCaptureSource) reviewLegacy(ctx context.Context) error {
	if s.legacyReviewed {
		return nil
	}
	if s.options.ReviewedLegacyAllocation != nil {
		if e := s.materializer.ReviewLegacyAllocation(ctx, *s.options.ReviewedLegacyAllocation); e != nil {
			return e
		}
	}
	s.legacyReviewed = true
	return nil
}
func sourcePending() error { return errors.Join(namespaceimport.ErrPending, ErrCaptureProgressPending) }

// Capture advances one real bounded unit. Internal metadata reads share only
// one synchronous validation scope; commits/raw references remain independently
// fresh. Joined failures must never be normalized to a plain Pending result.
// Its original raw session/cut/time is
// retained across pending calls. A result is returned only after canonical
// bodies, all actual set closures/pages, roots and the signed inventory close.
func (s *HistoricalCaptureSource) Capture(ctx context.Context, h namespaceimport.Header, f namespaceimport.FenceObservation) (namespaceimport.Inventory, error) {
	if s == nil {
		return namespaceimport.Inventory{}, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if h != s.header || s.checkReviewedSource() != nil || s.held(ctx) != nil || f.HeaderSHA256 != h.SHA256() || f.LeaseID != s.agent.hold.LeaseID() || f.AdmissionGuardSHA256 != s.agent.hold.RecordSHA256() || f.ExclusionMode != "held-until-explicit-release" {
		return namespaceimport.Inventory{}, ErrOwnedEvidence
	}
	if e := s.openDerived(ctx, true); e != nil {
		if e == ErrCaptureProgressPending {
			return namespaceimport.Inventory{}, sourcePending()
		}
		return namespaceimport.Inventory{}, e
	}
	if e := s.reviewLegacy(ctx); e != nil {
		return namespaceimport.Inventory{}, e
	}
	m := s.materializer
	st, _, e := m.workspace.Checkpoint(ctx)
	if e != nil {
		return namespaceimport.Inventory{}, e
	}
	switch st.Phase {
	case "raw", "reduce":
		e = m.workspace.WithSnapshotReadScope(ctx, func(readCtx context.Context) error {
			_, stepErr := m.Step(readCtx)
			return stepErr
		})
	case "roots":
		switch {
		case strings.HasPrefix(st.Subcursor, "history-sources:"):
			// Never pass the private metadata scope to an external producer/verifier.
			_, e = m.StepOriginalHistorySegments(ctx, h, s.producer, s.verifier)
		case st.Subcursor == "prepared" || strings.HasPrefix(st.Subcursor, "output-plan:"):
			e = m.workspace.WithSnapshotReadScope(ctx, func(readCtx context.Context) error {
				if st.Subcursor == "output-plan:complete" {
					return s.beginBodies(readCtx)
				}
				_, stepErr := m.StepOutputPlan(readCtx, h)
				return stepErr
			})
		default:
			// This capability belongs only to this synchronous StepRoots call.
			// Actual commits and original raw references keep fresh boundaries.
			e = m.workspace.WithSnapshotReadScope(ctx, func(readCtx context.Context) error {
				_, stepErr := m.StepRoots(readCtx)
				return stepErr
			})
		}
	case "pages":
		e = m.workspace.WithSnapshotReadScope(ctx, func(readCtx context.Context) error {
			return s.stepBodies(readCtx)
		})
	case "complete":
		var i namespaceimport.Inventory
		e = m.workspace.WithSnapshotReadScope(ctx, func(readCtx context.Context) error {
			var stepErr error
			i, stepErr = s.finalInventory(readCtx)
			return stepErr
		})
		return i, e
	default:
		return namespaceimport.Inventory{}, ErrOwnedEvidence
	}
	if e != nil && e != ErrCaptureProgressPending {
		return namespaceimport.Inventory{}, e
	}
	return namespaceimport.Inventory{}, sourcePending()
}
func (s *HistoricalCaptureSource) Revalidate(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory) error {
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.revalidate(ctx, h, i)
}
func (s *HistoricalCaptureSource) revalidate(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory) error {
	if h != s.header || s.checkReviewedSource() != nil || s.held(ctx) != nil {
		return ErrOwnedEvidence
	}
	if e := s.openDerived(ctx, false); e != nil {
		return e
	}
	if e := s.reviewLegacy(ctx); e != nil {
		return e
	}
	actual, e := s.finalInventory(ctx)
	if e != nil {
		return e
	}
	if historicalJSONSHA(actual) != historicalJSONSHA(i) {
		return ErrOwnedEvidence
	}
	return nil
}
func (s *HistoricalCaptureSource) ReadPage(ctx context.Context, i namespaceimport.Inventory, index uint64) (namespaceimport.CapturedPage, error) {
	if s == nil {
		return namespaceimport.CapturedPage{}, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.revalidate(ctx, s.header, i); e != nil {
		return namespaceimport.CapturedPage{}, e
	}
	if index >= i.PageCount {
		return namespaceimport.CapturedPage{}, ErrOwnedEvidence
	}
	m := s.materializer
	var ref materializedPageReference
	_, found, e := m.readDerived(ctx, "source-pages", m.snapshot.Fingerprint(), outputPlanKey(index), &ref)
	if e != nil || !found {
		return namespaceimport.CapturedPage{}, ErrOwnedEvidence
	}
	raw, e := m.workspace.ReadOutputDocument(ctx, ref.DocumentSHA256, s.options.PageBytes)
	if e != nil {
		return namespaceimport.CapturedPage{}, e
	}
	var p namespaceimport.PageData
	if namespaceinventory.DecodeStrict(raw, &p) != nil || p.Index != index || p.Identity().SHA256 != ref.PageSHA256 || p.HeaderSHA256 != s.header.SHA256() || p.InventoryBindingSHA256 != i.BindingSHA256() || p.RawSourceRootSHA256 != i.RawRootSHA256 {
		return namespaceimport.CapturedPage{}, ErrOwnedEvidence
	}
	root, proof, e := m.workspace.MerkleProof(ctx, materializerTreeID("source-page-hashes", m.snapshot.Fingerprint()), ref.PageSHA256)
	if e != nil || root != i.PagesRootSHA256 {
		return namespaceimport.CapturedPage{}, ErrOwnedEvidence
	}
	out := namespaceimport.CapturedPage{Data: p}
	for _, v := range proof {
		out.Proof = append(out.Proof, namespaceimport.MerkleSibling{SHA256: v.SHA256, Left: v.Left})
	}
	return out, nil
}

var _ namespaceimport.InventorySource = (*HistoricalCaptureSource)(nil)
