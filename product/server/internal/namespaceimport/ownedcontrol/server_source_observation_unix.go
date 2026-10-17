//go:build darwin || linux

package ownedcontrol

import (
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"github.com/openimsdk/open-im-server/v3/pkg/common/ownedsource"
)

// SourceObservation is not a successful drain and is never an exclusion permit.
// It retains only counters for the exact original owner whose writes may remain
// unconfirmed. No command/reply/body/connection/error text enters this record.
type SourceObservation struct {
	External                 ownedsource.Snapshot `json:"external"`
	Version                  string               `json:"version"`
	Role                     string               `json:"role"`
	InstanceID               string               `json:"instanceID"`
	BootstrapSHA256          string               `json:"bootstrapSHA256"`
	PolicySHA256             string               `json:"policySHA256"`
	SourceOutcomeUnconfirmed bool                 `json:"sourceOutcomeUnconfirmed"`
	Started                  uint64               `json:"started"`
	Succeeded                uint64               `json:"succeeded"`
	Failed                   uint64               `json:"failed"`
	InFlight                 int                  `json:"inFlight"`
	IntakeUnconfirmed        bool                 `json:"intakeUnconfirmed,omitempty"`
	Overflow                 bool                 `json:"overflow"`
}

func WriteServerSourceObservation(path, nonce string, value SourceObservation) (string, error) {
	if !isSHA(nonce) || value.Version != "owned-server-source-unconfirmed-v1" || (!serverRole(value.Role) && value.Role != "msgtransfer-managed") || !uuidPattern.MatchString(value.InstanceID) || !isSHA(value.BootstrapSHA256) || !isSHA(value.PolicySHA256) || !value.SourceOutcomeUnconfirmed || value.InFlight < 0 || value.InFlight > 1024 {
		return "", ErrOwnedEvidence
	}
	return writeServerEnvelope(path, struct {
		Schema      int               `json:"schema"`
		OwnerNonce  string            `json:"ownerNonce"`
		Observation SourceObservation `json:"observation"`
	}{1, nonce, value})
}

func ObserveMongoSnapshot(value ownedmongo.Snapshot) SourceObservation {
	return SourceObservation{External: value.External, SourceOutcomeUnconfirmed: value.SourceOutcomeUnconfirmed, Started: value.Started, Succeeded: value.Succeeded, Failed: value.Failed, InFlight: value.InFlight, Overflow: value.Overflow}
}
