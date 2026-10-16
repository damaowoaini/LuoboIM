package push

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/pkg/callbackstruct"
	"github.com/openimsdk/open-im-server/v3/pkg/common/config"
)

var ErrOwnedCallbackFile = errors.New("owned callback input invalid or changed")

type OwnedKafkaCallbackFile struct{ Path, ExpectedSHA256 string }

// The explicit production private-input loader: explicit source-owned file, never
// ADC, runtime YAML credential fallback, arbitrary URL, or caller document.
// Parent must be an owned private0700 directory; file must be owned0600,
// canonical/no-symlink/regular/single-link and exactly match the deployment pin.
// Fixed errors only: no file contents or underlying credential errors escape.
func readOwnedCallbackFile(input OwnedKafkaCallbackFile) ([]byte, error) {
	if !filepath.IsAbs(input.Path) || filepath.Clean(input.Path) != input.Path || !callbackDigest(input.ExpectedSHA256) {
		return nil, ErrOwnedCallbackFile
	}
	resolved, err := filepath.EvalSymlinks(input.Path)
	if err != nil || resolved != input.Path {
		return nil, ErrOwnedCallbackFile
	}
	parentPath := filepath.Dir(input.Path)
	parent, err := os.Lstat(parentPath)
	if err != nil || !privateOwnedCallback(parent, true) {
		return nil, ErrOwnedCallbackFile
	}
	before, err := os.Lstat(input.Path)
	if err != nil || !privateOwnedCallback(before, false) || before.Size() < 1 || before.Size() > 128<<10 {
		return nil, ErrOwnedCallbackFile
	}
	file, err := os.OpenFile(input.Path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrOwnedCallbackFile
	}
	opened, statErr := file.Stat()
	if statErr != nil || !privateOwnedCallback(opened, false) || !sameCallbackFile(before, opened) {
		file.Close()
		return nil, ErrOwnedCallbackFile
	}
	body, readErr := io.ReadAll(io.LimitReader(file, (128<<10)+1))
	after, statErr := file.Stat()
	closeErr := file.Close()
	current, pathErr := os.Lstat(input.Path)
	currentParent, parentErr := os.Lstat(parentPath)
	if readErr != nil || statErr != nil || closeErr != nil || pathErr != nil || parentErr != nil || !sameCallbackFile(opened, after) || !sameCallbackFile(opened, current) || !sameCallbackFile(parent, currentParent) || !privateOwnedCallback(current, false) || !privateOwnedCallback(currentParent, true) || len(body) != int(before.Size()) || digestGateway(body) != input.ExpectedSHA256 {
		for i := range body {
			body[i] = 0
		}
		return nil, ErrOwnedCallbackFile
	}
	return body, nil
}

func privateOwnedCallback(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0o700 && info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0o600 && info.Mode()&(os.ModeSetuid|os.ModeSetgid) == 0 && st.Nlink == 1
}
func sameCallbackFile(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() && left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}

func callbackExactKeys(raw []byte, keys ...string) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return true
}
func callbackDigest(value string) bool {
	b, err := hex.DecodeString(value)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == value
}

type ownedOfflineCallbackSpec struct {
	PolicySHA256   string `json:"policySHA256"`
	Base           string `json:"base"`
	Secret         string `json:"secret"`
	Enable         bool   `json:"enable"`
	Timeout        int    `json:"timeout"`
	FailedContinue *bool  `json:"failedContinue"`
}
type ownedOfflineCallbackRegistry struct {
	Version    string                     `json:"version"`
	Database   string                     `json:"database"`
	Epoch      string                     `json:"epoch"`
	Activation string                     `json:"activationSHA256"`
	Entries    []ownedOfflineCallbackSpec `json:"entries"`
}

// This file is dedicated operator material, not runtime YAML or caller input.
// Every enabled current/retained policy must have its exact endpoint/signing
// binding; no extra or duplicated entry can borrow another policy's receipt.
func LoadOwnedOfflineCallbacks(ctx context.Context, input OwnedKafkaCallbackFile, store *messagedelivery.Store, database, epoch, activation string, policies []messagedelivery.KafkaDeliveryPolicy) (map[string]*FrozenKafkaOfflineCallback, error) {
	required := map[string]messagedelivery.KafkaDeliveryPolicy{}
	for _, p := range policies {
		fingerprint, err := p.Fingerprint()
		if err != nil || p.Epoch != epoch || p.ActivationSHA256 != activation {
			return nil, ErrOwnedCallbackFile
		}
		if p.OfflinePushBinding != "" {
			if _, ok := required[fingerprint]; ok {
				return nil, ErrOwnedCallbackFile
			}
			required[fingerprint] = p
		}
	}
	if len(required) == 0 {
		if input.Path != "" || input.ExpectedSHA256 != "" {
			return nil, ErrOwnedCallbackFile
		}
		return map[string]*FrozenKafkaOfflineCallback{}, nil
	}
	if ctx == nil || ctx.Err() != nil || store == nil {
		return nil, ErrOwnedCallbackFile
	}
	raw, err := readOwnedCallbackFile(input)
	if err != nil {
		return nil, ErrOwnedCallbackFile
	}
	defer func() {
		for i := range raw {
			raw[i] = 0
		}
	}()
	if !offlineUniqueJSON(raw) || !callbackExactKeys(raw, "version", "database", "epoch", "activationSHA256", "entries") {
		return nil, ErrOwnedCallbackFile
	}
	var rawRoot map[string]json.RawMessage
	_ = json.Unmarshal(raw, &rawRoot)
	var rawEntries []json.RawMessage
	if json.Unmarshal(rawRoot["entries"], &rawEntries) != nil {
		return nil, ErrOwnedCallbackFile
	}
	for _, entry := range rawEntries {
		if !callbackExactKeys(entry, "policySHA256", "base", "secret", "enable", "timeout", "failedContinue") {
			return nil, ErrOwnedCallbackFile
		}
	}

	var registry ownedOfflineCallbackRegistry
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&registry) != nil || decoder.Decode(new(any)) != io.EOF || registry.Version != "ordinary-offline-callbacks-v1" || registry.Database != database || registry.Epoch != epoch || registry.Activation != activation || len(registry.Entries) != len(required) {
		return nil, ErrOwnedCallbackFile
	}
	result := map[string]*FrozenKafkaOfflineCallback{}
	ok := false
	defer func() {
		if !ok {
			for _, e := range result {
				_ = e.Close()
			}
		}
	}()
	for _, entry := range registry.Entries {
		policy, exists := required[entry.PolicySHA256]
		if !exists || result[entry.PolicySHA256] != nil || !entry.Enable {
			return nil, ErrOwnedCallbackFile
		}
		conf := config.CallBackConfig{Enable: entry.Enable, CallbackTimeOut: entry.Timeout, CallbackFailedContinue: entry.FailedContinue}
		endpoint, err := freezeKafkaCallbackEndpoint(database, epoch, "offline-push", callbackstruct.CallbackOfflinePushCommand, entry.Base, entry.Secret, policy.OfflinePushBinding, policy.OfflineFailureMode, conf)
		if err != nil {
			return nil, ErrOwnedCallbackFile
		}
		result[entry.PolicySHA256] = &FrozenKafkaOfflineCallback{store: store, fingerprint: entry.PolicySHA256, endpoint: endpoint}
	}
	ok = true
	return result, nil
}
