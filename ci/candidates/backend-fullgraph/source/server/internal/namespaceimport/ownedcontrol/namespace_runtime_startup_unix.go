//go:build linux || darwin

package ownedcontrol

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"go.mongodb.org/mongo-driver/mongo"
	"io"
	"os"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

const NamespaceRuntimePreparationPathEnv = "LUOBO_NAMESPACE_RUNTIME_PREPARATION_PATH"
const NamespaceRuntimePreparationSHAEnv = "LUOBO_NAMESPACE_RUNTIME_PREPARATION_SHA256"

type namespaceRuntimeContextKey struct{}

func WithNamespaceRuntimeStartup(ctx context.Context, g *NamespaceRuntimeStartupGate) context.Context {
	if ctx == nil || g == nil {
		return ctx
	}
	if NamespaceRuntimeStartupFromContext(ctx) == g {
		return ctx
	}
	return context.WithValue(ownedmongo.WithTargetValidator(ctx, g), namespaceRuntimeContextKey{}, g)
}
func NamespaceRuntimeStartupFromContext(ctx context.Context) *NamespaceRuntimeStartupGate {
	if ctx == nil {
		return nil
	}
	g, _ := ctx.Value(namespaceRuntimeContextKey{}).(*NamespaceRuntimeStartupGate)
	return g
}

// Immutable fact of the real controlled publication. It permits the staged
// original resources to admit work; it is not a claim that every process has
// already started or any external callback/provider has delivered an effect.
// The subsequent original owner announcements/drains are independent evidence.
type NamespaceRuntimeAdmissionProof struct {
	ID                       string `json:"id" bson:"_id"`
	Kind                     string `json:"kind" bson:"kind"`
	HeaderSHA256             string `json:"header_sha256" bson:"header_sha256"`
	RuntimePreparationSHA256 string `json:"runtime_preparation_sha256" bson:"runtime_preparation_sha256"`
	HistoryPreparationSHA256 string `json:"history_preparation_sha256" bson:"history_preparation_sha256"`
	HistoryReadinessSHA256   string `json:"history_readiness_sha256" bson:"history_readiness_sha256"`
	InventorySHA256          string `json:"inventory_sha256" bson:"inventory_sha256"`
	ManifestSHA256           string `json:"manifest_sha256" bson:"manifest_sha256"`
	RequirementsSHA256       string `json:"requirements_sha256" bson:"requirements_sha256"`
	GraphJSON                string `json:"graph_json" bson:"graph_json"`
	GraphSHA256              string `json:"graph_sha256" bson:"graph_sha256"`
	LeaseID                  string `json:"lease_id" bson:"lease_id"`
	AdmissionGuardSHA256     string `json:"admission_guard_sha256" bson:"admission_guard_sha256"`
}

func NamespaceRuntimeAdmissionID(h namespaceimport.Header) string {
	raw, _ := json.Marshal([]string{h.Epoch, "namespace-runtime-admission", h.MigrationID})
	return sha(raw)
}
func NamespaceRuntimePreparationID(h namespaceimport.Header) string {
	raw, _ := json.Marshal([]string{h.Epoch, "namespace-runtime-preparation", h.MigrationID})
	return sha(raw)
}

type NamespaceRuntimeRelease struct {
	Version            string `json:"version"`
	KeyID              string `json:"key_id"`
	Role               string `json:"role"`
	InstanceID         string `json:"instance_id"`
	OwnerNonceSHA256   string `json:"owner_nonce_sha256"`
	RegistrationSHA256 string `json:"registration_sha256"`
	PublicationJSON    string `json:"publication_json"`
	PublicationSHA256  string `json:"publication_sha256"`
	Signature          []byte `json:"signature"`
}

func (r NamespaceRuntimeRelease) SigningBytes() ([]byte, error) {
	r.Signature = nil
	return json.Marshal(r)
}
func WriteNamespaceRuntimeRelease(path string, r NamespaceRuntimeRelease) (string, error) {
	if r.Version != "namespace-runtime-release-v1" || !namespaceRuntimeIdentity(r.KeyID) || !uuidPattern.MatchString(r.InstanceID) || !isSHA(r.OwnerNonceSHA256) || !isSHA(r.RegistrationSHA256) || !isSHA(r.PublicationSHA256) || sha([]byte(r.PublicationJSON)) != r.PublicationSHA256 || len(r.Signature) != ed25519.SignatureSize {
		return "", ErrOwnedEvidence
	}
	return writeServerEnvelope(path, r)
}

// Called only after the producer has re-read the known actual publication and
// its same live graph. A previous exact private release may be inspected; any
// difference/unknown file is retained and never overwritten or regenerated.
func WriteOrInspectNamespaceRuntimeRelease(path string, r NamespaceRuntimeRelease) (string, error) {
	if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
		return WriteNamespaceRuntimeRelease(path, r)
	} else if e != nil {
		return "", e
	}
	var saved NamespaceRuntimeRelease
	sum, e := readServerEnvelope(path, &saved)
	if e != nil {
		return "", e
	}
	a, _ := json.Marshal(saved)
	b, _ := json.Marshal(r)
	if !bytes.Equal(a, b) {
		return "", ErrOwnedEvidence
	}
	return sum, nil
}

// Cross-process transport for a known actual publication, never the publisher
// authority itself. Only the MD producer's real database/graph capability may
// sign it after its exact strong-TX receipt is inspected as committed. Non-Mongo
// ingress roles use this transport; actual namespace Store writers additionally
// check the original immutable database receipt in their own transaction.
type NamespaceRuntimeStartupGate struct {
	mu                 sync.Mutex
	bootstrap          NamespaceRuntimeBootstrap
	bootstrapSHA256    string
	nonce              string
	registration       NamespaceRuntimeRegistration
	registrationSHA256 string
	publication        NamespaceRuntimeAdmissionProof
	publicationSHA256  string
	retired            bool
	mongoDB            *mongo.Database
	mongoTargetSHA256  string
	mongoUnconfirmed   bool
}

func NewNamespaceRuntimeStartupGate(path, sum, role, instance, originalBootstrap, configuration, environment, nonce string) (*NamespaceRuntimeStartupGate, error) {
	b, e := ReadNamespaceRuntimeBootstrap(path, sum)
	if e != nil {
		return nil, e
	}
	if !isSHA(nonce) || !uuidPattern.MatchString(instance) || b.ExpectedInstanceID != "" && b.ExpectedInstanceID != instance || b.Requirement.Role != role || b.Requirement.BootstrapSHA256 != originalBootstrap || b.Requirement.ConfigurationSHA256 != configuration || b.Requirement.EnvironmentSHA256 != environment {
		return nil, ErrOwnedEvidence
	}
	return &NamespaceRuntimeStartupGate{bootstrap: b, bootstrapSHA256: sum, nonce: nonce, registration: NamespaceRuntimeRegistration{InstanceID: instance}}, nil
}
func (g *NamespaceRuntimeStartupGate) Bootstrap() NamespaceRuntimeBootstrap {
	if g == nil {
		return NamespaceRuntimeBootstrap{}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	raw, _ := json.Marshal(g.bootstrap)
	var copy NamespaceRuntimeBootstrap
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func (g *NamespaceRuntimeStartupGate) Retire() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.retired = true
	g.mu.Unlock()
}
func (g *NamespaceRuntimeStartupGate) CheckConstructionCurrent() error {
	if g == nil {
		return ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.retired || g.mongoUnconfirmed || g.registrationSHA256 == "unconfirmed" {
		return ErrOwnedEvidence
	}
	return nil
}

// The actual source-pinned factory supplies the sealed original services and
// concrete bindings digest; this does not execute queued workers or Serve.
// An exclusive write is attempted once and a write ambiguity cannot cause a
// different registration/nonce to be created over the original private path.
func (g *NamespaceRuntimeStartupGate) RecordPrepared(ctx context.Context, services, components []string, bindings NamespaceRuntimeBindings) (NamespaceRuntimeRegistration, error) {
	var empty NamespaceRuntimeRegistration
	if g == nil || ctx == nil || ctx.Err() != nil {
		return empty, ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.bootstrap
	if NamespaceRuntimeUsesMongo(b.Requirement.Role) {
		if g.retired || g.mongoUnconfirmed || g.mongoDB == nil || !isSHA(g.mongoTargetSHA256) || g.registrationSHA256 != "" {
			return empty, ErrOwnedEvidence
		}
		if e := namespaceRuntimeReadMongoTarget(ctx, g.mongoDB, b.Header, b.MongoCatalog); e != nil {
			g.mongoUnconfirmed = true
			return empty, e
		}
		if bindings.MongoTargetSHA256 != "" && bindings.MongoTargetSHA256 != g.mongoTargetSHA256 {
			return empty, ErrOwnedEvidence
		}
		bindings.MongoTargetSHA256 = g.mongoTargetSHA256
	} else if bindings.MongoTargetSHA256 != "" || g.mongoDB != nil {
		return empty, ErrOwnedEvidence
	}
	if g.retired || g.registrationSHA256 != "" || bindings.ValidateShape(b.Requirement.Role, b.Header.SHA256()) != nil {
		return empty, ErrOwnedEvidence
	}
	if len(services) == 0 && len(b.Requirement.Services) == 0 {
		services = b.Requirement.Services
	}
	a, _ := json.Marshal(services)
	expectedServices, _ := json.Marshal(b.Requirement.Services)
	c, _ := json.Marshal(components)
	f, _ := json.Marshal(b.Requirement.Components)
	if !bytes.Equal(a, expectedServices) || !bytes.Equal(c, f) {
		return empty, ErrOwnedEvidence
	}
	bindingsRaw, e := json.Marshal(bindings)
	if e != nil {
		return empty, e
	}
	r := NamespaceRuntimeRegistration{Version: "namespace-runtime-registered-not-active-v1", Role: b.Requirement.Role, InstanceID: g.registration.InstanceID, HeaderSHA256: b.Header.SHA256(), RuntimePreparationSHA256: b.RuntimePreparationSHA256, RuntimeTokenSHA256: sha([]byte(b.RuntimeToken)), InventorySHA256: b.InventorySHA256, ManifestSHA256: b.ManifestSHA256, RequirementsSHA256: b.RequirementsSHA256, BootstrapSHA256: b.Requirement.BootstrapSHA256, ConfigurationSHA256: b.Requirement.ConfigurationSHA256, EnvironmentSHA256: b.Requirement.EnvironmentSHA256, ContractSHA256: b.Requirement.ContractSHA256, Services: cloneNamespaceRuntimeIdentities(services), Components: append([]string(nil), components...), BindingsSHA256: sha(bindingsRaw), BindingsJSON: string(bindingsRaw)}
	if r.ValidateShape() != nil {
		return empty, ErrOwnedEvidence
	}
	// Retain the original identity even when the exclusive/fsync write fails.
	g.registration = r
	g.registrationSHA256 = "unconfirmed"
	sum, err := WriteNamespaceRuntimeRegistration(b.RegistrationPath, g.nonce, r)
	if err != nil {
		return empty, err
	}
	g.registrationSHA256 = sum
	return r, nil
}

// Same live object only; a missing file is not permission to repeat its failed
// write. This can confirm a completed exclusive/fsync result after a lost local
// ACK, but cannot replace an identity or re-run resource construction.
func (g *NamespaceRuntimeStartupGate) InspectPreparedRegistration(ctx context.Context) (bool, error) {
	if g == nil || ctx == nil || ctx.Err() != nil {
		return false, ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.retired || g.registrationSHA256 == "" {
		return false, ErrOwnedEvidence
	}
	var envelope namespaceRuntimeRegistrationEnvelope
	sum, e := readServerEnvelope(g.bootstrap.RegistrationPath, &envelope)
	if errors.Is(e, os.ErrNotExist) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	a, _ := json.Marshal(envelope.Registration)
	b, _ := json.Marshal(g.registration)
	if envelope.Schema != 1 || envelope.OwnerNonce != g.nonce || !bytes.Equal(a, b) || !isSHA(sum) || g.registrationSHA256 != "unconfirmed" && g.registrationSHA256 != sum {
		return false, ErrOwnedEvidence
	}
	g.registrationSHA256 = sum
	return true, nil
}
func namespaceRuntimeDecodeCanonical(raw string, out any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(out); e != nil {
		return e
	}
	var tail any
	if e := decoder.Decode(&tail); !errors.Is(e, io.EOF) {
		return ErrOwnedEvidence
	}
	canonical, e := json.Marshal(out)
	if e != nil || string(canonical) != raw {
		return ErrOwnedEvidence
	}
	return nil
}
func (g *NamespaceRuntimeStartupGate) acceptRelease(r NamespaceRuntimeRelease) error {
	b := g.bootstrap
	if g.retired || !isSHA(g.registrationSHA256) || r.Version != "namespace-runtime-release-v1" || r.KeyID != b.ReleaseKeyID || r.Role != b.Requirement.Role || r.InstanceID != g.registration.InstanceID || r.OwnerNonceSHA256 != sha([]byte(g.nonce)) || r.RegistrationSHA256 != g.registrationSHA256 || !isSHA(r.PublicationSHA256) || sha([]byte(r.PublicationJSON)) != r.PublicationSHA256 {
		return ErrOwnedEvidence
	}
	signed, e := r.SigningBytes()
	if e != nil || !ed25519.Verify(b.ReleasePublicKey, signed, r.Signature) {
		return ErrOwnedEvidence
	}
	var publication NamespaceRuntimeAdmissionProof
	if namespaceRuntimeDecodeCanonical(r.PublicationJSON, &publication) != nil || publication.ID != NamespaceRuntimeAdmissionID(b.Header) || publication.Kind != "namespace-runtime-admission-v1" || publication.HeaderSHA256 != b.Header.SHA256() || publication.RuntimePreparationSHA256 != b.RuntimePreparationSHA256 || publication.HistoryPreparationSHA256 != b.HistoryPreparationSHA256 || publication.InventorySHA256 != b.InventorySHA256 || publication.ManifestSHA256 != b.ManifestSHA256 || publication.RequirementsSHA256 != b.RequirementsSHA256 || !isSHA(publication.HistoryReadinessSHA256) || !isSHA(publication.LeaseID) || !isSHA(publication.AdmissionGuardSHA256) || sha([]byte(publication.GraphJSON)) != publication.GraphSHA256 {
		return ErrOwnedEvidence
	}
	var graph []ObservedNamespaceRuntime
	if namespaceRuntimeDecodeCanonical(publication.GraphJSON, &graph) != nil || len(graph) != len(NamespaceRuntimeRoles()) {
		return ErrOwnedEvidence
	}
	matched := false
	for at, row := range graph {
		if row.Role != NamespaceRuntimeRoles()[at] || row.PID <= 1 || row.StartToken == "" || row.BootID == "" || !isSHA(row.OwnerNonceSHA256) || !isSHA(row.RegistrationSHA256) || !isSHA(row.BinarySHA256) || !isSHA(row.SourceSHA256) || !isSHA(row.BuildReceiptSHA256) || row.Registration.ValidateShape() != nil || row.Registration.Role != row.Role || row.Registration.HeaderSHA256 != b.Header.SHA256() || row.Registration.RuntimePreparationSHA256 != b.RuntimePreparationSHA256 || row.Registration.InventorySHA256 != b.InventorySHA256 || row.Registration.ManifestSHA256 != b.ManifestSHA256 || row.Registration.RequirementsSHA256 != b.RequirementsSHA256 {
			return ErrOwnedEvidence
		}
		if validateNamespaceRuntimeRegistrationMongo(row.Registration, b) != nil {
			return ErrOwnedEvidence
		}
		if row.Role == b.Requirement.Role {
			left, _ := json.Marshal(row.Registration)
			right, _ := json.Marshal(g.registration)
			if string(left) != string(right) || row.PID != os.Getpid() || row.OwnerNonceSHA256 != r.OwnerNonceSHA256 || row.RegistrationSHA256 != g.registrationSHA256 || row.BinarySHA256 != b.Requirement.BinarySHA256 || row.SourceSHA256 != b.Requirement.SourceSHA256 || row.BuildReceiptSHA256 != b.Requirement.BuildReceiptSHA256 {
				return ErrOwnedEvidence
			}
			matched = true
		}
	}
	if !matched {
		return ErrOwnedEvidence
	}
	// The same exact immutable publication may be inspected again; another
	// header/graph/record never renews this process's original local ticket.
	if g.publicationSHA256 != "" && (g.publicationSHA256 != r.PublicationSHA256 || g.publication != publication) {
		return ErrOwnedEvidence
	}
	g.publication = publication
	g.publicationSHA256 = r.PublicationSHA256
	return nil
}
func (g *NamespaceRuntimeStartupGate) AwaitAdmission(ctx context.Context) error {
	if g == nil || ctx == nil || ctx.Done() == nil {
		return ErrOwnedEvidence
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		g.mu.Lock()
		if g.retired || g.registrationSHA256 == "" || g.registrationSHA256 == "unconfirmed" {
			g.mu.Unlock()
			return ErrOwnedEvidence
		}
		path := g.bootstrap.ReleasePath
		if g.publicationSHA256 != "" {
			g.mu.Unlock()
			return nil
		}
		g.mu.Unlock()
		var release NamespaceRuntimeRelease
		if _, e := readServerEnvelope(path, &release); e == nil {
			g.mu.Lock()
			e = g.acceptRelease(release)
			g.mu.Unlock()
			return e
		} else if !errors.Is(e, os.ErrNotExist) {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
func (g *NamespaceRuntimeStartupGate) KnownPublication() (NamespaceRuntimeAdmissionProof, string, error) {
	if g == nil {
		return NamespaceRuntimeAdmissionProof{}, "", ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.retired || !isSHA(g.publicationSHA256) {
		return NamespaceRuntimeAdmissionProof{}, "", ErrOwnedEvidence
	}
	return g.publication, g.publicationSHA256, nil
}

func cloneNamespaceRuntimeIdentities(v []string) []string {
	if v == nil {
		return nil
	}
	return append([]string{}, v...)
}
