//go:build linux || darwin

package ownedcontrol

import (
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
)

// Private source-owned bootstrap protocol, never an OpenIM message/wire change.
// Old binaries reject these versions before resources/listeners can be opened.
const NamespaceRuntimeServerBootstrapVersion = "owned-server-namespace-admission-v2"
const NamespaceRuntimeManagedBootstrapVersion = "owned-ordinary-namespace-admission-v2"

// This finite graph is the actual required-mode replacement graph. Raw legacy
// msgtransfer and extend-msg's empty placeholder are not serving replacements.
// Required push retains token administration; delivery is owned by the real
// group/ordinary online/offline runtimes, never a raw PushMsg/Kafka fallback.
// A missing required role is an incomplete graph, not an optional capability.
func NamespaceRuntimeRoles() []string {
	return []string{"api", "crontask", "msggateway", "msgtransfer-managed", "push", "rpc-auth", "rpc-conversation", "rpc-encryption", "rpc-friend", "rpc-group", "rpc-msg", "rpc-rtc", "rpc-third", "rpc-user"}
}

type NamespaceRuntimeRequirement struct {
	Role                string   `json:"role"`
	BinarySHA256        string   `json:"binary_sha256"`
	SourceSHA256        string   `json:"source_sha256"`
	BuildReceiptSHA256  string   `json:"build_receipt_sha256"`
	BootstrapSHA256     string   `json:"bootstrap_sha256"`
	ConfigurationSHA256 string   `json:"configuration_sha256"`
	EnvironmentSHA256   string   `json:"environment_sha256"`
	ContractSHA256      string   `json:"contract_sha256"`
	Services            []string `json:"services"`
	Components          []string `json:"components"`
}

func namespaceRuntimeSortedIdentities(values []string, empty bool) bool {
	if !empty && len(values) == 0 {
		return false
	}
	for at, s := range values {
		if !namespaceRuntimeIdentity(s) || at > 0 && values[at-1] >= s {
			return false
		}
	}
	return true
}
func namespaceRuntimeIdentity(v string) bool {
	return v != "" && len(v) <= 512 && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "\x00\r\n")
}
func (r NamespaceRuntimeRequirement) ValidateShape() error {
	found := false
	for _, role := range NamespaceRuntimeRoles() {
		found = found || role == r.Role
	}
	if !found {
		return ErrOwnedEvidence
	}
	for _, v := range []string{r.BinarySHA256, r.SourceSHA256, r.BuildReceiptSHA256, r.BootstrapSHA256, r.ConfigurationSHA256, r.EnvironmentSHA256, r.ContractSHA256} {
		if !isSHA(v) {
			return ErrOwnedEvidence
		}
	}
	if r.ContractSHA256 != NamespaceRuntimeContractSHA256(r.Role) || !namespaceRuntimeSortedIdentities(r.Services, true) || !namespaceRuntimeSortedIdentities(r.Components, false) {
		return ErrOwnedEvidence
	}
	return nil
}
func NamespaceRuntimeContractSHA256(role string) string {
	raw, _ := json.Marshal([]string{"namespace-runtime-source-contract-v1", role, "same-original-owned-bootstrap-nonce-and-actual-process", "actual-DAO-store-provider-callback-construction-before-admission", "all-required-roles-not-an-enum-only-ready", "no-raw-legacy-consumer-or-allocator-fallback", "same-TX-permanent-origin-principals-and-counter", "original-branch-positive-scope-and-history-floors", "unknown-outcome-retained-with-original-owner-no-replay", "original-worker-resource-join-before-canonical-drain"})
	return sha(raw)
}

// No operation below mints a process/Store capability. These are canonical
// identities only; the actual operator must observe all original exec handles,
// source/build/image pins, nonce-bound registrations and genuine closed import.
func NamespaceRuntimeRequirementsSHA256(rows []NamespaceRuntimeRequirement) (string, error) {
	roles := NamespaceRuntimeRoles()
	if len(rows) != len(roles) {
		return "", ErrOwnedEvidence
	}
	for at, r := range rows {
		if r.Role != roles[at] || r.ValidateShape() != nil {
			return "", ErrOwnedEvidence
		}
	}
	raw, e := json.Marshal(rows)
	if e != nil {
		return "", e
	}
	return sha(raw), nil
}

type NamespaceRuntimeBootstrap struct {
	Version                  string                            `json:"version"`
	Header                   namespaceimport.Header            `json:"header"`
	HistoryPreparationID     string                            `json:"history_preparation_id"`
	HistoryPreparationSHA256 string                            `json:"history_preparation_sha256"`
	InventorySHA256          string                            `json:"inventory_sha256"`
	ManifestSHA256           string                            `json:"manifest_sha256"`
	MongoCatalog             []NamespaceRuntimeMongoCollection `json:"mongo_catalog"`
	MongoCatalogSHA256       string                            `json:"mongo_catalog_sha256"`
	RuntimePreparationID     string                            `json:"runtime_preparation_id"`
	RuntimePreparationSHA256 string                            `json:"runtime_preparation_sha256"`
	RuntimeToken             string                            `json:"runtime_token"`
	RequirementsSHA256       string                            `json:"requirements_sha256"`
	Requirement              NamespaceRuntimeRequirement       `json:"requirement"`
	ExpectedInstanceID       string                            `json:"expected_instance_id,omitempty"`
	RegistrationPath         string                            `json:"registration_path"`
	ReleasePath              string                            `json:"release_path"`
	ReleaseKeyID             string                            `json:"release_key_id"`
	ReleasePublicKey         ed25519.PublicKey                 `json:"release_public_key"`
}

func (b NamespaceRuntimeBootstrap) ValidateShape() error {
	if b.Version != "namespace-runtime-private-bootstrap-v1" || b.Header.Validate() != nil || b.Requirement.ValidateShape() != nil || !namespaceRuntimeIdentity(b.ReleaseKeyID) || len(b.ReleasePublicKey) != ed25519.PublicKeySize {
		return ErrOwnedEvidence
	}
	catalog, e := NamespaceRuntimeMongoCatalogSHA256(b.MongoCatalog)
	if e != nil || catalog != b.MongoCatalogSHA256 {
		return ErrOwnedEvidence
	}
	if b.Requirement.Role == "msgtransfer-managed" {
		if b.ExpectedInstanceID != "" {
			return ErrOwnedEvidence
		}
	} else if !uuidPattern.MatchString(b.ExpectedInstanceID) {
		return ErrOwnedEvidence
	}
	for _, s := range []string{b.HistoryPreparationID, b.HistoryPreparationSHA256, b.InventorySHA256, b.ManifestSHA256, b.RuntimePreparationID, b.RuntimePreparationSHA256, b.RuntimeToken, b.RequirementsSHA256} {
		if !isSHA(s) {
			return ErrOwnedEvidence
		}
	}
	if !filepath.IsAbs(b.RegistrationPath) || filepath.Clean(b.RegistrationPath) != b.RegistrationPath || !filepath.IsAbs(b.ReleasePath) || filepath.Clean(b.ReleasePath) != b.ReleasePath || filepath.Dir(b.RegistrationPath) != filepath.Dir(b.ReleasePath) || b.RegistrationPath == b.ReleasePath {
		return ErrOwnedEvidence
	}
	return nil
}

// A source-pinned original factory emits this only after its real handlers,
// storage instances, callbacks/providers and deferred worker entrypoints are
// constructed and sealed. It explicitly does NOT assert Serve, consumption,
// joined workers or successful external delivery. The operator independently
// observes the original process/nonce/image and publication before admission.
type NamespaceRuntimeRegistration struct {
	Version                  string   `json:"version"`
	Role                     string   `json:"role"`
	InstanceID               string   `json:"instance_id"`
	HeaderSHA256             string   `json:"header_sha256"`
	RuntimePreparationSHA256 string   `json:"runtime_preparation_sha256"`
	RuntimeTokenSHA256       string   `json:"runtime_token_sha256"`
	InventorySHA256          string   `json:"inventory_sha256"`
	ManifestSHA256           string   `json:"manifest_sha256"`
	RequirementsSHA256       string   `json:"requirements_sha256"`
	BootstrapSHA256          string   `json:"bootstrap_sha256"`
	ConfigurationSHA256      string   `json:"configuration_sha256"`
	EnvironmentSHA256        string   `json:"environment_sha256"`
	ContractSHA256           string   `json:"contract_sha256"`
	Services                 []string `json:"services"`
	Components               []string `json:"components"`
	// Canonical private-factory observations. Store/reader/provider identity is
	// checked locally by that factory before these non-authority hashes exist.
	BindingsSHA256 string `json:"bindings_sha256"`
	BindingsJSON   string `json:"bindings_json"`
}
type NamespaceRuntimeLocalBinding struct {
	Name           string `json:"name"`
	Kind           string `json:"kind"`
	InstanceSHA256 string `json:"instance_sha256"`
	HeaderSHA256   string `json:"header_sha256"`
	ContractSHA256 string `json:"contract_sha256"`
}
type NamespaceRuntimeBindings struct {
	Role              string                         `json:"role"`
	HeaderSHA256      string                         `json:"header_sha256"`
	Resources         []string                       `json:"resources"`
	Local             []NamespaceRuntimeLocalBinding `json:"local"`
	Source            ownedmongo.Snapshot            `json:"source"`
	MongoTargetSHA256 string                         `json:"mongo_target_sha256,omitempty"`
}

func (b NamespaceRuntimeBindings) ValidateShape(role, header string) error {
	if b.Role != role || b.HeaderSHA256 != header || !isSHA(header) || !namespaceRuntimeSortedIdentities(b.Resources, false) || b.Source.SourceOutcomeUnconfirmed || b.Source.Overflow || b.Source.InFlight != 0 || b.Source.External.Unconfirmed() {
		return ErrOwnedEvidence
	}
	if b.MongoTargetSHA256 != "" && (!NamespaceRuntimeUsesMongo(role) || !isSHA(b.MongoTargetSHA256)) {
		return ErrOwnedEvidence
	}
	for at, local := range b.Local {
		if !namespaceRuntimeIdentity(local.Name) || !isSHA(local.InstanceSHA256) || local.HeaderSHA256 != header || !isSHA(local.ContractSHA256) || at > 0 && b.Local[at-1].Name >= local.Name {
			return ErrOwnedEvidence
		}
		switch local.Kind {
		case "namespace-store", "history-reader", "frozen-online-runtime", "frozen-offline-runtime", "frozen-group-runtime", "retained-callback-provider":
		default:
			return ErrOwnedEvidence
		}
	}
	return nil
}
func (r NamespaceRuntimeRegistration) ValidateShape() error {
	if r.Version != "namespace-runtime-registered-not-active-v1" || !uuidPattern.MatchString(r.InstanceID) {
		return ErrOwnedEvidence
	}
	for _, s := range []string{r.HeaderSHA256, r.RuntimePreparationSHA256, r.RuntimeTokenSHA256, r.InventorySHA256, r.ManifestSHA256, r.RequirementsSHA256, r.BootstrapSHA256, r.ConfigurationSHA256, r.EnvironmentSHA256, r.ContractSHA256, r.BindingsSHA256} {
		if !isSHA(s) {
			return ErrOwnedEvidence
		}
	}
	if !namespaceRuntimeSortedIdentities(r.Services, true) || !namespaceRuntimeSortedIdentities(r.Components, false) {
		return ErrOwnedEvidence
	}
	var bindings NamespaceRuntimeBindings
	if sha([]byte(r.BindingsJSON)) != r.BindingsSHA256 || namespaceRuntimeDecodeCanonical(r.BindingsJSON, &bindings) != nil || bindings.ValidateShape(r.Role, r.HeaderSHA256) != nil {
		return ErrOwnedEvidence
	}
	if NamespaceRuntimeUsesMongo(r.Role) && !isSHA(bindings.MongoTargetSHA256) {
		return ErrOwnedEvidence
	}
	for _, role := range NamespaceRuntimeRoles() {
		if r.Role == role {
			return nil
		}
	}
	return ErrOwnedEvidence
}

type namespaceRuntimeRegistrationEnvelope struct {
	Schema       int                          `json:"schema"`
	OwnerNonce   string                       `json:"owner_nonce"`
	Registration NamespaceRuntimeRegistration `json:"registration"`
}
type ObservedNamespaceRuntime struct {
	Role               string                       `json:"role"`
	PID                int                          `json:"pid"`
	UID                uint32                       `json:"uid"`
	StartToken         string                       `json:"start_token"`
	BootID             string                       `json:"boot_id"`
	BinarySHA256       string                       `json:"binary_sha256"`
	SourceSHA256       string                       `json:"source_sha256"`
	BuildReceiptSHA256 string                       `json:"build_receipt_sha256"`
	OwnerNonceSHA256   string                       `json:"owner_nonce_sha256"`
	RegistrationSHA256 string                       `json:"registration_sha256"`
	Registration       NamespaceRuntimeRegistration `json:"registration"`
}

func WriteNamespaceRuntimeRegistration(path, nonce string, r NamespaceRuntimeRegistration) (string, error) {
	if !isSHA(nonce) || r.ValidateShape() != nil {
		return "", ErrOwnedEvidence
	}
	return writeServerEnvelope(path, namespaceRuntimeRegistrationEnvelope{1, nonce, r})
}
func ReadNamespaceRuntimeBootstrap(path, expectedSHA string) (NamespaceRuntimeBootstrap, error) {
	var b NamespaceRuntimeBootstrap
	sum, e := readServerEnvelope(path, &b)
	if e != nil || !isSHA(expectedSHA) || sum != expectedSHA || b.ValidateShape() != nil {
		return b, ErrOwnedEvidence
	}
	return b, nil
}
func WriteNamespaceRuntimeBootstrap(path string, b NamespaceRuntimeBootstrap) (string, error) {
	if b.ValidateShape() != nil {
		return "", ErrOwnedEvidence
	}
	return writeServerEnvelope(path, b)
}
