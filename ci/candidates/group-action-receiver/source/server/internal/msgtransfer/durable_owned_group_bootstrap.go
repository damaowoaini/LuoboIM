package msgtransfer

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/google/uuid"
	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

const OwnedGroupBootstrapPathEnv = "LUOBO_GROUP_OWNED_RECEIVER_INPUT"
const OwnedGroupBootstrapSHAEnv = "LUOBO_GROUP_OWNED_RECEIVER_INPUT_SHA256"

type OwnedGroupReceiverBootstrap struct {
	Version                string `json:"version"`
	Database               string `json:"database"`
	Epoch                  string `json:"epoch"`
	ActivationSHA256       string `json:"activationSHA256"`
	BadgeEvidenceSHA256    string `json:"badgeEvidenceSHA256"`
	VendorPath             string `json:"vendorPath"`
	VendorSHA256           string `json:"vendorSHA256"`
	MessageCallbacksPath   string `json:"messageCallbacksPath"`
	MessageCallbacksSHA256 string `json:"messageCallbacksSHA256"`
	OfflineCallbacksPath   string `json:"offlineCallbacksPath"`
	RoleCallbacksPath      string `json:"roleCallbacksPath"`
	RoleCallbacksSHA256    string `json:"roleCallbacksSHA256"`
	OfflineCallbacksSHA256 string `json:"offlineCallbacksSHA256"`
}

func groupStartupSHA(v string) bool {
	if len(v) != 64 {
		return false
	}
	for _, c := range v {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Required process-owned manifest. No default file, account credential or HTTP
// parameter is consulted. This loader does no network, DDL or cut capture.
func LoadOwnedGroupReceiverBootstrap(database string) (OwnedGroupReceiverBootstrap, error) {
	var value OwnedGroupReceiverBootstrap
	path, pin := os.Getenv(OwnedGroupBootstrapPathEnv), os.Getenv(OwnedGroupBootstrapSHAEnv)
	if database == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || !groupStartupSHA(pin) {
		return value, messagedelivery.ErrUnprepared
	}
	canonical, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || canonical != filepath.Dir(path) {
		return value, messagedelivery.ErrUnprepared
	}
	parent, err := os.Lstat(canonical)
	if err != nil || !parent.IsDir() || parent.Mode().Perm() != 0700 {
		return value, messagedelivery.ErrUnprepared
	}
	parentOwner, ok := parent.Sys().(*syscall.Stat_t)
	if !ok || parentOwner.Uid != uint32(os.Geteuid()) {
		return value, messagedelivery.ErrUnprepared
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() < 1 || info.Size() > 16384 {
		return value, messagedelivery.ErrUnprepared
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || owner.Nlink != 1 {
		return value, messagedelivery.ErrUnprepared
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return value, messagedelivery.ErrUnprepared
	}
	opened, statErr := file.Stat()
	raw, readErr := io.ReadAll(io.LimitReader(file, 16385))
	after, endErr := file.Stat()
	closeErr := file.Close()
	current, pathErr := os.Lstat(path)
	currentParent, parentErr := os.Lstat(canonical)
	if statErr != nil || readErr != nil || endErr != nil || closeErr != nil || pathErr != nil || parentErr != nil || !os.SameFile(info, opened) || !os.SameFile(info, after) || !os.SameFile(info, current) || !os.SameFile(parent, currentParent) || info.Mode() != after.Mode() || info.Mode() != current.Mode() || !info.ModTime().Equal(after.ModTime()) || !info.ModTime().Equal(current.ModTime()) || int64(len(raw)) != info.Size() || deliveryprofile.SHA(raw) != pin {
		return value, messagedelivery.ErrUnprepared
	}
	allowed := map[string]bool{"version": true, "database": true, "epoch": true, "activationSHA256": true, "badgeEvidenceSHA256": true, "vendorPath": true, "vendorSHA256": true, "messageCallbacksPath": true, "messageCallbacksSHA256": true, "offlineCallbacksPath": true, "offlineCallbacksSHA256": true, "roleCallbacksPath": true, "roleCallbacksSHA256": true}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return value, messagedelivery.ErrUnprepared
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || !allowed[key] || seen[key] {
			return value, messagedelivery.ErrUnprepared
		}
		seen[key] = true
		var ignored json.RawMessage
		if d.Decode(&ignored) != nil {
			return value, messagedelivery.ErrUnprepared
		}
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') || d.Decode(new(any)) != io.EOF || len(seen) != len(allowed) || json.Unmarshal(raw, &value) != nil {
		return value, messagedelivery.ErrUnprepared
	}
	epoch, err := uuid.Parse(value.Epoch)
	if err != nil || epoch == uuid.Nil || epoch.String() != value.Epoch || value.Version != "owned-group-receiver-bootstrap-v1" || value.Database != database || !groupStartupSHA(value.ActivationSHA256) || !groupStartupSHA(value.BadgeEvidenceSHA256) || !groupStartupSHA(value.VendorSHA256) || !filepath.IsAbs(value.VendorPath) || filepath.Clean(value.VendorPath) != value.VendorPath {
		return value, messagedelivery.ErrUnprepared
	}
	for _, pair := range []struct{ path, sha string }{{value.MessageCallbacksPath, value.MessageCallbacksSHA256}, {value.OfflineCallbacksPath, value.OfflineCallbacksSHA256}, {value.RoleCallbacksPath, value.RoleCallbacksSHA256}} {
		if pair.path == "" {
			if pair.sha != "" {
				return value, messagedelivery.ErrUnprepared
			}
		} else if !filepath.IsAbs(pair.path) || filepath.Clean(pair.path) != pair.path || !groupStartupSHA(pair.sha) {
			return value, messagedelivery.ErrUnprepared
		}
	}
	return value, nil
}
