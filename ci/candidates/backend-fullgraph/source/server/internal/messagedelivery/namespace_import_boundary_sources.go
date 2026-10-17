package messagedelivery

import (
	"context"
	"encoding/json"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// BoundaryJSON is kept in the original immutable row. This companion retains
// the actual private verified page including its three Merkle witnesses, so a
// later migration can revalidate its signed source rather than trusting row
// SHA equality. It is committed with the original boundary/page transaction.
type namespaceBoundaryPageProof struct {
	ID           string `bson:"_id"`
	Kind         string `bson:"kind"`
	Epoch        string `bson:"epoch"`
	CID          string `bson:"conversation_id"`
	Owner        string `bson:"user_id"`
	HeaderSHA    string `bson:"header_sha256"`
	InventorySHA string `bson:"inventory_sha256"`
	BoundarySHA  string `bson:"boundary_sha256"`
	PageID       string `bson:"page_id"`
	PageSHA      string `bson:"page_sha256"`
}

func boundaryPageProofID(epoch, cid, owner string) string {
	return key(epoch, "namespace-import-boundary-source", cid+"\x00"+owner)
}
func (w *NamespaceImportWriter) stageBoundarySource(ctx context.Context, page namespaceimport.VerifiedPage, witness namespaceimport.EffectiveBoundaryWitness) error {
	if err := w.store.requireTX(ctx); err != nil {
		return err
	}
	data := page.Data()
	matches := 0
	for _, candidate := range data.EffectiveBoundaries {
		if directHistoryDigest(candidate) == directHistoryDigest(witness) {
			matches++
		}
	}
	if matches != 1 || data.HeaderSHA256 != w.header.SHA256() || data.InventoryBindingSHA256 != page.Inventory().BindingSHA256() {
		return ErrConflict
	}
	// This page came from requireImportPage/Controller; no bare witness writer
	// exists. Source bodies are immutable and shared once per bounded page.
	h, e := json.Marshal(w.header)
	if e != nil {
		return e
	}
	i, e := json.Marshal(page.Inventory())
	if e != nil {
		return e
	}
	b, e := json.Marshal(data)
	if e != nil {
		return e
	}
	source := namespaceProspectivePageSource{ID: prospectivePageSourceID(w.header, data.Index), Kind: "namespace-import-prospective-page-v1", HeaderJSON: string(h), HeaderSHA: w.header.SHA256(), InventoryJSON: string(i), InventorySHA: page.InventorySHA256(), PageJSON: string(b), PageSHA: page.SHA256(), PageIndex: int64(data.Index)}
	if e = immutableImportDocument(ctx, w.store.db.Collection(Receipts), source.ID, source); e != nil {
		return e
	}
	boundarySHA, e := namespaceimport.EffectiveBoundarySHA256(witness.Boundary)
	if e != nil {
		return e
	}
	original := witness.Boundary.Window
	proof := namespaceBoundaryPageProof{ID: boundaryPageProofID(w.header.Epoch, original.CID, original.OwnerID), Kind: "namespace-import-boundary-source-v1", Epoch: w.header.Epoch, CID: original.CID, Owner: original.OwnerID, HeaderSHA: w.header.SHA256(), InventorySHA: page.InventorySHA256(), BoundarySHA: boundarySHA, PageID: source.ID, PageSHA: source.PageSHA}
	return immutableImportDocument(ctx, w.store.db.Collection(Receipts), proof.ID, proof)
}
