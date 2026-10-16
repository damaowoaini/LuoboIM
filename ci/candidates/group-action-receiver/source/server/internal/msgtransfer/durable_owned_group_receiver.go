package msgtransfer

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/OpenIMSDK/tools/discoveryregistry"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/groupeffects"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/fcm"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"google.golang.org/protobuf/proto"
)

// Explicit complete private inputs. Message and offline callback material have
// distinct schemas and hashes; retained policies require retained adapters.
type OwnedGroupReceiverInputs struct {
	Vendor           aliasvendor.OwnedInputFile
	MessageCallbacks push.OwnedKafkaCallbackFile
	OfflineCallbacks push.OwnedKafkaCallbackFile
	RoleCallbacks    push.OwnedKafkaCallbackFile
}
type OwnedGroupReceiverRuntime struct {
	worker   *ManagedGroupActionWorker
	lifetime context.Context
	cancel   context.CancelFunc
	profile  deliveryprofile.Group
}

// An executable separate owned worker entry, not the legacy StartTransfer
// service switch. Ordinary intake/readfloor/seed/drain gates remain mandatory.
func NewOwnedGroupReceiverRuntime(parent context.Context, db *mongo.Database, rdb redis.UniversalClient, tokens cache.MsgModel, reader messagedelivery.VisibilityReader, discovery discoveryregistry.SvcDiscoveryRegistry, gatewayService string, authorize func(context.Context, proto.Message) (context.Context, error), epoch, activation, badgeEvidence string, input OwnedGroupReceiverInputs) (*OwnedGroupReceiverRuntime, error) {
	if parent == nil || db == nil || rdb == nil || tokens == nil || reader == nil || discovery == nil || authorize == nil || gatewayService == "" {
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
	effects, err := groupeffects.NewStore(db)
	if err != nil {
		cancel()
		return nil, err
	}
	registry, err := aliasvendor.LoadOwnedActionOfflineRegistry(lifetime, input.Vendor, db.Name(), epoch, activation)
	if err != nil {
		cancel()
		return nil, messagedelivery.ErrUnprepared
	}
	var profiles []deliveryprofile.Group
	var policies []messagedelivery.KafkaDeliveryPolicy
	for _, entry := range registry.Entries {
		if entry.GroupProfile == nil {
			cancel()
			return nil, messagedelivery.ErrUnprepared
		}
		profiles = append(profiles, *entry.GroupProfile)
		policies = append(policies, entry.Policy)
	}
	messageEngines, err := push.LoadOwnedGroupMessageCallbacks(lifetime, store, input.MessageCallbacks, db.Name(), epoch, activation, profiles)
	if err != nil {
		cancel()
		return nil, err
	}
	engines, err := push.LoadOwnedOfflineCallbacks(lifetime, input.OfflineCallbacks, store, db.Name(), epoch, activation, policies)
	if err != nil {
		cancel()
		for _, e := range messageEngines {
			_ = e.Close()
		}
		return nil, err
	}
	roleEngine, err := push.LoadOwnedGroupRoleCallbacks(lifetime, input.RoleCallbacks, db.Name(), epoch)
	if err != nil {
		cancel()
		for _, e := range engines {
			_ = e.Close()
		}
		for _, e := range messageEngines {
			_ = e.Close()
		}
		return nil, err
	}
	var adapters []OwnedKafkaOfflineTransport
	release := func() {
		cancel()
		_ = roleEngine.Close()
		for _, a := range adapters {
			_ = a.Close()
		}
		for _, e := range engines {
			_ = e.Close()
		}
		for _, e := range messageEngines {
			_ = e.Close()
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
			badge, err := cache.NewOrdinaryBadgeLedger(rdb, store.KafkaBadgeScope(), epoch, activation, badgeEvidence)
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
			expected.Version = "group-effect-v1"
			expected.VendorBinding = entry.Policy.VendorBinding
			expected.ConfigurationSHA256, err = kafkaOwnedDigest([]any{db.Name(), epoch, activation, expected.Discovery, expected.EncryptionRequired, expected.BeforeGroupPushBinding, expected.OnlinePushBinding, expected.OfflinePushBinding, expected.Vendor, expected.VendorBinding})
			if err != nil || expected != entry.Policy {
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			current = entry.Policy
		}
	}
	var online []messagedelivery.KafkaOnlineTransport
	var ownedProfile deliveryprofile.Group
	for _, entry := range registry.Entries {
		sha, err := entry.GroupProfile.Fingerprint()
		if err != nil {
			release()
			return nil, err
		}
		e := messageEngines[sha]
		if e == nil {
			release()
			return nil, messagedelivery.ErrUnprepared
		}
		var configured []*push.FrozenKafkaPushCallbacks
		if pushEngine := e.PushCallbacks(); pushEngine != nil {
			configured = append(configured, pushEngine)
		}
		transport, err := push.NewKafkaFrozenGatewayTransport(discovery, gatewayService, entry.Policy.Discovery == "k8s", authorize, store, entry.Policy, configured...)
		if err != nil {
			release()
			return nil, err
		}
		online = append(online, transport)
		if entry.Fingerprint == registry.CurrentPolicySHA256 {
			frozen, err := deliveryprofile.FreezeGroup(db.Name(), epoch, activation, entry.Policy.VendorBinding)
			if err != nil || deliveryprofile.Digest(frozen) != deliveryprofile.Digest(*entry.GroupProfile) {
				release()
				return nil, messagedelivery.ErrUnprepared
			}
			ownedProfile = frozen
		}
	}
	after, err := FreezeOwnedKafkaPolicy(db.Name(), epoch, activation)
	if err != nil || after != currentSeed || current.Version != "group-effect-v1" || ownedProfile.Version == "" {
		release()
		return nil, messagedelivery.ErrUnprepared
	}
	projection, err := messagedelivery.NewRedisProjection(rdb)
	if err != nil {
		release()
		return nil, err
	}
	worker, err := NewManagedGroupActionWorker(store, effects, reader, projection, online, adapters, messageEngines, roleEngine)
	if err != nil {
		release()
		return nil, err
	}
	return &OwnedGroupReceiverRuntime{worker: worker, lifetime: lifetime, cancel: cancel, profile: ownedProfile}, nil
}
func (r *OwnedGroupReceiverRuntime) Start() error {
	if r == nil {
		return messagedelivery.ErrInvalid
	}
	return r.worker.Start(r.lifetime)
}
func (r *OwnedGroupReceiverRuntime) Stop(ctx context.Context) error {
	if r == nil {
		return messagedelivery.ErrInvalid
	}
	r.cancel()
	return r.worker.Stop(ctx)
}
func (r *OwnedGroupReceiverRuntime) Profile() deliveryprofile.Group {
	if r == nil {
		return deliveryprofile.Group{}
	}
	return r.profile
}

// The source scan and delivery scan rotate independently, so one callback
// unknown never starves accepted branch projection or another group command.
type ManagedGroupActionWorker struct {
	store            ownedGroupActionStore
	effects          ownedGroupActionEffects
	reader           messagedelivery.VisibilityReader
	projection       messagedelivery.Projection
	online           map[string]messagedelivery.KafkaOnlineTransport
	offline          map[string]OwnedKafkaOfflineTransport
	callbacks        map[string]*push.FrozenOwnedGroupMessageCallbacks
	roleCallbacks    *push.FrozenOwnedGroupRoleCallbacks
	mu               sync.Mutex
	cancel           context.CancelFunc
	started, closing bool
	done, closeDone  chan struct{}
	closeOnce        sync.Once
	closeErr         error
}

// Private seams mirror the concrete stores' exact methods. The exported
// constructor below still admits only real Mongo stores; tests replace only
// storage/transport boundaries while exercising this actual lifecycle.
type ownedGroupActionStore interface {
	CheckKafkaReady(context.Context) error
	KafkaOfflineInstanceID() string
	RequiredOwnedActionPolicies(context.Context) ([]string, error)
	ListOwnedActionPending(context.Context, string, int64) ([]messagedelivery.KafkaOnlinePending, error)
	RecoverOwnedGroupNotification(context.Context, string, messagedelivery.OwnedGroupMessageCallbacks) (messagedelivery.Receipt, error)
	RecoverOwnedActionOnline(context.Context, string, messagedelivery.VisibilityReader, messagedelivery.Projection, messagedelivery.KafkaOnlineTransport) error
	RecoverOwnedActionOffline(context.Context, string, messagedelivery.VisibilityReader, messagedelivery.KafkaOfflineTransport) error
}
type ownedGroupActionEffects interface {
	CheckReady(context.Context) error
	RequiredOwnedProfiles(context.Context) ([]deliveryprofile.Group, error)
	ListOwnedNotifications(context.Context, string, int64) ([]groupeffects.OwnedNotificationPending, error)
	RequiredOwnedRoleCallbackTransports(context.Context) ([]string, error)
	ListOwnedRoleCallbacks(context.Context, string, int64) ([]groupeffects.OwnedRoleCallbackRef, error)
	InstanceID() string
}

func NewManagedGroupActionWorker(store *messagedelivery.Store, effects *groupeffects.Store, reader messagedelivery.VisibilityReader, projection messagedelivery.Projection, online []messagedelivery.KafkaOnlineTransport, offline []OwnedKafkaOfflineTransport, callbacks map[string]*push.FrozenOwnedGroupMessageCallbacks, role ...*push.FrozenOwnedGroupRoleCallbacks) (*ManagedGroupActionWorker, error) {
	if store == nil || effects == nil {
		return nil, messagedelivery.ErrInvalid
	}
	return newManagedGroupActionWorker(store, effects, reader, projection, online, offline, callbacks, role...)
}
func newManagedGroupActionWorker(store ownedGroupActionStore, effects ownedGroupActionEffects, reader messagedelivery.VisibilityReader, projection messagedelivery.Projection, online []messagedelivery.KafkaOnlineTransport, offline []OwnedKafkaOfflineTransport, callbacks map[string]*push.FrozenOwnedGroupMessageCallbacks, role ...*push.FrozenOwnedGroupRoleCallbacks) (*ManagedGroupActionWorker, error) {
	if len(role) > 1 || store == nil || effects == nil || store.KafkaOfflineInstanceID() == "" || reader == nil || projection == nil || len(online) == 0 || len(offline) == 0 || len(callbacks) == 0 {
		return nil, messagedelivery.ErrInvalid
	}
	w := &ManagedGroupActionWorker{store: store, effects: effects, reader: reader, projection: projection, online: map[string]messagedelivery.KafkaOnlineTransport{}, offline: map[string]OwnedKafkaOfflineTransport{}, callbacks: callbacks, done: make(chan struct{}), closeDone: make(chan struct{})}
	if len(role) == 1 {
		if role[0] == nil {
			return nil, messagedelivery.ErrInvalid
		}
		w.roleCallbacks = role[0]
	}
	for _, a := range online {
		if a == nil || w.online[a.PolicyFingerprint()] != nil {
			return nil, messagedelivery.ErrInvalid
		}
		w.online[a.PolicyFingerprint()] = a
	}
	for _, a := range offline {
		if a == nil || w.offline[a.PolicyFingerprint()] != nil || w.online[a.PolicyFingerprint()] == nil {
			return nil, messagedelivery.ErrInvalid
		}
		w.offline[a.PolicyFingerprint()] = a
	}
	if len(w.online) != len(w.offline) {
		return nil, messagedelivery.ErrUnprepared
	}
	return w, nil
}
func (w *ManagedGroupActionWorker) Start(parent context.Context) error {
	if w == nil || parent == nil {
		return messagedelivery.ErrInvalid
	}
	// Reserve this exact lifecycle before readiness. Stop closes admission in the
	// SAME lock and cancels readiness; it cannot close pools before this turn joins.
	w.mu.Lock()
	if w.started || w.closing {
		w.mu.Unlock()
		return messagedelivery.ErrConflict
	}
	ctx, cancelRun := context.WithCancel(parent)
	w.cancel = cancelRun
	w.started = true
	w.mu.Unlock()
	launched := false
	defer func() {
		if !launched {
			cancelRun()
			close(w.done)
		}
	}()
	ready, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := w.store.CheckKafkaReady(ready); err != nil {
		return err
	}
	if err := w.effects.CheckReady(ready); err != nil {
		return err
	}
	required, err := w.store.RequiredOwnedActionPolicies(ready)
	if err != nil {
		return err
	}
	for _, sha := range required {
		if w.online[sha] == nil || w.offline[sha] == nil {
			return messagedelivery.ErrUnprepared
		}
	}
	profiles, err := w.effects.RequiredOwnedProfiles(ready)
	if err != nil {
		return err
	}
	for _, p := range profiles {
		sha, err := p.Fingerprint()
		if err != nil || w.callbacks[sha] == nil {
			return messagedelivery.ErrUnprepared
		}
		transport, err := messagedelivery.KafkaDeliveryPolicy(p.Transport).Fingerprint()
		if err != nil || w.online[transport] == nil || w.offline[transport] == nil {
			return messagedelivery.ErrUnprepared
		}
	}
	requiredAfter, err := w.effects.RequiredOwnedRoleCallbackTransports(ready)
	if err != nil {
		return err
	}
	if len(requiredAfter) > 0 && (w.roleCallbacks == nil || w.roleCallbacks.CheckReady(requiredAfter) != nil) {
		return messagedelivery.ErrUnprepared
	}
	for _, a := range w.offline {
		if err := a.CheckReady(ready); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	launched = true
	go w.run(ctx)
	return nil
}
func (w *ManagedGroupActionWorker) run(ctx context.Context) {
	defer close(w.done)
	sourceCursor, branchCursor, afterCursor := "", "", ""
	for ctx.Err() == nil {
		after, afterErr := w.effects.ListOwnedRoleCallbacks(ctx, afterCursor, 8)
		if afterErr == nil {
			if len(after) == 0 {
				afterCursor = ""
			}
			for _, ref := range after {
				if ctx.Err() != nil {
					return
				}
				afterCursor = ref.Cursor
				if w.roleCallbacks != nil {
					_ = w.recoverRoleCallback(ctx, ref)
				}
			}
		}
		sources, err := w.effects.ListOwnedNotifications(ctx, sourceCursor, 8)
		if err == nil {
			if len(sources) == 0 {
				sourceCursor = ""
			}
			for _, row := range sources {
				if ctx.Err() != nil {
					return
				}
				sourceCursor = row.Cursor
				// Lookup of profile is primary read-only inside the receiver transaction;
				// use its exact required-profile set, not current policy substitution.
				for _, engine := range w.callbacks {
					_, err := w.store.RecoverOwnedGroupNotification(ctx, row.ID, engine)
					if !errors.Is(err, messagedelivery.ErrUnprepared) {
						break
					}
				}
			}
		}
		branches, err := w.store.ListOwnedActionPending(ctx, branchCursor, 16)
		if err == nil {
			if len(branches) == 0 {
				branchCursor = ""
			}
			for _, row := range branches {
				if ctx.Err() != nil {
					return
				}
				branchCursor = row.Cursor
				online, offline := w.online[row.PolicySHA256], w.offline[row.PolicySHA256]
				if online == nil || offline == nil {
					continue
				}
				_ = w.store.RecoverOwnedActionOnline(ctx, row.BranchID, w.reader, w.projection, online)
				if ctx.Err() != nil {
					return
				}
				_ = w.store.RecoverOwnedActionOffline(ctx, row.BranchID, w.reader, offline)
			}
		}
		if !waitKafkaWorker(ctx, 250*time.Millisecond) {
			return
		}
	}
}

// Stop never closes a pool while the worker is still alive. Timeout retains
// ownership and can be called again to join; it is not cached as successful.
func (w *ManagedGroupActionWorker) Stop(ctx context.Context) error {
	if w == nil || ctx == nil {
		return messagedelivery.ErrInvalid
	}
	w.mu.Lock()
	w.closing = true
	started := w.started
	cancel := w.cancel
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	// One shutdown retains ownership even if this caller times out. Further Stop
	// calls join the same operation, rather than closing a live/reused pool.
	w.closeOnce.Do(func() {
		go func() {
			if started {
				<-w.done
			}
			var failures []error
			for _, a := range w.offline {
				failures = append(failures, a.Close())
			}
			for _, e := range w.callbacks {
				failures = append(failures, e.Close())
			}
			if w.roleCallbacks != nil {
				failures = append(failures, w.roleCallbacks.Close())
			}
			w.mu.Lock()
			w.closeErr = errors.Join(failures...)
			w.mu.Unlock()
			close(w.closeDone)
		}()
	})
	select {
	case <-w.closeDone:
		w.mu.Lock()
		err := w.closeErr
		w.mu.Unlock()
		return err
	case <-ctx.Done():
		return ErrKafkaOfflineShutdownPending
	}
}

// Metadata-only drain receipt: unknown callbacks/Seq0/vendor outcomes remain
// unknown. This is proof of this instance's local lifecycle, not remote ACK.
type OwnedGroupActionDrainSummary struct {
	Version                 string                                 `json:"version"`
	InstanceID              string                                 `json:"instanceID"`
	BusinessAfterInstanceID string                                 `json:"businessAfterInstanceID"`
	WorkerJoined            bool                                   `json:"workerJoined"`
	OwnedPoolsClosed        bool                                   `json:"ownedPoolsClosed"`
	Policies                []string                               `json:"policies"`
	Profiles                []string                               `json:"profiles"`
	Legacy                  *messagedelivery.OwnedLegacyDrainProof `json:"legacy"`
}

func (s OwnedGroupActionDrainSummary) SHA256() string {
	body, _ := json.Marshal(s)
	body = append(body, '\n')
	return deliveryprofile.SHA(body)
}
func (w *ManagedGroupActionWorker) DrainSummary() (OwnedGroupActionDrainSummary, error) {
	if w == nil {
		return OwnedGroupActionDrainSummary{}, messagedelivery.ErrInvalid
	}
	select {
	case <-w.closeDone:
	default:
		return OwnedGroupActionDrainSummary{}, ErrKafkaOfflineShutdownPending
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closing || w.closeErr != nil {
		return OwnedGroupActionDrainSummary{}, ErrKafkaOfflineShutdownPending
	}
	policies := make([]string, 0, len(w.offline))
	for sha := range w.offline {
		policies = append(policies, sha)
	}
	sort.Strings(policies)
	profileSet := map[string]bool{}
	for sha := range w.callbacks {
		profileSet[sha] = true
	}
	if w.roleCallbacks != nil {
		for _, sha := range w.roleCallbacks.Fingerprints() {
			profileSet[sha] = true
		}
	}
	profiles := make([]string, 0, len(profileSet))
	for sha := range profileSet {
		profiles = append(profiles, sha)
	}
	sort.Strings(profiles)
	return OwnedGroupActionDrainSummary{Version: "owned-group-action-drained-v1", InstanceID: w.store.KafkaOfflineInstanceID(), BusinessAfterInstanceID: w.effects.InstanceID(), WorkerJoined: true, OwnedPoolsClosed: true, Policies: policies, Profiles: profiles}, nil
}
func (r *OwnedGroupReceiverRuntime) StopAndWriteDrain(ctx context.Context, path string) (string, error) {
	if err := r.Stop(ctx); err != nil {
		return "", err
	}
	summary, err := r.worker.DrainSummary()
	if err != nil {
		return "", err
	}
	return writeOwnedGroupActionDrainSummary(path, summary)
}

func (w *ManagedGroupActionWorker) recoverRoleCallback(ctx context.Context, ref groupeffects.OwnedRoleCallbackRef) error {
	store, ok := w.effects.(*groupeffects.Store)
	if !ok || w.roleCallbacks == nil {
		return messagedelivery.ErrUnprepared
	}
	return w.roleCallbacks.Recover(ctx, store, ref)
}
