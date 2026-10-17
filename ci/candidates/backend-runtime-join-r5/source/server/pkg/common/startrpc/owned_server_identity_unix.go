//go:build darwin || linux

package startrpc

import (
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"sync"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

// OwnedServerIdentity borrows the already source-bound private bootstrap. It
// supplies no Start/Ready permission and owns no RPC/server/storage resource.
// Each supported assembly calls Announce only after actual complete startup, and Drain
// only after every named real stop step completed. Unowned defaults never use it.
type OwnedServerIdentity struct {
	mu                    sync.Mutex
	input                 ownedRPCInput
	announced             bool
	unconfirmed           bool
	source                *ownedmongo.Observer
	databaseName          string // exact actual database accepted before indexes/routes
	runtimeConfig, policy string
	steps                 []string
}

func LoadOwnedServerIdentity(role string) (*OwnedServerIdentity, error) {
	if role != "api" && role != "crontask" {
		return nil, errOwnedRPC
	}
	input, err := loadOwnedRPCInput(role)
	if err != nil {
		return nil, err
	}
	return &OwnedServerIdentity{input: input}, nil
}

func (i *OwnedServerIdentity) BindSourceObserver(source *ownedmongo.Observer) error {
	if i == nil || source == nil {
		return errOwnedRPC
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.announced || (i.source != nil && i.source != source) {
		return errOwnedRPC
	}
	i.source = source
	return nil
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

func ownedServerStopSteps(role string, steps []string) bool {
	switch role {
	case "api":
		return sameOwnedSteps(steps, apiOwnedStopSteps(false)) || sameOwnedSteps(steps, apiOwnedStopSteps(true))
	case "crontask":
		return sameOwnedSteps(steps, []string{"cron-admission", "cron-jobs", "kafka-producers", "redis", "mongo", "discovery"})
	default:
		return false
	}
}

func (i *OwnedServerIdentity) Announce(steps []string) error {
	if i == nil {
		return errOwnedRPC
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.source == nil || i.databaseName == "" || ownedSHA([]byte(config.Config.Mongo.Database)) != i.input.boot.DatabaseSHA256 || i.announced || i.unconfirmed || !ownedServerStopSteps(i.input.boot.Role, steps) {
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
	if i.source == nil || i.databaseName == "" || ownedSHA([]byte(config.Config.Mongo.Database)) != i.input.boot.DatabaseSHA256 || !i.announced || i.unconfirmed || !sameOwnedSteps(i.steps, completed) {
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
	outcome := i.source.Snapshot()
	if outcome.SourceOutcomeUnconfirmed {
		return errors.Join(errOwnedRPC, i.recordUnconfirmedLocked(ownedcontrol.ObserveMongoSnapshot(outcome)))
	}
	b := i.input.boot
	_, err = ownedcontrol.WriteServerRuntimeDrain(b.DrainPath, i.input.nonce, ownedcontrol.ServerRuntimeDrain{Version: "owned-server-drained-v1", Role: b.Role, Mode: b.Mode, InstanceID: b.InstanceID, DatabaseSHA256: b.DatabaseSHA256, Epoch: b.Epoch, ActivationSHA256: b.ActivationSHA256, BootstrapSHA256: i.input.rawSHA, PolicySHA256: i.policy, JoinedAndClosed: append([]string{}, completed...), SourceOutcomeUnconfirmed: outcome.SourceOutcomeUnconfirmed})
	return err
}

func (i *OwnedServerIdentity) RecordUnconfirmed(value ownedcontrol.SourceObservation) error {
	if i == nil {
		return errOwnedRPC
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.recordUnconfirmedLocked(value)
}
func (i *OwnedServerIdentity) recordUnconfirmedLocked(value ownedcontrol.SourceObservation) error {
	if !value.SourceOutcomeUnconfirmed || i.source == nil {
		return errOwnedRPC
	}
	i.unconfirmed = true
	configuration, environment, err := OwnedRPCConfigurationHashes()
	if err != nil || environment != i.input.environmentSHA {
		return errOwnedRPC
	}
	if i.announced && configuration != i.runtimeConfig {
		return errOwnedRPC
	}
	policy, err := i.input.policySHA(configuration)
	if err != nil || (i.announced && policy != i.policy) {
		return errOwnedRPC
	}
	value.Version, value.Role, value.InstanceID = "owned-server-source-unconfirmed-v1", i.input.boot.Role, i.input.boot.InstanceID
	value.BootstrapSHA256, value.PolicySHA256 = i.input.rawSHA, policy
	_, err = ownedcontrol.WriteServerSourceObservation(i.input.boot.DrainPath+".unconfirmed", i.input.nonce, value)
	return err
}
