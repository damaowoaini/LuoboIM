package aliasvendor

import (
	"context"
	"errors"
	"strings"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush"
)

type KafkaAliasOfflineAdapter struct {
	callbacks   messagedelivery.KafkaOfflineCallbackEngine
	store       *messagedelivery.Store
	policy      messagedelivery.KafkaDeliveryPolicy
	fingerprint string
	transport   *Transport
}

func NewKafkaAliasOfflineAdapter(store *messagedelivery.Store, policy messagedelivery.KafkaDeliveryPolicy, transport *Transport, callbacks ...messagedelivery.KafkaOfflineCallbackEngine) (*KafkaAliasOfflineAdapter, error) {
	fingerprint, err := policy.Fingerprint()
	if err != nil || store == nil || transport == nil || policy.Vendor != transport.Vendor() || policy.VendorBinding != transport.Binding() {
		return nil, ErrInput
	}
	var callback messagedelivery.KafkaOfflineCallbackEngine
	if len(callbacks) > 1 {
		return nil, ErrInput
	}
	if len(callbacks) == 1 {
		callback = callbacks[0]
	}
	if callback != nil && (callback.PolicyFingerprint() != fingerprint || policy.OfflinePushBinding == "") {
		return nil, ErrInput
	}
	return &KafkaAliasOfflineAdapter{callbacks: callback, store: store, policy: policy, fingerprint: fingerprint, transport: transport}, nil
}
func (a *KafkaAliasOfflineAdapter) PolicyFingerprint() string {
	if a == nil {
		return ""
	}
	return a.fingerprint
}
func (a *KafkaAliasOfflineAdapter) CheckReady(ctx context.Context) error {
	if a == nil || ctx == nil || a.transport == nil || a.store == nil {
		return ErrInput
	}
	if err := a.transport.CheckReady(ctx); err != nil {
		return err
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
	return nil
}
func (a *KafkaAliasOfflineAdapter) Close() error {
	if a == nil || a.transport == nil {
		return ErrInput
	}
	if a.callbacks != nil {
		return errors.Join(a.transport.Close(), a.callbacks.Close())
	}
	return a.transport.Close()
}
func (a *KafkaAliasOfflineAdapter) DeliverOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	if a == nil || ctx == nil || call.Message == nil || call.PolicySHA256 != a.fingerprint {
		return ErrInput
	}
	if err := a.CheckReady(ctx); err != nil {
		return err
	}
	effective, err := a.store.KafkaOfflineProviderMessage(ctx, call)
	if err != nil {
		return err
	}
	source := effective.SendID
	if call.Message.SessionType == constant.SuperGroupChatType || call.Message.SessionType == constant.GroupChatType {
		source = call.Message.GroupID
	}
	title, content, opts, err := offlinepush.MessageInfo(source, effective)
	if err != nil {
		return ErrInput
	}
	var failures []error
	for _, user := range call.DispatchRecipients {
		// Source/receipt before auth, request construction and any supplier action.
		state, err := a.store.ResolveKafkaAliasVendor(ctx, call, user)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		switch state {
		case "alias-provider-accepted", "alias-provider-ignored", "suppressed", "operator-rejected":
			continue
		case "pending", "not-dispatched", "pending-confirmed":
		default:
			failures = append(failures, messagedelivery.ErrKafkaOfflineUnconfirmed)
			continue
		}
		requestID, err := uuid.NewRandom()
		if err != nil {
			failures = append(failures, messagedelivery.ErrUnprepared)
			continue
		}
		prepared, err := a.transport.Prepare(AliasMessage{Alias: user, RequestID: strings.ReplaceAll(requestID.String(), "-", ""), Title: title, Content: content, ClientMsgID: opts.Signal.ClientMsgID})
		if err != nil {
			failures = append(failures, ErrInput)
			continue
		}
		input := messagedelivery.KafkaAliasRequest{Vendor: a.transport.Vendor(), Binding: a.transport.Binding(), Protocol: a.transport.Protocol(), RequestID: prepared.RequestID(), BodySHA256: prepared.BodySHA256()}
		dispatch, err := a.store.BeginKafkaAliasVendor(ctx, call, user, input)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if dispatch == nil {
			continue
		}
		outcome, sendErr := a.transport.Send(ctx, prepared, func() error { return dispatch.BeforeProvider(ctx, call, user, prepared.BodySHA256()) })
		if !outcome.PhysicalAttemptStarted {
			if err := dispatch.NotDispatched(ctx); err != nil {
				failures = append(failures, err)
			}
			failures = append(failures, ErrUnknown)
			continue
		}
		finishErr := dispatch.Finish(ctx, messagedelivery.KafkaAliasReport{PhysicalAttemptStarted: true, ProviderAccepted: outcome.ProviderAccepted, ProviderIgnored: outcome.ProviderIgnored, ProviderReceiptSHA256: outcome.ReceiptSHA256, OutcomeSHA256: outcome.OutcomeSHA256})
		if sendErr != nil || finishErr != nil {
			failures = append(failures, messagedelivery.ErrKafkaOfflineUnconfirmed)
		}
	}
	return errors.Join(failures...)
}

func (a *KafkaAliasOfflineAdapter) PrepareOffline(ctx context.Context, call messagedelivery.KafkaOfflineCall) error {
	if err := a.CheckReady(ctx); err != nil {
		return err
	}
	if a.callbacks == nil {
		return nil
	}
	return a.callbacks.ExecuteOffline(ctx, call)
}
