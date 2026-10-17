package fcm

import (
	"context"
	"errors"
	"sort"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/tools/errs"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
)

// Separate successor, never a fallback to the retired SDK batch transport.
// The process-owned factory must freeze transport.Binding() into policy at
// startup; the earlier service-account-path-only binding will be rejected.
type KafkaHTTPV1OfflineAdapter struct {
	callbacks messagedelivery.KafkaOfflineCallbackEngine
	store     *messagedelivery.Store
	policy    messagedelivery.KafkaDeliveryPolicy
	policySHA string
	tokens    httpV1TokenReader
	badge     *cache.OrdinaryBadgeLedger
	transport *HTTPV1Transport
}

type httpV1TokenReader interface {
	GetFcmToken(context.Context, string, int) (string, error)
}

func NewKafkaHTTPV1OfflineAdapter(store *messagedelivery.Store, policy messagedelivery.KafkaDeliveryPolicy, tokens cache.MsgModel, badge *cache.OrdinaryBadgeLedger, transport *HTTPV1Transport, callbacks ...messagedelivery.KafkaOfflineCallbackEngine) (*KafkaHTTPV1OfflineAdapter, error) {
	digest, err := policy.Fingerprint()
	if err != nil || store == nil || tokens == nil || badge == nil || transport == nil || transport.closed.Load() || policy.Vendor != "fcm" || policy.VendorBinding != transport.Binding() || !badge.BoundTo(store.KafkaBadgeScope(), policy.Epoch, policy.ActivationSHA256) {
		return nil, ErrHTTPV1Input
	}
	var callback messagedelivery.KafkaOfflineCallbackEngine
	if len(callbacks) > 1 {
		return nil, ErrHTTPV1Input
	}
	if len(callbacks) == 1 {
		callback = callbacks[0]
	}
	if callback != nil && (callback.PolicyFingerprint() != digest || policy.OfflinePushBinding == "") {
		return nil, ErrHTTPV1Input
	}
	return &KafkaHTTPV1OfflineAdapter{callbacks: callback, store: store, policy: policy, policySHA: digest, tokens: tokens, badge: badge, transport: transport}, nil
}
func (a *KafkaHTTPV1OfflineAdapter) PolicyFingerprint() string {
	if a == nil {
		return ""
	}
	return a.policySHA
}
func (a *KafkaHTTPV1OfflineAdapter) Close() error {
	if a == nil || a.transport == nil {
		return ErrHTTPV1Input
	}
	if a.callbacks != nil {
		return errors.Join(a.transport.Close(), a.callbacks.Close())
	}
	return a.transport.Close()
}
func (a *KafkaHTTPV1OfflineAdapter) CheckReady(ctx context.Context) error {
	if a == nil || ctx == nil || a.store == nil || a.tokens == nil || a.badge == nil || a.transport == nil || a.transport.closed.Load() {
		return ErrHTTPV1Input
	}
	if err := a.store.CheckKafkaReady(ctx); err != nil {
		return err
	}
	if a.policy.OfflinePushBinding != "" {
		if a.callbacks == nil {
			return messagedelivery.ErrUnprepared
		}
		if err := a.callbacks.CheckReady(ctx); err != nil {
			return err
		}
	}
	return a.badge.CheckReady(ctx)
}

func (a *KafkaHTTPV1OfflineAdapter) tokenMap(ctx context.Context, user string) (map[string]string, []string, error) {
	byHash := map[string]string{}
	seen := map[string]bool{}
	var tokens []string
	for _, platform := range []int{constant.IOSPlatformID, constant.AndroidPlatformID, constant.WebPlatformID} {
		token, err := a.tokens.GetFcmToken(ctx, user, platform)
		if err != nil {
			if errors.Is(err, redis.Nil) || errs.Unwrap(err) == redis.Nil {
				continue
			}
			return nil, nil, ErrHTTPV1Unknown
		}
		if token == "" {
			return nil, nil, ErrHTTPV1Input
		}
		if !seen[token] {
			seen[token] = true
			tokens = append(tokens, token)
			byHash[frozenHash([]byte(token))] = token
		}
	}
	sort.Strings(tokens)
	if _, err := TokenSetFingerprint(tokens); err != nil {
		return nil, nil, ErrHTTPV1Input
	}
	if len(byHash) != len(tokens) {
		return nil, nil, ErrHTTPV1Input
	}
	return byHash, tokens, nil
}

func (a *KafkaHTTPV1OfflineAdapter) DeliverOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	if a == nil || ctx == nil || call.Message == nil || call.PolicySHA256 != a.policySHA {
		return ErrHTTPV1Input
	}
	if err := a.CheckReady(ctx); err != nil {
		return err
	}
	effective, err := a.store.KafkaOfflineProviderMessage(ctx, call)
	if err != nil {
		return err
	}
	conversationID := effective.SendID
	if call.Message.SessionType == constant.SuperGroupChatType || call.Message.SessionType == constant.GroupChatType {
		conversationID = call.Message.GroupID
	}
	title, content, opts, err := offlinepush.MessageInfo(conversationID, effective)
	if err != nil {
		return ErrHTTPV1Input
	}
	var failures []error
	for _, user := range call.DispatchRecipients {
		if ctx.Err() != nil {
			failures = append(failures, messagedelivery.ErrKafkaOfflineUnconfirmed)
			break
		}
		state, err := a.store.ResolveKafkaVendor(ctx, call, user)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		switch state {
		case "completed", "no-registered-token", "suppressed", "no-vendor", "operator-rejected":
			continue
		case "pending", "devices-pending":
		default:
			failures = append(failures, messagedelivery.ErrKafkaOfflineUnconfirmed)
			continue
		}
		// A restart obtains the durable original before new token reads/badge.
		manifest, err := a.store.ReadKafkaFCMManifest(ctx, call, user)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		byHash, tokens, err := a.tokenMap(ctx, user)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if manifest == nil {
			tokenSet, err := TokenSetFingerprint(tokens)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			input := messagedelivery.KafkaVendorRequest{Vendor: "fcm", Binding: a.transport.Binding(), TokenSetSHA256: tokenSet, Protocol: "fcm-http-v1-device-v1"}
			if len(tokens) > 0 {
				snapshot, err := a.badge.Freeze(ctx, user, call.BranchID, opts.IOSBadgeCount)
				if err != nil {
					failures = append(failures, cache.ErrOrdinaryBadgeUnprepared)
					continue
				}
				input.Badge = int64(snapshot.Value)
				input.BadgeLedgerValue = int64(snapshot.Value)
				input.BadgeResetID = snapshot.ResetID
				input.BadgeReceiptSHA256 = snapshot.ReceiptSHA256
				if !opts.IOSBadgeCount && input.Badge == 0 {
					input.Badge = 1
				} // Existing read-only badge fallback, not a second increment.
				for device := range byHash {
					input.DeviceSHA256 = append(input.DeviceSHA256, device)
				}
				sort.Strings(input.DeviceSHA256)
				for _, device := range input.DeviceSHA256 {
					deviceCall := HTTPV1DeviceCall{Binding: input.Binding, Token: byHash[device], TokenSHA256: device, Title: title, Content: content, Extra: opts.Ex, Sound: opts.IOSPushSound, Badge: int(input.Badge)}
					proof, err := a.transport.RequestFingerprint(deviceCall)
					if err != nil {
						return err
					}
					input.DeviceRequestSHA256 = append(input.DeviceRequestSHA256, proof)
				}
			}
			input.ProviderRequestSHA256, err = messagedelivery.FCMManifestFingerprint(input)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			manifest, err = a.store.FreezeKafkaFCMManifest(ctx, call, user, input)
			if err != nil {
				failures = append(failures, err)
				continue
			}
		}
		if manifest.Terminal() {
			continue
		}
		input := manifest.Input()
		// Only hashes from the original manifest may be considered. A newly
		// registered token is NOT appended to an already accepted message.
		for i, device := range input.DeviceSHA256 {
			dispatch, err := manifest.BeginDevice(ctx, call, user, device, input.DeviceRequestSHA256[i])
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if dispatch == nil {
				continue
			}
			token, present := byHash[device]
			if !present {
				if err := dispatch.NotDispatched(ctx, frozenHash([]byte("original frozen token not in current owned token set"))); err != nil {
					failures = append(failures, err)
				}
				failures = append(failures, messagedelivery.ErrKafkaOfflineUnconfirmed)
				continue
			}
			deviceCall := HTTPV1DeviceCall{Binding: input.Binding, Token: token, TokenSHA256: device, Title: title, Content: content, Extra: opts.Ex, Sound: opts.IOSPushSound, Badge: int(input.Badge)}
			proof, proofErr := a.transport.RequestFingerprint(deviceCall)
			if proofErr != nil || proof != input.DeviceRequestSHA256[i] {
				if err := dispatch.NotDispatched(ctx, frozenHash([]byte("current template does not match original device request"))); err != nil {
					failures = append(failures, err)
				}
				failures = append(failures, messagedelivery.ErrKafkaOfflineUnconfirmed)
				continue
			}
			result, providerErr := a.transport.deliverDevice(ctx, deviceCall, func(current context.Context, requestProof string) error {
				// This callback executes after OAuth, immediately before the one
				// physical POST. Re-read exact token, badge reset and visibility.
				latest, _, err := a.tokenMap(current, user)
				if err != nil {
					return err
				}
				if latest[device] != token {
					return ErrHTTPV1Input
				}
				badge := cache.OrdinaryBadgeSnapshot{Value: int(input.BadgeLedgerValue), ResetID: input.BadgeResetID, ReceiptSHA256: input.BadgeReceiptSHA256}
				if err := a.badge.Validate(current, user, call.BranchID, badge); err != nil {
					return err
				}
				return dispatch.BeforeProvider(current, call, user, requestProof)
			})
			if !result.DispatchStarted {
				if err := dispatch.NotDispatched(ctx, frozenHash([]byte("owned physical HTTP gate proved zero alert POSTs"))); err != nil {
					failures = append(failures, err)
				}
			} else {
				err := dispatch.Finish(ctx, messagedelivery.KafkaFCMDeviceReport{DispatchStarted: true, KnownUnregistered: result.KnownUnregistered, ProviderReceiptSHA256: result.ProviderReceiptSHA256, OutcomeSHA256: result.OutcomeSHA256})
				if err != nil {
					failures = append(failures, err)
				}
			}
			if providerErr != nil {
				failures = append(failures, ErrHTTPV1Unknown)
			}
		}
		if err := manifest.Settle(ctx); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (a *KafkaHTTPV1OfflineAdapter) PrepareOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	if err := a.CheckReady(ctx); err != nil {
		return err
	}
	if a.callbacks == nil {
		return nil
	}
	return a.callbacks.ExecuteOffline(ctx, call)
}
