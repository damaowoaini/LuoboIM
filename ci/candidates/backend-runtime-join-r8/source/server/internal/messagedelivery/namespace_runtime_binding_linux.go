//go:build linux

package messagedelivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"sort"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type namespaceRuntimeBindingEntry struct{ name, kind, token string }
type namespaceRuntimeComponent struct {
	entry   namespaceRuntimeBindingEntry
	store   *Store
	owner   any
	inspect func(context.Context) error
}
type namespaceRuntimeBindingContextKey struct{}
type NamespaceRuntimeBinding struct {
	mu          sync.Mutex
	gate        *ownedcontrol.NamespaceRuntimeStartupGate
	db          *mongo.Database
	writer      *NamespaceImportWriter
	preparation namespaceRuntimePreparationRecord
	history     historyReadinessPreparationRecord
	stores      map[*Store]namespaceRuntimeBindingEntry
	names       map[string]*Store
	components  map[string]namespaceRuntimeComponent
	sealed      bool
}

// The lower gate supplies only the original local nonce/bootstrap transport.
// This constructor independently verifies the full original signed preparation,
// source header/catalog/UUID and SAME ACTUAL DAO database pointer before any
// namespace Store can be bound. A bare DB name, key/hash or marker cannot mint it.
func NewNamespaceRuntimeBinding(ctx context.Context, db *mongo.Database, gate *ownedcontrol.NamespaceRuntimeStartupGate) (*NamespaceRuntimeBinding, error) {
	if ctx == nil || db == nil || gate == nil || mongo.SessionFromContext(ctx) != nil {
		return nil, ErrInvalid
	}
	b := gate.Bootstrap()
	if b.ValidateShape() != nil || db.Name() != b.Header.Database {
		return nil, ErrInvalid
	}
	base, e := NewStore(db, b.Header.Epoch, b.Header.ActivationSHA256)
	if e != nil {
		return nil, e
	}
	writer, e := NewNamespaceImportWriter(base, b.Header)
	if e != nil {
		return nil, e
	}
	if _, e = gate.MongoTargetForDatabase(ctx, db); e != nil {
		return nil, e
	}
	if e = writer.checkLiveTarget(ctx, true); e != nil {
		return nil, e
	}
	if e = writer.CheckImportCatalogReady(ctx); e != nil {
		return nil, e
	}
	preparation, found, e := writer.readNamespaceRuntimePreparation(ctx)
	if e != nil {
		return nil, e
	}
	if !found || historyReadinessDigest(preparation) != b.RuntimePreparationSHA256 || preparation.ID != b.RuntimePreparationID || preparation.HistoryPreparationID != b.HistoryPreparationID || preparation.HistoryPreparationSHA256 != b.HistoryPreparationSHA256 || preparation.InventorySHA256 != b.InventorySHA256 || preparation.ManifestSHA256 != b.ManifestSHA256 || preparation.RequirementsSHA256 != b.RequirementsSHA256 || preparation.TokenSHA256 != hash([]byte(b.RuntimeToken)) || preparation.MongoCatalogSHA256 != b.MongoCatalogSHA256 || preparation.ReleaseKeyID != b.ReleaseKeyID || string(preparation.ReleasePublicKey) != string(b.ReleasePublicKey) {
		return nil, ErrConflict
	}
	var requirements []ownedcontrol.NamespaceRuntimeRequirement
	if decodeImportJSON(preparation.RequirementsJSON, &requirements) != nil {
		return nil, ErrConflict
	}
	matches := 0
	for _, r := range requirements {
		a, _ := json.Marshal(r)
		z, _ := json.Marshal(b.Requirement)
		if string(a) == string(z) {
			matches++
		}
	}
	if matches != 1 {
		return nil, ErrConflict
	}
	history, found, e := writer.readHistoryPreparation(ctx)
	if e != nil {
		return nil, e
	}
	if !found || historyReadinessDigest(history) != b.HistoryPreparationSHA256 {
		return nil, ErrConflict
	}
	return &NamespaceRuntimeBinding{gate: gate, db: db, writer: writer, preparation: preparation, history: history, stores: map[*Store]namespaceRuntimeBindingEntry{}, names: map[string]*Store{}, components: map[string]namespaceRuntimeComponent{}}, nil
}
func NewNamespaceRuntimeBindingFromContext(ctx context.Context, db *mongo.Database) (*NamespaceRuntimeBinding, error) {
	return NewNamespaceRuntimeBinding(ctx, db, ownedcontrol.NamespaceRuntimeStartupFromContext(ctx))
}
func NewNamespaceRuntimeBindingForOwnedContext(ctx context.Context, db *mongo.Database) (*NamespaceRuntimeBinding, error) {
	gate := ownedcontrol.NamespaceRuntimeStartupFromContext(ctx)
	if gate == nil {
		if namespaceRuntimeBindingRequired() {
			return nil, ErrUnprepared
		}
		return nil, nil
	}
	return NewNamespaceRuntimeBinding(ctx, db, gate)
}
func (b *NamespaceRuntimeBinding) BindRuntimeContext(ctx context.Context) context.Context {
	if b == nil || ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, namespaceRuntimeBindingContextKey{}, b)
}
func (b *NamespaceRuntimeBinding) MatchesNamespaceRuntimeGate(g *ownedcontrol.NamespaceRuntimeStartupGate) bool {
	return b != nil && g != nil && b.gate == g
}
func namespaceRuntimeBindingFromContext(ctx context.Context) *NamespaceRuntimeBinding {
	if ctx == nil {
		return nil
	}
	b, _ := ctx.Value(namespaceRuntimeBindingContextKey{}).(*NamespaceRuntimeBinding)
	return b
}

func (b *NamespaceRuntimeBinding) bindStore(store *Store, name, kind string) error {
	if b == nil || b.writer == nil || b.db == nil || store == nil || store.db == nil || !identity(name) || kind != "namespace-store" && kind != "history-reader" {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	h := b.writer.header
	if b.sealed || store.originalDatabase != b.db || store.db.Client() != b.db.Client() || store.db.Name() != b.db.Name() || store.epoch != h.Epoch || store.activationSHA != h.ActivationSHA256 || store.runtimeBinding != nil {
		return ErrUnprepared
	}
	if _, ok := b.names[name]; ok {
		return ErrConflict
	}
	var token [32]byte
	if _, e := rand.Read(token[:]); e != nil {
		return e
	}
	b.stores[store] = namespaceRuntimeBindingEntry{name, kind, hex.EncodeToString(token[:])}
	b.names[name] = store
	store.runtimeBinding = b
	return nil
}
func (b *NamespaceRuntimeBinding) BindOriginalStore(store *Store, name string) error {
	return b.bindStore(store, name, "namespace-store")
}
func (b *NamespaceRuntimeBinding) BindOriginalReader(reader *HistoryWindowReader, name string) error {
	if reader == nil {
		return ErrInvalid
	}
	return b.bindStore(reader.store, name, "history-reader")
}
func (b *NamespaceRuntimeBinding) NewNamespaceStore(name string, membership bool) (*Store, error) {
	if b == nil || b.writer == nil {
		return nil, ErrUnprepared
	}
	h := b.writer.header
	var store *Store
	var e error
	if membership {
		store, e = NewMembershipBoundStore(b.db, h.Epoch, h.ActivationSHA256)
	} else {
		store, e = NewStore(b.db, h.Epoch, h.ActivationSHA256)
	}
	if e != nil {
		return nil, e
	}
	if e = b.BindOriginalStore(store, name); e != nil {
		return nil, e
	}
	return store, nil
}
func NewRuntimeMembershipStore(ctx context.Context, db *mongo.Database, epoch, activation, label string) (*Store, error) {
	b := namespaceRuntimeBindingFromContext(ctx)
	if b == nil {
		if namespaceRuntimeBindingRequired() {
			return nil, ErrUnprepared
		}
		return NewMembershipBoundStore(db, epoch, activation)
	}
	if b.db != db || b.writer.header.Epoch != epoch || b.writer.header.ActivationSHA256 != activation {
		return nil, ErrConflict
	}
	return b.NewNamespaceStore(label, true)
}
func (b *NamespaceRuntimeBinding) NewHistoryReader(ctx context.Context, name string) (*HistoryWindowReader, error) {
	if b == nil {
		return nil, ErrUnprepared
	}
	reader, e := NewHistoryWindowReaderFromEnvironment(b.db)
	if e != nil {
		return nil, e
	}
	if e = b.BindOriginalReader(reader, name); e != nil {
		return nil, e
	}
	return reader, nil
}

// Only catalog construction, never history permission, TX admission or writer
// readiness. Two initial readers may be constructed before DataReady; all other
// role constructors still require the actual matching original DataReady row.
func (b *NamespaceRuntimeBinding) CheckPreparedCatalog(ctx context.Context) error {
	if b == nil || b.writer == nil || b.db == nil || ctx == nil {
		return ErrUnprepared
	}
	ticket := b.gate.Bootstrap()
	got, found, e := b.writer.readBoundHistoryPreparation(ctx, ticket.HistoryPreparationSHA256)
	if e != nil {
		return e
	}
	if !found || historyReadinessDigest(got) != historyReadinessDigest(b.history) {
		return ErrConflict
	}
	if e = b.writer.CheckImportCatalogReady(ctx); e != nil {
		return e
	}
	if ticket.Requirement.Role == "rpc-msg" || ticket.Requirement.Role == "rpc-conversation" {
		return nil
	}
	readiness, found, e := b.writer.readHistoryReadinessWithPreparation(ctx, b.history, true)
	if e != nil {
		return e
	}
	if !found || readiness.PreparationSHA256 != ticket.HistoryPreparationSHA256 {
		return ErrUnprepared
	}
	return b.writer.store.checkMembershipSchema(ctx)
}

// Called by Store.CheckReady/requireTX on the SAME concrete instance. The
// original full signed preparation was verified once above; every invocation
// still reads exact original immutable metadata and current epoch/member gates.
// No role gains permission from this metadata being present on another Store.
func (b *NamespaceRuntimeBinding) validateNamespaceRuntimeStore(ctx context.Context, store *Store) error {
	if b == nil || b.writer == nil || b.db == nil || ctx == nil || store == nil || store.db == nil {
		return ErrUnprepared
	}
	b.mu.Lock()
	_, bound := b.stores[store]
	db := b.db
	b.mu.Unlock()
	if !bound || store.runtimeBinding != b || store.originalDatabase != db || store.db.Client() != db.Client() || store.db.Name() != db.Name() || store.epoch != b.writer.header.Epoch || store.activationSHA != b.writer.header.ActivationSHA256 {
		return ErrUnprepared
	}
	proof, expected, e := b.gate.KnownPublication()
	if e != nil {
		return ErrNamespaceWritersUnbound
	}
	var raw bson.Raw
	if e = b.writer.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": proof.ID}).Decode(&raw); e != nil {
		return e
	}
	var actual ownedcontrol.NamespaceRuntimeAdmissionProof
	if !exactFields(raw, []string{"_id", "kind", "header_sha256", "runtime_preparation_sha256", "history_preparation_sha256", "history_readiness_sha256", "inventory_sha256", "manifest_sha256", "requirements_sha256", "graph_json", "graph_sha256", "lease_id", "admission_guard_sha256"}, nil, nil, "") || bson.Unmarshal(raw, &actual) != nil || historyReadinessDigest(actual) != expected || actual != proof {
		return ErrConflict
	}
	var runtimeRaw bson.Raw
	if e = b.writer.store.db.Collection(Receipts).FindOne(ctx, bson.M{"_id": b.preparation.ID}).Decode(&runtimeRaw); e != nil {
		return e
	}
	var runtime namespaceRuntimePreparationRecord
	if !importUniqueFields(runtimeRaw) || bson.Unmarshal(runtimeRaw, &runtime) != nil || historyReadinessDigest(runtime) != historyReadinessDigest(b.preparation) {
		return ErrConflict
	}
	original, found, e := b.writer.readBoundHistoryPreparation(ctx, b.preparation.HistoryPreparationSHA256)
	if e != nil {
		return e
	}
	if !found || historyReadinessDigest(original) != historyReadinessDigest(b.history) {
		return ErrConflict
	}
	readiness, found, e := b.writer.readHistoryReadinessWithPreparation(ctx, b.history, true)
	if e != nil {
		return e
	}
	if !found || historyReadinessDigest(readiness) != proof.HistoryReadinessSHA256 {
		return ErrConflict
	}
	return b.writer.store.checkMembershipSchema(ctx)
}

// Only these finite policy-inventory methods call this entry. It cannot be
// used by CheckReady, requireTX, history queries, workers or dispatch APIs.
// Configured retained adapters must be constructed while admission is closed,
// otherwise their readiness check and the global activation would deadlock.
func (b *NamespaceRuntimeBinding) checkNamespaceRuntimePolicyCatalog(ctx context.Context, store *Store) error {
	if b == nil || ctx == nil || store == nil || !store.membershipRequired || mongo.SessionFromContext(ctx) != nil || namespaceRuntimeBindingFromContext(ctx) != b {
		return ErrUnprepared
	}
	b.mu.Lock()
	_, bound := b.stores[store]
	sealed := b.sealed
	b.mu.Unlock()
	if !bound || store.runtimeBinding != b || b.gate.CheckConstructionCurrent() != nil {
		return ErrUnprepared
	}
	if _, _, e := b.gate.KnownPublication(); e == nil {
		return store.CheckKafkaReady(ctx)
	}
	if sealed {
		return ErrNamespaceWritersUnbound
	}
	if e := b.CheckPreparedCatalog(ctx); e != nil {
		return e
	}
	return store.checkCatalog(ctx, store.checkDataEpoch)
}

func (b *NamespaceRuntimeBinding) SealNamespaceBindings(ctx context.Context, resources []string, source ownedmongo.Snapshot) (ownedcontrol.NamespaceRuntimeBindings, error) {
	var out ownedcontrol.NamespaceRuntimeBindings
	if b == nil || ctx == nil || ctx.Err() != nil || b.gate.CheckConstructionCurrent() != nil || b.CheckPreparedCatalog(ctx) != nil {
		return out, ErrUnprepared
	}
	b.mu.Lock()
	if b.sealed {
		b.mu.Unlock()
		return out, ErrConflict
	}
	components := make([]namespaceRuntimeComponent, 0, len(b.components))
	for _, component := range b.components {
		components = append(components, component)
	}
	b.mu.Unlock()
	// Inventory reads run outside the local lock. Source construction is still
	// closed; a concurrent registration changes the count checked below.
	for _, component := range components {
		if e := component.inspect(ctx); e != nil {
			return out, e
		}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed || len(components) != len(b.components) {
		return out, ErrConflict
	}

	ticket := b.gate.Bootstrap()
	out = ownedcontrol.NamespaceRuntimeBindings{Role: ticket.Requirement.Role, HeaderSHA256: ticket.Header.SHA256(), Resources: append([]string(nil), resources...), Source: source}
	required := map[string][]string{"rpc-msg": {"msg-history", "msg-namespaces"}, "rpc-conversation": {"conversation-history"}, "rpc-group": {"group-history", "new-group-namespaces", "group-receiver"}, "msgtransfer-managed": {"ordinary-history", "ordinary-namespaces", "ordinary-offline", "ordinary-intake"}, "crontask": {"cron-history"}}[ticket.Requirement.Role]
	if len(required) == 0 {
		return out, ErrUnprepared
	}
	for _, name := range required {
		if b.names[name] == nil {
			return out, ErrUnprepared
		}
	}
	for store, entry := range b.stores {
		if store.runtimeBinding != b || store.originalDatabase != b.db || store.db.Client() != b.db.Client() || store.db.Name() != b.db.Name() || store.epoch != ticket.Header.Epoch || store.activationSHA != ticket.Header.ActivationSHA256 {
			return out, ErrConflict
		}
		out.Local = append(out.Local, ownedcontrol.NamespaceRuntimeLocalBinding{Name: entry.name, Kind: entry.kind, InstanceSHA256: hash([]byte(entry.token)), HeaderSHA256: ticket.Header.SHA256(), ContractSHA256: ticket.Requirement.ContractSHA256})
	}
	for _, component := range components {
		if component.store.runtimeBinding != b || b.stores[component.store].name == "" || component.owner == nil {
			return out, ErrUnprepared
		}
		out.Local = append(out.Local, ownedcontrol.NamespaceRuntimeLocalBinding{Name: component.entry.name, Kind: component.entry.kind, InstanceSHA256: hash([]byte(component.entry.token)), HeaderSHA256: ticket.Header.SHA256(), ContractSHA256: ticket.Requirement.ContractSHA256})
	}
	if ticket.Requirement.Role == "rpc-group" && b.components["group-provider-callbacks"].owner == nil {
		return out, ErrUnprepared
	}
	if ticket.Requirement.Role == "msgtransfer-managed" && (b.components["ordinary-offline-providers"].owner == nil || b.components["ordinary-online-providers"].owner == nil) {
		return out, ErrUnprepared
	}
	sort.Slice(out.Local, func(i, j int) bool { return out.Local[i].Name < out.Local[j].Name })
	target, e := b.gate.MongoTargetForDatabase(ctx, b.db)
	if e != nil {
		return out, e
	}
	out.MongoTargetSHA256 = target
	if out.ValidateShape(ticket.Requirement.Role, ticket.Header.SHA256()) != nil {
		return out, ErrUnprepared
	}
	b.sealed = true
	return out, nil
}
func (b *NamespaceRuntimeBinding) ValidateRuntimeStores(ctx context.Context) error {
	if b == nil || ctx == nil {
		return ErrUnprepared
	}
	b.mu.Lock()
	if !b.sealed {
		b.mu.Unlock()
		return ErrUnprepared
	}
	var names []string
	for name := range b.names {
		names = append(names, name)
	}
	sort.Strings(names)
	stores := make([]*Store, 0, len(names))
	for _, name := range names {
		stores = append(stores, b.names[name])
	}
	b.mu.Unlock()
	for _, store := range stores {
		if e := store.CheckReady(ctx); e != nil {
			return e
		}
	}
	return nil
}

// Only source-owned constructors holding the same private parent context and
// actual bound Store can register a concrete retained runtime. No external DTO,
// marker, profile hash or callback success substitutes for the retained object.
func BindPreparedNamespaceRuntime(ctx context.Context, store *Store, name, kind string, owner any, inspect func(context.Context) error) error {
	b := namespaceRuntimeBindingFromContext(ctx)
	if b == nil {
		if namespaceRuntimeBindingRequired() {
			return ErrUnprepared
		}
		return nil
	}
	if ctx == nil || store == nil || !identity(name) || owner == nil || inspect == nil || kind != "frozen-online-runtime" && kind != "frozen-offline-runtime" && kind != "frozen-group-runtime" && kind != "retained-callback-provider" {
		return ErrInvalid
	}
	v := reflect.ValueOf(owner)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return ErrInvalid
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.sealed || b.gate.CheckConstructionCurrent() != nil || b.stores[store].name == "" || store.runtimeBinding != b || b.names[name] != nil {
		return ErrUnprepared
	}
	if _, ok := b.components[name]; ok {
		return ErrConflict
	}
	var token [32]byte
	if _, e := rand.Read(token[:]); e != nil {
		return e
	}
	b.components[name] = namespaceRuntimeComponent{namespaceRuntimeBindingEntry{name, kind, hex.EncodeToString(token[:])}, store, owner, inspect}
	return nil
}
