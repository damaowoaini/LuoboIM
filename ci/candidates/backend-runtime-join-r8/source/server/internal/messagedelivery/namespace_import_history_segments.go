package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
)

type ownerHistoryImportProof struct {
	ID           string `bson:"_id"`
	Kind         string `bson:"kind"`
	Epoch        string `bson:"epoch"`
	CID          string `bson:"conversation_id"`
	Owner        string `bson:"user_id"`
	OriginSHA    string `bson:"origin_sha256"`
	SegmentJSON  string `bson:"segment_json"`
	SegmentSHA   string `bson:"segment_sha256"`
	HeaderSHA    string `bson:"header_sha256"`
	InventorySHA string `bson:"inventory_sha256"`
	PageID       string `bson:"page_id"`
	PageSHA      string `bson:"page_sha256"`
}

func importHistoryProofID(epoch, segment string) string {
	return key(epoch, "namespace-import-history-segment", segment)
}
func historyImportProofShape(raw bson.Raw) bool {
	return exactFields(raw, []string{"_id", "kind", "epoch", "conversation_id", "user_id", "origin_sha256", "segment_json", "segment_sha256", "header_sha256", "inventory_sha256", "page_id", "page_sha256"}, nil, nil, "")
}
func (s *Store) readOwnerHistoryImportProof(ctx context.Context, d NamespaceOriginDescriptor, owner, id, sum string) (ownerHistoryImportProof, namespaceimport.OwnerHistorySegment, namespaceimport.Header, namespaceimport.Inventory, error) {
	var p ownerHistoryImportProof
	var a namespaceimport.OwnerHistorySegment
	var h namespaceimport.Header
	var i namespaceimport.Inventory
	var raw bson.Raw
	e := s.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": id}).Decode(&raw)
	if e != nil {
		return p, a, h, i, errors.Join(ErrUnprepared, e)
	}
	if !historyImportProofShape(raw) || bson.Unmarshal(raw, &p) != nil || directHistoryDigest(p) != sum || p.ID != id || p.Kind != "namespace-owner-history-import-proof-v1" || p.Epoch != s.epoch || p.CID != d.CID || p.Owner != owner || p.OriginSHA != d.SHA256 || decodeImportJSON(p.SegmentJSON, &a) != nil {
		return p, a, h, i, ErrConflict
	}
	segmentSHA, e := namespaceimport.OwnerHistorySegmentSHA256(a)
	if e != nil || p.ID != importHistoryProofID(s.epoch, segmentSHA) || segmentSHA != p.SegmentSHA || a.CID != d.CID || a.OwnerID != owner || a.OriginSHA256 != d.SHA256 || a.SourceEpoch == s.epoch {
		return p, a, h, i, ErrConflict
	}
	binding := namespaceOwnerAdmissionProof{PageID: p.PageID, PageSHA: p.PageSHA, HeaderSHA: p.HeaderSHA, InventorySHA: p.InventorySHA}
	h, i, page, e := s.readBoundImportPage(ctx, binding)
	if e != nil {
		return p, a, h, i, e
	}
	matches := 0
	for _, w := range page.OwnerHistorySegments {
		candidate, e := namespaceimport.OwnerHistorySegmentSHA256(w.Segment)
		if e != nil {
			return p, a, h, i, e
		}
		if candidate == segmentSHA {
			if w.ValidateBinding(h, i) != nil || directHistoryDigest(w.Segment) != hash([]byte(p.SegmentJSON)) {
				return p, a, h, i, ErrConflict
			}
			matches++
		}
	}
	if matches != 1 {
		return p, a, h, i, ErrConflict
	}
	return p, a, h, i, nil
}
func (s *Store) readImportedHistoryInterval(ctx context.Context, d NamespaceOriginDescriptor, owner string, r ownerHistoryInterval) (ownerHistoryInterval, error) {
	p, a, h, i, e := s.readOwnerHistoryImportProof(ctx, d, owner, r.ProofID, r.ProofSHA)
	if e != nil {
		return r, e
	}
	if r.ID != historyIntervalID(s.epoch, r.Kind, d.CID, owner, aSegmentSHA(a)) || r.Lower != a.Lower || r.Upper != a.Upper || r.Revision != 1 || r.SourceID != "" || r.BranchID != "" || r.InputSHA != "" || r.ReceiptSHA != "" {
		return r, ErrConflict
	}
	publisher := &NamespaceImportWriter{store: s, header: h}
	published, e := publisher.readImportCutover(ctx)
	if e != nil || !published.Found {
		if e == nil {
			e = ErrUnprepared
		}
		return r, e
	}
	cut, _, e := s.namespaceCutover(ctx)
	if e != nil {
		return r, e
	}
	if published.Receipt.ManifestSHA256 != cut.ManifestSHA256 || p.InventorySHA != i.SHA256() {
		return r, ErrConflict
	}
	return r, nil
}
func aSegmentSHA(a namespaceimport.OwnerHistorySegment) string {
	sum, e := namespaceimport.OwnerHistorySegmentSHA256(a)
	if e != nil {
		return ""
	}
	return sum
}

// Source root membership plus semantic source validation have ALREADY been
// checked by Controller before VerifiedPage exists. The real Permit, original
// full signed Inventory, Header and exact page are persisted in this same TX.
func (w *NamespaceImportWriter) stageOwnerHistorySegments(ctx context.Context, permit *namespaceimport.Permit, page namespaceimport.VerifiedPage) error {
	if e := w.requireImportPage(ctx, permit, page); e != nil {
		return e
	}
	data := page.Data()
	if len(data.OwnerHistorySegments) == 0 {
		return nil
	}
	hJSON, e := json.Marshal(w.header)
	if e != nil {
		return e
	}
	iJSON, e := json.Marshal(page.Inventory())
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
	for _, witness := range page.OwnerHistorySegments() {
		a := witness.Segment
		sum, e := namespaceimport.OwnerHistorySegmentSHA256(a)
		if e != nil || witness.ValidateBinding(w.header, page.Inventory()) != nil || a.SourceEpoch == w.header.Epoch {
			return ErrConflict
		}
		if e = w.requireImportSetOpen(ctx, "history-segments", ""); e != nil {
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
			for _, owner := range d.OriginIDs {
				participant = participant || owner == a.OwnerID
			}
			if !participant {
				return ErrConflict
			}
		}
		head, e := w.store.directHistoryCounter(ctx, a.CID)
		if e != nil {
			return e
		}
		if a.Upper > head.SeedMaximum || head.Maximum != head.SeedMaximum {
			return ErrConflict
		}
		body, e := json.Marshal(a)
		if e != nil {
			return e
		}
		proof := ownerHistoryImportProof{ID: importHistoryProofID(w.header.Epoch, sum), Kind: "namespace-owner-history-import-proof-v1", Epoch: w.header.Epoch, CID: a.CID, Owner: a.OwnerID, OriginSHA: a.OriginSHA256, SegmentJSON: string(body), SegmentSHA: sum, HeaderSHA: w.header.SHA256(), InventorySHA: page.InventorySHA256(), PageID: pageRow.ID, PageSHA: page.SHA256()}
		if e = immutableImportDocument(ctx, w.store.db.Collection(Receipts), proof.ID, proof); e != nil {
			return e
		}
		row := ownerHistoryInterval{ID: historyIntervalID(w.header.Epoch, "namespace-owner-imported-history-v1", a.CID, a.OwnerID, sum), Kind: "namespace-owner-imported-history-v1", Epoch: w.header.Epoch, CID: a.CID, Owner: a.OwnerID, OriginSHA: a.OriginSHA256, ProofID: proof.ID, ProofSHA: directHistoryDigest(proof), Lower: a.Lower, Upper: a.Upper, Revision: 1}
		if e = immutableImportDocument(ctx, w.store.db.Collection(Sequences), row.ID, row); e != nil {
			return e
		}
		leaf := namespaceImportSetLeaf{ID: importSetPrefix(w.header, "history-segments", "") + sum, Kind: "namespace-import-set-leaf-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), ConversationID: a.CID, ValueSHA256: sum, ReferenceID: proof.ID}
		if _, e = w.store.db.Collection(Receipts).InsertOne(ctx, leaf); e != nil {
			return e
		}
	}
	return nil
}
func (w *NamespaceImportWriter) validateOwnerHistoryLeaf(ctx context.Context, leaf namespaceImportSetLeaf, inventory string) error {
	var raw bson.Raw
	var p ownerHistoryImportProof
	if e := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": leaf.ReferenceID}).Decode(&raw); e != nil {
		return e
	}
	if !historyImportProofShape(raw) || bson.Unmarshal(raw, &p) != nil || p.HeaderSHA != w.header.SHA256() || p.InventorySHA != inventory || p.SegmentSHA != leaf.ValueSHA256 || p.CID != leaf.ConversationID {
		return ErrConflict
	}
	d, e := w.store.readNamespacePrincipals(ctx, p.CID)
	if e != nil {
		return e
	}
	_, a, _, _, e := w.store.readOwnerHistoryImportProof(ctx, d, p.Owner, p.ID, directHistoryDigest(p))
	if e != nil {
		return e
	}
	var row ownerHistoryInterval
	if e = w.store.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": historyIntervalID(w.header.Epoch, "namespace-owner-imported-history-v1", p.CID, p.Owner, p.SegmentSHA)}).Decode(&row); e != nil {
		return e
	}
	expected := ownerHistoryInterval{ID: historyIntervalID(w.header.Epoch, "namespace-owner-imported-history-v1", p.CID, p.Owner, p.SegmentSHA), Kind: "namespace-owner-imported-history-v1", Epoch: w.header.Epoch, CID: p.CID, Owner: p.Owner, OriginSHA: p.OriginSHA, ProofID: p.ID, ProofSHA: directHistoryDigest(p), Lower: a.Lower, Upper: a.Upper, Revision: 1}
	if directHistoryDigest(row) != directHistoryDigest(expected) {
		return ErrConflict
	}
	return nil
}
