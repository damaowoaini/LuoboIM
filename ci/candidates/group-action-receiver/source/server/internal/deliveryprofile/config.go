package deliveryprofile

import "github.com/openimsdk/open-im-server/v3/pkg/common/config"

// FreezeGroup is a startup-only constructor. vendorBinding is the verified
// owned credential transport binding, not a raw secret/config substitute.
// It performs no I/O; adapters still must independently prove those inputs.
func FreezeGroup(database, epoch, activation, vendorBinding string) (Group, error) {
	if database == "" {
		return Group{}, ErrInvalid
	}
	p := Group{Version: "owned-group-effect-v1", RiskAction: "group-message", Transport: Transport{Version: "group-effect-v1", Epoch: epoch, ActivationSHA256: activation, Discovery: config.Config.Envs.Discovery, EncryptionRequired: config.Config.LongConnSvr.IsEncryption}}
	bind := func(name string, c config.CallBackConfig) (Callback, error) {
		if !c.Enable {
			return Callback{}, nil
		}
		if config.Config.Callback.CallbackUrl == "" || config.Config.Secret == "" {
			return Callback{}, ErrInvalid
		}
		mode := "default-stop"
		if c.CallbackFailedContinue != nil {
			mode = "explicit-stop"
			if *c.CallbackFailedContinue {
				mode = "continue"
			}
		}
		return Callback{Binding: Digest([]any{database, epoch, name, config.Config.Callback.CallbackUrl, c, config.Config.Secret}), Mode: mode, TimeoutSeconds: c.CallbackTimeOut}, nil
	}
	var err error
	if p.BeforeMessage, err = bind("before-group-message", config.Config.Callback.CallbackBeforeSendGroupMsg); err != nil {
		return Group{}, err
	}
	if p.ModifyMessage, err = bind("modify-message", config.Config.Callback.CallbackMsgModify); err != nil {
		return Group{}, err
	}
	if p.AfterMessage, err = bind("after-group-message", config.Config.Callback.CallbackAfterSendGroupMsg); err != nil {
		return Group{}, err
	}
	for _, entry := range []struct {
		name          string
		conf          config.CallBackConfig
		binding, mode *string
	}{
		{"supergroup-online-push", config.Config.Callback.CallbackBeforeSuperGroupOnlinePush, &p.Transport.BeforeGroupPushBinding, &p.Transport.BeforeGroupFailureMode},
		{"online-push", config.Config.Callback.CallbackOnlinePush, &p.Transport.OnlinePushBinding, &p.Transport.OnlineFailureMode},
		{"offline-push", config.Config.Callback.CallbackOfflinePush, &p.Transport.OfflinePushBinding, &p.Transport.OfflineFailureMode},
	} {
		c, err := bind(entry.name, entry.conf)
		if err != nil {
			return Group{}, err
		}
		*entry.binding = c.Binding
		*entry.mode = c.Mode
	}
	switch config.Config.Push.Enable {
	case "getui", "fcm", "jpush":
		p.Transport.Vendor = config.Config.Push.Enable
		p.Transport.VendorBinding = vendorBinding
	case "", "none":
		p.Transport.Vendor = "none"
		if vendorBinding != "" {
			return Group{}, ErrInvalid
		}
	default:
		return Group{}, ErrInvalid
	}
	p.Transport.ConfigurationSHA256 = Digest([]any{database, epoch, activation, p.Transport.Discovery, p.Transport.EncryptionRequired, p.Transport.BeforeGroupPushBinding, p.Transport.OnlinePushBinding, p.Transport.OfflinePushBinding, p.Transport.Vendor, p.Transport.VendorBinding})
	if _, err := p.Fingerprint(); err != nil {
		return Group{}, err
	}
	return p, nil
}
