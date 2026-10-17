package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/openimsdk/open-im-server/v3/internal/groupauthority"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// One bounded original page, not one copy per owner or a whole roster in BSON.
// The ordinary immutable page receipt commits with these source bodies/rows.
type namespaceProspectivePageSource struct {
	ID            string `bson:"_id"`
	Kind          string `bson:"kind"`
	HeaderJSON    string `bson:"header_json"`
	HeaderSHA     string `bson:"header_sha256"`
	InventoryJSON string `bson:"inventory_json"`
	InventorySHA  string `bson:"inventory_sha256"`
	PageJSON      string `bson:"page_json"`
	PageSHA       string `bson:"page_sha256"`
	PageIndex     int64  `bson:"page_index"`
}

func prospectivePageSourceID(h namespaceimport.Header, index uint64) string {
	return key(h.Epoch, "namespace-import-prospective-page", fmt.Sprintf("%s:%d", h.MigrationID, index))
}
func (s *Store) readBoundImportPage(ctx context.Context, proof namespaceOwnerAdmissionProof) (namespaceimport.Header, namespaceimport.Inventory, namespaceimport.PageData, error) {
	var raw bson.Raw
	var row namespaceProspectivePageSource
	var h namespaceimport.Header
	var inventory namespaceimport.Inventory
	var page namespaceimport.PageData
	e := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": proof.PageID}).Decode(&raw)
	if e != nil {
		return h, inventory, page, errors.Join(ErrUnprepared, e)
	}
	if !exactFields(raw, []string{"_id", "kind", "header_json", "header_sha256", "inventory_json", "inventory_sha256", "page_json", "page_sha256"}, []string{"page_index"}, nil, "") || bson.Unmarshal(raw, &row) != nil || decodeImportJSON(row.HeaderJSON, &h) != nil || decodeImportJSON(row.InventoryJSON, &inventory) != nil || decodeImportJSON(row.PageJSON, &page) != nil || h.Validate() != nil || h.Epoch != s.epoch || h.ActivationSHA256 != s.activationSHA || row.Kind != "namespace-import-prospective-page-v1" || row.ID != prospectivePageSourceID(h, page.Index) || row.ID != proof.PageID || row.HeaderSHA != h.SHA256() || row.HeaderSHA != proof.HeaderSHA || row.InventorySHA != inventory.SHA256() || row.InventorySHA != proof.InventorySHA || inventory.HeaderSHA256 != h.SHA256() || row.PageSHA != directHistoryDigest(page) || row.PageSHA != proof.PageSHA || row.PageIndex != int64(page.Index) || page.HeaderSHA256 != h.SHA256() || page.InventoryBindingSHA256 != inventory.BindingSHA256() || page.RawSourceRootSHA256 != inventory.RawRootSHA256 {
		return h, inventory, page, ErrConflict
	}
	var committed namespaceImportPage
	var committedRaw bson.Raw
	if e = s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importPageID(h, page.Index)}).Decode(&committedRaw); e != nil {
		return h, inventory, page, errors.Join(ErrUnprepared, e)
	}
	if !exactFields(committedRaw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "page_sha256", "previous_sha256", "receipt_json", "receipt_sha256", "attempt_id", "lease_id", "fence_sha256"}, []string{"page_index"}, nil, "") || bson.Unmarshal(committedRaw, &committed) != nil || committed.ID != importPageID(h, page.Index) || committed.Kind != "namespace-import-page-v1" || committed.HeaderSHA256 != h.SHA256() || committed.InventorySHA256 != inventory.SHA256() || committed.PageSHA256 != row.PageSHA || committed.PageIndex != row.PageIndex || committed.PreviousSHA256 != page.PreviousSHA256 {
		return h, inventory, page, ErrConflict
	}
	var pageReceipt namespaceimport.PageReceipt
	if decodeImportJSON(committed.ReceiptJSON, &pageReceipt) != nil || pageReceipt.Page != page.Identity() || pageReceipt.HeaderSHA256 != h.SHA256() || pageReceipt.InventorySHA256 != inventory.SHA256() || pageReceipt.ReceiptSHA256 != committed.ReceiptSHA256 || namespaceimport.ReceiptDigest(pageReceipt) != committed.ReceiptSHA256 {
		return h, inventory, page, ErrConflict
	}
	return h, inventory, page, nil
}

func (s *Store) readProspectivePageSource(ctx context.Context, proof namespaceOwnerAdmissionProof) (namespaceimport.Header, namespaceimport.Inventory, namespaceimport.PageData, error) {
	h, inventory, page, e := s.readBoundImportPage(ctx, proof)
	if e != nil {
		return h, inventory, page, e
	}
	matches := 0
	for _, w := range page.ProspectiveAdmissions {
		sum, e := namespaceimport.ProspectiveAdmissionSHA256(w.Admission)
		if e != nil {
			return h, inventory, page, e
		}
		if sum == proof.AdmissionSHA {
			if w.ValidateBinding(h, inventory) != nil || w.Admission.CID != proof.CID || w.Admission.OwnerID != proof.Owner || w.Admission.OriginSHA256 != proof.OriginSHA || directHistoryDigest(w.Admission) != hash([]byte(proof.AdmissionJSON)) {
				return h, inventory, page, ErrConflict
			}
			matches++
		}
	}
	if matches != 1 {
		return h, inventory, page, ErrConflict
	}
	return h, inventory, page, nil
}

// Called only by ApplyPage's original private Permit/VerifiedPage transaction.
// A bare Admission or its SHA can never call this writer port.
func (w *NamespaceImportWriter) stageProspectiveConsumer(ctx context.Context, permit *namespaceimport.Permit, page namespaceimport.VerifiedPage) error {
	if e := w.requireImportPage(ctx, permit, page); e != nil {
		return e
	}
	data := page.Data()
	inventory := page.Inventory()
	if len(data.ProspectiveAdmissions) == 0 {
		return nil
	}
	hJSON, e := json.Marshal(w.header)
	if e != nil {
		return e
	}
	iJSON, e := json.Marshal(inventory)
	if e != nil {
		return e
	}
	pJSON, e := json.Marshal(data)
	if e != nil {
		return e
	}
	pageRow := namespaceProspectivePageSource{ID: prospectivePageSourceID(w.header, data.Index), Kind: "namespace-import-prospective-page-v1", HeaderJSON: string(hJSON), HeaderSHA: w.header.SHA256(), InventoryJSON: string(iJSON), InventorySHA: page.InventorySHA256(), PageJSON: string(pJSON), PageSHA: page.SHA256(), PageIndex: int64(data.Index)}
	if e = immutableImportDocument(ctx, w.store.db.Collection(Receipts), pageRow.ID, pageRow); e != nil {
		return e
	}
	for _, witness := range page.ProspectiveAdmissions() {
		a := witness.Admission
		if a.Kind != "held-cut-roster-v1" || a.HeaderSHA256 != w.header.SHA256() || a.InventoryBindingSHA256 != inventory.BindingSHA256() || witness.ValidateBinding(w.header, inventory) != nil {
			return ErrConflict
		}
		if e = w.requireImportSetOpen(ctx, "prospective", ""); e != nil {
			return e
		}
		d, e := w.store.readNamespacePrincipals(ctx, a.CID)
		if e != nil {
			return e
		}
		if d.SHA256 != a.OriginSHA256 {
			return ErrConflict
		}
		if d.Kind != "group" {
			participant := false
			for _, uid := range d.OriginIDs {
				participant = participant || uid == a.OwnerID
			}
			if !participant {
				return ErrConflict
			}
		}
		head, e := w.store.directHistoryCounter(ctx, a.CID)
		if e != nil {
			return e
		}
		if head.SeedMaximum != a.SeedMaximum || head.Maximum != head.SeedMaximum || a.Lower != head.Maximum+1 {
			return ErrConflict
		}
		admissionSHA, e := namespaceimport.ProspectiveAdmissionSHA256(a)
		if e != nil {
			return e
		}
		admissionJSON, e := json.Marshal(a)
		if e != nil {
			return e
		}
		baseSHA, e := w.prospectiveBase(ctx, page, d, a, admissionSHA)
		if e != nil {
			return e
		}
		proof := namespaceOwnerAdmissionProof{ID: namespaceOwnerAdmissionID(w.header.Epoch, a.CID, a.OwnerID), Kind: "namespace-owner-admission-v1", Epoch: w.header.Epoch, CID: a.CID, Owner: a.OwnerID, OriginSHA: a.OriginSHA256, BaseSHA: baseSHA, AdmissionJSON: string(admissionJSON), AdmissionSHA: admissionSHA, HeaderSHA: w.header.SHA256(), InventorySHA: page.InventorySHA256(), PageID: pageRow.ID, PageSHA: page.SHA256()}
		if _, e = w.store.db.Collection(Receipts).InsertOne(ctx, proof); e != nil {
			return e
		}
		row := namespaceOwnerProspective{ID: namespaceOwnerProspectiveID(w.header.Epoch, a.CID, a.OwnerID), Kind: "namespace-owner-prospective-v1", Epoch: w.header.Epoch, CID: a.CID, Owner: a.OwnerID, OriginSHA: a.OriginSHA256, BaseSHA: baseSHA, AdmissionJSON: proof.AdmissionJSON, AdmissionSHA: admissionSHA, SeedMaximum: a.SeedMaximum, Lower: a.Lower, Revision: 1}
		if _, e = w.store.db.Collection(Sequences).InsertOne(ctx, row); e != nil {
			return e
		}
		leaf := namespaceImportSetLeaf{ID: importSetPrefix(w.header, "prospective", "") + admissionSHA, Kind: "namespace-import-set-leaf-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), ConversationID: a.CID, ValueSHA256: admissionSHA, ReferenceID: proof.ID}
		if _, e = w.store.db.Collection(Receipts).InsertOne(ctx, leaf); e != nil {
			return e
		}
	}
	return nil
}
func (w *NamespaceImportWriter) prospectiveBase(ctx context.Context, page namespaceimport.VerifiedPage, d NamespaceOriginDescriptor, a namespaceimport.ProspectiveOwnerAdmission, admissionSHA string) (string, error) {
	var b namespaceImportBoundary
	e := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importBoundaryID(w.header, d.CID, a.OwnerID)}).Decode(&b)
	if e == nil {
		ownerKey := namespaceimport.EffectiveOwnerKey(d.CID, a.OwnerID)
		leaf := namespaceImportSetLeaf{ID: importSetPrefix(w.header, "owners", d.CID) + ownerKey, Kind: "namespace-import-set-leaf-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), ConversationID: d.CID, ValueSHA256: ownerKey, ReferenceID: b.ID}
		if e = w.validateImportSetLeaf(ctx, leaf, "owners", d.CID, page.InventorySHA256()); e != nil {
			return "", e
		}
		return b.WindowSHA256, nil
	}
	if !errors.Is(e, mongo.ErrNoDocuments) {
		return "", e
	}
	if d.Kind == "group" {
		// No historical boundary is minted. A closed-empty physical marker lets
		// the verified prospective reader distinguish live future eligibility from
		// the absence of any proven old history. Its Lower is NOT import head+1.
		revision, e := w.prospectiveGroupRevision(ctx, d.GroupID)
		if e != nil {
			return "", e
		}
		empty := MembershipWindow{ID: key(w.header.Epoch, "membership-window", d.CID+"\x00"+a.OwnerID), Kind: "membership-window-v1", Epoch: w.header.Epoch, ConversationID: d.CID, GroupID: d.GroupID, UserID: a.OwnerID, Closed: true, Revision: 1, GroupRevision: revision, LastCommand: w.header.MigrationID, SeedEvidenceSHA256: admissionSHA}
		if !w.store.validMembershipWindow(empty, d.CID, a.OwnerID) {
			return "", ErrConflict
		}
		if e = immutableImportDocument(ctx, w.store.db.Collection(Sequences), empty.ID, empty); e != nil {
			return "", e
		}
		return directHistoryDigest([]string{"namespace-no-historical-base-v1", w.header.Epoch, d.CID, a.OwnerID, d.SHA256}), nil
	}
	b0 := directNamespaceWindow{ID: directNamespaceWindowID(w.header.Epoch, d.CID, a.OwnerID), Kind: directHeldOwnerKind, Epoch: w.header.Epoch, ConversationID: d.CID, UserID: a.OwnerID, OriginSHA256: d.SHA256, Closed: true, HeaderSHA256: w.header.SHA256(), BoundarySHA256: admissionSHA}
	if e = immutableImportDocument(ctx, w.store.db.Collection(Sequences), b0.ID, b0); e != nil {
		return "", e
	}
	raw, e := bson.Marshal(b0)
	if e != nil {
		return "", e
	}
	return hash(raw), nil
}
func (w *NamespaceImportWriter) validateProspectiveLeaf(ctx context.Context, leaf namespaceImportSetLeaf, inventory string) error {
	var proof namespaceOwnerAdmissionProof
	var raw bson.Raw
	if e := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": leaf.ReferenceID}).Decode(&raw); e != nil {
		return e
	}
	if !ownerAdmissionProofShape(raw) || bson.Unmarshal(raw, &proof) != nil || proof.HeaderSHA != w.header.SHA256() || proof.InventorySHA != inventory || proof.Kind != "namespace-owner-admission-v1" || proof.ID != namespaceOwnerAdmissionID(w.header.Epoch, proof.CID, proof.Owner) || proof.Epoch != w.header.Epoch || proof.AdmissionSHA != leaf.ValueSHA256 || proof.CID != leaf.ConversationID {
		return ErrConflict
	}
	_, _, _, e := w.store.readProspectivePageSource(ctx, proof)
	if e != nil {
		return e
	}
	var row bson.Raw
	if e = w.store.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceOwnerProspectiveID(w.header.Epoch, proof.CID, proof.Owner)}).Decode(&row); e != nil {
		return e
	}
	var f namespaceOwnerProspective
	if !ownerProspectiveShape(row) || bson.Unmarshal(row, &f) != nil || f.Kind != "namespace-owner-prospective-v1" || f.Epoch != proof.Epoch || f.CID != proof.CID || f.Owner != proof.Owner || f.OriginSHA != proof.OriginSHA || f.BaseSHA != proof.BaseSHA || f.AdmissionSHA != proof.AdmissionSHA || f.AdmissionJSON != proof.AdmissionJSON || f.Upper != 0 || f.Revision != 1 || f.HistoricalFloor != 0 || f.SourceID != "" || f.BranchID != "" || f.InputSHA != "" || f.ReceiptSHA != "" || f.RevocationCommand != "" || f.RevocationSHA != "" {
		return ErrConflict
	}
	var a namespaceimport.ProspectiveOwnerAdmission
	if decodeImportJSON(f.AdmissionJSON, &a) != nil || a.Kind != "held-cut-roster-v1" || f.SeedMaximum != a.SeedMaximum || f.Lower != a.Lower || a.ValidateShape() != nil {
		return ErrConflict
	}
	return nil
}

// Retain the same revision source used by the original membership owner rows.
func (w *NamespaceImportWriter) prospectiveGroupRevision(ctx context.Context, gid string) (int64, error) {
	var raw bson.Raw
	if e := w.store.db.Collection("group").FindOne(ctx, bson.M{"group_id": gid}).Decode(&raw); e != nil {
		return 0, e
	}
	revision, ok := raw.Lookup(groupauthority.RevisionField).Int64OK()
	if !ok || revision < 1 || revision >= math.MaxInt64 {
		return 0, ErrConflict
	}
	return revision, nil
}
