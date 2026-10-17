package msgtransfer

import (
	"context"
	"path/filepath"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/fcm"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
)

type OwnedOrdinaryOfflineRuntime struct {
	worker   *ManagedKafkaOfflineWorker
	lifetime context.Context
	cancel   context.CancelFunc
	policy   messagedelivery.KafkaDeliveryPolicy
	// Kept private and set only after the owned registry/credentials were
	// accepted. Intake cannot adopt an arbitrary caller-supplied policy.
	database, epoch, activation string
	databaseHandle              *mongo.Database
	bindingSeed                 messagedelivery.KafkaDeliveryPolicy
}

// Executable offline assembly for current AND retained vendor policies. A
// missing old adapter fails managed startup instead of substituting current
// secrets or silently discarding old obligations. This does not remove the
// whole MsgTransfer activation fence or assert online/callback/Seq0 readiness.
func NewOwnedOrdinaryOfflineRuntime(parent context.Context, db *mongo.Database, rdb redis.UniversalClient, tokens cache.MsgModel, reader messagedelivery.VisibilityReader, epoch, activation, badgeEvidenceSHA string, registryFile aliasvendor.OwnedInputFile, callbackFiles ...push.OwnedKafkaCallbackFile) (*OwnedOrdinaryOfflineRuntime, error) {
	if parent == nil || db == nil || rdb == nil || tokens == nil || reader == nil {
		return nil, messagedelivery.ErrInvalid
	}
	currentSeed, err := FreezeOwnedKafkaPolicy(db.Name(), epoch, activation)
	if err != nil {
		return nil, err
	}
	lifetime, cancel := context.WithCancel(parent)
	store, err := messagedelivery.NewMembershipBoundStore(db, epoch, activation)
	if err != nil {
		cancel()
		return nil, err
	}
	registry, err := aliasvendor.LoadOwnedOfflineRegistry(lifetime, registryFile, db.Name(), epoch, activation)
	if err != nil {
		cancel()
		return nil, messagedelivery.ErrUnprepared
	}
	if len(callbackFiles) > 1 {
		cancel()
		return nil, messagedelivery.ErrInvalid
	}
	var callbackFile push.OwnedKafkaCallbackFile
	if len(callbackFiles) == 1 {
		callbackFile = callbackFiles[0]
	}
	var policies []messagedelivery.KafkaDeliveryPolicy
	for _, entry := range registry.Entries {
		policies = append(policies, entry.Policy)
	}
	engines, err := push.LoadOwnedOfflineCallbacks(lifetime, callbackFile, store, db.Name(), epoch, activation, policies)
	if err != nil {
		cancel()
		return nil, messagedelivery.ErrUnprepared
	}
	var adapters []OwnedKafkaOfflineTransport
	release := func() {
		cancel()
		for _, adapter := range adapters {
			_ = adapter.Close()
		}
		for _, engine := range engines {
			_ = engine.Close()
		}
	}
	var current messagedelivery.KafkaDeliveryPolicy
	for _, entry := range registry.Entries {
		// A missing map value is a nil concrete pointer, which must not be
		// converted into a non-nil callback interface for disabled policies.
		var engine messagedelivery.KafkaOfflineCallbackEngine
		if concrete := engines[entry.Fingerprint]; concrete != nil {
			engine = concrete
		}
		isCurrent := entry.Fingerprint == registry.CurrentPolicySHA256
		if isCurrent && entry.BindingSeedSHA256 != currentSeed.ConfigurationSHA256 {
			release()
			return nil, messagedelivery.ErrUnprepared
		}
		switch entry.Policy.Vendor {
		case "fcm":
			if isCurrent {
				path, err := filepath.Abs(config.Config.Push.Fcm.ServiceAccount)
				if err != nil || config.Config.Push.Fcm.ServiceAccount == "" || path != entry.CredentialPath {
					release()
					return nil, messagedelivery.ErrUnprepared
				}
			}
			transport, err := fcm.NewOwnedHTTPV1TransportFromFile(lifetime, fcm.OwnedCredentialFile{Path: entry.CredentialPath, ExpectedSHA256: entry.CredentialSHA256}, entry.BindingSeedSHA256)
			if err != nil {
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			if transport.Binding() != entry.Policy.VendorBinding {
				_ = transport.Close()
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			badge, err := cache.NewOrdinaryBadgeLedger(rdb, store.KafkaBadgeScope(), epoch, activation, badgeEvidenceSHA)
			if err != nil {
				_ = transport.Close()
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			adapter, err := fcm.NewKafkaHTTPV1OfflineAdapter(store, entry.Policy, tokens, badge, transport, engine)
			if err != nil {
				_ = transport.Close()
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			adapters = append(adapters, adapter)
		case "getui", "jpush":
			transport, err := aliasvendor.NewOwnedTransportFromFile(lifetime, aliasvendor.OwnedInputFile{Path: entry.CredentialPath, ExpectedSHA256: entry.CredentialSHA256}, entry.BindingSeedSHA256)
			if err != nil {
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			if transport.Binding() != entry.Policy.VendorBinding {
				_ = transport.Close()
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			if isCurrent {
				matches := transport.MatchesOwnedConfiguration("getui", config.Config.Push.GeTui.PushUrl, config.Config.Push.GeTui.ChannelID, config.Config.Push.GeTui.ChannelName, "", config.Config.IOSPush.Production)
				if entry.Policy.Vendor == "jpush" {
					matches = transport.MatchesOwnedConfiguration("jpush", config.Config.Push.Jpns.PushUrl, "", "", config.Config.Push.Jpns.PushIntent, config.Config.IOSPush.Production)
				}
				if !matches {
					_ = transport.Close()
					release()
					return nil, messagedelivery.ErrUnprepared
				}
			}
			adapter, err := aliasvendor.NewKafkaAliasOfflineAdapter(store, entry.Policy, transport, engine)
			if err != nil {
				_ = transport.Close()
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			adapters = append(adapters, adapter)
		case "none":
			adapters = append(adapters, &ownedNoVendorOffline{store: store, policy: entry.Policy, fingerprint: entry.Fingerprint, callbacks: engine})
		default:
			release()
			return nil, messagedelivery.ErrUnprepared
		}
		if isCurrent {
			expected := currentSeed
			expected.VendorBinding = entry.Policy.VendorBinding
			expected.ConfigurationSHA256, err = kafkaOwnedDigest([]any{db.Name(), epoch, activation, expected.Discovery, expected.EncryptionRequired, expected.BeforeGroupPushBinding, expected.OnlinePushBinding, expected.OfflinePushBinding, expected.Vendor, expected.VendorBinding})
			if err != nil || expected != entry.Policy {
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			current = entry.Policy
		}
	}
	after, err := FreezeOwnedKafkaPolicy(db.Name(), epoch, activation)
	if err != nil || after != currentSeed || current.Version == "" {
		release()
		return nil, messagedelivery.ErrUnprepared
	}
	worker, err := NewManagedKafkaOfflineWorker(store, reader, adapters)
	if err != nil {
		release()
		return nil, err
	}
	return &OwnedOrdinaryOfflineRuntime{worker: worker, lifetime: lifetime, cancel: cancel, policy: current, database: db.Name(), epoch: epoch, activation: activation, bindingSeed: currentSeed, databaseHandle: db}, nil
}
func (r *OwnedOrdinaryOfflineRuntime) Start() error {
	if r == nil {
		return messagedelivery.ErrInvalid
	}
	return r.worker.Start(r.lifetime)
}
func (r *OwnedOrdinaryOfflineRuntime) Stop(ctx context.Context) error {
	if r == nil {
		return messagedelivery.ErrInvalid
	}
	r.cancel()
	return r.worker.Stop(ctx)
}
func (r *OwnedOrdinaryOfflineRuntime) Policy() messagedelivery.KafkaDeliveryPolicy {
	if r == nil {
		return messagedelivery.KafkaDeliveryPolicy{}
	}
	return r.policy
}
func (r *OwnedOrdinaryOfflineRuntime) StopAndWriteDrain(ctx context.Context, path string) (string, error) {
	if err := r.Stop(ctx); err != nil {
		return "", err
	}
	summary, err := r.worker.DrainSummary()
	if err != nil {
		return "", err
	}
	return writeKafkaOfflineDrainSummary(path, summary)
}

type ownedNoVendorOffline struct {
	store       *messagedelivery.Store
	policy      messagedelivery.KafkaDeliveryPolicy
	fingerprint string
	callbacks   messagedelivery.KafkaOfflineCallbackEngine
}

func (a *ownedNoVendorOffline) PolicyFingerprint() string {
	if a == nil {
		return ""
	}
	return a.fingerprint
}
func (a *ownedNoVendorOffline) Close() error {
	if a != nil && a.callbacks != nil {
		return a.callbacks.Close()
	}
	return nil
}
func (a *ownedNoVendorOffline) CheckReady(ctx context.Context) error {
	if a == nil || a.store == nil || a.policy.Vendor != "none" {
		return messagedelivery.ErrUnprepared
	}
	if a.policy.OfflinePushBinding != "" {
		if a.callbacks == nil {
			return messagedelivery.ErrUnprepared
		}
		if err := a.callbacks.CheckReady(ctx); err != nil {
			return err
		}
	}
	return a.store.CheckKafkaReady(ctx)
}
func (a *ownedNoVendorOffline) DeliverOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	if a == nil || call.PolicySHA256 != a.fingerprint {
		return messagedelivery.ErrInvalid
	}
	if err := a.CheckReady(ctx); err != nil {
		return err
	}
	for _, user := range call.DispatchRecipients {
		if err := a.store.RecordKafkaNoVendor(ctx, call, user); err != nil {
			return err
		}
	}
	return nil
}

func (a *ownedNoVendorOffline) PrepareOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	if err := a.CheckReady(ctx); err != nil {
		return err
	}
	if a.callbacks == nil {
		return nil
	}
	return a.callbacks.ExecuteOffline(ctx, call)
}
