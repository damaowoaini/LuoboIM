package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

var _ namespaceimport.Writer = (*NamespaceImportWriter)(nil)

type namespaceImportCutover struct {
	ID              string `bson:"_id"`
	Kind            string `bson:"kind"`
	HeaderSHA256    string `bson:"header_sha256"`
	InventorySHA256 string `bson:"inventory_sha256"`
	ManifestSHA256  string `bson:"manifest_sha256"`
	ManifestJSON    string `bson:"manifest_json"`
	CutoverSHA256   string `bson:"cutover_sha256"`
	ReceiptJSON     string `bson:"receipt_json"`
	ReceiptSHA256   string `bson:"receipt_sha256"`
	AttemptID       string `bson:"attempt_id"`
	LeaseID         string `bson:"lease_id"`
	FenceSHA256     string `bson:"fence_sha256"`
}

func importCutoverReceiptID(h namespaceimport.Header) string {
	return key(h.Epoch, "namespace-import-cutover", h.MigrationID)
}

func (w *NamespaceImportWriter) readImportCutover(ctx context.Context) (namespaceimport.CutoverInspection, error) {
	var raw bson.Raw
	err := w.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": importCutoverReceiptID(w.header)}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return namespaceimport.CutoverInspection{}, nil
	}
	if err != nil {
		return namespaceimport.CutoverInspection{}, err
	}
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "inventory_sha256", "manifest_sha256", "manifest_json", "cutover_sha256", "receipt_json", "receipt_sha256", "attempt_id", "lease_id", "fence_sha256"}, nil, nil, "") {
		return namespaceimport.CutoverInspection{}, ErrConflict
	}
	var row namespaceImportCutover
	var manifest namespaceimport.ManifestData
	var receipt namespaceimport.CutoverReceipt
	if bson.Unmarshal(raw, &row) != nil || row.ID != importCutoverReceiptID(w.header) || row.Kind != "namespace-import-cutover-v1" || row.HeaderSHA256 != w.header.SHA256() || !validSHA(row.InventorySHA256) || !validSHA(row.ManifestSHA256) || !validSHA(row.CutoverSHA256) || !identity(row.AttemptID) || !identity(row.LeaseID) || !validSHA(row.FenceSHA256) || decodeImportJSON(row.ManifestJSON, &manifest) != nil || hash([]byte(row.ManifestJSON)) != row.ManifestSHA256 || manifest.HeaderSHA256 != row.HeaderSHA256 || manifest.InventorySHA256 != row.InventorySHA256 || decodeImportJSON(row.ReceiptJSON, &receipt) != nil || receipt.HeaderSHA256 != row.HeaderSHA256 || receipt.ManifestSHA256 != row.ManifestSHA256 || receipt.ReceiptSHA256 != row.ReceiptSHA256 || namespaceimport.CutoverReceiptDigest(receipt) != row.ReceiptSHA256 {
		return namespaceimport.CutoverInspection{}, ErrConflict
	}
	cutover, cutoverSHA, err := w.readPublishedNamespaceCutover(ctx, row.CutoverSHA256)
	if err != nil {
		return namespaceimport.CutoverInspection{}, err
	}
	if cutover.MigrationID != w.header.MigrationID || cutover.ManifestSHA256 != row.ManifestSHA256 || cutoverSHA != row.CutoverSHA256 {
		return namespaceimport.CutoverInspection{}, ErrConflict
	}
	return namespaceimport.CutoverInspection{Found: true, Receipt: receipt}, nil
}

func (w *NamespaceImportWriter) InspectCutover(ctx context.Context, h namespaceimport.Header) (namespaceimport.CutoverInspection, error) {
	if !w.validHeader(h) || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return namespaceimport.CutoverInspection{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := w.checkImportInspectionReady(current); err != nil {
		return namespaceimport.CutoverInspection{}, err
	}
	return w.readImportCutover(current)
}

// This publishes the completed data-import cut, not runtime readiness. The
// real managed writer/reader startup and membership schema binding remain
// separate gates. No ready bool or operator-supplied inventory hash is minted.
// Work is O(1) here: every actual sorted owner/boundary set has already closed
// through bounded signed pages and the final global coverage root/count.
func (w *NamespaceImportWriter) PublishCutover(ctx context.Context, permit *namespaceimport.Permit, manifest namespaceimport.VerifiedManifest) (namespaceimport.CutoverReceipt, error) {
	if w == nil || !w.validHeader(w.header) || ctx == nil || permit == nil || mongo.SessionFromContext(ctx) != nil {
		return namespaceimport.CutoverReceipt{}, ErrInvalid
	}
	if err := permit.ValidateManifest(w.header, manifest); err != nil {
		return namespaceimport.CutoverReceipt{}, err
	}
	data := manifest.Data()
	inventory := manifest.Inventory()
	if data.PageCount == 0 || data.PageCount >= math.MaxInt64 || data.Counts.Origins >= math.MaxInt64 || data.Counts.Windows >= math.MaxInt64 || data.Counts.Quarantines > uint64(math.MaxInt64)-data.Counts.Origins || data.CoverageCount != data.Counts.Origins || inventory.PageCount != data.PageCount || data.ProspectiveCount >= math.MaxInt64 || data.ProspectiveCount != inventory.ProspectiveCount || data.ProspectiveSetRootSHA256 != inventory.ProspectiveSetRootSHA256 || data.OwnerHistorySegmentCount >= math.MaxInt64 || data.OwnerHistorySegmentCount != inventory.OwnerHistorySegmentCount || data.OwnerHistorySegmentSetRootSHA256 != inventory.OwnerHistorySegmentSetRootSHA256 {
		return namespaceimport.CutoverReceipt{}, ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, mutationTimeout)
	defer cancel()
	if err := w.CheckImportCatalogReady(current); err != nil {
		return namespaceimport.CutoverReceipt{}, err
	}
	epochPublisher := w.epochPublisherSnapshot()
	if epochPublisher != nil {
		if err := epochPublisher.prepareEpochPublish(current, permit, manifest); err != nil {
			return namespaceimport.CutoverReceipt{}, err
		}
	}
	created := time.Now().UTC().Truncate(time.Millisecond)
	var result namespaceimport.CutoverReceipt
	err := w.store.transaction(current, func(tx context.Context) error {
		result = namespaceimport.CutoverReceipt{}
		if err := permit.ValidateManifest(w.header, manifest); err != nil {
			return err
		}
		if err := w.store.requireTX(tx); err != nil {
			return err
		}
		if err := w.checkStorageIdentity(tx, true); err != nil {
			return err
		}
		known, err := w.readImportCutover(tx)
		if err != nil {
			return err
		}
		if known.Found {
			if known.Receipt.ManifestSHA256 != manifest.SHA256() {
				return ErrConflict
			}
			result = known.Receipt
			return nil
		}
		state, found, err := w.readImportState(tx)
		if err != nil {
			return err
		}
		if !found || state.InventorySHA256 != data.InventorySHA256 || state.InventoryBindingSHA256 != inventory.BindingSHA256() || state.RawSourceRootSHA256 != inventory.RawRootSHA256 || state.NextPage != int64(data.PageCount) || state.LastPageSHA256 != data.FinalPageSHA256 || state.Origins != int64(data.Counts.Origins) || state.Windows != int64(data.Counts.Windows) || state.Quarantines != int64(data.Counts.Quarantines) || state.Prospective != int64(data.ProspectiveCount) || state.HistorySegments != int64(data.OwnerHistorySegmentCount) {
			return ErrConflict
		}
		// The durable final page, not just the mutable progress row, must carry
		// the same cumulative counts. Its receipt was committed with its writes.
		var terminal bson.Raw
		if err := w.store.db.Collection(Receipts).FindOne(tx, bson.M{"_id": importPageID(w.header, data.PageCount-1)}).Decode(&terminal); err != nil {
			return err
		}
		previous, ok := terminal.Lookup("previous_sha256").StringValueOK()
		if !ok {
			return ErrConflict
		}
		page, err := w.readImportPage(tx, namespaceimport.PageIdentity{Index: data.PageCount - 1, PreviousSHA256: previous, SHA256: data.FinalPageSHA256})
		if err != nil {
			return err
		}
		if !page.Found || page.Receipt.InventorySHA256 != data.InventorySHA256 || page.Receipt.Cumulative != data.Counts || page.Receipt.CumulativeProspectives != data.ProspectiveCount || page.Receipt.CumulativeOwnerHistorySegments != data.OwnerHistorySegmentCount {
			return ErrConflict
		}
		if err := w.checkCompletedImportSet(tx, "coverage", "", data.InventorySHA256, data.CoverageSetRootSHA256, data.CoverageCount); err != nil {
			return err
		}
		if data.ProspectiveSetRootSHA256 != "" || data.ProspectiveCount != 0 {
			if err := w.checkCompletedImportSet(tx, "prospective", "", data.InventorySHA256, data.ProspectiveSetRootSHA256, data.ProspectiveCount); err != nil {
				return err
			}
		}
		if data.OwnerHistorySegmentSetRootSHA256 != "" || data.OwnerHistorySegmentCount != 0 {
			if err := w.checkCompletedImportSet(tx, "history-segments", "", data.InventorySHA256, data.OwnerHistorySegmentSetRootSHA256, data.OwnerHistorySegmentCount); err != nil {
				return err
			}
		}
		fence := permit.FenceObservation()
		if fence.HeaderSHA256 != w.header.SHA256() || fence.LeaseID != permit.LeaseID() || fence.SHA256() != permit.FenceSHA256() || !identity(permit.AttemptID()) || !identity(permit.LeaseID()) || !validSHA(fence.AdmissionGuardSHA256) || len(fence.Joined) == 0 || len(fence.Revoked) == 0 || len(fence.Drained) == 0 {
			return ErrUnprepared
		}
		drainBody, err := json.Marshal(fence.Drained)
		if err != nil {
			return err
		}
		exclusionBody, err := json.Marshal(struct {
			Mode            string
			AdmissionRecord string
			AdmissionSHA256 string
			Joined          []namespaceimport.JoinedWriter
			Revoked         []namespaceimport.RevokedCredential
		}{fence.ExclusionMode, fence.AdmissionGuardRecord, fence.AdmissionGuardSHA256, fence.Joined, fence.Revoked})
		if err != nil {
			return err
		}
		namespaceCount := int64(data.Counts.Origins + data.Counts.Quarantines)
		cutover := NamespaceCutover{ID: NamespaceCutoverID, Kind: "immutable-namespace-cutover-v1", Database: w.header.Database, Epoch: w.header.Epoch, ActivationSHA256: w.header.ActivationSHA256, MigrationID: w.header.MigrationID, State: "all-inventoried-namespaces-permanently-claimed", ManifestSHA256: manifest.SHA256(), MongoInventorySHA256: inventory.MongoRootSHA256, RedisInventorySHA256: inventory.RedisRootSHA256, DrainSHA256: hash(drainBody), OldBinaryFenceSHA256: hash(exclusionBody), NamespaceCount: namespaceCount, ClaimedCount: namespaceCount, RegisteredAt: created}
		if epochPublisher != nil {
			if err := epochPublisher.publishEpochCutover(tx, permit, manifest, cutover); err != nil {
				return err
			}
		} else {
			if err := immutableImportDocument(tx, w.store.db.Collection(Sequences), NamespaceCutoverID, cutover); err != nil {
				return err
			}
		}
		_, cutoverSHA, err := w.store.namespaceCutover(tx)
		if err != nil {
			return err
		}
		result = namespaceimport.CutoverReceipt{HeaderSHA256: w.header.SHA256(), ManifestSHA256: manifest.SHA256()}
		result.ReceiptSHA256 = namespaceimport.CutoverReceiptDigest(result)
		receiptBody, err := json.Marshal(result)
		if err != nil {
			return err
		}
		manifestBody, err := json.Marshal(data)
		if err != nil {
			return err
		}
		row := namespaceImportCutover{ID: importCutoverReceiptID(w.header), Kind: "namespace-import-cutover-v1", HeaderSHA256: w.header.SHA256(), InventorySHA256: inventory.SHA256(), ManifestSHA256: manifest.SHA256(), ManifestJSON: string(manifestBody), CutoverSHA256: cutoverSHA, ReceiptJSON: string(receiptBody), ReceiptSHA256: result.ReceiptSHA256, AttemptID: permit.AttemptID(), LeaseID: permit.LeaseID(), FenceSHA256: permit.FenceSHA256()}
		_, err = w.store.db.Collection(Receipts).InsertOne(tx, row)
		return err
	})
	if err != nil {
		return namespaceimport.CutoverReceipt{}, err
	}
	return result, nil
}
