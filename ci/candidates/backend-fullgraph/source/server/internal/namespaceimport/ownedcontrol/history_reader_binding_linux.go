//go:build linux

package ownedcontrol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

var ErrHistoryReaderRegistrationPending = errors.New("owned history reader has not registered yet")

// This is a source-selected build/role contract, not an inventory of resource
// names and not an operator-supplied assertion that a reader is already bound.
// Only the two actual authoritative history RPC factories register it today.
// Other writers/consumers retain their separate runtime admission hard fence.
type HistoryReaderRequirement struct {
	Role                string   `json:"role"`
	BinarySHA256        string   `json:"binary_sha256"`
	SourceSHA256        string   `json:"source_sha256"`
	BuildReceiptSHA256  string   `json:"build_receipt_sha256"`
	BootstrapSHA256     string   `json:"bootstrap_sha256"`
	ConfigurationSHA256 string   `json:"configuration_sha256"`
	EnvironmentSHA256   string   `json:"environment_sha256"`
	Services            []string `json:"services"`
	ReadContractSHA256  string   `json:"read_contract_sha256"`
}

func HistoryReaderRequirementsSHA256(rows []HistoryReaderRequirement) (string, error) {
	if len(rows) != 2 {
		return "", ErrOwnedEvidence
	}
	for n, r := range rows {
		if n == 0 && r.Role != "rpc-conversation" || n == 1 && r.Role != "rpc-msg" || len(r.Services) == 0 || len(r.Services) > 16 {
			return "", ErrOwnedEvidence
		}
		for _, s := range []string{r.BinarySHA256, r.SourceSHA256, r.BuildReceiptSHA256, r.BootstrapSHA256, r.ConfigurationSHA256, r.EnvironmentSHA256, r.ReadContractSHA256} {
			if !isSHA(s) {
				return "", ErrOwnedEvidence
			}
		}
		for at, s := range r.Services {
			if s == "" || len(s) > 256 || at > 0 && r.Services[at-1] >= s {
				return "", ErrOwnedEvidence
			}
		}
	}
	body, e := json.Marshal(rows)
	if e != nil {
		return "", e
	}
	return sha(body), nil
}

// Only the private, explicitly selected per-role startup file contains Token.
// It authorizes construction/registration while NOT SERVING, never a public
// history read, SendMsg, Kafka intake or a membership/window grant.
type HistoryReaderBootstrap struct {
	Version            string                   `json:"version"`
	Header             namespaceimport.Header   `json:"header"`
	PreparationID      string                   `json:"preparation_id"`
	PreparationSHA256  string                   `json:"preparation_sha256"`
	PreparationToken   string                   `json:"preparation_token"`
	InventorySHA256    string                   `json:"inventory_sha256"`
	ManifestSHA256     string                   `json:"manifest_sha256"`
	RequirementsSHA256 string                   `json:"requirements_sha256"`
	Requirement        HistoryReaderRequirement `json:"requirement"`
	BindingPath        string                   `json:"binding_path"`
}

func (b HistoryReaderBootstrap) ValidateShape() error {
	r := b.Requirement
	if b.Version != "private-history-reader-prepare-v1" || b.Header.Validate() != nil || !isSHA(b.PreparationID) || !isSHA(b.PreparationSHA256) || !isSHA(b.PreparationToken) || !isSHA(b.InventorySHA256) || !isSHA(b.ManifestSHA256) || !isSHA(b.RequirementsSHA256) || (r.Role != "rpc-msg" && r.Role != "rpc-conversation") || !filepath.IsAbs(b.BindingPath) || filepath.Clean(b.BindingPath) != b.BindingPath {
		return ErrOwnedEvidence
	}
	for _, s := range []string{r.BinarySHA256, r.SourceSHA256, r.BuildReceiptSHA256, r.BootstrapSHA256, r.ConfigurationSHA256, r.EnvironmentSHA256, r.ReadContractSHA256} {
		if !isSHA(s) {
			return ErrOwnedEvidence
		}
	}
	if len(r.Services) == 0 || len(r.Services) > 16 {
		return ErrOwnedEvidence
	}
	for at, s := range r.Services {
		if s == "" || len(s) > 256 || at > 0 && r.Services[at-1] >= s {
			return ErrOwnedEvidence
		}
	}
	return nil
}
func WriteHistoryReaderBootstrap(path string, b HistoryReaderBootstrap) (string, error) {
	if b.ValidateShape() != nil || path == b.BindingPath {
		return "", ErrOwnedEvidence
	}
	return writeServerEnvelope(path, b)
}
func ReadHistoryReaderBootstrap(path, exactSHA string) (HistoryReaderBootstrap, error) {
	var b HistoryReaderBootstrap
	sum, e := readServerEnvelope(path, &b)
	if e != nil || !isSHA(exactSHA) || sum != exactSHA || b.ValidateShape() != nil {
		return b, ErrOwnedEvidence
	}
	return b, nil
}

type HistoryReadinessSession struct {
	Version              string                 `json:"version"`
	Header               namespaceimport.Header `json:"header"`
	InventorySHA256      string                 `json:"inventory_sha256"`
	ManifestSHA256       string                 `json:"manifest_sha256"`
	RequirementsSHA256   string                 `json:"requirements_sha256"`
	LeaseID              string                 `json:"lease_id"`
	AdmissionGuardSHA256 string                 `json:"admission_guard_sha256"`
	Token                string                 `json:"token"`
}

func (s HistoryReadinessSession) ValidateShape() error {
	if s.Version != "private-history-readiness-session-v1" || s.Header.Validate() != nil {
		return ErrOwnedEvidence
	}
	for _, v := range []string{s.InventorySHA256, s.ManifestSHA256, s.RequirementsSHA256, s.LeaseID, s.AdmissionGuardSHA256, s.Token} {
		if !isSHA(v) {
			return ErrOwnedEvidence
		}
	}
	return nil
}

// Preserve the original session/token on reopen. No failed/unknown DB commit
// can cause a new identity to be generated over the old readiness operation.
func OpenHistoryReadinessSession(path string, hold *AdmissionHold, h namespaceimport.Header, inventory, manifest, requirements string) (HistoryReadinessSession, string, error) {
	var saved HistoryReadinessSession
	if hold == nil || hold.ValidateExclusion() != nil || hold.HeaderSHA256() != h.SHA256() || !isSHA(inventory) || !isSHA(manifest) || !isSHA(requirements) {
		return saved, "", ErrOwnedEvidence
	}
	expected := HistoryReadinessSession{Version: "private-history-readiness-session-v1", Header: h, InventorySHA256: inventory, ManifestSHA256: manifest, RequirementsSHA256: requirements, LeaseID: hold.LeaseID(), AdmissionGuardSHA256: hold.RecordSHA256()}
	_, statErr := os.Lstat(path)
	var sum string
	var e error
	if errors.Is(statErr, os.ErrNotExist) {
		var nonce [32]byte
		if _, e = rand.Read(nonce[:]); e != nil {
			return saved, "", e
		}
		expected.Token = hex.EncodeToString(nonce[:])
		sum, e = writeServerEnvelope(path, expected)
		if e != nil {
			return saved, "", e
		}
		saved = expected
	} else {
		if statErr != nil {
			return saved, "", statErr
		}
		sum, e = readServerEnvelope(path, &saved)
		if e != nil {
			return saved, "", e
		}
	}
	if saved.ValidateShape() != nil {
		return saved, "", ErrOwnedEvidence
	}
	expected.Token = saved.Token
	left, _ := json.Marshal(saved)
	right, _ := json.Marshal(expected)
	if string(left) != string(right) {
		return saved, "", ErrOwnedEvidence
	}
	return saved, sum, nil
}

type historyReadinessIntent struct {
	Version       string `json:"version"`
	SessionSHA256 string `json:"session_sha256"`
	Operation     string `json:"operation"`
	RecordSHA256  string `json:"record_sha256"`
}

// created=false means inspect-only. A negative live read after this durable
// intent is not a not-applied certificate and never authorizes another write.
func BeginHistoryReadinessIntent(path, sessionSHA, operation, recordSHA string) (created bool, resultErr error) {
	if !isSHA(sessionSHA) || !isSHA(recordSHA) || operation != "prepare" && operation != "publish" {
		return false, ErrOwnedEvidence
	}
	expected := historyReadinessIntent{"history-readiness-intent-v1", sessionSHA, operation, recordSHA}
	var got historyReadinessIntent
	_, statErr := os.Lstat(path)
	if errors.Is(statErr, os.ErrNotExist) {
		_, e := writeServerEnvelope(path, expected)
		return e == nil, e
	}
	if statErr != nil {
		return false, statErr
	}
	_, e := readServerEnvelope(path, &got)
	if e != nil {
		return false, e
	}
	if got != expected {
		return false, ErrOwnedEvidence
	}
	return false, nil
}

// Emitted by the actual owned RPC start AFTER its real factory/DAO has bound
// the same reader and grpc.Server.GetServiceInfo has confirmed registration,
// but BEFORE discovery.Register/server.Serve/public admission. Its name does
// not claim that the server is serving or that readiness was published.
type HistoryReaderRegistration struct {
	Version                string   `json:"version"`
	Role                   string   `json:"role"`
	Mode                   string   `json:"mode"`
	InstanceID             string   `json:"instance_id"`
	HeaderSHA256           string   `json:"header_sha256"`
	DatabaseSHA256         string   `json:"database_sha256"`
	Epoch                  string   `json:"epoch"`
	ActivationSHA256       string   `json:"activation_sha256"`
	PreparationID          string   `json:"preparation_id"`
	PreparationSHA256      string   `json:"preparation_sha256"`
	PreparationTokenSHA256 string   `json:"preparation_token_sha256"`
	InventorySHA256        string   `json:"inventory_sha256"`
	ManifestSHA256         string   `json:"manifest_sha256"`
	RequirementsSHA256     string   `json:"requirements_sha256"`
	BootstrapSHA256        string   `json:"bootstrap_sha256"`
	ConfigurationSHA256    string   `json:"configuration_sha256"`
	EnvironmentSHA256      string   `json:"environment_sha256"`
	ReadContractSHA256     string   `json:"read_contract_sha256"`
	Services               []string `json:"services"`
}
type historyReaderEnvelope struct {
	Schema       int                       `json:"schema"`
	OwnerNonce   string                    `json:"owner_nonce"`
	Registration HistoryReaderRegistration `json:"registration"`
}

func (r HistoryReaderRegistration) ValidateShape() error {
	if r.Version != "history-reader-registered-not-serving-v1" || (r.Role != "rpc-msg" && r.Role != "rpc-conversation") || r.Mode != "required" || !uuidPattern.MatchString(r.InstanceID) || !uuidPattern.MatchString(r.Epoch) || len(r.Services) == 0 || len(r.Services) > 16 {
		return ErrOwnedEvidence
	}
	for _, s := range []string{r.HeaderSHA256, r.DatabaseSHA256, r.ActivationSHA256, r.PreparationID, r.PreparationSHA256, r.PreparationTokenSHA256, r.InventorySHA256, r.ManifestSHA256, r.RequirementsSHA256, r.BootstrapSHA256, r.ConfigurationSHA256, r.EnvironmentSHA256, r.ReadContractSHA256} {
		if !isSHA(s) {
			return ErrOwnedEvidence
		}
	}
	for at, s := range r.Services {
		if s == "" || len(s) > 256 || at > 0 && r.Services[at-1] >= s {
			return ErrOwnedEvidence
		}
	}
	return nil
}
func WriteHistoryReaderRegistration(path, nonce string, r HistoryReaderRegistration) (string, error) {
	if !isSHA(nonce) || r.ValidateShape() != nil {
		return "", ErrOwnedEvidence
	}
	return writeServerEnvelope(path, historyReaderEnvelope{1, nonce, r})
}

// The JSON is audit evidence, not a mintable authorization object. All actual
// exec.Cmd ownership, original nonce, process start/boot/exe image and private
// file custody checks happen before VerifiedHistoryReaderGraph can exist.
type ObservedHistoryReader struct {
	Role               string                    `json:"role"`
	PID                int                       `json:"pid"`
	UID                uint32                    `json:"uid"`
	StartToken         string                    `json:"start_token"`
	BootID             string                    `json:"boot_id"`
	BinarySHA256       string                    `json:"binary_sha256"`
	SourceSHA256       string                    `json:"source_sha256"`
	BuildReceiptSHA256 string                    `json:"build_receipt_sha256"`
	OwnerNonceSHA256   string                    `json:"owner_nonce_sha256"`
	RegistrationSHA256 string                    `json:"registration_sha256"`
	Registration       HistoryReaderRegistration `json:"registration"`
}
type HistoryReaderProcess struct {
	Service   *OwnedService
	Bootstrap HistoryReaderBootstrap
}
type VerifiedHistoryReaderGraph struct {
	mu                                                     sync.Mutex
	hold                                                   *AdmissionHold
	header, preparation, inventory, manifest, requirements string
	processes                                              []HistoryReaderProcess
	rows                                                   []ObservedHistoryReader
	observedAt                                             time.Time
}

func ObserveHistoryReaderGraph(ctx context.Context, hold *AdmissionHold, h namespaceimport.Header, preparation, inventory, manifest string, requirements []HistoryReaderRequirement, processes []HistoryReaderProcess) (*VerifiedHistoryReaderGraph, error) {
	graphSHA, e := HistoryReaderRequirementsSHA256(requirements)
	if e != nil || ctx == nil || h.Validate() != nil || hold == nil || hold.ValidateExclusion() != nil || hold.HeaderSHA256() != h.SHA256() || !isSHA(preparation) || !isSHA(inventory) || !isSHA(manifest) || len(processes) != len(requirements) {
		return nil, ErrOwnedEvidence
	}
	g := &VerifiedHistoryReaderGraph{hold: hold, header: h.SHA256(), preparation: preparation, inventory: inventory, manifest: manifest, requirements: graphSHA, processes: append([]HistoryReaderProcess(nil), processes...)}
	pending := false
	for n, p := range processes {
		b := p.Bootstrap
		r := requirements[n]
		if b.ValidateShape() != nil || b.Header != h || b.PreparationSHA256 != preparation || b.InventorySHA256 != inventory || b.ManifestSHA256 != manifest || b.RequirementsSHA256 != graphSHA {
			return nil, ErrOwnedEvidence
		}
		one, _ := json.Marshal(b.Requirement)
		two, _ := json.Marshal(r)
		if string(one) != string(two) {
			return nil, ErrOwnedEvidence
		}
		row, e := observeHistoryReader(ctx, p, r)
		if errors.Is(e, ErrHistoryReaderRegistrationPending) {
			pending = true
			continue
		}
		if e != nil {
			return nil, e
		}
		g.rows = append(g.rows, row)
	}
	if pending {
		return nil, ErrHistoryReaderRegistrationPending
	}
	g.observedAt = time.Now()
	return g, nil
}
func observeHistoryReader(ctx context.Context, p HistoryReaderProcess, expected HistoryReaderRequirement) (ObservedHistoryReader, error) {
	var out ObservedHistoryReader
	s := p.Service
	b := p.Bootstrap
	if s == nil || ctx == nil || ctx.Err() != nil {
		return out, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil || s.owner.Role != expected.Role || s.owner.BinarySHA256 != expected.BinarySHA256 || s.owner.SourceSHA256 != expected.SourceSHA256 || s.owner.BuildReceiptSHA256 != expected.BuildReceiptSHA256 || s.owner.BootstrapSHA256 != expected.BootstrapSHA256 || s.joined {
		return out, ErrOwnedEvidence
	}
	select {
	case <-s.done:
		return out, ErrOwnedEvidence
	default:
	}
	before, e := processIdentity(s.owner.PID)
	if e != nil || !sameProcess(before, s.owner) || actualImage(s.owner.PID, s.owner.Executable, s.owner.BinarySHA256) != nil {
		return out, ErrOwnedEvidence
	}
	if _, e = os.Lstat(b.BindingPath); errors.Is(e, os.ErrNotExist) {
		return out, ErrHistoryReaderRegistrationPending
	} else if e != nil {
		return out, e
	}
	var envelope historyReaderEnvelope
	sum, e := readServerEnvelope(b.BindingPath, &envelope)
	if e != nil || envelope.Schema != 1 || envelope.OwnerNonce != s.owner.OwnerNonce || envelope.Registration.ValidateShape() != nil {
		return out, ErrOwnedEvidence
	}
	r := envelope.Registration
	if r.Role != expected.Role || r.HeaderSHA256 != b.Header.SHA256() || r.DatabaseSHA256 != sha([]byte(b.Header.Database)) || r.Epoch != b.Header.Epoch || r.ActivationSHA256 != b.Header.ActivationSHA256 || r.PreparationID != b.PreparationID || r.PreparationSHA256 != b.PreparationSHA256 || r.PreparationTokenSHA256 != sha([]byte(b.PreparationToken)) || r.InventorySHA256 != b.InventorySHA256 || r.ManifestSHA256 != b.ManifestSHA256 || r.RequirementsSHA256 != b.RequirementsSHA256 || r.BootstrapSHA256 != expected.BootstrapSHA256 || r.ConfigurationSHA256 != expected.ConfigurationSHA256 || r.EnvironmentSHA256 != expected.EnvironmentSHA256 || r.ReadContractSHA256 != expected.ReadContractSHA256 {
		return out, ErrOwnedEvidence
	}
	left, _ := json.Marshal(r.Services)
	right, _ := json.Marshal(expected.Services)
	if string(left) != string(right) {
		return out, ErrOwnedEvidence
	}
	after, e := processIdentity(s.owner.PID)
	if e != nil || !sameProcess(before, after) {
		return out, ErrOwnedEvidence
	}
	return ObservedHistoryReader{Role: expected.Role, PID: s.owner.PID, UID: s.owner.UID, StartToken: s.owner.StartToken, BootID: s.owner.BootID, BinarySHA256: s.owner.BinarySHA256, SourceSHA256: s.owner.SourceSHA256, BuildReceiptSHA256: s.owner.BuildReceiptSHA256, OwnerNonceSHA256: sha([]byte(s.owner.OwnerNonce)), RegistrationSHA256: sum, Registration: r}, nil
}

// Refresh outside Mongo retry callbacks. It does not renew the source capture,
// target epoch, original cut or process identity; ANY changed row is rejected.
func (g *VerifiedHistoryReaderGraph) Refresh(ctx context.Context) error {
	if g == nil {
		return ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hold == nil || g.hold.ValidateExclusion() != nil || len(g.processes) != 2 || len(g.rows) != len(g.processes) {
		return ErrOwnedEvidence
	}
	for n, p := range g.processes {
		current, e := observeHistoryReader(ctx, p, p.Bootstrap.Requirement)
		if errors.Is(e, ErrHistoryReaderRegistrationPending) {
			return ErrOwnedEvidence
		}
		if e != nil {
			return errors.Join(ErrOwnedEvidence, e)
		}
		a, _ := json.Marshal(current)
		b, _ := json.Marshal(g.rows[n])
		if string(a) != string(b) {
			return ErrOwnedEvidence
		}
	}
	g.observedAt = time.Now()
	return nil
}

// Local capability check only: no OS/network work inside Mongo retries. A
// short-lived genuine observation is required again for a later transaction.
func (g *VerifiedHistoryReaderGraph) ValidateLocal(hold *AdmissionHold, h, preparation, inventory, manifest, requirements string) error {
	if g == nil {
		return ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hold != hold || hold == nil || hold.ValidateExclusion() != nil || g.header != h || g.preparation != preparation || g.inventory != inventory || g.manifest != manifest || g.requirements != requirements || len(g.rows) != 2 || time.Since(g.observedAt) < 0 || time.Since(g.observedAt) > 20*time.Second {
		return ErrOwnedEvidence
	}
	return nil
}
func (g *VerifiedHistoryReaderGraph) AuditRows() []ObservedHistoryReader {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	body, _ := json.Marshal(g.rows)
	var rows []ObservedHistoryReader
	_ = json.Unmarshal(body, &rows)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Role < rows[j].Role })
	return rows
}
