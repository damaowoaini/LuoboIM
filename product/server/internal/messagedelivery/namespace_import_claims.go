package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

const historicalNamespaceKind = "permanent-historical-namespace-v1"

type historicalNamespaceClaim struct {
	ID                 string    `bson:"_id"`
	Kind               string    `bson:"kind"`
	ConversationID     string    `bson:"conversation_id"`
	OriginKind         string    `bson:"origin_kind"`
	OriginSHA256       string    `bson:"origin_sha256"`
	GroupID            string    `bson:"group_id"`
	Epoch              string    `bson:"created_epoch"`
	ActivationSHA256   string    `bson:"created_activation_sha256"`
	MigrationID        string    `bson:"migration_id"`
	HeaderSHA256       string    `bson:"header_sha256"`
	InventorySHA256    string    `bson:"inventory_sha256"`
	SeedEvidenceSHA256 string    `bson:"seed_evidence_sha256"`
	CreatedAt          time.Time `bson:"created_at"`
}

type historicalGroupReservation struct {
	ID               string    `bson:"_id"`
	Kind             string    `bson:"kind"`
	GroupID          string    `bson:"group_id"`
	OriginSHA256     string    `bson:"origin_sha256"`
	Epoch            string    `bson:"created_epoch"`
	ActivationSHA256 string    `bson:"created_activation_sha256"`
	MigrationID      string    `bson:"migration_id"`
	HeaderSHA256     string    `bson:"header_sha256"`
	InventorySHA256  string    `bson:"inventory_sha256"`
	CreatedAt        time.Time `bson:"created_at"`
}

func decodeHistoricalNamespaceClaim(raw bson.Raw, cid string) (historicalNamespaceClaim, error) {
	if !exactFields(raw, []string{"_id", "kind", "conversation_id", "origin_kind", "origin_sha256", "group_id", "created_epoch", "created_activation_sha256", "migration_id", "header_sha256", "inventory_sha256", "seed_evidence_sha256"}, nil, nil, "created_at") {
		return historicalNamespaceClaim{}, ErrConflict
	}
	var row historicalNamespaceClaim
	if bson.Unmarshal(raw, &row) != nil || row.ID != namespaceClaimID(cid) || row.Kind != historicalNamespaceKind || row.ConversationID != cid || !identity(cid) || !validUUID(row.Epoch) || !validUUID(row.MigrationID) || !validSHA(row.ActivationSHA256) || !validSHA(row.OriginSHA256) || !validSHA(row.HeaderSHA256) || !validSHA(row.InventorySHA256) || !validSHA(row.SeedEvidenceSHA256) || row.CreatedAt.IsZero() {
		return row, ErrConflict
	}
	if row.OriginKind == "group" {
		d := NamespaceOriginDescriptor{CID: cid, Kind: "group", GroupID: row.GroupID, OriginIDs: []string{row.GroupID}, SHA256: row.OriginSHA256}
		if err := validateNamespaceDescriptor(d); err != nil {
			return row, err
		}
	} else if row.GroupID != "" || row.OriginKind != "single" && row.OriginKind != "server-notification" {
		return row, ErrConflict
	}
	return row, nil
}

func (s *Store) historicalNamespaceOwner(ctx context.Context, cid string, raw bson.Raw) (messageOrigin, string, error) {
	row, err := decodeHistoricalNamespaceClaim(raw, cid)
	if err != nil {
		return messageOrigin{}, "", err
	}
	if row.OriginKind == "group" {
		if err := s.requireHistoricalGroupReservation(ctx, row.GroupID, row.OriginSHA256); err != nil {
			return messageOrigin{}, "", err
		}
	}
	return messageOrigin{CID: cid, Kind: row.OriginKind, GroupID: row.GroupID, SHA256: row.OriginSHA256}, row.SeedEvidenceSHA256, nil
}

// The group key is permanent across epochs, deletion and orphan history. It is
// shared by canonical g_/sg_/n_ descriptors and prevents reusing the group ID.
// Existing fresh-group reservations retain their original claim, never a new
// migration timestamp/command masquerading as an old creation proof.
func (s *Store) requireHistoricalGroupReservation(ctx context.Context, gid, originSHA string) error {
	var raw bson.Raw
	if err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": groupOriginClaimID(gid)}).Decode(&raw); err != nil {
		return err
	}
	kind, ok := raw.Lookup("kind").StringValueOK()
	if !ok {
		return ErrConflict
	}
	if kind == "permanent-new-group-namespace-v1" {
		if !exactFields(raw, []string{"_id", "kind", "conversation_id", "group_id", "branch", "epoch", "activation_sha256", "command_id", "input_sha256", "cutover_sha256"}, nil, nil, "created_at") {
			return ErrConflict
		}
		var original groupNamespaceClaim
		if bson.Unmarshal(raw, &original) != nil || original.ID != groupOriginClaimID(gid) || original.Kind != kind || original.ConversationID != "" || original.GroupID != gid || original.Branch != "group-origin" || !validUUID(original.Epoch) || !validSHA(original.ActivationSHA256) || !validUUID(original.CommandID) || !validSHA(original.InputSHA256) || !validSHA(original.CutoverSHA256) || original.CreatedAt.IsZero() {
			return ErrConflict
		}
		body, err := json.Marshal([]any{"group", []string{gid}})
		if err != nil || hash(body) != originSHA {
			return ErrConflict
		}
		return nil
	}
	if !exactFields(raw, []string{"_id", "kind", "group_id", "origin_sha256", "created_epoch", "created_activation_sha256", "migration_id", "header_sha256", "inventory_sha256"}, nil, nil, "created_at") {
		return ErrConflict
	}
	var row historicalGroupReservation
	if bson.Unmarshal(raw, &row) != nil || row.ID != groupOriginClaimID(gid) || row.Kind != "permanent-historical-group-origin-v1" || row.GroupID != gid || row.OriginSHA256 != originSHA || !validSHA(originSHA) || !validUUID(row.Epoch) || !validUUID(row.MigrationID) || !validSHA(row.ActivationSHA256) || !validSHA(row.HeaderSHA256) || !validSHA(row.InventorySHA256) || row.CreatedAt.IsZero() {
		return ErrConflict
	}
	return nil
}

func (w *NamespaceImportWriter) reserveHistoricalGroup(ctx context.Context, d NamespaceOriginDescriptor, inventory string, created time.Time) error {
	err := w.store.requireHistoricalGroupReservation(ctx, d.GroupID, d.SHA256)
	if err == nil {
		return nil
	}
	if !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	// Missing CID rows under an existing (possibly malformed) group reservation
	// are not permission to overwrite that permanent reservation.
	if err := absent(ctx, w.store.db.Collection(Sequences), bson.M{"_id": groupOriginClaimID(d.GroupID)}); err != nil {
		return err
	}
	row := historicalGroupReservation{ID: groupOriginClaimID(d.GroupID), Kind: "permanent-historical-group-origin-v1", GroupID: d.GroupID, OriginSHA256: d.SHA256, Epoch: w.header.Epoch, ActivationSHA256: w.header.ActivationSHA256, MigrationID: w.header.MigrationID, HeaderSHA256: w.header.SHA256(), InventorySHA256: inventory, CreatedAt: created}
	_, err = w.store.db.Collection(Sequences).InsertOne(ctx, row)
	return err
}

type historicalOriginReceipt struct {
	ID                 string `bson:"_id"`
	Kind               string `bson:"kind"`
	HeaderSHA256       string `bson:"header_sha256"`
	InventorySHA256    string `bson:"inventory_sha256"`
	PageSHA256         string `bson:"page_sha256"`
	ConversationID     string `bson:"conversation_id"`
	OriginSHA256       string `bson:"origin_sha256"`
	SeedEvidenceSHA256 string `bson:"seed_evidence_sha256"`
	SourceRecordSHA256 string `bson:"source_record_sha256"`
	MongoMaximum       *int64 `bson:"mongo_maximum,omitempty"`
	MongoHeadJSON      string `bson:"mongo_head_json"`
	RedisHeadJSON      string `bson:"redis_head_json"`
	SourceRecordJSON   string `bson:"source_record_json"`
	RedisMaximum       int64  `bson:"redis_maximum"`
	Maximum            int64  `bson:"maximum"`
}

func importOriginReceiptID(h namespaceimport.Header, cid string) string {
	return key(h.Epoch, "namespace-import-origin", h.MigrationID+"\x00"+cid)
}

// Only ApplyPage invokes this after rechecking its original private permit in
// the same strong transaction. Its seed is the verified Mongo/Redis maximum,
// including controlled proven-empty evidence; it never calls the zero-only
// new-namespace helper and never guesses a missing source head as zero.
func (w *NamespaceImportWriter) stageHistoricalOrigin(ctx context.Context, permit *namespaceimport.Permit, p namespaceimport.VerifiedPage, record namespaceimport.OriginRecord, created time.Time) error {
	if err := w.store.requireTX(ctx); err != nil {
		return err
	}
	d := NamespaceOriginDescriptor{CID: record.Origin.CID, Kind: record.Origin.Kind, GroupID: record.Origin.GroupID, OriginIDs: append([]string(nil), record.Origin.OriginIDs...), SHA256: record.Origin.SHA256}
	if err := validateNamespaceDescriptor(d); err != nil {
		return err
	}
	if err := w.requireImportPage(ctx, permit, p); err != nil {
		return err
	}
	maximum, err := record.EffectiveSeedMaximum()
	if err != nil {
		return err
	}
	if record.MongoHead.State == namespaceimport.LegacyCounterNotAllocatedHere {
		expected, err := namespaceimport.SourceSeedEvidenceSHA256(w.header, p.Inventory(), record)
		if err != nil || expected != record.SeedEvidenceSHA256 {
			return ErrConflict
		}
	}
	if !validSHA(record.SeedEvidenceSHA256) {
		return ErrInvalid
	}
	if err := w.store.requireNamespaceNotQuarantined(ctx, d.CID); err != nil {
		return err
	}
	id := importOriginReceiptID(w.header, d.CID)
	if err := absent(ctx, w.store.db.Collection(Receipts), bson.M{"_id": id}); err != nil {
		return err
	}
	actual, _, err := w.store.namespaceOwner(ctx, d.CID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		if d.Kind == "group" {
			if err := w.reserveHistoricalGroup(ctx, d, p.InventorySHA256(), created); err != nil {
				return err
			}
		}
		row := historicalNamespaceClaim{ID: namespaceClaimID(d.CID), Kind: historicalNamespaceKind, ConversationID: d.CID, OriginKind: d.Kind, OriginSHA256: d.SHA256, GroupID: d.GroupID, Epoch: w.header.Epoch, ActivationSHA256: w.header.ActivationSHA256, MigrationID: w.header.MigrationID, HeaderSHA256: w.header.SHA256(), InventorySHA256: p.InventorySHA256(), SeedEvidenceSHA256: record.SeedEvidenceSHA256, CreatedAt: created}
		if _, err = w.store.db.Collection(Sequences).InsertOne(ctx, row); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if actual.Kind != d.Kind || actual.SHA256 != d.SHA256 || actual.GroupID != d.GroupID {
		return ErrConflict
	}
	if err = w.initializeHistoricalCounter(ctx, permit, p, record, maximum); err != nil {
		return err
	}
	if err := w.store.recordNamespacePrincipals(ctx, d); err != nil {
		return err
	}
	body, err := json.Marshal(record)
	if err != nil {
		return err
	}
	mongoJSON, err := json.Marshal(record.MongoHead)
	if err != nil {
		return err
	}
	redisJSON, err := json.Marshal(record.RedisHead)
	if err != nil {
		return err
	}
	var mongoMaximum *int64
	if record.MongoHead.State != namespaceimport.LegacyCounterNotAllocatedHere {
		value := record.MongoHead.Value
		mongoMaximum = &value
	}
	row := historicalOriginReceipt{ID: id, Kind: "namespace-import-origin-v2", HeaderSHA256: w.header.SHA256(), InventorySHA256: p.InventorySHA256(), PageSHA256: p.SHA256(), ConversationID: d.CID, OriginSHA256: d.SHA256, SeedEvidenceSHA256: record.SeedEvidenceSHA256, SourceRecordSHA256: hash(body), MongoHeadJSON: string(mongoJSON), RedisHeadJSON: string(redisJSON), SourceRecordJSON: string(body), MongoMaximum: mongoMaximum, RedisMaximum: record.RedisHead.Value, Maximum: maximum}
	_, err = w.store.db.Collection(Receipts).InsertOne(ctx, row)
	return err
}

type namespaceQuarantine struct {
	ID              string `bson:"_id"`
	Kind            string `bson:"kind"`
	ConversationID  string `bson:"conversation_id"`
	HeaderSHA256    string `bson:"header_sha256"`
	InventorySHA256 string `bson:"inventory_sha256"`
	PageSHA256      string `bson:"page_sha256"`
	Reason          string `bson:"reason"`
	EvidenceSHA256  string `bson:"evidence_sha256"`
}

func namespaceQuarantineID(cid string) string {
	return "permanent-namespace-quarantine:" + hash([]byte(cid))
}

func (s *Store) requireNamespaceNotQuarantined(ctx context.Context, cid string) error {
	var raw bson.Raw
	err := s.db.Collection(Sequences).FindOne(ctx, bson.M{"_id": namespaceQuarantineID(cid)}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return err
	}
	// Even malformed quarantine evidence remains unavailable; a runtime miss
	// must never discard it and allocate a fresh counter or infer a new owner.
	return errors.Join(ErrUnprepared, namespaceimport.ErrCoverageIncomplete)
}

func (w *NamespaceImportWriter) stageNamespaceQuarantine(ctx context.Context, p namespaceimport.VerifiedPage, q namespaceimport.Quarantine) error {
	if !identity(q.CID) || !identity(q.Reason) || len(q.Evidence) == 0 {
		return ErrInvalid
	}
	if err := w.store.requireTX(ctx); err != nil {
		return err
	}
	if err := absent(ctx, w.store.db.Collection(Receipts), bson.M{"_id": importOriginReceiptID(w.header, q.CID)}); err != nil {
		return err
	}
	body, err := json.Marshal(q.Evidence)
	if err != nil {
		return err
	}
	row := namespaceQuarantine{ID: namespaceQuarantineID(q.CID), Kind: "permanent-namespace-quarantine-v1", ConversationID: q.CID, HeaderSHA256: w.header.SHA256(), InventorySHA256: p.InventorySHA256(), PageSHA256: p.SHA256(), Reason: q.Reason, EvidenceSHA256: hash(body)}
	return immutableImportDocument(ctx, w.store.db.Collection(Sequences), row.ID, row)
}
