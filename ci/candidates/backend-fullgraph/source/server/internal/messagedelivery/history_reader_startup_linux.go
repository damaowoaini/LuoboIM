//go:build linux

package messagedelivery

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"go.mongodb.org/mongo-driver/mongo"
)

// This separate startup capability NEVER changes HistoryWindowReader.CheckReady
// or any public read/query/filter method. All those methods continue requiring
// the real schema. It is used only by source-owned constructors BEFORE Serve.
type HistoryReaderStartupBinding struct {
	mu                        sync.Mutex
	reader                    *HistoryWindowReader
	store                     *Store
	writer                    *NamespaceImportWriter
	bootstrap                 ownedcontrol.HistoryReaderBootstrap
	registration              ownedcontrol.HistoryReaderRegistration
	registrationSHA256        string
	preparationVerifiedSHA256 string
	ownerNonceSHA256          string
	bound                     bool
}

// Empty path AND SHA retains the exact ordinary factory and readiness check.
// An owned RPC caller supplies the validated role; unowned factories must never
// call this function with private preparation inputs merely because env is set.
func NewStartupHistoryWindowReader(ctx context.Context, db *mongo.Database, role, path, exactSHA string) (*HistoryWindowReader, *HistoryReaderStartupBinding, error) {
	reader, e := NewHistoryWindowReaderFromEnvironment(db)
	if e != nil {
		return nil, nil, e
	}
	if path == "" && exactSHA == "" {
		if e = reader.CheckReady(ctx); e != nil {
			return nil, nil, e
		}
		return reader, nil, nil
	}
	if reader == nil || reader.store == nil || !reader.store.membershipRequired || path == "" || !validSHA(exactSHA) || role != "rpc-msg" && role != "rpc-conversation" {
		return nil, nil, ErrUnprepared
	}
	b, e := ownedcontrol.ReadHistoryReaderBootstrap(path, exactSHA)
	if e != nil || b.Requirement.Role != role || b.Header.Database != db.Name() || b.Header.Epoch != reader.store.epoch || b.Header.ActivationSHA256 != reader.store.activationSHA || b.Requirement.ReadContractSHA256 != HistoryReaderContractSHA256() {
		return nil, nil, ErrConflict
	}
	base, e := NewStore(db, b.Header.Epoch, b.Header.ActivationSHA256)
	if e != nil {
		return nil, nil, e
	}
	writer, e := NewNamespaceImportWriter(base, b.Header)
	if e != nil {
		return nil, nil, e
	}
	binding := &HistoryReaderStartupBinding{reader: reader, store: reader.store, writer: writer, bootstrap: b}
	if e = binding.CheckPrepared(ctx); e != nil {
		return nil, nil, e
	}
	reader.store.historyReaderBinding = binding
	return reader, binding, nil
}

// Only data/catalog/publication and this exact private preparation are checked.
// No Ready flags, admission or message effect are manufactured on a cache miss.
func (b *HistoryReaderStartupBinding) CheckPrepared(ctx context.Context) error {
	if b == nil || ctx == nil || b.reader == nil || b.writer == nil || mongo.SessionFromContext(ctx) != nil {
		return ErrUnprepared
	}
	if e := b.writer.CheckImportCatalogReady(ctx); e != nil {
		return e
	}
	boot := b.bootstrap
	b.mu.Lock()
	verified := b.preparationVerifiedSHA256
	b.mu.Unlock()
	var row historyReadinessPreparationRecord
	var found bool
	var e error
	if verified == boot.PreparationSHA256 {
		row, found, e = b.writer.readBoundHistoryPreparation(ctx, verified)
	} else {
		row, found, e = b.writer.readHistoryPreparation(ctx)
	}
	if e != nil {
		return e
	}
	if !found || row.ID != boot.PreparationID || historyReadinessDigest(row) != boot.PreparationSHA256 || row.InventorySHA256 != boot.InventorySHA256 || row.ManifestSHA256 != boot.ManifestSHA256 || row.RequirementsSHA256 != boot.RequirementsSHA256 || !historyReadinessTokenMatches(boot.PreparationToken, row.TokenSHA256) {
		return ErrConflict
	}
	var requirements []ownedcontrol.HistoryReaderRequirement
	if decodeImportJSON(row.RequirementsJSON, &requirements) != nil {
		return ErrConflict
	}
	matches := 0
	expected, _ := json.Marshal(boot.Requirement)
	for _, r := range requirements {
		body, _ := json.Marshal(r)
		if string(body) == string(expected) {
			matches++
		}
	}
	if matches != 1 {
		return ErrConflict
	}
	var inventory namespaceimport.Inventory
	if e = decodeImportJSON(row.InventoryJSON, &inventory); e != nil {
		return e
	}
	_, e = b.writer.checkPublishedReadinessData(ctx, inventory, row.ManifestSHA256)
	if e != nil {
		return e
	}
	b.mu.Lock()
	b.preparationVerifiedSHA256 = boot.PreparationSHA256
	b.mu.Unlock()
	return nil
}

// Runtime identity comes from the original validated owned bootstrap, not from
// network input or the preparation JSON. Services are the actual gRPC server's
// registration snapshot after its source-owned scope has been sealed.
func (b *HistoryReaderStartupBinding) RecordRegistration(ctx context.Context, instance, bootstrapSHA, configurationSHA, environmentSHA, ownerNonce string, actualServices []string) (ownedcontrol.HistoryReaderRegistration, error) {
	var empty ownedcontrol.HistoryReaderRegistration
	if b == nil || b.CheckPrepared(ctx) != nil {
		return empty, ErrUnprepared
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.bound {
		return empty, ErrConflict
	}
	boot := b.bootstrap
	r := boot.Requirement
	actualServices = append([]string(nil), actualServices...)
	sort.Strings(actualServices)
	actual, _ := json.Marshal(actualServices)
	expected, _ := json.Marshal(r.Services)
	if string(actual) != string(expected) || bootstrapSHA != r.BootstrapSHA256 || configurationSHA != r.ConfigurationSHA256 || environmentSHA != r.EnvironmentSHA256 || !validSHA(ownerNonce) {
		return empty, ErrConflict
	}
	value := ownedcontrol.HistoryReaderRegistration{Version: "history-reader-registered-not-serving-v1", Role: r.Role, Mode: "required", InstanceID: instance, HeaderSHA256: boot.Header.SHA256(), DatabaseSHA256: hash([]byte(boot.Header.Database)), Epoch: boot.Header.Epoch, ActivationSHA256: boot.Header.ActivationSHA256, PreparationID: boot.PreparationID, PreparationSHA256: boot.PreparationSHA256, PreparationTokenSHA256: hash([]byte(boot.PreparationToken)), InventorySHA256: boot.InventorySHA256, ManifestSHA256: boot.ManifestSHA256, RequirementsSHA256: boot.RequirementsSHA256, BootstrapSHA256: bootstrapSHA, ConfigurationSHA256: configurationSHA, EnvironmentSHA256: environmentSHA, ReadContractSHA256: r.ReadContractSHA256, Services: actualServices}
	sum, e := ownedcontrol.WriteHistoryReaderRegistration(boot.BindingPath, ownerNonce, value)
	if e != nil {
		return empty, e
	}
	b.registration = value
	b.registrationSHA256 = sum
	b.ownerNonceSHA256 = hash([]byte(ownerNonce))
	b.bound = true
	return value, nil
}

// This private capability is attached ONLY to the exact prepared DAO Store.
// checkEpoch consumes it both in CheckReady and in the original strong read
// transaction. The separate import-writer Store has no callback, so proof
// reads do not recursively call this reader. No network/OS refresh is done in
// retry callbacks; current exact Config/Inventory/publication is read using
// the same Mongo session as the history query, receipt and final slot filter.
func (b *HistoryReaderStartupBinding) validateReaderStore(ctx context.Context, s *Store) error {
	if b == nil || ctx == nil || s == nil || s.db == nil || b.store == nil || s != b.store || b.reader == nil || b.reader.store != s || b.writer == nil || b.writer.store == nil || b.writer.store.db == nil || s.db.Client() != b.writer.store.db.Client() || s.db.Name() != b.bootstrap.Header.Database || s.epoch != b.bootstrap.Header.Epoch || s.activationSHA != b.bootstrap.Header.ActivationSHA256 {
		return ErrUnprepared
	}
	b.mu.Lock()
	bound, registration, registrationSHA, nonceSHA, verified := b.bound, b.registration, b.registrationSHA256, b.ownerNonceSHA256, b.preparationVerifiedSHA256
	b.mu.Unlock()
	if !bound || !validSHA(registrationSHA) || !validSHA(nonceSHA) || verified != b.bootstrap.PreparationSHA256 {
		return ErrUnprepared
	}
	preparation, found, e := b.writer.readBoundHistoryPreparation(ctx, verified)
	if e != nil {
		return e
	}
	if !found || historyReadinessDigest(preparation) != b.bootstrap.PreparationSHA256 || !historyReadinessTokenMatches(b.bootstrap.PreparationToken, preparation.TokenSHA256) {
		return ErrConflict
	}
	var inventory namespaceimport.Inventory
	if decodeImportJSON(preparation.InventoryJSON, &inventory) != nil {
		return ErrConflict
	}
	if _, e = b.writer.checkPublishedReadinessData(ctx, inventory, b.bootstrap.ManifestSHA256); e != nil {
		return e
	}
	ready, found, e := b.writer.readHistoryReadinessWithPreparation(ctx, preparation, found)
	if e != nil {
		return e
	}
	if !found {
		return ErrUnprepared
	}
	var rows []ownedcontrol.ObservedHistoryReader
	if decodeImportJSON(ready.GraphJSON, &rows) != nil {
		return ErrConflict
	}
	matches := 0
	for _, r := range rows {
		if r.Role != registration.Role {
			continue
		}
		expected, _ := json.Marshal(registration)
		actual, _ := json.Marshal(r.Registration)
		if string(actual) != string(expected) || r.PID != os.Getpid() || r.OwnerNonceSHA256 != nonceSHA || r.RegistrationSHA256 != registrationSHA {
			return ErrConflict
		}
		matches++
	}
	if matches != 1 {
		return ErrConflict
	}
	return nil
}

// Polls the one original O(1) readiness receipt only. This never performs a
// fresh history capture, marks messages, writes an ACK or retries a business
// operation. Context cancellation retains all original private evidence.
func (b *HistoryReaderStartupBinding) AwaitReadReady(ctx context.Context, ownerNonce string) error {
	if b == nil || ctx == nil || !validSHA(ownerNonce) {
		return ErrUnprepared
	}
	b.mu.Lock()
	bound := b.bound
	registration := b.registration
	registrationSHA := b.registrationSHA256
	b.mu.Unlock()
	if !bound {
		return ErrUnprepared
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e := b.CheckPrepared(ctx); e != nil {
			return e
		}
		preparation, present, e := b.writer.readBoundHistoryPreparation(ctx, b.bootstrap.PreparationSHA256)
		if e != nil {
			return e
		}
		if !present {
			return ErrConflict
		}
		ready, found, e := b.writer.readHistoryReadinessWithPreparation(ctx, preparation, present)
		if e != nil {
			return e
		}
		if found {
			if ready.PreparationSHA256 != b.bootstrap.PreparationSHA256 || ready.RequirementsSHA256 != b.bootstrap.RequirementsSHA256 || ready.InventorySHA256 != b.bootstrap.InventorySHA256 || ready.ManifestSHA256 != b.bootstrap.ManifestSHA256 {
				return ErrConflict
			}
			var rows []ownedcontrol.ObservedHistoryReader
			if decodeImportJSON(ready.GraphJSON, &rows) != nil {
				return ErrConflict
			}
			matches := 0
			for _, r := range rows {
				if r.Role != registration.Role {
					continue
				}
				expected, _ := json.Marshal(registration)
				actual, _ := json.Marshal(r.Registration)
				if string(actual) != string(expected) || r.PID != os.Getpid() || r.OwnerNonceSHA256 != hash([]byte(ownerNonce)) || r.RegistrationSHA256 != registrationSHA {
					return ErrConflict
				}
				matches++
			}
			if matches != 1 {
				return ErrConflict
			}
			return b.checkPublishedDataReady(ctx)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ReadReady is not final writer/runtime activation. Keep the original hard
// predicate and wait while its explicit "writers unbound" state remains. This
// avoids a new registration/activation cycle without opening Serve early.
func (b *HistoryReaderStartupBinding) AwaitFinalRuntimeReady(ctx context.Context) error {
	if b == nil || ctx == nil {
		return ErrUnprepared
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e := b.CheckPrepared(ctx); e != nil {
			return e
		}
		e := b.reader.store.CheckNewGroupSeedRuntimeReady(ctx)
		if e == nil {
			return nil
		}
		if !errors.Is(e, ErrNamespaceWritersUnbound) {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Private data phase only. The exact original nonce/DAO proof is still checked;
// public Reader.CheckReady/transactions remain closed by runtimeBinding until
// the complete graph admission. This breaks no permission gate and avoids a
// DataReady -> Runtime-registration -> DataReady startup cycle.
func (b *HistoryReaderStartupBinding) checkPublishedDataReady(ctx context.Context) error {
	if b == nil || b.store == nil || b.reader == nil || b.reader.store != b.store || b.store.historyReaderBinding != b {
		return ErrUnprepared
	}
	return b.store.checkCatalog(ctx, func(current context.Context) error {
		if e := b.store.checkDataEpoch(current); e != nil {
			return e
		}
		return b.validateReaderStore(current, b.store)
	})
}
