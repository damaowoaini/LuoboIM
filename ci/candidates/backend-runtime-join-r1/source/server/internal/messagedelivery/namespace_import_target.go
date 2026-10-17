package messagedelivery

import (
	"context"
	"errors"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// This is an offline operator adapter. Its store is deliberately a base Store:
// membership readiness can only be published after every verified owner row.
// A runtime service must continue to use its required-mode startup factories.
type NamespaceImportWriter struct {
	store  *Store
	header namespaceimport.Header
}

func NewNamespaceImportWriter(store *Store, h namespaceimport.Header) (*NamespaceImportWriter, error) {
	if store == nil || store.db == nil || h.Validate() != nil || store.db.Name() != h.Database || store.epoch != h.Epoch || store.activationSHA != h.ActivationSHA256 || !validUUID(h.StorageInstance) {
		return nil, ErrInvalid
	}
	base, err := NewStore(store.db, store.epoch, store.activationSHA)
	if err != nil {
		return nil, err
	}
	return &NamespaceImportWriter{store: base, header: h}, nil
}

func importUniqueFields(raw bson.Raw) bool {
	elements, err := raw.Elements()
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	for _, e := range elements {
		if seen[e.Key()] {
			return false
		}
		seen[e.Key()] = true
	}
	return true
}

func (w *NamespaceImportWriter) validHeader(h namespaceimport.Header) bool {
	return w != nil && w.store != nil && w.store.db != nil && h.Validate() == nil && h == w.header && h.Database == w.store.db.Name() && h.Epoch == w.store.epoch && h.ActivationSHA256 == w.store.activationSHA
}

// CheckTarget is the controller's initial, read-only target check before it can
// acquire a real exclusion hold. An absent storage marker is only provisional
// at this phase. It grants no import, implicit initialization or runtime Ready.
// The held metadata bootstrap and strict catalog check MUST precede capture.
func (w *NamespaceImportWriter) CheckTarget(ctx context.Context, h namespaceimport.Header) error {
	if !w.validHeader(h) || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return w.checkLiveTarget(current, false)
}

func (w *NamespaceImportWriter) checkLiveTarget(ctx context.Context, requireStorage bool) error {
	return w.checkLiveTargetMode(ctx, requireStorage, true)
}

func (w *NamespaceImportWriter) checkLiveTargetMode(ctx context.Context, requireStorage, originalProcess bool) error {
	if w == nil || w.store == nil || w.store.db == nil || ctx == nil {
		return ErrInvalid
	}
	var hello bson.Raw
	if err := w.store.db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return errors.Join(ErrUnprepared, err)
	}
	if !importUniqueFields(hello) {
		return ErrConflict
	}
	primary, pok := hello.Lookup("isWritablePrimary").BooleanOK()
	set, sok := hello.Lookup("setName").StringValueOK()
	top, tok := hello.Lookup("topologyVersion").DocumentOK()
	if !pok || !primary || !sok || set != w.header.ReplicaSet || !tok || !importUniqueFields(top) {
		return ErrConflict
	}
	pid, iok := top.Lookup("processId").ObjectIDOK()
	counter, cok := top.Lookup("counter").Int64OK()
	if !iok || pid.IsZero() || !cok || counter < 0 || originalProcess && (pid.Hex() != w.header.MongoProcess || counter != w.header.MongoTopology) {
		return ErrConflict
	}
	return w.checkStorageIdentity(ctx, requireStorage)
}

// A restarted process cannot acquire another write permit under the original
// capture. It may nevertheless reveal an already committed immutable receipt
// from the same primary replica set/database/storage UUID/tenant. Inspection
// grants no activation, replacement source capture or replay after absence.
func (w *NamespaceImportWriter) checkImportInspectionReady(ctx context.Context) error {
	if w == nil || !w.validHeader(w.header) || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	return w.checkLiveTargetMode(ctx, true, false)
}

func (w *NamespaceImportWriter) checkStorageIdentity(ctx context.Context, required bool) error {
	var raw bson.Raw
	err := w.store.db.Collection("luobo_storage_identity").FindOne(ctx, bson.M{"_id": "message-visibility-v1"}).Decode(&raw)
	if errors.Is(err, mongo.ErrNoDocuments) && !required {
		return nil
	}
	if err != nil {
		return errors.Join(ErrUnprepared, err)
	}
	if !importUniqueFields(raw) {
		return ErrConflict
	}
	id, iok := raw.Lookup("_id").StringValueOK()
	instance, sok := raw.Lookup("instance").StringValueOK()
	if !iok || id != "message-visibility-v1" || !sok || !validUUID(instance) || instance != w.header.StorageInstance {
		return ErrConflict
	}
	value := raw.Lookup("tenant")
	tenant, tok := value.StringValueOK()
	if value.Type == 0 && !required {
		return nil
	}
	if !tok || tenant != w.header.Tenant {
		return ErrConflict
	}
	return nil
}

// Unlike CheckTarget, this rejects missing tenant/storage identity and requires
// the real epoch/catalog prepared under the private AdmissionHold. It performs
// no DDL, counter seeding, owner inference, marker repair or Ready publication.
func (w *NamespaceImportWriter) CheckImportCatalogReady(ctx context.Context) error {
	if w == nil || !w.validHeader(w.header) || ctx == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrInvalid
	}
	current, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := w.checkLiveTarget(current, true); err != nil {
		return err
	}
	return w.store.CheckReady(current)
}

// A page transaction may only consume the original live-bound header and the
// private one-value permit. External lifecycle I/O remains outside transaction
// retries; this local check is repeated within every actual Mongo attempt.
func (w *NamespaceImportWriter) requireImportPage(ctx context.Context, p *namespaceimport.Permit, v namespaceimport.VerifiedPage) error {
	if w == nil || !w.validHeader(w.header) || ctx == nil || p == nil {
		return ErrInvalid
	}
	if err := p.ValidatePage(w.header, v); err != nil {
		return err
	}
	if !identity(p.AttemptID()) || !identity(p.LeaseID()) || !validSHA(p.FenceSHA256()) {
		return ErrConflict
	}
	if err := w.store.requireTX(ctx); err != nil {
		return err
	}
	if err := w.checkStorageIdentity(ctx, true); err != nil {
		return err
	}
	data := v.Data()
	if data.HeaderSHA256 != w.header.SHA256() || !validSHA(v.InventorySHA256()) || !validSHA(v.SHA256()) || data.Identity().SHA256 != v.SHA256() {
		return ErrConflict
	}
	return nil
}
