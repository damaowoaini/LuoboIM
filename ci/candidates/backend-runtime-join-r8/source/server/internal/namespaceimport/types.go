// Package namespaceimport coordinates a controlled, offline namespace import.
// It never grants history access, starts a writer, or changes service readiness.
// Storage implementations may import this package; this package must not import
// messagedelivery, its controllers, or deployment-specific clients.
package namespaceimport

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid           = errors.New("namespace import input invalid")
	ErrConflict          = errors.New("namespace import immutable binding differs")
	ErrUntrusted         = errors.New("namespace import controlled evidence unavailable")
	ErrCommitUnconfirmed = errors.New("namespace import commit unconfirmed: inspect only")
	ErrPending           = errors.New("namespace import previous operation unresolved")
	ErrFinished          = errors.New("namespace import already published")
)

// Header is selected in the private operator bootstrap, not by a network RPC.
// StorageInstance is the real Mongo collection/storage UUID inventory identity;
// process/topology/run IDs additionally make a source restart a new capture.
type Header struct {
	Tenant                 string `json:"tenant"`
	Database               string `json:"database"`
	StorageInstance        string `json:"storage_instance"`
	ReplicaSet             string `json:"replica_set"`
	MongoProcess           string `json:"mongo_process"`
	MongoTopology          int64  `json:"mongo_topology"`
	RedisRunID             string `json:"redis_run_id"`
	RedisDB                int    `json:"redis_db"`
	KafkaClusterID         string `json:"kafka_cluster_id"`
	Epoch                  string `json:"epoch"`
	ActivationSHA256       string `json:"activation_sha256"`
	MigrationID            string `json:"migration_id"`
	DeploymentPolicySHA256 string `json:"deployment_policy_sha256"`
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func identity(s string) bool {
	if s == "" || len(s) > 4096 || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}
func portNil(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return r.IsNil()
	}
	return false
}
func validSHA(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && hex.EncodeToString(b) == s
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func clone[T any](v T) T {
	b, err := json.Marshal(v)
	if err != nil {
		panic("namespace import internal non-JSON value")
	}
	var out T
	if json.Unmarshal(b, &out) != nil {
		panic("namespace import internal copy")
	}
	return out
}
func (h Header) Validate() error {
	if !identity(h.Tenant) || !identity(h.Database) || !identity(h.StorageInstance) || !identity(h.ReplicaSet) || !identity(h.MongoProcess) || h.MongoTopology < 0 || !identity(h.RedisRunID) || h.RedisDB < 0 || h.RedisDB > 15 || !identity(h.KafkaClusterID) || (!uuidPattern.MatchString(h.Epoch) || h.Epoch == "00000000-0000-0000-0000-000000000000") || (!uuidPattern.MatchString(h.MigrationID) || h.MigrationID == "00000000-0000-0000-0000-000000000000") || !validSHA(h.ActivationSHA256) || !validSHA(h.DeploymentPolicySHA256) {
		return ErrInvalid
	}
	return nil
}
func (h Header) SHA256() string { return digest(h) }

// OriginIDs preserve the original messageNamespace tuple. A group contains only
// its group ID, a single conversation its sorted original IDs, and a direct
// server notification its original ordered SendID/RecvID. No CID splitting.
type NamespaceOriginDescriptor struct {
	CID       string   `json:"cid"`
	Kind      string   `json:"kind"`
	OriginIDs []string `json:"origin_ids"`
	GroupID   string   `json:"group_id,omitempty"`
	SHA256    string   `json:"sha256"`
}
type SourceRef struct {
	ObjectSHA256 string `json:"object_sha256"`
	RecordSHA256 string `json:"record_sha256"`
	Kind         string `json:"kind"`
	Position     uint64 `json:"position"`
}

// A missing head is never zero. A proven-empty record must be backed by the
// held lifecycle/source proof checked by OriginValidator, not by absence alone.
type Head struct {
	State         string                `json:"state"` // present | proven-empty | legacy-counter-not-allocated-here
	Value         int64                 `json:"value"`
	Evidence      []SourceRef           `json:"evidence"`
	LegacyAbsence *LegacyCounterAbsence `json:"legacy_absence,omitempty"`
}
type OriginRecord struct {
	Origin             NamespaceOriginDescriptor `json:"origin"`
	MongoHead          Head                      `json:"mongo_head"`
	RedisHead          Head                      `json:"redis_head"`
	Evidence           []SourceRef               `json:"evidence"`
	SeedEvidenceSHA256 string                    `json:"seed_evidence_sha256"`
}

// Every CID, including n_, has its own numerical domain and historical owner
// boundary. A current roster cannot certify a removed owner's historical bound.
// These rows may span pages: there is no per-group roster total limit here.
type OwnerWindow struct {
	CID          string      `json:"cid"`
	OriginSHA256 string      `json:"origin_sha256"`
	GroupID      string      `json:"group_id,omitempty"`
	OwnerID      string      `json:"owner_id"`
	Lower        int64       `json:"lower"`
	Upper        int64       `json:"upper"`
	Closed       bool        `json:"closed"`
	Evidence     []SourceRef `json:"evidence"`
}
type Quarantine struct {
	CID      string      `json:"cid"`
	Reason   string      `json:"reason"`
	Evidence []SourceRef `json:"evidence"`
}
type Counts struct {
	Origins     uint64 `json:"origins"`
	Windows     uint64 `json:"windows"`
	Quarantines uint64 `json:"quarantines"`
}

func (a Counts) add(b Counts) (Counts, error) {
	if math.MaxUint64-a.Origins < b.Origins || math.MaxUint64-a.Windows < b.Windows || math.MaxUint64-a.Quarantines < b.Quarantines {
		return Counts{}, ErrInvalid
	}
	return Counts{a.Origins + b.Origins, a.Windows + b.Windows, a.Quarantines + b.Quarantines}, nil
}

type PageIdentity struct {
	Index          uint64 `json:"index"`
	PreviousSHA256 string `json:"previous_sha256"`
	SHA256         string `json:"sha256"`
}

// CoverageClosure advances a persisted Store accumulator, not an expected set.
// PreviousCursor is opaque and must equal the actual saved Store cursor.
type CoverageClosure struct {
	Kind           string `json:"kind"`
	CID            string `json:"cid"`
	PreviousCursor string `json:"previous_cursor"`
}
type PageData struct {
	HeaderSHA256           string                       `json:"header_sha256"`
	InventoryBindingSHA256 string                       `json:"inventory_binding_sha256"`
	Index                  uint64                       `json:"index"`
	PreviousSHA256         string                       `json:"previous_sha256"`
	RawSourceRootSHA256    string                       `json:"raw_source_root_sha256"`
	CoverageClosures       []CoverageClosure            `json:"coverage_closures"`
	Origins                []OriginRecord               `json:"origins"`
	OriginCoverages        []OriginCoverageWitness      `json:"origin_coverages"`
	EffectiveBoundaries    []EffectiveBoundaryWitness   `json:"effective_boundaries"`
	OwnerWindows           []OwnerWindow                `json:"owner_windows"`
	Quarantines            []Quarantine                 `json:"quarantines"`
	ProspectiveAdmissions  []ProspectiveOwnerWitness    `json:"prospective_admissions,omitempty"`
	OwnerHistorySegments   []OwnerHistorySegmentWitness `json:"owner_history_segments,omitempty"`
}

func (p PageData) Identity() PageIdentity { return PageIdentity{p.Index, p.PreviousSHA256, digest(p)} }
func (p PageData) Counts() Counts {
	return Counts{uint64(len(p.Origins)), uint64(len(p.OwnerWindows)), uint64(len(p.Quarantines))}
}

// Inventory is a signed, bounded manifest of a capture produced AFTER old
// writers/drains were held. It preserves the scanner's UNTRUSTED label: the
// signature authenticates custody and roots, not historical ownership or a grant.
type Inventory struct {
	HeaderSHA256                     string    `json:"header_sha256"`
	LeaseID                          string    `json:"lease_id"`
	CaptureID                        string    `json:"capture_id"`
	CaptureAgent                     string    `json:"capture_agent"`
	CapturedAt                       time.Time `json:"captured_at"`
	OriginalTrust                    string    `json:"original_trust"`
	MongoRootSHA256                  string    `json:"mongo_root_sha256"`
	RedisRootSHA256                  string    `json:"redis_root_sha256"`
	RawRootSHA256                    string    `json:"raw_root_sha256"`
	PagesRootSHA256                  string    `json:"pages_root_sha256"`
	CoverageSetRootSHA256            string    `json:"coverage_set_root_sha256"`
	CoverageCount                    uint64    `json:"coverage_count"`
	ProspectiveSetRootSHA256         string    `json:"prospective_set_root_sha256,omitempty"`
	ProspectiveCount                 uint64    `json:"prospective_count,omitempty"`
	OwnerHistorySegmentSetRootSHA256 string    `json:"owner_history_segment_set_root_sha256,omitempty"`
	OwnerHistorySegmentCount         uint64    `json:"owner_history_segment_count,omitempty"`
	HeldAdmissionGuardSHA256         string    `json:"held_admission_guard_sha256,omitempty"`
	PageCount                        uint64    `json:"page_count"`
	Counts                           Counts    `json:"counts"`
	KeyID                            string    `json:"key_id"`
	Signature                        []byte    `json:"signature"`
}

func (i Inventory) signingValue() Inventory { i.Signature = nil; return i }
func (i Inventory) SHA256() string          { return digest(i.signingValue()) }

// Binding excludes only the page root, avoiding a circular page/root hash. The
// capture signature and full inventory hash still bind that root exactly.
func (i Inventory) BindingSHA256() string {
	v := i.signingValue()
	v.PagesRootSHA256 = ""
	v.CoverageSetRootSHA256 = ""
	v.ProspectiveSetRootSHA256 = ""
	v.OwnerHistorySegmentSetRootSHA256 = ""
	return digest(v)
}

type MerkleSibling struct {
	SHA256 string `json:"sha256"`
	Left   bool   `json:"left"`
}
type CapturedPage struct {
	Data  PageData
	Proof []MerkleSibling
}

type InventorySource interface {
	// Capture uses real private source connections under this held fence; it is
	// not an operation that imports an arbitrary operator-supplied JSON manifest.
	Capture(context.Context, Header, FenceObservation) (Inventory, error)
	ReadPage(context.Context, Inventory, uint64) (CapturedPage, error)
	// Revalidate checks current UUID/RS/process/topology/Redis run ID plus frozen
	// raw roots and scan drift, without recapturing a new cut or renewing payload.
	Revalidate(context.Context, Header, Inventory) error
}
type OriginValidator interface {
	// Implementations use the authoritative shared namespace algorithm and raw
	// source/lifecycle proof. A hash, visible maximum, or parsed CID is insufficient.
	ValidateOrigin(context.Context, Header, Inventory, OriginRecord) error
	ValidateWindow(context.Context, Header, Inventory, OwnerWindow) error
	ValidateQuarantine(context.Context, Header, Inventory, Quarantine) error
}

type PageReceipt struct {
	HeaderSHA256                   string       `json:"header_sha256"`
	InventorySHA256                string       `json:"inventory_sha256"`
	Page                           PageIdentity `json:"page"`
	Cumulative                     Counts       `json:"cumulative"`
	ReceiptSHA256                  string       `json:"receipt_sha256"`
	CumulativeProspectives         uint64       `json:"cumulative_prospectives,omitempty"`
	CumulativeOwnerHistorySegments uint64       `json:"cumulative_owner_history_segments,omitempty"`
}
type PageInspection struct {
	Found   bool
	Receipt PageReceipt
}
type ManifestData struct {
	HeaderSHA256                     string `json:"header_sha256"`
	InventorySHA256                  string `json:"inventory_sha256"`
	FinalPageSHA256                  string `json:"final_page_sha256"`
	PageCount                        uint64 `json:"page_count"`
	Counts                           Counts `json:"counts"`
	CoverageSetRootSHA256            string `json:"coverage_set_root_sha256"`
	CoverageCount                    uint64 `json:"coverage_count"`
	ProspectiveSetRootSHA256         string `json:"prospective_set_root_sha256,omitempty"`
	ProspectiveCount                 uint64 `json:"prospective_count,omitempty"`
	OwnerHistorySegmentSetRootSHA256 string `json:"owner_history_segment_set_root_sha256,omitempty"`
	OwnerHistorySegmentCount         uint64 `json:"owner_history_segment_count,omitempty"`
}
type CutoverReceipt struct {
	HeaderSHA256   string `json:"header_sha256"`
	ManifestSHA256 string `json:"manifest_sha256"`
	ReceiptSHA256  string `json:"receipt_sha256"`
}
type CutoverInspection struct {
	Found   bool
	Receipt CutoverReceipt
}
type Writer interface {
	CheckTarget(context.Context, Header) error
	ApplyPage(context.Context, *Permit, VerifiedPage) (PageReceipt, error)
	InspectPage(context.Context, Header, PageIdentity) (PageInspection, error)
	PublishCutover(context.Context, *Permit, VerifiedManifest) (CutoverReceipt, error)
	InspectCutover(context.Context, Header) (CutoverInspection, error)
}

type Config struct {
	Header           Header
	Writers          []WriterIdentity
	Credentials      []CredentialIdentity
	KafkaPartitions  []KafkaPartitionIdentity
	ProbeKeyID       string
	ProbePublicKey   ed25519.PublicKey
	CaptureKeyID     string
	CapturePublicKey ed25519.PublicKey
	CaptureAgent     string
	OperationTimeout time.Duration
	PermitLifetime   time.Duration
	PageBytes        int
	PageRows         int
}
