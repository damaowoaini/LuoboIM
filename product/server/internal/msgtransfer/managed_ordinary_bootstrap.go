package msgtransfer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

const OwnedOrdinaryBootstrapPathEnv = "LUOBO_ORDINARY_OWNED_RUNTIME_INPUT"
const OwnedOrdinaryBootstrapSHAEnv = "LUOBO_ORDINARY_OWNED_RUNTIME_INPUT_SHA256"

// Deployment-owned input, not an HTTP/RPC capability or a Ready flag. The
// namespace runtime gate still verifies the complete writer/reader cutover.
type OwnedOrdinaryBootstrap struct {
	Version                string `json:"version"`
	Database               string `json:"database"`
	Epoch                  string `json:"epoch"`
	ActivationSHA256       string `json:"activationSHA256"`
	BadgeEvidenceSHA256    string `json:"badgeEvidenceSHA256"`
	Topic                  string `json:"topic"`
	ConsumerGroup          string `json:"consumerGroup"`
	PolicySHA256           string `json:"policySHA256"`
	VendorPath             string `json:"vendorPath"`
	VendorSHA256           string `json:"vendorSHA256"`
	OnlineCallbacksPath    string `json:"onlineCallbacksPath"`
	OnlineCallbacksSHA256  string `json:"onlineCallbacksSHA256"`
	OfflineCallbacksPath   string `json:"offlineCallbacksPath"`
	OfflineCallbacksSHA256 string `json:"offlineCallbacksSHA256"`
	DrainPath              string `json:"drainPath"`
	inputSHA               string
}

func ordinaryCanonicalPath(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path
}

func ordinaryKafkaName(value string) bool {
	return value != "" && len(value) <= 249 && strings.TrimSpace(value) == value && !strings.ContainsFunc(value, unicode.IsControl)
}

func (b OwnedOrdinaryBootstrap) valid(database string) bool {
	epoch, err := uuid.Parse(b.Epoch)
	if err != nil || epoch == uuid.Nil || epoch.String() != b.Epoch || (b.Version != "owned-ordinary-runtime-v1" && b.Version != ownedcontrol.NamespaceRuntimeManagedBootstrapVersion) || database == "" || b.Database != database || !groupStartupSHA(b.ActivationSHA256) || !groupStartupSHA(b.BadgeEvidenceSHA256) || !groupStartupSHA(b.PolicySHA256) || !ordinaryKafkaName(b.Topic) || !ordinaryKafkaName(b.ConsumerGroup) || !ordinaryCanonicalPath(b.VendorPath) || !groupStartupSHA(b.VendorSHA256) || !ordinaryCanonicalPath(b.DrainPath) {
		return false
	}
	for _, pair := range []struct{ path, sha string }{{b.OnlineCallbacksPath, b.OnlineCallbacksSHA256}, {b.OfflineCallbacksPath, b.OfflineCallbacksSHA256}} {
		if pair.path == "" {
			if pair.sha != "" {
				return false
			}
		} else if !ordinaryCanonicalPath(pair.path) || !groupStartupSHA(pair.sha) {
			return false
		}
	}
	return true
}

func LoadOwnedOrdinaryBootstrap(database string) (OwnedOrdinaryBootstrap, error) {
	var b OwnedOrdinaryBootstrap
	path, pin := os.Getenv(OwnedOrdinaryBootstrapPathEnv), os.Getenv(OwnedOrdinaryBootstrapSHAEnv)
	if !ordinaryCanonicalPath(path) || !groupStartupSHA(pin) {
		return b, messagedelivery.ErrUnprepared
	}
	// Existing exact-size/no-link/private0700/0600/stable-file reader. Never
	// import an UNTRUSTED inventory as a claim or as a runtime admission grant.
	raw, err := namespaceinventory.ReadPrivateDocument(path, 16<<10)
	if err != nil || deliveryprofile.SHA(raw) != pin || namespaceinventory.DecodeStrict(raw, &b) != nil || !b.valid(database) {
		return OwnedOrdinaryBootstrap{}, messagedelivery.ErrUnprepared
	}
	var keys map[string]json.RawMessage
	if json.Unmarshal(raw, &keys) != nil || len(keys) != 15 {
		return OwnedOrdinaryBootstrap{}, messagedelivery.ErrUnprepared
	}
	if os.Getenv(messagedelivery.HistoryWindowModeEnv) != "required" || os.Getenv(messagedelivery.HistoryWindowEpochEnv) != b.Epoch || os.Getenv(messagedelivery.HistoryWindowActivationEnv) != b.ActivationSHA256 {
		return OwnedOrdinaryBootstrap{}, messagedelivery.ErrUnprepared
	}
	// A controlled activation cannot launch an old binary which ignores the
	// new barrier: the new private protocol is mandatory when supervised.
	supervised := os.Getenv(ManagedOwnerAnnouncementPathEnv) != "" || os.Getenv(ManagedOwnerNonceEnv) != ""
	if b.Version == ownedcontrol.NamespaceRuntimeManagedBootstrapVersion {
		if !supervised || os.Getenv(ownedcontrol.NamespaceRuntimePreparationPathEnv) == "" || !groupStartupSHA(os.Getenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv)) {
			return OwnedOrdinaryBootstrap{}, messagedelivery.ErrUnprepared
		}
	} else if supervised || os.Getenv(ownedcontrol.NamespaceRuntimePreparationPathEnv) != "" || os.Getenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv) != "" {
		return OwnedOrdinaryBootstrap{}, messagedelivery.ErrUnprepared
	}
	if err := namespaceinventory.CheckFreshPrivatePath(b.DrainPath); err != nil {
		return OwnedOrdinaryBootstrap{}, messagedelivery.ErrUnprepared
	}
	b.inputSHA = pin
	return b, nil
}
