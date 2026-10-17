package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type namespaceImportState struct {
	ID                     string `bson:"_id"`
	Kind                   string `bson:"kind"`
	HeaderSHA256           string `bson:"header_sha256"`
	InventorySHA256        string `bson:"inventory_sha256"`
	InventoryBindingSHA256 string `bson:"inventory_binding_sha256"`
	RawSourceRootSHA256    string `bson:"raw_source_root_sha256"`
	NextPage               int64  `bson:"next_page"`
	LastPageSHA256         string `bson:"last_page_sha256"`
	Origins                int64  `bson:"origins"`
	Windows                int64  `bson:"windows"`
	Quarantines            int64  `bson:"quarantines"`
	Revision               int64  `bson:"revision"`
}

type namespaceImportPage struct {
	ID              string `bson:"_id"`
	Kind            string `bson:"kind"`
	HeaderSHA256    string `bson:"header_sha256"`
	InventorySHA256 string `bson:"inventory_sha256"`
	PageSHA256      string `bson:"page_sha256"`
	PreviousSHA256  string `bson:"previous_sha256"`
	ReceiptJSON     string `bson:"receipt_json"`
	ReceiptSHA256   string `bson:"receipt_sha256"`
	AttemptID       string `bson:"attempt_id"`
	LeaseID         string `bson:"lease_id"`
	FenceSHA256     string `bson:"fence_sha256"`
	PageIndex       int64  `bson:"page_index"`
}

func importStateID(h namespaceimport.Header) string {
	return key(h.Epoch, "namespace-import-state", "exclusive")
}
func importPageID(h namespaceimport.Header, index uint64) string {
	return key(h.Epoch, "namespace-import-page", fmt.Sprintf("%s:%d", h.MigrationID, index))
}

func decodeImportJSON(value string, out any) error {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return ErrConflict
	}
	// Canonical writer JSON must round-trip byte-for-byte. This also rejects
	// duplicate fields, trailing values and alternate number encodings.
	body, err := json.Marshal(out)
	if err != nil || string(body) != value {
		return ErrConflict
	}
	return nil
}

func (w *NamespaceImportWriter) readImportState(ctx context.Context) (namespaceImportState, bool, error) {
	var raw bson.Raw
	err := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importStateID(w.header)}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return namespaceImportState{}, false, nil
	}
	if err != nil {
		return namespaceImportState{}, false, err
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "inventory_binding_sha256", "raw_source_root_sha256", "last_page_sha256"}, []string{"next_page", "origins", "windows", "quarantines", "revision"}, nil, "") {
		return namespaceImportState{}, false, ErrConflict
	}
	var row namespaceImportState
	if bson.Unmarshal(raw, &row) != nil || row.ID != importStateID(w.header) || row.Kind != "namespace-import-state-v1" || row.HeaderSHA256 != w.header.SHA256() || !validSHA(row.InventorySHA256) || !validSHA(row.InventoryBindingSHA256) || !validSHA(row.RawSourceRootSHA256) || row.NextPage < 0 || row.NextPage >= math.MaxInt64 || row.Origins < 0 || row.Windows < 0 || row.Quarantines < 0 || row.Revision < 1 || row.Revision >= math.MaxInt64 || row.NextPage > 0 && !validSHA(row.LastPageSHA256) || row.NextPage == 0 && row.LastPageSHA256 != "" {
		return row, false, ErrConflict
	}
	return row, true, nil
}

func (w *NamespaceImportWriter) readImportPage(ctx context.Context, id namespaceimport.PageIdentity) (namespaceimport.PageInspection, error) {
	if id.Index >= math.MaxInt64 || !validSHA(id.SHA256) || id.Index == 0 && id.PreviousSHA256 != "" || id.Index > 0 && !validSHA(id.PreviousSHA256) {
		return namespaceimport.PageInspection{}, ErrInvalid
	}
	var raw bson.Raw
	err := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importPageID(w.header, id.Index)}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return namespaceimport.PageInspection{}, nil
	}
	if err != nil {
		return namespaceimport.PageInspection{}, err
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "page_sha256", "previous_sha256", "receipt_json", "receipt_sha256", "attempt_id", "lease_id", "fence_sha256"}, []string{"page_index"}, nil, "") {
		return namespaceimport.PageInspection{}, ErrConflict
	}
	var row namespaceImportPage
	if bson.Unmarshal(raw, &row) != nil || row.ID != importPageID(w.header, id.Index) || row.Kind != "namespace-import-page-v1" || row.HeaderSHA256 != w.header.SHA256() || row.PageIndex != int64(id.Index) || row.PageSHA256 != id.SHA256 || row.PreviousSHA256 != id.PreviousSHA256 || !validSHA(row.InventorySHA256) || !validSHA(row.ReceiptSHA256) || !identity(row.AttemptID) || !identity(row.LeaseID) || !validSHA(row.FenceSHA256) {
		return namespaceimport.PageInspection{}, ErrConflict
	}
	var receipt namespaceimport.PageReceipt
	if decodeImportJSON(row.ReceiptJSON, &receipt) != nil || receipt.HeaderSHA256 != row.HeaderSHA256 || receipt.InventorySHA256 != row.InventorySHA256 || receipt.Page != id || receipt.ReceiptSHA256 != row.ReceiptSHA256 || namespaceimport.ReceiptDigest(receipt) != row.ReceiptSHA256 {
		return namespaceimport.PageInspection{}, ErrConflict
	}
	return namespaceimport.PageInspection{Found: true, Receipt: receipt}, nil
}

// Inspection is read-only and uses the original page identity. A negative read
// is only "not observed"; it is never a rollback certificate or replay permit.
func (w *NamespaceImportWriter) InspectPage(ctx context.Context, h namespaceimport.Header, id namespaceimport.PageIdentity) (namespaceimport.PageInspection, error) {
	if !w.validHeader(h) || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return namespaceimport.PageInspection{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := w.checkImportInspectionReady(current); err != nil {
		return namespaceimport.PageInspection{}, err
	}
	return w.readImportPage(current, id)
}

func (w *NamespaceImportWriter) ApplyPage(ctx context.Context, permit *namespaceimport.Permit, page namespaceimport.VerifiedPage) (namespaceimport.PageReceipt, error) {
	if w == nil || !w.validHeader(w.header) || ctx == nil || mongo.SessionFromContext(ctx) != nil || permit == nil {
		return namespaceimport.PageReceipt{}, ErrInvalid
	}
	if err := permit.ValidatePage(w.header, page); err != nil {
		return namespaceimport.PageReceipt{}, err
	}
	data := page.Data()
	inventory := page.Inventory()
	if data.Index >= math.MaxInt64-1 || inventory.PageCount >= math.MaxInt64 || data.Index >= inventory.PageCount || len(data.OriginCoverages) != len(data.Origins) || len(data.EffectiveBoundaries) != len(data.OwnerWindows) {
		return namespaceimport.PageReceipt{}, ErrInvalid
	}
	if len(data.CoverageClosures) > 1 || len(data.CoverageClosures) > 0 && (len(data.Origins)+len(data.OwnerWindows)+len(data.Quarantines)+len(data.OriginCoverages)+len(data.EffectiveBoundaries) > 0) {
		return namespaceimport.PageReceipt{}, ErrInvalid
	}
	coverageByCID := make(map[string]namespaceimport.OriginCoverage, len(data.OriginCoverages))
	for _, witness := range data.OriginCoverages {
		if _, duplicate := coverageByCID[witness.Coverage.CID]; duplicate {
			return namespaceimport.PageReceipt{}, ErrConflict
		}
		coverageByCID[witness.Coverage.CID] = witness.Coverage
	}
	current, cancel := context.WithTimeout(ctx, mutationTimeout)
	defer cancel()
	if err := w.CheckImportCatalogReady(current); err != nil {
		return namespaceimport.PageReceipt{}, err
	}
	created := time.Now().UTC().Truncate(time.Millisecond)
	var result namespaceimport.PageReceipt
	err := w.store.transaction(current, func(tx context.Context) error {
		result = namespaceimport.PageReceipt{}
		if err := w.requireImportPage(tx, permit, page); err != nil {
			return err
		}
		prior, err := w.readImportPage(tx, data.Identity())
		if err != nil {
			return err
		}
		if prior.Found {
			if prior.Receipt.InventorySHA256 != page.InventorySHA256() {
				return ErrConflict
			}
			result = prior.Receipt
			return nil
		}
		state, found, err := w.readImportState(tx)
		if err != nil {
			return err
		}
		if !found {
			if data.Index != 0 || data.PreviousSHA256 != "" {
				return ErrConflict
			}
			state = namespaceImportState{ID: importStateID(w.header), Kind: "namespace-import-state-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), InventoryBindingSHA256: inventory.BindingSHA256(), RawSourceRootSHA256: inventory.RawRootSHA256, Revision: 1}
			if _, err = w.store.db.Collection(Receipts).InsertOne(tx, state); err != nil {
				return err
			}
		}
		if state.InventorySHA256 != page.InventorySHA256() || state.InventoryBindingSHA256 != data.InventoryBindingSHA256 || state.RawSourceRootSHA256 != data.RawSourceRootSHA256 || state.NextPage != int64(data.Index) || state.LastPageSHA256 != data.PreviousSHA256 || state.Revision >= math.MaxInt64-1 {
			return ErrConflict
		}
		for _, origin := range data.Origins {
			if err := w.stageHistoricalOrigin(tx, page, origin, created); err != nil {
				return err
			}
			coverage, ok := coverageByCID[origin.Origin.CID]
			if !ok {
				return ErrConflict
			}
			if err := w.stageOriginCoverage(tx, page, coverage, origin); err != nil {
				return err
			}
		}
		for _, boundary := range data.EffectiveBoundaries {
			if err := w.stageHistoricalBoundary(tx, page, boundary); err != nil {
				return err
			}
		}
		for _, quarantine := range data.Quarantines {
			if err := w.stageNamespaceQuarantine(tx, page, quarantine); err != nil {
				return err
			}
		}
		for _, closure := range data.CoverageClosures {
			if err := w.stageImportClosure(tx, page, closure); err != nil {
				return err
			}
		}
		count := data.Counts()
		if count.Origins > uint64(math.MaxInt64-state.Origins) || count.Windows > uint64(math.MaxInt64-state.Windows) || count.Quarantines > uint64(math.MaxInt64-state.Quarantines) {
			return ErrInvalid
		}
		next := state
		next.NextPage++
		next.LastPageSHA256 = page.SHA256()
		next.Origins += int64(count.Origins)
		next.Windows += int64(count.Windows)
		next.Quarantines += int64(count.Quarantines)
		next.Revision++
		result = namespaceimport.PageReceipt{HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), Page: data.Identity(), Cumulative: namespaceimport.Counts{Origins: uint64(next.Origins), Windows: uint64(next.Windows), Quarantines: uint64(next.Quarantines)}}
		result.ReceiptSHA256 = namespaceimport.ReceiptDigest(result)
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		row := namespaceImportPage{ID: importPageID(w.header, data.Index), Kind: "namespace-import-page-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: page.InventorySHA256(), PageSHA256: page.SHA256(), PreviousSHA256: data.PreviousSHA256, ReceiptJSON: string(body), ReceiptSHA256: result.ReceiptSHA256, AttemptID: permit.AttemptID(), LeaseID: permit.LeaseID(), FenceSHA256: permit.FenceSHA256(), PageIndex: int64(data.Index)}
		if _, err = w.store.db.Collection(Receipts).InsertOne(tx, row); err != nil {
			return err
		}
		updated, err := w.store.db.Collection(Receipts).ReplaceOne(tx, bson.M{"_id": state.ID, "header_sha256": state.HeaderSHA256, "inventory_sha256": state.InventorySHA256, "revision": state.Revision, "next_page": state.NextPage, "last_page_sha256": state.LastPageSHA256}, next)
		if err != nil {
			return err
		}
		if updated.MatchedCount != 1 || updated.ModifiedCount != 1 {
			return ErrConflict
		}
		return nil
	})
	if err != nil {
		return namespaceimport.PageReceipt{}, err
	}
	return result, nil
}
