package push

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func offlineOwnedFixture(t *testing.T) (*messagedelivery.Store, string, string, string, messagedelivery.KafkaDeliveryPolicy, OwnedKafkaCallbackFile) {
	t.Helper()
	epoch, activation := uuid.NewString(), digestGateway([]byte("synthetic activation"))
	database := "synthetic_callback_owned"
	// NewClient without Connect does not start discovery or contact a database.
	client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := messagedelivery.NewMembershipBoundStore(client.Database(database), epoch, activation)
	if err != nil {
		t.Fatal(err)
	}
	conf := config.CallBackConfig{Enable: true, CallbackTimeOut: 3}
	binding, err := kafkaCallbackBinding(database, epoch, "offline-push", "https://synthetic.invalid", "synthetic-private-material", conf)
	if err != nil {
		t.Fatal(err)
	}
	p := messagedelivery.KafkaDeliveryPolicy{Version: "ordinary-kafka-v1", Epoch: epoch, ActivationSHA256: activation, ConfigurationSHA256: digestGateway([]byte("synthetic config")), Discovery: "zookeeper", Vendor: "none", OfflinePushBinding: binding, OfflineFailureMode: "default-stop"}
	fingerprint, err := p.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	doc := ownedOfflineCallbackRegistry{Version: "ordinary-offline-callbacks-v1", Database: database, Epoch: epoch, Activation: activation, Entries: []ownedOfflineCallbackSpec{{PolicySHA256: fingerprint, Base: "https://synthetic.invalid", Secret: "synthetic-private-material", Enable: true, Timeout: 3}}}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "callbacks.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return store, database, epoch, activation, p, OwnedKafkaCallbackFile{Path: path, ExpectedSHA256: digestGateway(body)}
}
func TestOwnedOfflineCallbackLoaderBindsActualCurrentAndRetainedMaterialWithoutNetwork(t *testing.T) {
	store, db, epoch, activation, p, file := offlineOwnedFixture(t)
	body, err := os.ReadFile(file.Path)
	if err != nil {
		t.Fatal(err)
	}
	var doc ownedOfflineCallbackRegistry
	if json.Unmarshal(body, &doc) != nil {
		t.Fatal("fixture registry")
	}
	retained := p
	retained.ConfigurationSHA256 = digestGateway([]byte("synthetic retained configuration"))
	continueAfterFailure := false
	oldConfig := config.CallBackConfig{Enable: true, CallbackTimeOut: 2, CallbackFailedContinue: &continueAfterFailure}
	retained.OfflinePushBinding, err = kafkaCallbackBinding(db, epoch, "offline-push", "https://retained.synthetic.invalid", "synthetic-retained-private-material", oldConfig)
	if err != nil {
		t.Fatal(err)
	}
	retained.OfflineFailureMode = "explicit-stop"
	oldFingerprint, err := retained.Fingerprint()
	if err != nil {
		t.Fatal(err)
	}
	doc.Entries = append(doc.Entries, ownedOfflineCallbackSpec{PolicySHA256: oldFingerprint, Base: "https://retained.synthetic.invalid", Secret: "synthetic-retained-private-material", Enable: true, Timeout: 2, FailedContinue: &continueAfterFailure})
	body, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file.Path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	file.ExpectedSHA256 = digestGateway(body)
	engines, err := LoadOwnedOfflineCallbacks(context.Background(), file, store, db, epoch, activation, []messagedelivery.KafkaDeliveryPolicy{p, retained})
	if err != nil || len(engines) != 2 {
		t.Fatal("current and distinct retained material rejected", err)
	}
	currentFingerprint, _ := p.Fingerprint()
	if engines[currentFingerprint] == nil || engines[oldFingerprint] == nil {
		t.Fatal("missing exact current or retained engine")
	}
	if engines[currentFingerprint] == engines[oldFingerprint] || engines[currentFingerprint].endpoint.binding == engines[oldFingerprint].endpoint.binding {
		t.Fatal("retained material borrowed current endpoint")
	}
	for _, fingerprint := range []string{currentFingerprint, oldFingerprint} {
		e := engines[fingerprint]
		if e == nil || e.PolicyFingerprint() != fingerprint {
			t.Fatal("owned policy retargeted")
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		for _, b := range e.endpoint.secret {
			if b != 0 {
				t.Fatal("closed engine retained signing bytes")
			}
		}
	}
}
func TestOwnedOfflineCallbackMissingForeignChangedOrPublicFileRejects(t *testing.T) {
	store, db, epoch, activation, p, file := offlineOwnedFixture(t)
	for _, candidate := range []struct {
		name     string
		file     OwnedKafkaCallbackFile
		database string
		policies []messagedelivery.KafkaDeliveryPolicy
	}{
		{"missing", OwnedKafkaCallbackFile{}, db, []messagedelivery.KafkaDeliveryPolicy{p}},
		{"foreign-db", file, db + "_foreign", []messagedelivery.KafkaDeliveryPolicy{p}},
		{"missing-retained", file, db, []messagedelivery.KafkaDeliveryPolicy{p, p}},
		{"wrong-pin", OwnedKafkaCallbackFile{Path: file.Path, ExpectedSHA256: digestGateway([]byte("foreign"))}, db, []messagedelivery.KafkaDeliveryPolicy{p}},
	} {
		t.Run(candidate.name, func(t *testing.T) {
			if _, err := LoadOwnedOfflineCallbacks(context.Background(), candidate.file, store, candidate.database, epoch, activation, candidate.policies); err == nil {
				t.Fatal("foreign/missing input accepted")
			}
		})
	}
	if err := os.Chmod(file.Path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOwnedOfflineCallbacks(context.Background(), file, store, db, epoch, activation, []messagedelivery.KafkaDeliveryPolicy{p}); err == nil {
		t.Fatal("public private-input file accepted")
	}
}
