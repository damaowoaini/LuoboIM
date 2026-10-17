//go:build darwin || linux

package startrpc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

const OwnedRPCBootstrapEnv = "LUOBO_OWNED_SERVER_BOOTSTRAP_PATH"
const OwnedRPCBootstrapSHAEnv = "LUOBO_OWNED_SERVER_BOOTSTRAP_SHA256"
const ownedAnnouncementEnv = "LUOBO_MANAGED_OWNER_ANNOUNCEMENT_PATH"
const ownedNonceEnv = "LUOBO_MANAGED_OWNER_NONCE"

type OwnedRPCBootstrap struct {
	Version             string `json:"version"`
	Role                string `json:"role"`
	Mode                string `json:"mode"`
	InstanceID          string `json:"instanceID"`
	DatabaseSHA256      string `json:"databaseSHA256"`
	Epoch               string `json:"epoch"`
	ActivationSHA256    string `json:"activationSHA256"`
	ConfigurationSHA256 string `json:"configurationSHA256"`
	EnvironmentSHA256   string `json:"environmentSHA256"`
	DrainPath           string `json:"drainPath"`
}
type ownedRPCInput struct {
	boot                                                       OwnedRPCBootstrap
	rawSHA, nonce, path, sourcePath, configSHA, environmentSHA string
}

func OwnedRPCConfigured() bool {
	return os.Getenv(OwnedRPCBootstrapEnv) != "" || os.Getenv(OwnedRPCBootstrapSHAEnv) != "" || os.Getenv(ownedAnnouncementEnv) != "" || os.Getenv(ownedNonceEnv) != ""
}
func ownedSHA(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func ownedHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && value == strings.ToLower(value)
}

// Values remain in memory only. The private bootstrap and announcement contain
// hashes, never config, environment values, endpoints, passwords or tokens.
func OwnedRPCConfigurationHashes() (string, string, error) {
	raw, err := json.Marshal(config.Config)
	if err != nil {
		return "", "", errOwnedRPC
	}
	var env []string
	bytesUsed := 0
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || len(key) > 128 || len(value) > 16384 {
			return "", "", errOwnedRPC
		}
		if key == OwnedRPCBootstrapEnv || key == OwnedRPCBootstrapSHAEnv || key == ownedAnnouncementEnv || key == ownedNonceEnv || key == HistoryReaderPreparationEnv || key == HistoryReaderPreparationSHAEnv || key == ownedcontrol.NamespaceRuntimePreparationPathEnv || key == ownedcontrol.NamespaceRuntimePreparationSHAEnv {
			continue
		}
		bytesUsed += len(entry)
		if bytesUsed > 256*1024 || len(env) >= 512 {
			return "", "", errOwnedRPC
		}
		env = append(env, entry)
	}
	sort.Strings(env)
	encoded, err := json.Marshal(env)
	if err != nil {
		return "", "", errOwnedRPC
	}
	return ownedSHA(raw), ownedSHA(encoded), nil
}
func loadOwnedRPCInput(role string) (ownedRPCInput, error) {
	var input ownedRPCInput
	path, nonce, announcement := os.Getenv(OwnedRPCBootstrapEnv), os.Getenv(ownedNonceEnv), os.Getenv(ownedAnnouncementEnv)
	if path == "" || !ownedHash(os.Getenv(OwnedRPCBootstrapSHAEnv)) || !ownedHash(nonce) || announcement == "" || (ownedResourceNames(role) == nil && role != "api" && role != "crontask" && role != "msgtransfer-legacy") {
		return input, errOwnedRPC
	}
	raw, err := namespaceinventory.ReadPrivateDocument(path, 64*1024)
	if err != nil {
		return input, errOwnedRPC
	}
	if ownedSHA(raw) != os.Getenv(OwnedRPCBootstrapSHAEnv) {
		return input, errOwnedRPC
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input.boot) != nil {
		return input, errOwnedRPC
	}
	encoded, err := json.Marshal(input.boot)
	if err != nil || !bytes.Equal(append(encoded, '\n'), raw) {
		return input, errOwnedRPC
	}
	boot := input.boot
	instance, err := uuid.Parse(boot.InstanceID)
	if err != nil || instance == uuid.Nil || instance.String() != boot.InstanceID || (boot.Version != "owned-server-bootstrap-v1" && boot.Version != ownedcontrol.NamespaceRuntimeServerBootstrapVersion) || boot.Role != role || !ownedHash(boot.DatabaseSHA256) || !ownedHash(boot.ConfigurationSHA256) || !ownedHash(boot.EnvironmentSHA256) {
		return input, errOwnedRPC
	}
	if config.Config.Mongo.Database == "" || boot.DatabaseSHA256 != ownedSHA([]byte(config.Config.Mongo.Database)) {
		return input, errOwnedRPC
	}
	if boot.Version == ownedcontrol.NamespaceRuntimeServerBootstrapVersion {
		if boot.Mode != "required" || os.Getenv(ownedcontrol.NamespaceRuntimePreparationPathEnv) == "" || !ownedHash(os.Getenv(ownedcontrol.NamespaceRuntimePreparationSHAEnv)) {
			return input, errOwnedRPC
		}
	} else if boot.Mode == "required" {
		return input, errOwnedRPC
	}
	mode, epoch, activation := os.Getenv("LUOBO_GROUP_HISTORY_WINDOW_V1"), os.Getenv("LUOBO_GROUP_HISTORY_WINDOW_EPOCH"), os.Getenv("LUOBO_GROUP_HISTORY_WINDOW_ACTIVATION_SHA256")
	switch boot.Mode {
	case "legacy":
		if (mode != "" && mode != "0") || epoch != "" || activation != "" || boot.Epoch != "" || boot.ActivationSHA256 != "" {
			return input, errOwnedRPC
		}
	case "required":
		value, err := uuid.Parse(epoch)
		if mode != "required" || err != nil || value == uuid.Nil || value.String() != epoch || !ownedHash(activation) || boot.Epoch != epoch || boot.ActivationSHA256 != activation {
			return input, errOwnedRPC
		}
	default:
		return input, errOwnedRPC
	}
	for _, value := range []string{path, announcement, boot.DrainPath} {
		if !filepath.IsAbs(value) || filepath.Clean(value) != value {
			return input, errOwnedRPC
		}
	}
	if filepath.Dir(announcement) != filepath.Dir(boot.DrainPath) || path == announcement || path == boot.DrainPath || announcement == boot.DrainPath {
		return input, errOwnedRPC
	}
	configSHA, environmentSHA, err := OwnedRPCConfigurationHashes()
	if err != nil || configSHA != boot.ConfigurationSHA256 || environmentSHA != boot.EnvironmentSHA256 {
		return input, errOwnedRPC
	}
	input.rawSHA = ownedSHA(raw)
	input.nonce = nonce
	input.path = announcement
	input.sourcePath = path
	input.configSHA = configSHA
	input.environmentSHA = environmentSHA
	return input, nil
}
func (i ownedRPCInput) policySHA(runtimeConfigSHA string) (string, error) {
	if os.Getenv(OwnedRPCBootstrapEnv) != i.sourcePath || os.Getenv(OwnedRPCBootstrapSHAEnv) != i.rawSHA || os.Getenv(ownedNonceEnv) != i.nonce || os.Getenv(ownedAnnouncementEnv) != i.path {
		return "", errOwnedRPC
	}
	source, err := namespaceinventory.ReadPrivateDocument(i.sourcePath, 64*1024)
	if err != nil || ownedSHA(source) != i.rawSHA {
		return "", errOwnedRPC
	}
	_, environmentSHA, err := OwnedRPCConfigurationHashes()
	if err != nil || environmentSHA != i.environmentSHA {
		return "", errOwnedRPC
	}
	value := struct{ Role, Mode, Epoch, Activation, Configuration, Environment, RuntimeConfiguration string }{i.boot.Role, i.boot.Mode, i.boot.Epoch, i.boot.ActivationSHA256, i.configSHA, i.environmentSHA, runtimeConfigSHA}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", errors.Join(errOwnedRPC, err)
	}
	return ownedSHA(raw), nil
}
