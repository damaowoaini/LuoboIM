//go:build darwin || linux

package startrpc

import (
	"context"
	"errors"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/ownedmongo"
	"sort"
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
	namespace             *ownedNamespaceStartup
	steps                 []string
}

func LoadOwnedServerIdentity(role string) (*OwnedServerIdentity, error) {
	if role != "api" && role != "crontask" && role != "msgtransfer-legacy" {
		return nil, errOwnedRPC
	}
	input, err := loadOwnedRPCInput(role)
	if err != nil {
		return nil, err
	}
	if role == "msgtransfer-legacy" && input.boot.Mode != "legacy" {
		return nil, errOwnedRPC
	}
	state, e := parseOwnedNamespaceStartup(input)
	if e != nil {
		return nil, e
	}
	return &OwnedServerIdentity{input: input, namespace: state}, nil
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
	case "msgtransfer-legacy":
		plain := []string{"kafka-admission", "kafka-consumers", "kafka-producers", "redis", "mongo", "discovery"}
		measured := []string{"kafka-admission", "kafka-consumers", "kafka-producers", "metrics", "redis", "mongo", "discovery"}
		return sameOwnedSteps(steps, plain) || sameOwnedSteps(steps, measured)
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
	if i.namespace != nil {
		if _, _, e := i.namespace.gate.KnownPublication(); e != nil {
			return e
		}
	}
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

// The actual non-RPC original owner transports the same private startup gate;
// it does not manufacture DAO bindings or report a worker as already started.
func (i *OwnedServerIdentity) NamespaceRuntimeContext(ctx context.Context) (context.Context, error) {
	if i == nil || ctx == nil {
		return nil, errOwnedRPC
	}
	if i.namespace == nil {
		if i.input.boot.Mode == "required" {
			return nil, errOwnedRPC
		}
		return ctx, nil
	}
	return ownedcontrol.WithNamespaceRuntimeStartup(ctx, i.namespace.gate), nil
}
func (i *OwnedServerIdentity) RetireNamespaceRuntime() {
	if i != nil && i.namespace != nil {
		i.namespace.gate.Retire()
	}
}
func (i *OwnedServerIdentity) AwaitNamespaceRuntimeAdmission(ctx context.Context, resources, services, components []string, binding OwnedNamespaceRuntimeBinding) error {
	if i == nil || ctx == nil {
		return errOwnedRPC
	}
	if i.namespace == nil {
		if i.input.boot.Mode == "required" {
			return errOwnedRPC
		}
		return nil
	}
	i.mu.Lock()
	source, db := i.source, i.databaseName
	i.mu.Unlock()
	if source == nil || db == "" {
		return errOwnedRPC
	}
	state := i.namespace
	if binding != nil && !binding.MatchesNamespaceRuntimeGate(state.gate) {
		return errOwnedRPC
	}
	resources = append([]string(nil), resources...)
	services = append([]string(nil), services...)
	components = append([]string(nil), components...)
	sort.Strings(resources)
	sort.Strings(services)
	sort.Strings(components)
	var bindings ownedcontrol.NamespaceRuntimeBindings
	var err error
	if binding != nil {
		bindings, err = binding.SealNamespaceBindings(ctx, resources, source.Snapshot())
		if err != nil {
			return err
		}
	} else {
		bindings = ownedcontrol.NamespaceRuntimeBindings{Role: i.input.boot.Role, HeaderSHA256: state.gate.Bootstrap().Header.SHA256(), Resources: resources, Source: source.Snapshot()}
	}
	if _, err = state.gate.RecordPrepared(ctx, services, components, bindings); err != nil {
		return err
	}
	if err = state.gate.AwaitAdmission(ctx); err != nil {
		return err
	}
	if binding != nil {
		return binding.ValidateRuntimeStores(ctx)
	}
	return nil
}
