package msgtransfer

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/push"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/aliasvendor"
	"github.com/openimsdk/open-im-server/v3/internal/push/offlinepush/fcm"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"github.com/openimsdk/open-im-server/v3/pkg/common/db/cache"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Actual public runtime factory, actual private loaders and vendor constructors.
// NewClient is intentionally unconnected; Start/CheckReady are not invoked.
// Construction/Stop must not contact Mongo, Redis, OAuth or a vendor endpoint.
func TestOwnedOfflineOptionalCallbackFactoryNeverBoxesNilAcrossCurrentAndRetainedVendors(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	account := map[string]any{"type": "service_account", "project_id": "synthetic-callback-fixture", "client_email": "synthetic@synthetic-callback-fixture.iam.gserviceaccount.com", "private_key_id": "synthetic-key", "private_key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), "token_uri": "https://oauth2.googleapis.com/token"}
	for _, enabled := range []bool{false, true} {
		name := "disabled-current-enabled-retained"
		if enabled {
			name = "enabled-current-disabled-retained"
		}
		t.Run(name, func(t *testing.T) {
			oldCallback, oldSecret, oldDiscovery, oldPush, oldEncryption := config.Config.Callback, config.Config.Secret, config.Config.Envs.Discovery, config.Config.Push.Enable, config.Config.LongConnSvr.IsEncryption
			t.Cleanup(func() {
				config.Config.Callback = oldCallback
				config.Config.Secret = oldSecret
				config.Config.Envs.Discovery = oldDiscovery
				config.Config.Push.Enable = oldPush
				config.Config.LongConnSvr.IsEncryption = oldEncryption
			})
			config.Config.Callback.CallbackBeforeSuperGroupOnlinePush = config.CallBackConfig{}
			config.Config.Callback.CallbackOnlinePush = config.CallBackConfig{}
			config.Config.Callback.CallbackOfflinePush = config.CallBackConfig{Enable: enabled, CallbackTimeOut: 3}
			config.Config.Callback.CallbackUrl = "https://current.synthetic.invalid"
			config.Config.Secret = "synthetic-current-signing"
			config.Config.Envs.Discovery = "zookeeper"
			config.Config.Push.Enable = "none"
			config.Config.LongConnSvr.IsEncryption = false
			root := t.TempDir()
			if err := os.Chmod(root, 0o700); err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			epoch, activation := uuid.NewString(), kafkaHashForFixture("synthetic construction activation")
			client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://127.0.0.1:38417/?replicaSet=luoboIntegration"))
			if err != nil {
				t.Fatal(err)
			}
			db := client.Database("synthetic_optional_callback")
			current, err := FreezeOwnedKafkaPolicy(db.Name(), epoch, activation)
			if err != nil {
				t.Fatal(err)
			}
			currentSHA, err := current.Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			entries := []aliasvendor.OwnedOfflinePolicySpec{{Policy: current, Fingerprint: currentSHA, BindingSeedSHA256: current.ConfigurationSHA256}}
			var callbacks []any
			callbackSpec := func(p messagedelivery.KafkaDeliveryPolicy, base, secret string) {
				fingerprint, e := p.Fingerprint()
				if e != nil {
					t.Fatal(e)
				}
				callbacks = append(callbacks, map[string]any{"policySHA256": fingerprint, "base": base, "secret": secret, "enable": true, "timeout": 3, "failedContinue": nil})
			}
			if enabled {
				callbackSpec(current, config.Config.Callback.CallbackUrl, config.Config.Secret)
			}
			retained := current
			retained.ConfigurationSHA256 = kafkaHashForFixture("synthetic retained none")
			retained.OfflinePushBinding = ""
			retained.OfflineFailureMode = ""
			if !enabled {
				retained.OfflinePushBinding, err = kafkaOwnedDigest([]any{db.Name(), epoch, "offline-push", "https://retained.synthetic.invalid", config.CallBackConfig{Enable: true, CallbackTimeOut: 3}, "synthetic-retained-signing"})
				if err != nil {
					t.Fatal(err)
				}
				retained.OfflineFailureMode = "default-stop"
				callbackSpec(retained, "https://retained.synthetic.invalid", "synthetic-retained-signing")
			}
			retainedSHA, err := retained.Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			entries = append(entries, aliasvendor.OwnedOfflinePolicySpec{Policy: retained, Fingerprint: retainedSHA, BindingSeedSHA256: retained.ConfigurationSHA256})
			aliasSeed := kafkaHashForFixture("synthetic alias seed")
			aliasPath, aliasSHA := offlineWorkerPrivateDoc(t, root, "alias.json", map[string]any{"version": "ordinary-alias-provider-input-v1", "vendor": "getui", "endpoint": "https://getui.synthetic.invalid/appid", "appKey": "synthetic-key", "masterSecret": "synthetic-secret", "channelID": "", "channelName": "", "pushIntent": "", "production": false})
			at, err := aliasvendor.NewOwnedTransportFromFile(ctx, aliasvendor.OwnedInputFile{Path: aliasPath, ExpectedSHA256: aliasSHA}, aliasSeed)
			if err != nil {
				t.Fatal(err)
			}
			aliasBinding := at.Binding()
			if err := at.Close(); err != nil {
				t.Fatal(err)
			}
			ap := current
			ap.Vendor = "getui"
			ap.VendorBinding = aliasBinding
			ap.ConfigurationSHA256 = kafkaHashForFixture("synthetic retained alias config")
			ap.OfflinePushBinding = ""
			ap.OfflineFailureMode = ""
			afp, err := ap.Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			entries = append(entries, aliasvendor.OwnedOfflinePolicySpec{Policy: ap, Fingerprint: afp, BindingSeedSHA256: aliasSeed, CredentialPath: aliasPath, CredentialSHA256: aliasSHA})
			fcmPath, fcmSHA := offlineWorkerPrivateDoc(t, root, "fcm.json", account)
			fcmSeed := kafkaHashForFixture("synthetic fcm seed")
			ft, err := fcm.NewOwnedHTTPV1TransportFromFile(ctx, fcm.OwnedCredentialFile{Path: fcmPath, ExpectedSHA256: fcmSHA}, fcmSeed)
			if err != nil {
				t.Fatal(err)
			}
			fcmBinding := ft.Binding()
			if err := ft.Close(); err != nil {
				t.Fatal(err)
			}
			fp := current
			fp.Vendor = "fcm"
			fp.VendorBinding = fcmBinding
			fp.ConfigurationSHA256 = kafkaHashForFixture("synthetic retained fcm config")
			fp.OfflinePushBinding = ""
			fp.OfflineFailureMode = ""
			ffp, err := fp.Fingerprint()
			if err != nil {
				t.Fatal(err)
			}
			entries = append(entries, aliasvendor.OwnedOfflinePolicySpec{Policy: fp, Fingerprint: ffp, BindingSeedSHA256: fcmSeed, CredentialPath: fcmPath, CredentialSHA256: fcmSHA})
			registryPath, registrySHA := offlineWorkerPrivateDoc(t, root, "registry.json", aliasvendor.OwnedOfflinePolicyRegistry{Version: "ordinary-owned-offline-policies-v1", Database: db.Name(), Epoch: epoch, ActivationSHA256: activation, CurrentPolicySHA256: currentSHA, Entries: entries})
			callbackPath, callbackSHA := offlineWorkerPrivateDoc(t, root, "callbacks.json", map[string]any{"version": "ordinary-offline-callbacks-v1", "database": db.Name(), "epoch": epoch, "activationSHA256": activation, "entries": callbacks})
			rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:38418", DB: 15, MaxRetries: -1})
			t.Cleanup(func() {
				if err := rdb.Close(); err != nil {
					t.Error(err)
				}
			})
			runtime, err := NewOwnedOrdinaryOfflineRuntime(ctx, db, rdb, cache.NewMsgCacheModel(rdb), offlineWorkerReader{}, epoch, activation, kafkaHashForFixture("synthetic badge evidence"), aliasvendor.OwnedInputFile{Path: registryPath, ExpectedSHA256: registrySHA}, push.OwnedKafkaCallbackFile{Path: callbackPath, ExpectedSHA256: callbackSHA})
			if err != nil {
				t.Fatal("actual runtime rejected disabled/retained vendor policy", err)
			}
			if len(runtime.worker.transports) != 4 {
				t.Fatal("actual factory lost a retained adapter")
			}
			for _, entry := range entries {
				if entry.Policy.Vendor == "none" {
					adapter, ok := runtime.worker.transports[entry.Fingerprint].(*ownedNoVendorOffline)
					if !ok || (adapter.callbacks != nil) != (entry.Policy.OfflinePushBinding != "") {
						t.Fatal("disabled callback boxed as non-nil or enabled binding lost")
					}
				}
			}
			end, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if err := runtime.Stop(end); err != nil {
				t.Fatal("owned constructor cleanup did not close actual pools", err)
			}
		})
	}
}
