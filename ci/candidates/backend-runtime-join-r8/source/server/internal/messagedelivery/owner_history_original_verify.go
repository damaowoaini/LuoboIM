package messagedelivery

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sort"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	table "github.com/openimsdk/open-im-server/v3/pkg/common/db/table/unrelation"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/unrelation"
	"go.mongodb.org/mongo-driver/bson"
)

var _ namespaceimport.OriginalHistorySegmentVerifier = (*OriginalHistoryVerifier)(nil)
var _ namespaceimport.OriginalHistorySegmentProducer = (*OriginalHistoryVerifier)(nil)

// All reads go through the actually sealed original snapshot. A decoder-only
// Store holds epoch/activation; none of its Mongo methods are called here.
type originalHistoryChain struct {
	ctx             context.Context
	reader          namespaceimport.OriginalHistoryRecordReader
	trust           OriginalHistoryEpochTrust
	decoder         *Store
	refs            map[string]namespaceimport.SourceRef
	canonicalBaseID string
	preparation     *originalHistoryPreparation
	scopeContract   string // set only by a complete original private-page/notice proof
}

func (c *originalHistoryChain) read(collection, id string) (bson.Raw, error) {
	body, ref, e := c.reader.ReadOriginalHistoryRecord(c.ctx, collection, id)
	if e != nil {
		return nil, e
	}
	raw := bson.Raw(body)
	if raw.Validate() != nil || !importUniqueFields(raw) {
		return nil, ErrConflict
	}
	actual, ok := raw.Lookup("_id").StringValueOK()
	if !ok || actual != id || !validSHA(ref.ObjectSHA256) || !validSHA(ref.RecordSHA256) || !identity(ref.Kind) {
		return nil, ErrConflict
	}
	c.refs[directHistoryDigest(ref)] = ref
	return append(bson.Raw(nil), raw...), nil
}
func (c *originalHistoryChain) evidence() []namespaceimport.SourceRef {
	keys := make([]string, 0, len(c.refs))
	for k := range c.refs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]namespaceimport.SourceRef, 0, len(keys))
	for _, k := range keys {
		out = append(out, c.refs[k])
	}
	return out
}
func (v *OriginalHistoryVerifier) buildOriginalHistoryCandidateRanges(ctx context.Context, h namespaceimport.Header, i namespaceimport.Inventory, epoch, kind, id string, reader namespaceimport.OriginalHistoryRecordReader, preparation *originalHistoryPreparation) ([]namespaceimport.OwnerHistorySegment, error) {
	if v == nil || ctx == nil || reader == nil || h.Validate() != nil || i.HeaderSHA256 != h.SHA256() || i.OriginalTrust != "UNTRUSTED" || !identity(i.LeaseID) || !validSHA(i.RawRootSHA256) || !validSHA(i.MongoRootSHA256) || !validSHA(i.RedisRootSHA256) || !identity(id) {
		return nil, ErrInvalid
	}
	// The producer's target page roots/counts do not exist yet. Only immutable
	// capture identity is used here; verification later repeats the same derive.
	if kind == "namespace-import-boundary-v1" && epoch == "" {
		raw, _, e := reader.ReadOriginalHistoryRecord(ctx, Receipts, id)
		if e != nil {
			return nil, e
		}
		header, ok := bson.Raw(raw).Lookup("header_sha256").StringValueOK()
		if !ok {
			return nil, ErrConflict
		}
		for _, t := range v.epochs {
			if t.Import != nil && t.Import.Header.SHA256() == header {
				if epoch != "" {
					return nil, ErrConflict
				}
				epoch = t.Epoch
			}
		}
	}
	trust, e := v.checkTrust(h, epoch)
	if e != nil {
		if errors.Is(e, ErrUnprepared) {
			return nil, &namespaceimport.OriginalHistoryProofPending{Reason: "source-trust-unavailable"}
		}
		return nil, e
	}
	c := &originalHistoryChain{ctx: ctx, reader: reader, trust: trust, decoder: &Store{epoch: epoch, activationSHA: trust.ActivationSHA256}, refs: map[string]namespaceimport.SourceRef{}, preparation: preparation}
	collection := Sequences
	if kind == "member-notice-entitlement-v1" || kind == "namespace-import-boundary-v1" {
		collection = Receipts
	}
	raw, e := c.read(collection, id)
	if e != nil {
		return nil, e
	}
	actualKind, ok := raw.Lookup("kind").StringValueOK()
	if !ok || actualKind != kind {
		return nil, ErrConflict
	}
	cid, ok := raw.Lookup("conversation_id").StringValueOK()
	if !ok {
		return nil, ErrConflict
	}
	owner, ok := raw.Lookup("user_id").StringValueOK()
	if !ok || !identity(owner) {
		return nil, ErrConflict
	}
	if kind != "namespace-import-boundary-v1" {
		actualEpoch, ok := raw.Lookup("epoch").StringValueOK()
		if !ok || actualEpoch != epoch {
			return nil, ErrConflict
		}
	}
	d, e := c.origin(cid)
	if e != nil {
		return nil, c.pending(e)
	}
	if d.Kind != "group" {
		found := false
		for _, p := range d.OriginIDs {
			found = found || p == owner
		}
		if !found {
			return nil, ErrNamespaceHistoryNotOwned
		}
	}
	var intervals [][2]int64
	switch kind {
	case "namespace-owner-prospective-v1":
		intervals, e = c.prospective(raw, d, owner)
	case "namespace-owner-live-history-v1", "namespace-owner-history-admission-v1", "namespace-owner-imported-history-v1":
		intervals, e = c.interval(raw, d, owner)
	case "member-notice-entitlement-v1":
		intervals, e = c.notice(raw, d, owner)
	case "namespace-import-boundary-v1":
		var b namespaceimport.EffectiveOwnerBoundary
		b, e = c.boundary(raw, d, owner)
		if e == nil {
			var head counter
			head, e = c.counter(d.CID)
			if e == nil {
				upper := head.SeedMaximum
				if b.Window.Closed {
					upper = min(upper, b.Window.Upper)
				}
				intervals = append(intervals, [2]int64{max(int64(1), b.Window.Lower), upper})
			}
		}
	default:
		e = ErrInvalid
	}
	if e != nil {
		return nil, c.pending(e)
	}
	floor, e := c.floor(d, owner)
	if e != nil {
		return nil, c.pending(e)
	}
	evidence := c.evidence()
	out := make([]namespaceimport.OwnerHistorySegment, 0, len(intervals))
	seen := map[string]bool{}
	for _, r := range intervals {
		lower := max(r[0], floor, int64(1))
		upper := r[1]
		if upper < lower {
			continue
		}
		row := namespaceimport.OwnerHistorySegment{ScopeContract: c.scopeContract, Kind: "preserved-owner-history-v1", CID: d.CID, OwnerID: owner, OriginSHA256: d.SHA256, Lower: lower, Upper: upper, SourceEpoch: epoch, SourceKind: kind, SourceID: id, Evidence: append([]namespaceimport.SourceRef(nil), evidence...)}
		sum, e := namespaceimport.OwnerHistorySegmentSHA256(row)
		if e != nil {
			return nil, e
		}
		if seen[sum] {
			return nil, ErrConflict
		}
		seen[sum] = true
		out = append(out, row)
	}
	if c.canonicalBaseID != "" {
		base, e := v.buildOriginalHistoryCandidateRanges(ctx, h, i, epoch, "namespace-import-boundary-v1", c.canonicalBaseID, reader, preparation)
		if e != nil {
			return nil, e
		}
		out = append(base, out...)
	}
	if _, e = v.checkTrust(h, epoch); e != nil {
		return nil, e
	}
	return out, nil
}

func (c *originalHistoryChain) origin(cid string) (NamespaceOriginDescriptor, error) {
	raw, e := c.read(Sequences, namespacePrincipalsID(cid))
	if e != nil {
		return NamespaceOriginDescriptor{}, e
	}
	p, e := decodeNamespacePrincipals(raw)
	if e != nil {
		return NamespaceOriginDescriptor{}, e
	}
	d := NamespaceOriginDescriptor{CID: cid, Kind: p.OriginKind, SHA256: p.OriginSHA256, OriginIDs: append([]string(nil), p.OriginIDs...)}
	if d.Kind == "group" && len(d.OriginIDs) == 1 {
		d.GroupID = d.OriginIDs[0]
	}
	if p.ConversationID != cid || validateNamespaceDescriptor(d) != nil {
		return d, ErrConflict
	}
	raw, e = c.read(Sequences, namespaceClaimID(cid))
	if e != nil {
		return d, e
	}
	kind, _ := raw.Lookup("kind").StringValueOK()
	sum, epoch, activation := "", "", ""
	var created int64
	switch kind {
	case historicalNamespaceKind:
		r, e := decodeHistoricalNamespaceClaim(raw, cid)
		if e != nil || r.OriginKind != d.Kind || r.OriginSHA256 != d.SHA256 || r.GroupID != d.GroupID {
			return d, ErrConflict
		}
		sum = directHistoryDigest(r)
		epoch = r.Epoch
		activation = r.ActivationSHA256
		created = r.CreatedAt.UnixMilli()
		if d.Kind == "group" {
			if e = c.historicalGroupOrigin(d); e != nil {
				return d, e
			}
		}
	case "permanent-message-origin-v1":
		var r directNamespaceClaim
		if !exactFields(raw, []string{"_id", "kind", "conversation_id", "origin_kind", "origin_sha256", "created_epoch", "created_activation_sha256", "seed_evidence_sha256"}, nil, nil, "created_at") || bson.Unmarshal(raw, &r) != nil || r.ID != namespaceClaimID(cid) || r.ConversationID != cid || r.OriginKind != d.Kind || d.Kind == "group" || r.OriginSHA256 != d.SHA256 || !validSHA(r.SeedEvidenceSHA256) {
			return d, ErrConflict
		}
		sum = directHistoryDigest(r)
		epoch = r.Epoch
		activation = r.ActivationSHA256
		created = r.CreatedAt.UnixMilli()
	case "permanent-new-group-namespace-v1":
		var r groupNamespaceClaim
		if !exactFields(raw, []string{"_id", "kind", "conversation_id", "group_id", "branch", "epoch", "activation_sha256", "command_id", "input_sha256", "cutover_sha256"}, nil, nil, "created_at") || bson.Unmarshal(raw, &r) != nil || r.ID != namespaceClaimID(cid) || d.Kind != "group" || r.ConversationID != cid || r.GroupID != d.GroupID || !validUUID(r.CommandID) || !validSHA(r.InputSHA256) || !validSHA(r.CutoverSHA256) {
			return d, ErrConflict
		}
		normal, notice, e := groupNamespacePair(d.GroupID)
		if e != nil || cid == normal && r.Branch != "normal" || cid == notice && r.Branch != "notification" || cid != normal && cid != notice {
			return d, ErrConflict
		}
		sum = directHistoryDigest(r)
		epoch = r.Epoch
		activation = r.ActivationSHA256
		created = r.CreatedAt.UnixMilli()
		rootRaw, e := c.read(Sequences, groupOriginClaimID(d.GroupID))
		if e != nil {
			return d, e
		}
		var root groupNamespaceClaim
		if !exactFields(rootRaw, []string{"_id", "kind", "conversation_id", "group_id", "branch", "epoch", "activation_sha256", "command_id", "input_sha256", "cutover_sha256"}, nil, nil, "created_at") || bson.Unmarshal(rootRaw, &root) != nil {
			return d, ErrConflict
		}
		expected := r
		expected.ID = groupOriginClaimID(d.GroupID)
		expected.ConversationID = ""
		expected.Branch = "group-origin"
		if directHistoryDigest(root) != directHistoryDigest(expected) {
			return d, ErrConflict
		}
	default:
		return d, ErrUnprepared
	}
	if p.ClaimSHA256 != sum || p.Epoch != epoch || p.ActivationSHA256 != activation || p.CreatedAt.UnixMilli() != created {
		return d, ErrConflict
	}
	return d, nil
}
func (c *originalHistoryChain) historicalGroupOrigin(d NamespaceOriginDescriptor) error {
	raw, e := c.read(Sequences, groupOriginClaimID(d.GroupID))
	if e != nil {
		return e
	}
	kind, ok := raw.Lookup("kind").StringValueOK()
	if !ok {
		return ErrConflict
	}
	if kind == "permanent-new-group-namespace-v1" {
		var r groupNamespaceClaim
		if !exactFields(raw, []string{"_id", "kind", "conversation_id", "group_id", "branch", "epoch", "activation_sha256", "command_id", "input_sha256", "cutover_sha256"}, nil, nil, "created_at") || bson.Unmarshal(raw, &r) != nil || r.ID != groupOriginClaimID(d.GroupID) || r.Kind != kind || r.ConversationID != "" || r.GroupID != d.GroupID || r.Branch != "group-origin" || !validUUID(r.Epoch) || !validSHA(r.ActivationSHA256) || !validUUID(r.CommandID) || !validSHA(r.InputSHA256) || !validSHA(r.CutoverSHA256) || r.CreatedAt.IsZero() {
			return ErrConflict
		}
		return nil
	}
	var r historicalGroupReservation
	if !exactFields(raw, []string{"_id", "kind", "group_id", "origin_sha256", "created_epoch", "created_activation_sha256", "migration_id", "header_sha256", "inventory_sha256"}, nil, nil, "created_at") || bson.Unmarshal(raw, &r) != nil || r.ID != groupOriginClaimID(d.GroupID) || r.Kind != "permanent-historical-group-origin-v1" || r.GroupID != d.GroupID || r.OriginSHA256 != d.SHA256 || !validUUID(r.Epoch) || !validUUID(r.MigrationID) || !validSHA(r.ActivationSHA256) || !validSHA(r.HeaderSHA256) || !validSHA(r.InventorySHA256) || r.CreatedAt.IsZero() {
		return ErrConflict
	}
	return nil
}
func (c *originalHistoryChain) counter(cid string) (counter, error) {
	var row counter
	raw, e := c.read(Sequences, key(c.trust.Epoch, "sequence", cid))
	if e != nil {
		return row, e
	}
	if !exactFields(raw, []string{"_id", "epoch", "conversation_id", "seed_evidence_sha256"}, []string{"maximum", "revision", "seed_maximum"}, nil, "") || bson.Unmarshal(raw, &row) != nil || row.ID != key(c.trust.Epoch, "sequence", cid) || row.Epoch != c.trust.Epoch || row.CID != cid || row.Maximum < 0 || row.Maximum >= math.MaxInt64 || row.SeedMaximum < 0 || row.SeedMaximum > row.Maximum || row.Revision < 1 || !validSHA(row.SeedEvidenceSHA) {
		return row, ErrConflict
	}
	return row, nil
}
func (c *originalHistoryChain) receipt(d NamespaceOriginDescriptor, source, branch, input, sum string, lower, upper int64) error {
	raw, e := c.read(Receipts, key(c.trust.Epoch, "source", source))
	if e != nil {
		return e
	}
	r, e := c.decoder.decodeNoticeSourceReceipt(raw)
	if e != nil || r.SourceID != source || r.InputSHA256 != input || directHistoryDigest(r) != sum {
		return ErrConflict
	}
	seq := int64(0)
	for _, a := range r.Branches {
		if a.ID == branch {
			if seq != 0 || !a.Persist || a.Hidden || a.ConversationID != d.CID || a.Seq < lower || upper != 0 && a.Seq != upper {
				return ErrConflict
			}
			seq = a.Seq
		}
	}
	if seq == 0 {
		return ErrConflict
	}
	duty, e := c.read(Effects, key(c.trust.Epoch, "branch", branch))
	if e != nil {
		return e
	}
	for k, want := range map[string]string{"_id": key(c.trust.Epoch, "branch", branch), "epoch": c.trust.Epoch, "source_id": source, "branch_id": branch, "conversation_id": d.CID, "namespace_origin_kind": d.Kind, "namespace_origin_sha256": d.SHA256, "input_sha256": input} {
		got, ok := duty.Lookup(k).StringValueOK()
		if !ok || got != want {
			return ErrConflict
		}
	}
	got, ok := duty.Lookup("seq").Int64OK()
	persist, pok := duty.Lookup("persist").BooleanOK()
	if !ok || got != seq || !pok || !persist {
		return ErrConflict
	}
	return nil
}

// The custody getter returns the original cutover preimage and an ACTUAL
// whole-record SourceRef (archive when archived). Never attach the archive's
// ref to invented embedded rows or learn old trust from today's singleton.
func (c *originalHistoryChain) originalCutoverRaw() (bson.Raw, error) {
	h, selected, e := selectedOriginalEpochHeader(c.trust)
	if e != nil {
		return nil, e
	}
	if !selected {
		// Backward compatibility for already selected unarchived source contracts.
		// Both callers below still require exact OLD epoch/activation/database and
		// reviewed CutoverSHA. A new current epoch is not a fallback for this path.
		return c.read(Sequences, NamespaceCutoverID)
	}
	custody, e := ReadOriginalEpochCutoverCustody(c.ctx, h, c.reader)
	if e != nil {
		return nil, e
	}
	raw := bson.Raw(custody.CutoverRaw)
	ref := custody.SourceRef
	if raw.Validate() != nil || !importUniqueFields(raw) || !validSHA(ref.ObjectSHA256) || !validSHA(ref.RecordSHA256) || !identity(ref.Kind) {
		return nil, ErrConflict
	}
	c.refs[directHistoryDigest(ref)] = ref
	return append(bson.Raw(nil), raw...), nil
}
func (c *originalHistoryChain) publication() error {
	p := c.trust.Import
	if p == nil || validateOriginalImportTrust(c.trust) != nil {
		return ErrUnprepared
	}
	h := p.Header
	raw, e := c.read(Receipts, importCutoverReceiptID(h))
	if e != nil {
		return e
	}
	var r namespaceImportCutover
	var m namespaceimport.ManifestData
	var receipt namespaceimport.CutoverReceipt
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "manifest_sha256", "manifest_json", "cutover_sha256", "receipt_json", "receipt_sha256", "attempt_id", "lease_id", "fence_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &r) != nil || r.ID != importCutoverReceiptID(h) || r.Kind != "namespace-import-cutover-v1" || r.HeaderSHA256 != h.SHA256() || r.InventorySHA256 != p.Inventory.SHA256() || r.ManifestSHA256 != p.ManifestSHA256 || hash([]byte(r.ManifestJSON)) != r.ManifestSHA256 || decodeImportJSON(r.ManifestJSON, &m) != nil || m.HeaderSHA256 != h.SHA256() || m.InventorySHA256 != p.Inventory.SHA256() || m.PageCount != p.Inventory.PageCount || m.Counts != p.Inventory.Counts || m.CoverageSetRootSHA256 != p.Inventory.CoverageSetRootSHA256 || m.CoverageCount != p.Inventory.CoverageCount || m.ProspectiveSetRootSHA256 != p.Inventory.ProspectiveSetRootSHA256 || m.ProspectiveCount != p.Inventory.ProspectiveCount || m.OwnerHistorySegmentSetRootSHA256 != p.Inventory.OwnerHistorySegmentSetRootSHA256 || m.OwnerHistorySegmentCount != p.Inventory.OwnerHistorySegmentCount || !identity(r.AttemptID) || r.LeaseID != p.Inventory.LeaseID || !validSHA(r.FenceSHA256) || decodeImportJSON(r.ReceiptJSON, &receipt) != nil || receipt.HeaderSHA256 != h.SHA256() || receipt.ManifestSHA256 != r.ManifestSHA256 || namespaceimport.CutoverReceiptDigest(receipt) != r.ReceiptSHA256 || receipt.ReceiptSHA256 != r.ReceiptSHA256 {
		return ErrConflict
	}
	raw, e = c.originalCutoverRaw()
	if e != nil {
		return e
	}
	var cut NamespaceCutover
	if !exactFields(raw, []string{"_id", "kind", "database", "epoch", "activation_sha256", "migration_id", "state", "manifest_sha256", "mongo_inventory_sha256", "redis_inventory_sha256", "drain_sha256", "old_binary_fence_sha256"}, []string{"namespace_count", "claimed_count"}, nil, "registered_at") || bson.Unmarshal(raw, &cut) != nil || cut.ID != NamespaceCutoverID || cut.Kind != "immutable-namespace-cutover-v1" || cut.Database != h.Database || cut.Epoch != h.Epoch || cut.ActivationSHA256 != h.ActivationSHA256 || cut.MigrationID != h.MigrationID || cut.State != "all-inventoried-namespaces-permanently-claimed" || cut.ManifestSHA256 != r.ManifestSHA256 || cut.MongoInventorySHA256 != p.Inventory.MongoRootSHA256 || cut.RedisInventorySHA256 != p.Inventory.RedisRootSHA256 || cut.NamespaceCount < 0 || cut.NamespaceCount != cut.ClaimedCount || !validSHA(cut.DrainSHA256) || !validSHA(cut.OldBinaryFenceSHA256) || cut.RegisteredAt.IsZero() || directHistoryDigest(cut) != r.CutoverSHA256 || directHistoryDigest(cut) != c.trust.CutoverSHA256 {
		return ErrConflict
	}
	return nil
}
func (c *originalHistoryChain) cutover() (NamespaceCutover, error) {
	var cut NamespaceCutover
	raw, e := c.originalCutoverRaw()
	if e != nil {
		return cut, e
	}
	if !exactFields(raw, []string{"_id", "kind", "database", "epoch", "activation_sha256", "migration_id", "state", "manifest_sha256", "mongo_inventory_sha256", "redis_inventory_sha256", "drain_sha256", "old_binary_fence_sha256"}, []string{"namespace_count", "claimed_count"}, nil, "registered_at") || bson.Unmarshal(raw, &cut) != nil || cut.ID != NamespaceCutoverID || cut.Kind != "immutable-namespace-cutover-v1" || cut.Database != c.trust.Database || cut.Epoch != c.trust.Epoch || cut.ActivationSHA256 != c.trust.ActivationSHA256 || !validUUID(cut.MigrationID) || cut.State != "all-inventoried-namespaces-permanently-claimed" || !validSHA(cut.ManifestSHA256) || !validSHA(cut.MongoInventorySHA256) || !validSHA(cut.RedisInventorySHA256) || !validSHA(cut.DrainSHA256) || !validSHA(cut.OldBinaryFenceSHA256) || cut.NamespaceCount < 0 || cut.NamespaceCount != cut.ClaimedCount || cut.RegisteredAt.IsZero() || directHistoryDigest(cut) != c.trust.CutoverSHA256 {
		return cut, ErrConflict
	}
	return cut, nil
}
func (c *originalHistoryChain) page(binding namespaceOwnerAdmissionProof) (namespaceimport.PageData, error) {
	var data namespaceimport.PageData
	p := c.trust.Import
	if p == nil {
		return data, ErrUnprepared
	}
	raw, e := c.read(Receipts, binding.PageID)
	if e != nil {
		return data, e
	}
	var r namespaceProspectivePageSource
	if !exactFields(raw, []string{"_id", "kind", "header_json", "header_sha256", "inventory_json", "inventory_sha256", "page_json", "page_sha256"}, []string{"page_index"}, nil, "") || bson.Unmarshal(raw, &r) != nil || r.Kind != "namespace-import-prospective-page-v1" || r.HeaderSHA != p.Header.SHA256() || r.HeaderSHA != binding.HeaderSHA || r.InventorySHA != p.Inventory.SHA256() || r.InventorySHA != binding.InventorySHA || decodeImportJSON(r.PageJSON, &data) != nil || directHistoryDigest(data) != r.PageSHA || r.PageSHA != binding.PageSHA || r.ID != prospectivePageSourceID(p.Header, data.Index) || r.PageIndex != int64(data.Index) || data.HeaderSHA256 != r.HeaderSHA || data.InventoryBindingSHA256 != p.Inventory.BindingSHA256() || data.RawSourceRootSHA256 != p.Inventory.RawRootSHA256 {
		return data, ErrConflict
	}
	var h namespaceimport.Header
	var i namespaceimport.Inventory
	if decodeImportJSON(r.HeaderJSON, &h) != nil || directHistoryDigest(h) != p.Header.SHA256() || decodeImportJSON(r.InventoryJSON, &i) != nil || directHistoryDigest(i) != directHistoryDigest(p.Inventory) {
		return data, ErrConflict
	}
	raw, e = c.read(Receipts, importPageID(p.Header, data.Index))
	if e != nil {
		return data, e
	}
	var committed namespaceImportPage
	var receipt namespaceimport.PageReceipt
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "page_sha256", "previous_sha256", "receipt_json", "receipt_sha256", "attempt_id", "lease_id", "fence_sha256"}, []string{"page_index"}, nil, "") || bson.Unmarshal(raw, &committed) != nil || committed.ID != importPageID(p.Header, data.Index) || committed.Kind != "namespace-import-page-v1" || committed.HeaderSHA256 != r.HeaderSHA || committed.InventorySHA256 != r.InventorySHA || committed.PageSHA256 != r.PageSHA || committed.PreviousSHA256 != data.PreviousSHA256 || committed.PageIndex != r.PageIndex || decodeImportJSON(committed.ReceiptJSON, &receipt) != nil || receipt.Page != data.Identity() || receipt.HeaderSHA256 != r.HeaderSHA || receipt.InventorySHA256 != r.InventorySHA || receipt.ReceiptSHA256 != committed.ReceiptSHA256 || namespaceimport.ReceiptDigest(receipt) != committed.ReceiptSHA256 {
		return data, ErrConflict
	}
	return data, c.publication()
}

func (c *originalHistoryChain) boundary(raw bson.Raw, d NamespaceOriginDescriptor, owner string) (namespaceimport.EffectiveOwnerBoundary, error) {
	var r namespaceImportBoundary
	var b namespaceimport.EffectiveOwnerBoundary
	p := c.trust.Import
	if p == nil {
		return b, ErrUnprepared
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "conversation_id", "user_id", "owner_key_sha256", "boundary_sha256", "coverage_sha256", "boundary_json", "window_id", "window_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &r) != nil || r.ID != importBoundaryID(p.Header, d.CID, owner) || r.Kind != "namespace-import-boundary-v1" || r.HeaderSHA256 != p.Header.SHA256() || r.InventorySHA256 != p.Inventory.SHA256() || r.ConversationID != d.CID || r.UserID != owner || r.OwnerKeySHA256 != namespaceimport.EffectiveOwnerKey(d.CID, owner) || decodeImportJSON(r.BoundaryJSON, &b) != nil {
		return b, ErrConflict
	}
	sum, e := namespaceimport.EffectiveBoundarySHA256(b)
	if e != nil || sum != r.BoundarySHA256 || b.Window.CID != d.CID || b.Window.OwnerID != owner || b.Window.OriginSHA256 != d.SHA256 || b.Window.GroupID != d.GroupID {
		return b, ErrConflict
	}
	var witness namespaceimport.EffectiveBoundaryWitness
	found := 0
	proofRaw, e := c.read(Receipts, boundaryPageProofID(c.trust.Epoch, d.CID, owner))
	if e == nil {
		var proof namespaceBoundaryPageProof
		if !exactFields(proofRaw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "header_sha256", "inventory_sha256", "boundary_sha256", "page_id", "page_sha256"}, nil, nil, "") || bson.Unmarshal(proofRaw, &proof) != nil || proof.ID != boundaryPageProofID(c.trust.Epoch, d.CID, owner) || proof.Kind != "namespace-import-boundary-source-v1" || proof.Epoch != c.trust.Epoch || proof.CID != d.CID || proof.Owner != owner || proof.BoundarySHA != sum {
			return b, ErrConflict
		}
		page, e := c.page(namespaceOwnerAdmissionProof{PageID: proof.PageID, PageSHA: proof.PageSHA, HeaderSHA: proof.HeaderSHA, InventorySHA: proof.InventorySHA})
		if e != nil {
			return b, e
		}
		for _, w := range page.EffectiveBoundaries {
			actual, e := namespaceimport.EffectiveBoundarySHA256(w.Boundary)
			if e != nil {
				return b, e
			}
			if actual == sum {
				witness = w
				found++
			}
		}
	} else if errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
		// Compatibility requires the ORIGINAL signed witness custody. No invented
		// page, current window, or arbitrary operator hash substitutes for it.
		if p.BoundaryWitnessDirectory == "" {
			return b, &namespaceimport.OriginalHistoryProofPending{Reason: "boundary-witness-unavailable", Evidence: c.evidence()}
		}
		body, e := c.readPrivateProof(filepath.Join(p.BoundaryWitnessDirectory, namespaceimport.EffectiveOwnerKey(d.CID, owner)+".json"), 1<<20)
		if e != nil {
			return b, &namespaceimport.OriginalHistoryProofPending{Reason: "boundary-witness-unavailable", Evidence: c.evidence()}
		}
		if namespaceinventory.DecodeStrict(body, &witness) != nil {
			return b, ErrConflict
		}
		found = 1
	} else {
		return b, e
	}
	if found != 1 || directHistoryDigest(witness.Boundary) != directHistoryDigest(b) || namespaceimport.CoverageSHA256(witness.Coverage.Coverage) != r.CoverageSHA256 || verifyOriginalBoundaryWitness(p.Header, p.Inventory, witness, d, owner) != nil {
		return b, ErrConflict
	}
	if e = c.publication(); e != nil {
		return b, e
	}
	return b, nil
}

func (c *originalHistoryChain) prospective(raw bson.Raw, d NamespaceOriginDescriptor, owner string) ([][2]int64, error) {
	var f namespaceOwnerProspective
	if !ownerProspectiveShape(raw) || bson.Unmarshal(raw, &f) != nil || f.ID != namespaceOwnerProspectiveID(c.trust.Epoch, d.CID, owner) || f.Epoch != c.trust.Epoch || f.CID != d.CID || f.Owner != owner || f.OriginSHA != d.SHA256 || !validSHA(f.BaseSHA) || f.SeedMaximum < 0 || f.SeedMaximum >= math.MaxInt64-1 || f.Lower != f.SeedMaximum+1 || f.Upper < 0 || f.Upper >= math.MaxInt64 || f.Upper != 0 && f.Upper < f.Lower || f.Revision < 1 || f.Revision >= math.MaxInt64 || f.HistoricalFloor < 0 {
		return nil, ErrConflict
	}
	head, e := c.counter(d.CID)
	if e != nil {
		return nil, e
	}
	if f.SeedMaximum != head.SeedMaximum || f.Upper > head.Maximum {
		return nil, ErrConflict
	}
	proofRaw, e := c.read(Receipts, namespaceOwnerAdmissionID(c.trust.Epoch, d.CID, owner))
	if e != nil {
		return nil, e
	}
	var p namespaceOwnerAdmissionProof
	var a namespaceimport.ProspectiveOwnerAdmission
	if !ownerAdmissionProofShape(proofRaw) || bson.Unmarshal(proofRaw, &p) != nil || p.ID != namespaceOwnerAdmissionID(c.trust.Epoch, d.CID, owner) || p.Kind != "namespace-owner-admission-v1" || p.Epoch != f.Epoch || p.CID != d.CID || p.Owner != owner || p.OriginSHA != d.SHA256 || p.BaseSHA != f.BaseSHA || p.AdmissionJSON != f.AdmissionJSON || p.AdmissionSHA != f.AdmissionSHA || decodeImportJSON(p.AdmissionJSON, &a) != nil || a.ValidateShape() != nil || directHistoryDigest(a) != p.AdmissionSHA || a.CID != d.CID || a.OwnerID != owner || a.OriginSHA256 != d.SHA256 || a.SeedMaximum != f.SeedMaximum || a.Lower != f.Lower {
		return nil, ErrConflict
	}
	switch a.Kind {
	case "held-cut-roster-v1":
		if p.SourceWindowJSON != "" || p.SourceWindowSHA != "" {
			return nil, ErrConflict
		}
		page, e := c.page(p)
		if e != nil {
			return nil, e
		}
		matches := 0
		for _, w := range page.ProspectiveAdmissions {
			if directHistoryDigest(w.Admission) == p.AdmissionSHA {
				if w.ValidateBinding(c.trust.Import.Header, c.trust.Import.Inventory) != nil {
					return nil, ErrConflict
				}
				matches++
			}
		}
		if matches != 1 {
			return nil, ErrConflict
		}
	case "actual-positive-source-v1":
		if p.HeaderSHA != "" || p.InventorySHA != "" || p.PageSHA != "" || p.PageID != "" {
			return nil, ErrConflict
		}
		if e = c.receipt(d, a.SourceID, a.BranchID, a.InputSHA256, a.ReceiptSHA256, a.Lower, 0); e != nil {
			return nil, e
		}
		if d.Kind == "group" {
			var window MembershipWindow
			if decodeImportJSON(p.SourceWindowJSON, &window) != nil || hash([]byte(p.SourceWindowJSON)) != p.SourceWindowSHA || !c.decoder.validMembershipWindow(window, d.CID, owner) || window.Closed || window.GroupID != d.GroupID || window.Lower > a.Lower {
				return nil, ErrConflict
			}
			original, e := c.read(Receipts, key(c.trust.Epoch, "new-group-seed", window.LastCommand))
			if e != nil {
				return nil, e
			}
			seed, e := c.decoder.decodeNewGroupSeed(original)
			if e != nil || seed.CommandID != window.LastCommand || seed.GroupID != d.GroupID || seed.EvidenceSHA256 != window.SeedEvidenceSHA256 {
				return nil, ErrConflict
			}
		} else if p.SourceWindowJSON != "" || p.SourceWindowSHA != "" {
			return nil, ErrConflict
		}
	default:
		return nil, ErrConflict
	}
	if f.Upper == 0 {
		if f.SourceID != "" || f.BranchID != "" || f.InputSHA != "" || f.ReceiptSHA != "" {
			return nil, ErrConflict
		}
	} else if e = c.receipt(d, f.SourceID, f.BranchID, f.InputSHA, f.ReceiptSHA, f.Lower, f.Upper); e != nil {
		return nil, e
	}
	if f.HistoricalFloor == 0 {
		if f.RevocationCommand != "" || f.RevocationSHA != "" {
			return nil, ErrConflict
		}
	} else if e = c.oldRevocation(d, owner, f); e != nil {
		return nil, e
	}
	intervals := [][2]int64{}
	cut, e := c.cutover()
	if e != nil {
		return nil, e
	}
	originalHeader := namespaceimport.Header{Epoch: c.trust.Epoch, MigrationID: cut.MigrationID}
	boundaryRaw, e := c.read(Receipts, importBoundaryID(originalHeader, d.CID, owner))
	if e == nil {
		b, e := c.boundary(boundaryRaw, d, owner)
		if e != nil {
			return nil, e
		}
		var bound namespaceImportBoundary
		if bson.Unmarshal(boundaryRaw, &bound) != nil || bound.WindowSHA256 != f.BaseSHA {
			return nil, ErrConflict
		}
		// Return the SAME canonical source identity/evidence as independent base
		// enumeration. Never deduplicate different proofs by bare CID/range.
		_ = b
		c.canonicalBaseID = bound.ID
	} else if errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
		if d.Kind == "group" {
			if f.BaseSHA != directHistoryDigest([]string{"namespace-no-historical-base-v1", c.trust.Epoch, d.CID, owner, d.SHA256}) {
				return nil, ErrConflict
			}
		} else {
			baseRaw, e := c.read(Sequences, directNamespaceWindowID(c.trust.Epoch, d.CID, owner))
			if e != nil {
				return nil, e
			}
			var b directNamespaceWindow
			if !exactFields(baseRaw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "header_sha256", "boundary_sha256"}, []string{"lower", "upper"}, []string{"closed"}, "") || bson.Unmarshal(baseRaw, &b) != nil || hash(baseRaw) != f.BaseSHA || b.ID != directNamespaceWindowID(c.trust.Epoch, d.CID, owner) || b.Epoch != c.trust.Epoch || b.ConversationID != d.CID || b.UserID != owner || b.OriginSHA256 != d.SHA256 || !b.Closed || b.Lower != 0 || b.Upper != 0 {
				return nil, ErrConflict
			}
			if a.Kind == "held-cut-roster-v1" {
				if b.Kind != directHeldOwnerKind || b.HeaderSHA256 != a.HeaderSHA256 || b.BoundarySHA256 != p.AdmissionSHA {
					return nil, ErrConflict
				}
			} else {
				if b.Kind != directSourceOwnerKind || b.HeaderSHA256 != c.trust.CutoverSHA256 || b.BoundarySHA256 != directHistoryDigest([]string{a.SourceID, a.BranchID, a.ReceiptSHA256, owner}) {
					return nil, ErrConflict
				}
			}
		}
	} else {
		return nil, e
	}
	upper := f.Upper
	if d.Kind == "group" {
		command := cut.MigrationID
		held := a.Kind == "held-cut-roster-v1"
		if !held {
			var original MembershipWindow
			if decodeImportJSON(p.SourceWindowJSON, &original) != nil {
				return nil, ErrConflict
			}
			command = original.LastCommand
		}
		upper, e = groupHistoryBound(c.trust.Epoch, d, owner, command, f.Lower, head.Maximum, held, f.Upper, c.read)
		if e != nil {
			return nil, e
		}
	}
	intervals = append(intervals, [2]int64{max(f.Lower, f.HistoricalFloor), upper})
	return intervals, nil
}

func (c *originalHistoryChain) generation(d NamespaceOriginDescriptor, owner, id, sum string) (ownerHistoryGeneration, error) {
	var g ownerHistoryGeneration
	raw, e := c.read(Receipts, id)
	if e != nil {
		return g, e
	}
	if !historyGenerationShape(raw) || bson.Unmarshal(raw, &g) != nil || g.ID != id || g.ID != historyGenerationID(c.trust.Epoch, d.CID, owner, g.Command) || g.Kind != "namespace-owner-history-generation-v1" && g.Kind != "namespace-owner-created-history-generation-v1" || g.Epoch != c.trust.Epoch || g.CID != d.CID || g.Owner != owner || d.Kind != "group" || g.GroupID != d.GroupID || g.OriginSHA != d.SHA256 || directHistoryDigest(g) != sum || !validUUID(g.Command) || g.Head < 0 || g.Head >= math.MaxInt64-1 || g.Lower < 0 || g.Lower >= math.MaxInt64 || hash([]byte(g.CounterJSON)) != g.CounterSHA {
		return g, ErrConflict
	}
	var original counter
	if decodeImportJSON(g.CounterJSON, &original) != nil || original.ID != key(c.trust.Epoch, "sequence", d.CID) || original.Epoch != c.trust.Epoch || original.CID != d.CID || original.Maximum != g.Head || original.SeedMaximum < 0 || original.SeedMaximum > g.Head || original.Revision < 1 || !validSHA(original.SeedEvidenceSHA) {
		return g, ErrConflict
	}
	current, e := c.counter(d.CID)
	if e != nil {
		return g, e
	}
	if current.Maximum < g.Head || current.SeedMaximum != original.SeedMaximum || current.SeedEvidenceSHA != original.SeedEvidenceSHA || current.Revision < original.Revision {
		return g, ErrConflict
	}
	if g.Kind == "namespace-owner-created-history-generation-v1" {
		return g, createdHistoryMember(g, d, owner, c.trust.ActivationSHA256, c.read)
	}
	lower, e := c.admissionMarker(d, owner, g.Command, g.ReceiptSHA, g.MarkerSHA, g.Forbid)
	if e != nil {
		return g, e
	}
	if lower != g.Lower || g.Forbid && lower < g.Head+1 {
		return g, ErrConflict
	}
	return g, nil
}
func (c *originalHistoryChain) admissionMarker(d NamespaceOriginDescriptor, owner, command, receiptSHA, markerSHA string, forbid bool) (int64, error) {
	raw, e := c.read(Receipts, key(c.trust.Epoch, "member-admission", command))
	if e != nil {
		return 0, e
	}
	r, e := c.decoder.decodeAdmissionReceipt(raw)
	if e != nil || r.CommandID != command || r.GroupID != d.GroupID || r.ForbidHistory != forbid || directHistoryDigest(r) != receiptSHA {
		return 0, ErrConflict
	}
	raw, e = c.read(Receipts, key(c.trust.Epoch, "member-admission-owner", command+"\x00"+owner))
	if e != nil {
		return 0, e
	}
	var m memberAdmissionOwner
	if !exactFields(raw, []string{"_id", "kind", "epoch", "command_id", "input_sha256", "group_id", "user_id", "normal_cid", "notification_cid"}, []string{"group_revision", "normal_revision", "notification_revision", "normal_lower", "notification_lower"}, nil, "") || bson.Unmarshal(raw, &m) != nil || m.ID != key(c.trust.Epoch, "member-admission-owner", command+"\x00"+owner) || m.Kind != "member-admission-owner-v1" || m.Epoch != c.trust.Epoch || m.CommandID != command || m.GroupID != d.GroupID || m.UserID != owner || m.InputSHA256 != r.InputSHA256 || m.GroupRevision != r.GroupRevision || m.NormalRevision < 1 || m.NotificationRevision < 1 || markerSHA != "" && directHistoryDigest(m) != markerSHA {
		return 0, ErrConflict
	}
	normal, notice, e := groupNamespacePair(d.GroupID)
	if e != nil || m.NormalCID != normal || m.NotificationCID != notice {
		return 0, ErrConflict
	}
	if d.CID == normal {
		return m.NormalLower, nil
	}
	if d.CID == notice {
		return m.NotificationLower, nil
	}
	return 0, ErrConflict
}
func (c *originalHistoryChain) oldRevocation(d NamespaceOriginDescriptor, owner string, f namespaceOwnerProspective) error {
	if d.Kind != "group" || f.HistoricalFloor < 1 || !validUUID(f.RevocationCommand) || !validSHA(f.RevocationSHA) {
		return ErrConflict
	}
	lower, e := c.admissionMarker(d, owner, f.RevocationCommand, f.RevocationSHA, "", true)
	if e != nil {
		return e
	}
	if lower != f.HistoricalFloor {
		return ErrConflict
	}
	return nil
}
func (c *originalHistoryChain) interval(raw bson.Raw, d NamespaceOriginDescriptor, owner string) ([][2]int64, error) {
	var r ownerHistoryInterval
	if !historyIntervalShape(raw) || bson.Unmarshal(raw, &r) != nil || r.Epoch != c.trust.Epoch || r.CID != d.CID || r.Owner != owner || r.OriginSHA != d.SHA256 || !validSHA(r.ProofSHA) || r.Lower < 1 || r.Upper < 0 || r.Upper >= math.MaxInt64 || r.Upper != 0 && r.Upper < r.Lower || r.Revision < 1 {
		return nil, ErrConflict
	}
	if r.Kind == "namespace-owner-imported-history-v1" {
		proofRaw, e := c.read(Receipts, r.ProofID)
		if e != nil {
			return nil, e
		}
		var p ownerHistoryImportProof
		var s namespaceimport.OwnerHistorySegment
		if !historyImportProofShape(proofRaw) || bson.Unmarshal(proofRaw, &p) != nil || directHistoryDigest(p) != r.ProofSHA || p.ID != r.ProofID || p.Kind != "namespace-owner-history-import-proof-v1" || p.Epoch != c.trust.Epoch || p.CID != d.CID || p.Owner != owner || p.OriginSHA != d.SHA256 || decodeImportJSON(p.SegmentJSON, &s) != nil || s.ValidateShape() != nil || aSegmentSHA(s) != p.SegmentSHA || p.ID != importHistoryProofID(c.trust.Epoch, p.SegmentSHA) || s.SourceEpoch == c.trust.Epoch || s.CID != d.CID || s.OwnerID != owner || s.OriginSHA256 != d.SHA256 || r.ID != historyIntervalID(c.trust.Epoch, r.Kind, d.CID, owner, p.SegmentSHA) || r.Lower != s.Lower || r.Upper != s.Upper || r.Revision != 1 || r.SourceID != "" || r.BranchID != "" || r.InputSHA != "" || r.ReceiptSHA != "" {
			return nil, ErrConflict
		}
		page, e := c.page(namespaceOwnerAdmissionProof{PageID: p.PageID, PageSHA: p.PageSHA, HeaderSHA: p.HeaderSHA, InventorySHA: p.InventorySHA})
		if e != nil {
			return nil, e
		}
		matches := 0
		for _, w := range page.OwnerHistorySegments {
			if aSegmentSHA(w.Segment) == p.SegmentSHA {
				if w.ValidateBinding(c.trust.Import.Header, c.trust.Import.Inventory) != nil {
					return nil, ErrConflict
				}
				matches++
			}
		}
		if matches != 1 {
			return nil, ErrConflict
		}
		// ScopeContract is INSIDE the original SegmentSHA, actual signed Inventory
		// witness, known page receipt and exact publication checked above. It is
		// never learned from a mutable raw interval or a later signature/JSON flag.
		if s.ScopeContract != "" && d.Kind != "group" {
			return nil, ErrConflict
		}
		c.scopeContract = s.ScopeContract

	} else {
		g, e := c.generation(d, owner, r.ProofID, r.ProofSHA)
		if e != nil {
			return nil, e
		}
		if r.ID != historyIntervalID(c.trust.Epoch, r.Kind, d.CID, owner, g.Command) {
			return nil, ErrConflict
		}
		switch r.Kind {
		case "namespace-owner-history-admission-v1":
			if g.Forbid || r.Lower != max(g.Lower, int64(1)) || r.Upper != g.Head || r.Revision != 1 || r.SourceID != "" || r.BranchID != "" || r.InputSHA != "" || r.ReceiptSHA != "" {
				return nil, ErrConflict
			}
		case "namespace-owner-live-history-v1":
			if r.Lower != max(g.Head+1, g.Lower) {
				return nil, ErrConflict
			}
			if r.Upper == 0 {
				if r.SourceID != "" || r.BranchID != "" || r.InputSHA != "" || r.ReceiptSHA != "" || r.Revision != 1 {
					return nil, ErrConflict
				}
			} else if e = c.receipt(d, r.SourceID, r.BranchID, r.InputSHA, r.ReceiptSHA, r.Lower, r.Upper); e != nil {
				return nil, e
			}
			head, e := c.counter(d.CID)
			if e != nil {
				return nil, e
			}
			r.Upper, e = groupHistoryBound(c.trust.Epoch, d, owner, g.Command, r.Lower, head.Maximum, false, r.Upper, c.read)
			if e != nil {
				return nil, e
			}
		default:
			return nil, ErrConflict
		}
	}
	return [][2]int64{{r.Lower, r.Upper}}, nil
}

func (c *originalHistoryChain) floor(d NamespaceOriginDescriptor, owner string) (int64, error) {
	floor := int64(0)
	for _, uid := range []string{"", owner} {
		id := unrelation.MessageVisibilityID(d.CID, uid)
		raw, e := c.read(unrelation.MessageVisibilityCollection, id)
		if errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
			continue
		}
		if e != nil {
			return 0, e
		}
		var f table.MessageVisibilityFloor
		if !exactFields(raw, []string{"_id", "conversation_id", "user_id"}, []string{"min_seq"}, nil, "updated_at") || bson.Unmarshal(raw, &f) != nil || f.ID != id || f.ConversationID != d.CID || f.UserID != uid || f.MinSeq < 0 {
			return 0, ErrConflict
		}
		floor = max(floor, f.MinSeq)
	}
	if d.Kind != "group" {
		// Canonical base evidence also contains an existing prospective locator,
		// including direct owners. Its import Lower still does not revoke base.
		raw, e := c.read(Sequences, namespaceOwnerProspectiveID(c.trust.Epoch, d.CID, owner))
		if e == nil {
			var f namespaceOwnerProspective
			if !ownerProspectiveShape(raw) || bson.Unmarshal(raw, &f) != nil || f.ID != namespaceOwnerProspectiveID(c.trust.Epoch, d.CID, owner) || f.Epoch != c.trust.Epoch || f.CID != d.CID || f.Owner != owner || f.OriginSHA != d.SHA256 || f.HistoricalFloor != 0 || f.RevocationCommand != "" || f.RevocationSHA != "" {
				return 0, ErrConflict
			}
		} else if !errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
			return 0, e
		}
		return floor, nil
	}
	raw, e := c.read(Sequences, historyFloorID(c.trust.Epoch, d.CID, owner))
	if e == nil {
		var f ownerHistoryFloor
		if !historyFloorShape(raw) || bson.Unmarshal(raw, &f) != nil || f.ID != historyFloorID(c.trust.Epoch, d.CID, owner) || f.Kind != "namespace-owner-historical-floor-v1" || f.Epoch != c.trust.Epoch || f.CID != d.CID || f.Owner != owner || f.OriginSHA != d.SHA256 || f.Floor < 1 || f.Revision < 1 {
			return 0, ErrConflict
		}
		g, e := c.generation(d, owner, f.ProofID, f.ProofSHA)
		if e != nil {
			return 0, e
		}
		if !g.Forbid || g.Lower != f.Floor {
			return 0, ErrConflict
		}
		floor = max(floor, f.Floor)
	} else if !errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
		return 0, e
	}
	// The first implementation stored the revocation alongside the original
	// prospective row. It must also clip independently enumerated old segments.
	raw, e = c.read(Sequences, namespaceOwnerProspectiveID(c.trust.Epoch, d.CID, owner))
	if e == nil {
		var f namespaceOwnerProspective
		if !ownerProspectiveShape(raw) || bson.Unmarshal(raw, &f) != nil || f.ID != namespaceOwnerProspectiveID(c.trust.Epoch, d.CID, owner) || f.Epoch != c.trust.Epoch || f.CID != d.CID || f.Owner != owner || f.OriginSHA != d.SHA256 || f.HistoricalFloor < 0 {
			return 0, ErrConflict
		}
		if f.HistoricalFloor > 0 {
			if e = c.oldRevocation(d, owner, f); e != nil {
				return 0, e
			}
			floor = max(floor, f.HistoricalFloor)
		} else if f.RevocationCommand != "" || f.RevocationSHA != "" {
			return 0, ErrConflict
		}
	} else if !errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
		return 0, e
	}
	return floor, nil
}
func (c *originalHistoryChain) pending(e error) error {
	if errors.Is(e, ErrUnprepared) || errors.Is(e, namespaceimport.ErrOriginalHistoryRecordAbsent) {
		return &namespaceimport.OriginalHistoryProofPending{Reason: "original-chain-unavailable", Evidence: c.evidence()}
	}
	return e
}

func (c *originalHistoryChain) notice(raw bson.Raw, d NamespaceOriginDescriptor, owner string) ([][2]int64, error) {
	if d.Kind != "group" {
		return nil, ErrConflict
	}
	entitlement, e := c.decoder.decodeNoticeEntitlement(raw)
	if e != nil || entitlement.ConversationID != d.CID || entitlement.UserID != owner {
		return nil, ErrConflict
	}
	pendingRaw, e := c.read(Receipts, entitlement.PendingID)
	if e != nil {
		return nil, e
	}
	pending, e := c.decoder.decodeNoticePending(pendingRaw)
	if e != nil || pending.ID != entitlement.PendingID || canonicalRecordSHA(pending) != entitlement.PendingSHA256 || pending.UserID != owner || pending.ConversationID != d.CID || !pending.Persist || entitlement.Seq <= pending.Upper || entitlement.Seq < pending.Lower {
		return nil, ErrConflict
	}
	headerRaw, e := c.read(Receipts, pending.HeaderID)
	if e != nil {
		return nil, e
	}
	header, e := c.decoder.decodeNoticeHeader(headerRaw)
	if e != nil || header.ID != pending.HeaderID || canonicalRecordSHA(header) != pending.HeaderSHA256 || header.GroupID != d.GroupID || pending.ID != noticePendingID(c.trust.Epoch, header.EffectID, pending.BranchID, owner) || entitlement.ReceiptID != key(c.trust.Epoch, "source", header.EffectID) {
		return nil, ErrConflict
	}
	index := sort.SearchStrings(header.Owners, owner)
	if index == len(header.Owners) || header.Owners[index] != owner {
		return nil, ErrConflict
	}
	removalRaw, e := c.read(Receipts, key(c.trust.Epoch, "membership-removal", header.CommandID))
	if e != nil {
		return nil, e
	}
	removal, e := decodeRemovalReceipt(removalRaw)
	if e != nil || c.decoder.noticeHeaderMatchesRemoval(header, removal) != nil {
		return nil, ErrConflict
	}
	upper := removal.Upper
	if d.CID == removal.NotificationCID {
		upper = removal.NotificationUpper
	} else if d.CID != removal.ConversationID {
		return nil, ErrConflict
	}
	if pending.Upper != upper {
		return nil, ErrConflict
	}
	sourceRaw, e := c.read(Receipts, entitlement.ReceiptID)
	if e != nil {
		return nil, e
	}
	source, e := c.decoder.decodeNoticeSourceReceipt(sourceRaw)
	if e != nil || source.ID != entitlement.ReceiptID || source.SourceID != header.EffectID || canonicalRecordSHA(source) != entitlement.ReceiptSHA256 {
		return nil, ErrConflict
	}
	duty, e := c.read(Effects, key(c.trust.Epoch, "branch", pending.BranchID))
	if e != nil {
		return nil, e
	}
	// The whole BSON was duplicate-key checked by read(); exactly the original
	// projected metadata is interpreted here, not message bodies or fresh roster.
	for k, want := range map[string]string{"_id": key(c.trust.Epoch, "branch", pending.BranchID), "epoch": c.trust.Epoch, "source_id": header.EffectID, "branch_id": pending.BranchID, "conversation_id": d.CID, "input_sha256": source.InputSHA256, "action_input_payload_sha256": pending.PayloadSHA256, "action_profile_sha256": header.ProfileSHA256, "producer_version": ownedGroupProducer} {
		got, ok := duty.Lookup(k).StringValueOK()
		if !ok || got != want {
			return nil, ErrConflict
		}
	}
	seq, ok := duty.Lookup("seq").Int64OK()
	persist, pok := duty.Lookup("persist").BooleanOK()
	if !ok || seq != entitlement.Seq || !pok || !persist {
		return nil, ErrConflict
	}
	found := false
	for _, a := range source.Branches {
		if a.ID == pending.BranchID {
			found = a.Persist && !a.Hidden && a.ConversationID == d.CID && a.Seq == seq
		}
	}
	if !found {
		return nil, ErrConflict
	}
	c.scopeContract = namespaceimport.OriginalGroupSequenceScopeV1
	return [][2]int64{{seq, seq}}, nil
}
