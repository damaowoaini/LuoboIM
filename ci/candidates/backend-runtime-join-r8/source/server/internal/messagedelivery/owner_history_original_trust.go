package messagedelivery

import (
	"crypto/ed25519"
	"encoding/json"
	"path/filepath"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// This is a PRIVATE operator source contract, selected by an exact file SHA.
// No value is learned from current DB rows, current roster or supplied RPCs.
// The reviewed producer source SHA identifies the actual old row/receipt
// semantics; the original public key verifies custody, not an ownership grant.
type OriginalHistoryEpochTrust struct {
	Epoch                string `json:"epoch"`
	ActivationSHA256     string `json:"activation_sha256"`
	Database             string `json:"database"`
	ProducerSourceSHA256 string `json:"producer_source_sha256"`
	CutoverSHA256        string `json:"cutover_sha256"`
	// Explicit reviewed old Header selects an archive; never derive it from a
	// current cutover, KeyID, database name or epoch string. Import already has
	// this full Header; supplying both requires exact equality.
	OriginalHeader *namespaceimport.Header     `json:"original_header,omitempty"`
	Import         *OriginalHistoryImportTrust `json:"import,omitempty"`
}
type OriginalHistoryImportTrust struct {
	Header         namespaceimport.Header    `json:"header"`
	Inventory      namespaceimport.Inventory `json:"inventory"`
	ManifestSHA256 string                    `json:"manifest_sha256"`
	PublicKey      []byte                    `json:"public_key"`
	// Only older boundary rows omitted their original Merkle witness. Explicit
	// original signed witness custody can supply it; no matching bare SHA can.
	// Files are named EffectiveOwnerKey(cid,owner)+".json", private regular 0600.
	BoundaryWitnessDirectory string `json:"boundary_witness_directory,omitempty"`
}
type originalHistoryTrustConfig struct {
	Kind               string                      `json:"kind"`
	TargetHeaderSHA256 string                      `json:"target_header_sha256"`
	Epochs             []OriginalHistoryEpochTrust `json:"epochs"`
}
type OriginalHistoryVerifier struct {
	path, sum, target string
	epochs            map[string]OriginalHistoryEpochTrust
	preparedMu        sync.Mutex
	prepared          []originalPreparedHistory
}

func LoadOriginalHistoryVerifier(path, sum string, target namespaceimport.Header) (*OriginalHistoryVerifier, error) {
	if target.Validate() != nil || !validSHA(sum) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrInvalid
	}
	body, e := namespaceinventory.ReadPrivateDocument(path, 1<<20)
	if e != nil {
		return nil, e
	}
	if hash(body) != sum {
		return nil, ErrConflict
	}
	var config originalHistoryTrustConfig
	if namespaceinventory.DecodeStrict(body, &config) != nil || config.Kind != "reviewed-original-history-source-trust-v1" || config.TargetHeaderSHA256 != target.SHA256() {
		return nil, ErrInvalid
	}
	out := &OriginalHistoryVerifier{path: path, sum: sum, target: target.SHA256(), epochs: map[string]OriginalHistoryEpochTrust{}}
	for _, t := range config.Epochs {
		if !validUUID(t.Epoch) || t.Epoch == target.Epoch || !validSHA(t.ActivationSHA256) || !identity(t.Database) || t.Database != target.Database || !validSHA(t.ProducerSourceSHA256) || !validSHA(t.CutoverSHA256) || out.epochs[t.Epoch].Epoch != "" {
			return nil, ErrInvalid
		}
		if _, _, e = selectedOriginalEpochHeader(t); e != nil {
			return nil, e
		}
		if t.Import != nil {
			if e = validateOriginalImportTrust(t); e != nil {
				return nil, e
			}
		}
		out.epochs[t.Epoch] = t
	}
	return out, nil
}

// No bare caller Header grants anything. The loader binds these exact bytes
// in the selected private trust document; original signatures/publication and
// finite admission/receipt proof are still independently checked by the chain.
func selectedOriginalEpochHeader(t OriginalHistoryEpochTrust) (namespaceimport.Header, bool, error) {
	var h namespaceimport.Header
	selected := false
	if t.OriginalHeader != nil {
		h = *t.OriginalHeader
		selected = true
	}
	if t.Import != nil {
		if selected && h.SHA256() != t.Import.Header.SHA256() {
			return h, false, ErrConflict
		}
		h = t.Import.Header
		selected = true
	}
	if !selected {
		return h, false, nil
	}
	if h.Validate() != nil || h.Epoch != t.Epoch || h.ActivationSHA256 != t.ActivationSHA256 || h.Database != t.Database {
		return h, false, ErrConflict
	}
	return h, true, nil
}
func validateOriginalImportTrust(t OriginalHistoryEpochTrust) error {
	p := t.Import
	if p == nil {
		return ErrUnprepared
	}
	h, i := p.Header, p.Inventory
	if h.Validate() != nil || h.Epoch != t.Epoch || h.ActivationSHA256 != t.ActivationSHA256 || h.Database != t.Database || i.HeaderSHA256 != h.SHA256() || !validSHA(p.ManifestSHA256) || len(p.PublicKey) != ed25519.PublicKeySize || !identity(i.KeyID) || !identity(i.CaptureAgent) || !identity(i.CaptureID) || !identity(i.LeaseID) || i.OriginalTrust != "UNTRUSTED" || i.CapturedAt.IsZero() || i.PageCount == 0 || i.CoverageCount != i.Counts.Origins {
		return ErrConflict
	}
	for _, s := range []string{i.MongoRootSHA256, i.RedisRootSHA256, i.RawRootSHA256, i.PagesRootSHA256, i.CoverageSetRootSHA256} {
		if !validSHA(s) {
			return ErrConflict
		}
	}
	unsigned := i
	unsigned.Signature = nil
	body, e := json.Marshal(unsigned)
	if e != nil || !ed25519.Verify(ed25519.PublicKey(p.PublicKey), body, i.Signature) {
		return ErrConflict
	}
	if p.BoundaryWitnessDirectory != "" && (!filepath.IsAbs(p.BoundaryWitnessDirectory) || filepath.Clean(p.BoundaryWitnessDirectory) != p.BoundaryWitnessDirectory) {
		return ErrInvalid
	}
	return nil
}
func (v *OriginalHistoryVerifier) checkConfig(target namespaceimport.Header) error {
	if v == nil || target.Validate() != nil || target.SHA256() != v.target {
		return ErrInvalid
	}
	body, e := namespaceinventory.ReadPrivateDocument(v.path, 1<<20)
	if e != nil {
		return e
	}
	if hash(body) != v.sum {
		return ErrConflict
	}
	return nil
}
func (v *OriginalHistoryVerifier) checkTrust(target namespaceimport.Header, epoch string) (OriginalHistoryEpochTrust, error) {
	if e := v.checkConfig(target); e != nil {
		return OriginalHistoryEpochTrust{}, e
	}
	t, ok := v.epochs[epoch]
	if !ok || epoch == target.Epoch {
		return t, ErrUnprepared
	}
	return t, nil
}

func historySetMembership(leaf string, proof []namespaceimport.MerkleSibling, root string) error {
	if !validSHA(leaf) || !validSHA(root) || len(proof) > 64 {
		return ErrInvalid
	}
	value := directHistoryDigest([]string{"namespace-import-set-leaf-v1", leaf})
	for _, s := range proof {
		if !validSHA(s.SHA256) {
			return ErrInvalid
		}
		if s.Left {
			value = directHistoryDigest([]string{"namespace-import-set-node-v1", s.SHA256, value})
		} else {
			value = directHistoryDigest([]string{"namespace-import-set-node-v1", value, s.SHA256})
		}
	}
	if value != root {
		return ErrConflict
	}
	return nil
}
func verifyOriginalBoundaryWitness(h namespaceimport.Header, i namespaceimport.Inventory, w namespaceimport.EffectiveBoundaryWitness, d NamespaceOriginDescriptor, owner string) error {
	c, b := w.Coverage.Coverage, w.Boundary
	original := namespaceimport.NamespaceOriginDescriptor{CID: d.CID, Kind: d.Kind, GroupID: d.GroupID, OriginIDs: append([]string(nil), d.OriginIDs...), SHA256: d.SHA256}
	if c.ValidateBinding(h, i, original) != nil || c.EffectiveOwners == 0 || b.Window.CID != d.CID || b.Window.OwnerID != owner || b.Window.OriginSHA256 != d.SHA256 || b.Window.GroupID != d.GroupID || b.HistoryPolicySHA256 != c.HistoryPolicySHA256 {
		return ErrConflict
	}
	sum, e := namespaceimport.EffectiveBoundarySHA256(b)
	if e != nil {
		return e
	}
	if e = historySetMembership(namespaceimport.CoverageSHA256(c), w.Coverage.Proof, i.CoverageSetRootSHA256); e != nil {
		return e
	}
	if e = historySetMembership(namespaceimport.EffectiveOwnerKey(d.CID, owner), w.OwnerProof, c.OwnerSetRootSHA256); e != nil {
		return e
	}
	return historySetMembership(sum, w.BoundaryProof, c.EffectiveBoundarySetRootSHA256)
}
