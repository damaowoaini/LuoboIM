//go:build linux

package ownedcontrol

import (
	"context"
	"errors"
	"os"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// HistoricalSourcePolicy is a SOURCE implementation, not an HTTP/command
// claim. Its inputs are sealed raw rows, original full inventory custody and
// the authoritative shared DescribeNamespace function. It must rederive the
// original owner policy/floor/order; absent/erased evidence requires quarantine.
// Root's Store/namespace group supplies this pure interpretation port so this
// package cannot import messagedelivery and form an import cycle.
type HistoricalSourcePolicy interface {
	VerifyOrigin(context.Context, *namespaceinventory.ImportSnapshot, namespaceimport.OriginRecord, []namespaceinventory.ImportRawRow) error
	VerifyWindow(context.Context, *namespaceinventory.ImportSnapshot, namespaceimport.OwnerWindow, []namespaceinventory.ImportRawRow) error
	VerifyQuarantine(context.Context, *namespaceinventory.ImportSnapshot, namespaceimport.Quarantine, []namespaceinventory.ImportRawRow) error
}
type SnapshotValidator struct {
	hold     *AdmissionHold
	header   namespaceimport.Header
	snapshot *namespaceinventory.ImportSnapshot
	live     namespaceinventory.PageSource
	policy   HistoricalSourcePolicy
}

func NewSnapshotValidator(hold *AdmissionHold, header namespaceimport.Header, snapshot *namespaceinventory.ImportSnapshot, live namespaceinventory.PageSource, policy HistoricalSourcePolicy) (*SnapshotValidator, error) {
	if hold == nil || hold.Validate() != nil || header.Validate() != nil || hold.HeaderSHA256() != header.SHA256() || snapshot == nil || live == nil || policy == nil {
		return nil, ErrOwnedEvidence
	}
	info := snapshot.Identity()
	if info.Trust != "UNTRUSTED" || !matchesHeader(info.Identity, header) {
		return nil, ErrOwnedEvidence
	}
	return &SnapshotValidator{hold, header, snapshot, live, policy}, nil
}
func matchesHeader(i namespaceinventory.Identity, h namespaceimport.Header) bool {
	b := i.Binding
	return i.TenantAttested && b.Tenant == h.Tenant && b.Database == h.Database && b.StorageInstance == h.StorageInstance && b.ReplicaSet == h.ReplicaSet && b.RedisRunID == h.RedisRunID && b.RedisDB == h.RedisDB && i.MongoProcess == h.MongoProcess && i.MongoTopology == h.MongoTopology
}
func (s *SnapshotValidator) Revalidate(ctx context.Context, header namespaceimport.Header, inventory namespaceimport.Inventory) error {
	if s == nil || ctx == nil || header != s.header || s.hold.Validate() != nil || inventory.HeaderSHA256 != header.SHA256() || inventory.LeaseID != s.hold.LeaseID() || inventory.RawRootSHA256 != s.snapshot.Identity().RawRootSHA256 || inventory.OriginalTrust != "UNTRUSTED" {
		return ErrOwnedEvidence
	}
	// No live collection re-scan: legitimate importer receipts/derived rows are
	// added after capture. UUID/catalog readiness is checked independently by the
	// Store. Every consumed original row still verifies current private custody.
	_, _, _, e := s.snapshot.ReadRows(ctx, s.live, "", 1)
	return e
}
func (s *SnapshotValidator) witnesses(ctx context.Context, refs []namespaceimport.SourceRef) ([]namespaceinventory.ImportRawRow, error) {
	if len(refs) == 0 || len(refs) > 1024 {
		return nil, ErrOwnedEvidence
	}
	out := make([]namespaceinventory.ImportRawRow, 0, len(refs))
	seen := map[namespaceimport.SourceRef]bool{}
	for _, r := range refs {
		if seen[r] || !isSHA(r.ObjectSHA256) || !isSHA(r.RecordSHA256) {
			return nil, ErrOwnedEvidence
		}
		seen[r] = true
		row, e := s.snapshot.ReadReference(ctx, s.live, r.Kind, r.ObjectSHA256, r.RecordSHA256, r.Position)
		if e != nil {
			return nil, e
		}
		out = append(out, row)
	}
	return out, nil
}
func (s *SnapshotValidator) ValidateOrigin(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, value namespaceimport.OriginRecord) error {
	if e := s.Revalidate(ctx, h, i); e != nil {
		return e
	}
	refs := append(append(append([]namespaceimport.SourceRef(nil), value.Evidence...), value.MongoHead.Evidence...), value.RedisHead.Evidence...)
	rows, e := s.witnesses(ctx, uniqueRefs(refs))
	if e != nil {
		return e
	}
	return s.policy.VerifyOrigin(ctx, s.snapshot, value, rows)
}
func uniqueRefs(refs []namespaceimport.SourceRef) []namespaceimport.SourceRef {
	out := make([]namespaceimport.SourceRef, 0, len(refs))
	seen := map[namespaceimport.SourceRef]bool{}
	for _, r := range refs {
		if !seen[r] {
			out = append(out, r)
			seen[r] = true
		}
	}
	return out
}
func (s *SnapshotValidator) ValidateWindow(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, value namespaceimport.OwnerWindow) error {
	if e := s.Revalidate(ctx, h, i); e != nil {
		return e
	}
	rows, e := s.witnesses(ctx, value.Evidence)
	if e != nil {
		return e
	}
	return s.policy.VerifyWindow(ctx, s.snapshot, value, rows)
}
func (s *SnapshotValidator) ValidateQuarantine(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, value namespaceimport.Quarantine) error {
	if e := s.Revalidate(ctx, h, i); e != nil {
		return e
	}
	rows, e := s.witnesses(ctx, value.Evidence)
	if e != nil {
		return e
	}
	return s.policy.VerifyQuarantine(ctx, s.snapshot, value, rows)
}

var ErrCaptureProgressPending = errors.New("owned import raw capture incomplete; resume original private session")

// CaptureRaw resumes only this held cut's original session. Missing attested
// tenant/original lower/owner evidence is never repaired from a fresh capture.
// A context/budget stop preserves raw files; it is NOT a completed Inventory.
func (a *OwnedAgent) CaptureRaw(ctx context.Context, connectionPath, dir string, live *namespaceinventory.LiveSource, steps int) (*namespaceinventory.ImportSnapshot, error) {
	if a == nil || ctx == nil {
		return nil, ErrOwnedEvidence
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hold == nil || a.hold.Validate() != nil || len(a.joined) != len(a.writers) || a.metadata == nil || !a.prepared || live == nil {
		return nil, ErrOwnedEvidence
	}
	connection, e := namespaceinventory.LoadPrivateConnection(connectionPath)
	if e != nil {
		return nil, e
	}
	b := connection.Binding
	if b.Tenant != a.header.Tenant || b.Database != a.header.Database || b.StorageInstance != a.header.StorageInstance || b.ReplicaSet != a.header.ReplicaSet || b.RedisRunID != a.header.RedisRunID || b.RedisDB != a.header.RedisDB {
		return nil, ErrOwnedEvidence
	}
	// Caller constructs the same bounded live clients through the private source
	// operator. This method never writes Mongo/Redis or changes runtime readiness.
	actual, e := live.Identity(ctx)
	if e != nil || !matchesHeader(actual, a.header) {
		return nil, ErrOwnedEvidence
	}
	resume := false
	if info, statErr := os.Lstat(dir); statErr == nil {
		if !private(info, true) {
			return nil, ErrOwnedEvidence
		}
		resume = true
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	summary, e := namespaceinventory.CaptureResumablePrivate(ctx, connectionPath, dir, resume, steps)
	if e != nil {
		return nil, e
	}
	if !summary.RecordedTwoPassComplete {
		return nil, ErrCaptureProgressPending
	}
	return namespaceinventory.OpenImportSnapshot(ctx, live, dir, b)
}
