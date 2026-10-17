//go:build darwin || linux

package startrpc

import (
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

// OwnedServerIdentity borrows the already source-bound private bootstrap. It
// supplies no Start/Ready permission and owns no RPC/server/storage resource.
// The API assembly calls Announce only after actual complete startup, and Drain
// only after every named real stop step completed. Unowned defaults never use it.
type OwnedServerIdentity struct {
	mu                    sync.Mutex
	input                 ownedRPCInput
	announced             bool
	unconfirmed           bool
	databaseName          string // exact actual database accepted before indexes/routes
	runtimeConfig, policy string
	steps                 []string
}

func LoadOwnedServerIdentity(role string) (*OwnedServerIdentity, error) {
	if role != "api" {
		return nil, errOwnedRPC
	}
	input, err := loadOwnedRPCInput(role)
	if err != nil {
		return nil, err
	}
	return &OwnedServerIdentity{input: input}, nil
}

func (i *OwnedServerIdentity) CheckDatabase(name string) error {
	if i == nil {
		return errOwnedRPC
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if name == "" || ownedSHA([]byte(name)) != i.input.boot.DatabaseSHA256 || (i.databaseName != "" && i.databaseName != name) {
		return errOwnedRPC
	}
	i.databaseName = name
	return nil
}

func apiOwnedStopSteps(metrics bool) []string {
	steps := []string{"http-admission", "http-handlers", "http-serve"}
	if metrics {
		steps = append(steps, "metrics")
	}
	return append(steps, "exact-logout-recovery", "sensitive-word-workers", "http-validator", "redis", "mongo", "discovery")
}

func (i *OwnedServerIdentity) Announce(steps []string) error {
	if i == nil {
		return errOwnedRPC
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.databaseName == "" || ownedSHA([]byte(config.Config.Mongo.Database)) != i.input.boot.DatabaseSHA256 || i.announced || (!sameOwnedSteps(steps, apiOwnedStopSteps(false)) && !sameOwnedSteps(steps, apiOwnedStopSteps(true))) {
		return errOwnedRPC
	}
	runtimeConfig, env, err := OwnedRPCConfigurationHashes()
	if err != nil || env != i.input.environmentSHA {
		return errOwnedRPC
	}
	policy, err := i.input.policySHA(runtimeConfig)
	if err != nil {
		return err
	}
	b := i.input.boot
	value := ownedcontrol.ServerRuntimeAnnouncement{Version: "owned-server-owner-ready-v1", Role: b.Role, Mode: b.Mode, InstanceID: b.InstanceID, DatabaseSHA256: b.DatabaseSHA256, Epoch: b.Epoch, ActivationSHA256: b.ActivationSHA256, BootstrapSHA256: i.input.rawSHA, PolicySHA256: policy, DrainPath: b.DrainPath, OwnedStopStepNames: append([]string{}, steps...)}
	if _, err = ownedcontrol.WriteServerRuntimeAnnouncement(i.input.path, i.input.nonce, value); err != nil {
		return err
	}
	i.runtimeConfig, i.policy, i.steps, i.announced = runtimeConfig, policy, append([]string{}, steps...), true
	return nil
}

func (i *OwnedServerIdentity) Drain(completed []string) error {
	if i == nil {
		return errOwnedRPC
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.databaseName == "" || ownedSHA([]byte(config.Config.Mongo.Database)) != i.input.boot.DatabaseSHA256 || !i.announced || i.unconfirmed || !sameOwnedSteps(i.steps, completed) {
		return errOwnedRPC
	}
	configuration, env, err := OwnedRPCConfigurationHashes()
	if err != nil || configuration != i.runtimeConfig || env != i.input.environmentSHA {
		return errOwnedRPC
	}
	policy, err := i.input.policySHA(i.runtimeConfig)
	if err != nil || policy != i.policy {
		return errOwnedRPC
	}
	b := i.input.boot
	_, err = ownedcontrol.WriteServerRuntimeDrain(b.DrainPath, i.input.nonce, ownedcontrol.ServerRuntimeDrain{Version: "owned-server-drained-v1", Role: b.Role, Mode: b.Mode, InstanceID: b.InstanceID, DatabaseSHA256: b.DatabaseSHA256, Epoch: b.Epoch, ActivationSHA256: b.ActivationSHA256, BootstrapSHA256: i.input.rawSHA, PolicySHA256: i.policy, JoinedAndClosed: append([]string{}, completed...), SourceOutcomeUnconfirmed: false})
	return err
}

func (i *OwnedServerIdentity) RecordUnconfirmed(value ownedcontrol.SourceObservation) error {
	if i == nil {
		return errOwnedRPC
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.announced || !value.SourceOutcomeUnconfirmed {
		return errOwnedRPC
	}
	i.unconfirmed = true
	policy, err := i.input.policySHA(i.runtimeConfig)
	if err != nil || policy != i.policy {
		return errOwnedRPC
	}
	value.Version, value.Role, value.InstanceID = "owned-server-source-unconfirmed-v1", i.input.boot.Role, i.input.boot.InstanceID
	value.BootstrapSHA256, value.PolicySHA256 = i.input.rawSHA, i.policy
	_, err = ownedcontrol.WriteServerSourceObservation(i.input.boot.DrainPath+".unconfirmed", i.input.nonce, value)
	return err
}
