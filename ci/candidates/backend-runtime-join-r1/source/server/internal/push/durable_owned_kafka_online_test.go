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

func ownedKafkaOnlineFixture(t *testing.T) (*messagedelivery.Store, ownedKafkaOnlineRegistry, []messagedelivery.KafkaDeliveryPolicy, OwnedKafkaCallbackFile) {
	t.Helper()
	client, err := mongo.NewClient(options.Client().ApplyURI("mongodb://127.0.0.1:37017/?replicaSet=luoboIntegration"))
	if err != nil {
		t.Fatal(err)
	}
	db, epoch, activation := "synthetic_owned_online", uuid.NewString(), digestGateway([]byte("synthetic activation"))
	store, err := messagedelivery.NewMembershipBoundStore(client.Database(db), epoch, activation)
	if err != nil {
		t.Fatal(err)
	}
	doc := ownedKafkaOnlineRegistry{Version: "ordinary-owned-online-callbacks-v1", Database: db, Epoch: epoch, Activation: activation}
	var policies []messagedelivery.KafkaDeliveryPolicy
	for _, label := range []string{"current", "retained"} {
		conf := config.CallBackConfig{Enable: true, CallbackTimeOut: 3}
		base, secret := "https://"+label+".synthetic.invalid", "synthetic-"+label+"-private-material"
		binding, err := kafkaCallbackBinding(db, epoch, "online-push", base, secret, conf)
		if err != nil {
			t.Fatal(err)
		}
		p := messagedelivery.KafkaDeliveryPolicy{Version: "ordinary-kafka-v1", Epoch: epoch, ActivationSHA256: activation, ConfigurationSHA256: digestGateway([]byte(label)), Discovery: "zookeeper", Vendor: "none", OnlinePushBinding: binding, OnlineFailureMode: "default-stop"}
		sha, err := p.Fingerprint()
		if err != nil {
			t.Fatal(err)
		}
		policies = append(policies, p)
		doc.Entries = append(doc.Entries, ownedKafkaOnlineSpec{PolicySHA256: sha, Base: base, Secret: secret, Online: conf})
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	file := OwnedKafkaCallbackFile{Path: filepath.Join(root, "online.json")}
	return store, doc, policies, writeOwnedOnlineFixture(t, file, doc)
}

func writeOwnedOnlineFixture(t *testing.T, file OwnedKafkaCallbackFile, doc ownedKafkaOnlineRegistry) OwnedKafkaCallbackFile {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file.Path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	file.ExpectedSHA256 = digestGateway(raw)
	return file
}

func TestOwnedKafkaOnlineRegistryBindsCurrentAndRetainedWithoutDispatchAndClearsSecrets(t *testing.T) {
	store, doc, policies, file := ownedKafkaOnlineFixture(t)
	engines, err := LoadOwnedKafkaOnlineCallbacks(context.Background(), file, store, doc.Database, doc.Epoch, doc.Activation, policies)
	if err != nil || len(engines) != 2 {
		t.Fatal("current/retained callback assembly rejected", err)
	}
	var previous *FrozenKafkaPushCallbacks
	for _, entry := range doc.Entries {
		e := engines[entry.PolicySHA256]
		if e == nil || e == previous || e.policySHA != entry.PolicySHA256 || len(e.endpoints) != 1 {
			t.Fatal("retained callback borrowed current binding")
		}
		secret := e.endpoints["online-push"].secret
		if string(secret) != entry.Secret {
			t.Fatal("wrong exact private signing material")
		}
		if err := e.CloseOwned(); err != nil {
			t.Fatal(err)
		}
		for _, b := range secret {
			if b != 0 {
				t.Fatal("closed owned callback retained signing bytes")
			}
		}
		if len(e.endpoints) != 0 {
			t.Fatal("closed callback kept active endpoints")
		}
		previous = e
	}
}

func TestOwnedKafkaOnlineRegistryRejectsForeignMissingDuplicateAndPublicMaterial(t *testing.T) {
	for _, fault := range []string{"foreign-database", "missing-retained", "duplicate-entry", "changed-binding", "wrong-pin", "public-file", "duplicate-policy"} {
		t.Run(fault, func(t *testing.T) {
			store, doc, policies, file := ownedKafkaOnlineFixture(t)
			database := doc.Database
			switch fault {
			case "foreign-database":
				database += "_foreign"
			case "missing-retained":
				doc.Entries = doc.Entries[:1]
				file = writeOwnedOnlineFixture(t, file, doc)
			case "duplicate-entry":
				doc.Entries[1] = doc.Entries[0]
				file = writeOwnedOnlineFixture(t, file, doc)
			case "changed-binding":
				doc.Entries[1].Secret = "other-material"
				file = writeOwnedOnlineFixture(t, file, doc)
			case "wrong-pin":
				file.ExpectedSHA256 = digestGateway([]byte("different"))
			case "public-file":
				if err := os.Chmod(file.Path, 0644); err != nil {
					t.Fatal(err)
				}
			case "duplicate-policy":
				policies = append(policies, policies[0])
			}
			if _, err := LoadOwnedKafkaOnlineCallbacks(context.Background(), file, store, database, doc.Epoch, doc.Activation, policies); err == nil {
				t.Fatal("unbound private policy accepted")
			}
		})
	}
}
