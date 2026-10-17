package namespaceimport

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

type WriterIdentity struct {
	Deployment   string `json:"deployment"`
	Incarnation  string `json:"incarnation"`
	BinarySHA256 string `json:"binary_sha256"`
	SourceSHA256 string `json:"source_sha256"`
}
type CredentialIdentity struct {
	Scope        string `json:"scope"`
	CredentialID string `json:"credential_id"`
	Version      string `json:"version"`
}
type KafkaPartitionIdentity struct {
	Plane     string `json:"plane"`
	Topic     string `json:"topic"`
	Group     string `json:"group"`
	Partition int32  `json:"partition"`
}
type JoinedWriter struct {
	Identity      WriterIdentity `json:"identity"`
	StopRecord    string         `json:"stop_record"`
	JoinRecord    string         `json:"join_record"`
	ProcessExitAt time.Time      `json:"process_exit_at"`
}
type RevokedCredential struct {
	Identity             CredentialIdentity `json:"identity"`
	RevocationRecord     string             `json:"revocation_record"`
	RevocationGeneration uint64             `json:"revocation_generation"`
	State                string             `json:"state"`
}
type DrainedPartition struct {
	Identity                KafkaPartitionIdentity `json:"identity"`
	LeaderEpoch             int32                  `json:"leader_epoch"`
	PreviousEnd             int64                  `json:"previous_end"`
	CurrentEnd              int64                  `json:"current_end"`
	CommittedNext           int64                  `json:"committed_next"`
	PreviousSampleAt        time.Time              `json:"previous_sample_at"`
	CurrentSampleAt         time.Time              `json:"current_sample_at"`
	BrokerObservationRecord string                 `json:"broker_observation_record"`
}

// This is an authenticated deployment-agent observation, not a boolean in an
// operator JSON file. Its authority comes from the pinned control-plane key and
// actual provider implementation. It covers ALL declared legacy writers and
// both Kafka queue planes. Privileged control-plane administrators remain trusted.
type FenceObservation struct {
	HeaderSHA256         string              `json:"header_sha256"`
	Nonce                string              `json:"nonce"`
	LeaseID              string              `json:"lease_id"`
	Generation           uint64              `json:"generation"`
	KeyID                string              `json:"key_id"`
	IssuedAt             time.Time           `json:"issued_at"`
	ValidUntil           time.Time           `json:"valid_until"`
	ExclusionMode        string              `json:"exclusion_mode"`
	AdmissionGuardRecord string              `json:"admission_guard_record"`
	AdmissionGuardSHA256 string              `json:"admission_guard_sha256"`
	Joined               []JoinedWriter      `json:"joined"`
	Revoked              []RevokedCredential `json:"revoked"`
	Drained              []DrainedPartition  `json:"drained"`
	Signature            []byte              `json:"signature"`
}

func (f FenceObservation) signingValue() FenceObservation { f.Signature = nil; return f }
func (f FenceObservation) SHA256() string                 { return digest(f.signingValue()) }

type ProbeRequest struct {
	Header            Header `json:"header"`
	Nonce             string `json:"nonce"`
	LeaseID           string `json:"lease_id,omitempty"`
	MinimumGeneration uint64 `json:"minimum_generation"`
}
type ControlPlane interface {
	// Acquire stops/joins the controlled owned old writers, drains BOTH queue
	// planes, revokes old binary credentials and holds the deployment admission
	// guard. Observe rechecks that same held fence; neither runs in Mongo retries.
	Acquire(context.Context, ProbeRequest) (FenceObservation, error)
	Observe(context.Context, ProbeRequest) (FenceObservation, error)
}

func (c Config) policySHA256() string {
	return digest(struct {
		Writers      []WriterIdentity
		Credentials  []CredentialIdentity
		Kafka        []KafkaPartitionIdentity
		ProbeKeyID   string
		ProbeKey     []byte
		CaptureKeyID string
		CaptureKey   []byte
		CaptureAgent string
	}{c.Writers, c.Credentials, c.KafkaPartitions, c.ProbeKeyID, c.ProbePublicKey, c.CaptureKeyID, c.CapturePublicKey, c.CaptureAgent})
}

// DeploymentPolicySHA256 is an integrity binding. It does not by itself attest
// to stopped processes, revoked credentials, source completeness or permissions.
func DeploymentPolicySHA256(c Config) string { return c.policySHA256() }
func nonce() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func verifySignature(key ed25519.PublicKey, v any, signature []byte) bool {
	b, err := json.Marshal(v)
	return err == nil && len(key) == ed25519.PublicKeySize && ed25519.Verify(key, b, signature)
}
func (c Config) validate() error {
	if c.Header.Validate() != nil || len(c.ProbePublicKey) != ed25519.PublicKeySize || len(c.CapturePublicKey) != ed25519.PublicKeySize || !identity(c.ProbeKeyID) || !identity(c.CaptureKeyID) || !identity(c.CaptureAgent) || c.Header.DeploymentPolicySHA256 != c.policySHA256() || c.OperationTimeout <= 0 || c.OperationTimeout > time.Minute || c.PermitLifetime <= 0 || c.PermitLifetime > 30*time.Second || c.PageBytes < 1024 || c.PageBytes > 8*1024*1024 || c.PageRows < 1 || c.PageRows > 1024 || len(c.Writers) == 0 || len(c.Credentials) == 0 || len(c.KafkaPartitions) == 0 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	planes := map[string]bool{}
	for _, w := range c.Writers {
		k := digest(w)
		if seen[k] || !identity(w.Deployment) || !identity(w.Incarnation) || !validSHA(w.BinarySHA256) || !validSHA(w.SourceSHA256) {
			return ErrInvalid
		}
		seen[k] = true
	}
	for _, r := range c.Credentials {
		k := "credential:" + digest(r)
		if seen[k] || !identity(r.Scope) || !identity(r.CredentialID) || !identity(r.Version) {
			return ErrInvalid
		}
		seen[k] = true
	}
	for _, p := range c.KafkaPartitions {
		k := "partition:" + digest(p)
		if seen[k] || !identity(p.Topic) || !identity(p.Group) || p.Partition < 0 || p.Plane != "producer-to-allocation" && p.Plane != "allocation-to-persistence" {
			return ErrInvalid
		}
		seen[k] = true
		planes[p.Plane] = true
	}
	if len(planes) != 2 {
		return ErrInvalid
	}
	return nil
}
func (c Config) verifyFence(f FenceObservation, request ProbeRequest, now time.Time) error {
	if f.HeaderSHA256 != c.Header.SHA256() || f.Nonce != request.Nonce || !identity(f.LeaseID) || request.LeaseID != "" && request.LeaseID != f.LeaseID || f.Generation == 0 || f.Generation < request.MinimumGeneration || f.KeyID != c.ProbeKeyID || !verifySignature(c.ProbePublicKey, f.signingValue(), f.Signature) || f.IssuedAt.After(now) || now.Sub(f.IssuedAt) > time.Minute || !now.Before(f.ValidUntil) || f.ValidUntil.Sub(f.IssuedAt) > 2*time.Minute || f.ExclusionMode != "held-until-explicit-release" || !identity(f.AdmissionGuardRecord) || !validSHA(f.AdmissionGuardSHA256) {
		return ErrUntrusted
	}
	if len(f.Joined) != len(c.Writers) || len(f.Revoked) != len(c.Credentials) || len(f.Drained) != len(c.KafkaPartitions) {
		return ErrUntrusted
	}
	for i, w := range c.Writers {
		got := f.Joined[i]
		if got.Identity != w || !identity(got.StopRecord) || !identity(got.JoinRecord) || got.ProcessExitAt.IsZero() || got.ProcessExitAt.After(f.IssuedAt) {
			return ErrUntrusted
		}
	}
	for i, r := range c.Credentials {
		got := f.Revoked[i]
		if got.Identity != r || got.State != "revoked" || got.RevocationGeneration == 0 || !identity(got.RevocationRecord) {
			return ErrUntrusted
		}
	}
	for i, p := range c.KafkaPartitions {
		got := f.Drained[i]
		if got.Identity != p || got.LeaderEpoch < 0 || got.PreviousEnd < 0 || got.CurrentEnd != got.PreviousEnd || got.CommittedNext != got.CurrentEnd || got.PreviousSampleAt.IsZero() || !got.PreviousSampleAt.Before(got.CurrentSampleAt) || got.CurrentSampleAt.After(f.IssuedAt) || f.IssuedAt.Sub(got.CurrentSampleAt) > time.Minute || !identity(got.BrokerObservationRecord) {
			return ErrUntrusted
		}
	}
	return nil
}
func (c Config) verifyInventory(i Inventory, f FenceObservation) error {
	if i.HeaderSHA256 != c.Header.SHA256() || i.LeaseID != f.LeaseID || !identity(i.CaptureID) || i.CaptureAgent != c.CaptureAgent || i.CapturedAt.IsZero() || i.CapturedAt.After(time.Now()) || i.OriginalTrust != "UNTRUSTED" || !validSHA(i.MongoRootSHA256) || !validSHA(i.RedisRootSHA256) || !validSHA(i.RawRootSHA256) || !validSHA(i.PagesRootSHA256) || !validSHA(i.CoverageSetRootSHA256) || i.CoverageCount != i.Counts.Origins || i.PageCount == 0 || i.KeyID != c.CaptureKeyID || !verifySignature(c.CapturePublicKey, i.signingValue(), i.Signature) {
		return ErrUntrusted
	}
	for _, w := range f.Joined {
		if i.CapturedAt.Before(w.ProcessExitAt) {
			return ErrUntrusted
		}
	}
	for _, p := range f.Drained {
		if i.CapturedAt.Before(p.CurrentSampleAt) {
			return ErrUntrusted
		}
	}
	return nil
}
func refsValid(refs []SourceRef) bool {
	if len(refs) == 0 {
		return false
	}
	for _, r := range refs {
		if !validSHA(r.ObjectSHA256) || !validSHA(r.RecordSHA256) || !identity(r.Kind) {
			return false
		}
	}
	return true
}
func headValid(h Head) bool {
	return h.Value >= 0 && h.Value < 9223372036854775807 && (h.State == "present" || h.State == "proven-empty" && h.Value == 0) && refsValid(h.Evidence)
}
func originValid(o NamespaceOriginDescriptor) bool {
	if !identity(o.CID) || !validSHA(o.SHA256) {
		return false
	}
	for _, id := range o.OriginIDs {
		if !identity(id) {
			return false
		}
	}
	switch o.Kind {
	case "group":
		if len(o.OriginIDs) != 1 || o.GroupID != o.OriginIDs[0] {
			return false
		}
	case "single":
		if len(o.OriginIDs) != 2 || o.GroupID != "" || o.OriginIDs[0] > o.OriginIDs[1] {
			return false
		}
	case "server-notification":
		if len(o.OriginIDs) != 2 || o.GroupID != "" {
			return false
		}
	default:
		return false
	}
	return digest([]any{o.Kind, o.OriginIDs}) == o.SHA256
}
func verifyMembership(leaf string, proof []MerkleSibling, root string) error {
	if !validSHA(leaf) || !validSHA(root) || len(proof) > 64 {
		return ErrInvalid
	}
	value := digest([]string{"namespace-import-page-leaf-v1", leaf})
	for _, s := range proof {
		if !validSHA(s.SHA256) {
			return ErrInvalid
		}
		if s.Left {
			value = digest([]string{"namespace-import-page-node-v1", s.SHA256, value})
		} else {
			value = digest([]string{"namespace-import-page-node-v1", value, s.SHA256})
		}
	}
	if value != root {
		return fmt.Errorf("%w: source page membership", ErrConflict)
	}
	return nil
}
