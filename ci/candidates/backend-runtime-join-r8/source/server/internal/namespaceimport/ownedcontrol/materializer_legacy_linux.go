//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// Review specs are source-owned operator policy, pinned during deployment
// review just like OwnedWriter.Binary/SourceSHA256. They are NOT a child/HTTP
// assertion that a writer is stopped. Actual files, owner/build identities,
// runtime modes and live exclusion are independently consumed below.
type ReviewedSourceFile struct {
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type ReviewedLegacyTree struct {
	SourceSHA256 string               `json:"source_sha256"`
	Root         string               `json:"-"`
	Files        []ReviewedSourceFile `json:"files"`
	// Exact leaf pins identify the already-reviewed Redis-only allocation
	// implementation inside this complete source tree. Supplying a digest of an
	// unknown implementation does not make it a reviewed policy entry.
	AllocationFiles []ReviewedSourceFile `json:"allocation_files"`
}
type LegacyAllocationReviewSpec struct {
	Version string               `json:"version"`
	Trees   []ReviewedLegacyTree `json:"trees"`
}
type legacyAllocationReview struct {
	hold      *AdmissionHold
	policySHA string
	sources   map[string]bool
	guardSHA  string
}

func sourceTreeDigest(rows []ReviewedSourceFile) string {
	h := sha256.New()
	for _, r := range rows {
		h.Write([]byte(r.Path))
		h.Write([]byte{0})
		h.Write([]byte(r.SHA256))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ReviewLegacyAllocation binds the complete original code vector to the real
// process build source and reads every pinned source file without following a
// path alias. It never execs a command or trusts a writer's self-reported SHA.
// The approved spec itself must be bound by m.policySHA; a changed allocation
// implementation or operator-selected spec is a different reviewed policy.
func (m *HistoricalMaterializer) ReviewLegacyAllocation(ctx context.Context, spec LegacyAllocationReviewSpec) error {
	if m == nil || m.hold.ValidateExclusion() != nil || spec.Version != "reviewed-legacy-redis-only-allocation-v1" || historicalJSONSHA(spec) != m.policySHA || len(spec.Trees) == 0 {
		return ErrOwnedEvidence
	}
	bySource := map[string]bool{}
	for _, tree := range spec.Trees {
		if !isSHA(tree.SourceSHA256) || bySource[tree.SourceSHA256] || !filepath.IsAbs(tree.Root) || filepath.Clean(tree.Root) != tree.Root || len(tree.Files) == 0 || len(tree.AllocationFiles) < 2 {
			return ErrOwnedEvidence
		}
		canonical, e := filepath.EvalSymlinks(tree.Root)
		if e != nil || canonical != tree.Root {
			return ErrOwnedEvidence
		}
		rows := append([]ReviewedSourceFile(nil), tree.Files...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].Path < rows[j].Path })
		if sourceTreeDigest(rows) != tree.SourceSHA256 {
			return ErrOwnedEvidence
		}
		expected := map[string]ReviewedSourceFile{}
		for _, r := range tree.AllocationFiles {
			if expected[r.Path].Path != "" {
				return ErrOwnedEvidence
			}
			expected[r.Path] = r
		}
		if expected["server/pkg/common/db/controller/msg.go"].Path == "" || expected["server/pkg/common/db/cache/msg.go"].Path == "" {
			return ErrOwnedEvidence
		}
		consumed := 0
		for i, r := range rows {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if r.Path == "" || filepath.IsAbs(r.Path) || filepath.Clean(r.Path) != r.Path || strings.HasPrefix(r.Path, "../") || strings.ContainsAny(r.Path, "\x00\r\n") || r.Bytes < 1 || r.Bytes > 16<<20 || !isSHA(r.SHA256) || i > 0 && rows[i-1].Path == r.Path {
				return ErrOwnedEvidence
			}
			path := filepath.Join(tree.Root, r.Path)
			actual, e := filepath.EvalSymlinks(path)
			if e != nil || actual != path {
				return ErrOwnedEvidence
			}
			body, e := boundedFile(path, 16<<20, false)
			if e != nil || int64(len(body)) != r.Bytes || sha(body) != r.SHA256 {
				return ErrOwnedEvidence
			}
			if pin, ok := expected[r.Path]; ok {
				if pin != r {
					return ErrOwnedEvidence
				}
				consumed++
			}
		}
		if consumed != len(expected) {
			return ErrOwnedEvidence
		}
		bySource[tree.SourceSHA256] = true
	}
	services, e := m.hold.Services()
	if e != nil || len(services) == 0 {
		return ErrOwnedEvidence
	}
	for _, s := range services {
		// Every actual joined writer in this held graph must have the approved
		// source. A required runtime may have allocated a shared Mongo counter;
		// its absent record cannot use the Redis-only legacy explanation.
		s.mu.Lock()
		joined := s.joined
		source := s.owner.SourceSHA256
		legacy := s.serverAnnouncement != nil && s.serverAnnouncement.Mode == "legacy"
		s.mu.Unlock()
		if !joined || !bySource[source] || !legacy {
			return ErrOwnedEvidence
		}
	}
	if m.hold.ValidateExclusion() != nil {
		return ErrOwnedEvidence
	}
	m.legacy = &legacyAllocationReview{m.hold, m.policySHA, bySource, m.hold.RecordSHA256()}
	return nil
}
func (r *legacyAllocationReview) valid(m *HistoricalMaterializer) bool {
	if r == nil || m == nil || r.hold != m.hold || r.policySHA != m.policySHA || r.guardSHA != m.hold.RecordSHA256() || m.hold.ValidateExclusion() != nil {
		return false
	}
	services, e := m.hold.Services()
	if e != nil {
		return false
	}
	for _, s := range services {
		s.mu.Lock()
		ok := s.joined && r.sources[s.owner.SourceSHA256] && s.serverAnnouncement != nil && s.serverAnnouncement.Mode == "legacy"
		s.mu.Unlock()
		if !ok {
			return false
		}
	}
	return true
}
func materializerTreeID(kind, scope string) string {
	return historicalJSONSHA([]string{"namespace-materializer-tree-v1", kind, scope})
}

// LegacyMongoAbsence requires the actual completed raw pass, exact complete
// counter collection set/root and zero recognized or unrecognized counter row
// for this CID. A set with no row is never re-labelled present/proven-empty.
func (m *HistoricalMaterializer) LegacyMongoAbsence(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, cid string) (namespaceimport.Head, error) {
	if m == nil || !m.legacy.valid(m) || h.SHA256() != m.hold.HeaderSHA256() || i.HeaderSHA256 != h.SHA256() || i.RawRootSHA256 != m.snapshot.Identity().RawRootSHA256 || i.HeldAdmissionGuardSHA256 != m.hold.RecordSHA256() {
		return namespaceimport.Head{}, ErrOwnedEvidence
	}
	_, unattributed, e := m.workspace.DerivedSetIdentity(ctx, namespaceinventory.DerivedSetID("unattributed-gaps", m.snapshot.Fingerprint()))
	if e != nil || unattributed != 0 {
		return namespaceimport.Head{}, ErrOwnedEvidence
	}
	var reduced namespaceReduction
	_, found, e := m.readDerived(ctx, "namespace-reductions", m.snapshot.Fingerprint(), sha([]byte(cid)), &reduced)
	if e != nil || !found || !reduced.Complete || reduced.MongoCounterRows != 0 || reduced.IntegrityReason != "" {
		return namespaceimport.Head{}, ErrOwnedEvidence
	}
	tree := materializerTreeID("collection-rows", "message_delivery_sequences_v1")
	state, _, found, e := m.workspace.MerkleState(ctx, tree)
	if e != nil || !found || state.Phase != "complete" || state.SetID != namespaceinventory.DerivedSetID("collection-rows", "message_delivery_sequences_v1") {
		return namespaceimport.Head{}, ErrCaptureProgressPending
	}
	actual, count, e := m.workspace.DerivedSetIdentity(ctx, state.SetID)
	if e != nil || actual != state.SetRoot || count != state.Count {
		return namespaceimport.Head{}, ErrOwnedEvidence
	}
	identity := m.snapshot.Identity()
	absence := namespaceimport.LegacyCounterAbsence{CID: cid, Collection: "message_delivery_sequences_v1", SnapshotSHA256: m.snapshot.Fingerprint(), RawRootSHA256: identity.RawRootSHA256, RowsRootSHA256: identity.RowsRootSHA256, CollectionRootSHA256: state.RootSHA256, CollectionRows: state.Count, AllocatorSourceSHA256: m.legacy.policySHA, AdmissionGuardSHA256: m.legacy.guardSHA}
	return namespaceimport.Head{State: namespaceimport.LegacyCounterNotAllocatedHere, LegacyAbsence: &absence}, nil
}
func (m *HistoricalMaterializer) VerifyLegacyCounterAbsence(ctx context.Context, snapshot *namespaceinventory.ImportSnapshot, h namespaceimport.Header, i namespaceimport.Inventory, r namespaceimport.OriginRecord) error {
	if snapshot != m.snapshot {
		return ErrOwnedEvidence
	}
	expected, e := m.LegacyMongoAbsence(ctx, h, i, r.Origin.CID)
	if e != nil || historicalJSONSHA(expected) != historicalJSONSHA(r.MongoHead) {
		return ErrOwnedEvidence
	}
	return nil
}
