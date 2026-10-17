//go:build darwin || linux

package ownedcontrol

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// ServerRuntimeAnnouncement is for actual source-owned server processes. Legacy
// mode has no epoch/activation. It must never borrow the future import target.
// The original managed-ordinary announcement/drain schema stays unchanged.
type ServerRuntimeAnnouncement struct {
	Version            string   `json:"version"`
	Role               string   `json:"role"`
	Mode               string   `json:"mode"`
	InstanceID         string   `json:"instanceID"`
	DatabaseSHA256     string   `json:"databaseSHA256"`
	Epoch              string   `json:"epoch"`
	ActivationSHA256   string   `json:"activationSHA256"`
	BootstrapSHA256    string   `json:"bootstrapSHA256"`
	PolicySHA256       string   `json:"policySHA256"`
	DrainPath          string   `json:"drainPath"`
	OwnedStopStepNames []string `json:"ownedStopStepNames"`
}

// ServerRuntimeDrain has actual joined ownership only; it does not fabricate a
// managed offline result or attest remote non-application. The same supervisor
// nonce binds the immutable start announcement and the final drain envelope.
type ServerRuntimeDrain struct {
	Version                  string   `json:"version"`
	Role                     string   `json:"role"`
	Mode                     string   `json:"mode"`
	InstanceID               string   `json:"instanceID"`
	DatabaseSHA256           string   `json:"databaseSHA256"`
	Epoch                    string   `json:"epoch"`
	ActivationSHA256         string   `json:"activationSHA256"`
	BootstrapSHA256          string   `json:"bootstrapSHA256"`
	PolicySHA256             string   `json:"policySHA256"`
	JoinedAndClosed          []string `json:"joinedAndClosed"`
	SourceOutcomeUnconfirmed bool     `json:"sourceOutcomeUnconfirmed"`
}
type serverAnnouncementEnvelope struct {
	Schema     int                       `json:"schema"`
	OwnerNonce string                    `json:"ownerNonce"`
	Runtime    ServerRuntimeAnnouncement `json:"runtime"`
}
type serverDrainEnvelope struct {
	Schema     int                `json:"schema"`
	OwnerNonce string             `json:"ownerNonce"`
	Runtime    ServerRuntimeDrain `json:"runtime"`
}

func serverRole(role string) bool {
	switch role {
	case "rpc-group", "rpc-msg", "rpc-conversation", "rpc-user", "rpc-friend", "rpc-auth", "rpc-third", "rpc-extend-msg", "rpc-encryption", "rpc-rtc", "msggateway", "api", "push", "crontask", "msgtransfer-legacy":
		return true
	}
	return false
}
func runtimeMode(mode, epoch, activation string) bool {
	if mode == "legacy" {
		return epoch == "" && activation == ""
	}
	return mode == "required" && uuidPattern.MatchString(epoch) && epoch != "00000000-0000-0000-0000-000000000000" && isSHA(activation)
}
func uniqueSteps(steps []string) bool {
	if len(steps) == 0 || len(steps) > 256 {
		return false
	}
	seen := map[string]bool{}
	for _, step := range steps {
		if step == "" || len(step) > 128 || seen[step] {
			return false
		}
		seen[step] = true
	}
	return true
}
func (a ServerRuntimeAnnouncement) validate() error {
	if a.Version != "owned-server-owner-ready-v1" || !serverRole(a.Role) || !runtimeMode(a.Mode, a.Epoch, a.ActivationSHA256) || !uuidPattern.MatchString(a.InstanceID) || !isSHA(a.DatabaseSHA256) || !isSHA(a.BootstrapSHA256) || !isSHA(a.PolicySHA256) || !filepath.IsAbs(a.DrainPath) || filepath.Clean(a.DrainPath) != a.DrainPath || !uniqueSteps(a.OwnedStopStepNames) {
		return ErrOwnedEvidence
	}
	return nil
}
func (d ServerRuntimeDrain) validate() error {
	if d.Version != "owned-server-drained-v1" || !serverRole(d.Role) || !runtimeMode(d.Mode, d.Epoch, d.ActivationSHA256) || !uuidPattern.MatchString(d.InstanceID) || !isSHA(d.DatabaseSHA256) || !isSHA(d.BootstrapSHA256) || !isSHA(d.PolicySHA256) || d.SourceOutcomeUnconfirmed || !uniqueSteps(d.JoinedAndClosed) {
		return ErrOwnedEvidence
	}
	return nil
}
func writeServerEnvelope(path string, value any) (sum string, resultErr error) {
	parent, id, e := privateParent(path)
	if e != nil {
		return "", e
	}
	root, e := os.OpenRoot(parent)
	if e != nil {
		return "", e
	}
	defer root.Close()
	body, e := json.Marshal(value)
	if e != nil || len(body) > 64*1024 {
		return "", ErrOwnedEvidence
	}
	body = append(body, '\n')
	f, e := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	defer func() {
		if f.Close() != nil {
			sum = ""
			resultErr = ErrOwnedEvidence
		}
	}()
	made, e := f.Stat()
	if e != nil || !private(made, false) {
		return "", ErrOwnedEvidence
	}
	if n, e := f.Write(body); e != nil || n != len(body) {
		return "", ErrOwnedEvidence
	}
	if f.Sync() != nil {
		return "", ErrOwnedEvidence
	}
	dir, e := os.OpenFile(parent, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", e
	}
	defer dir.Close()
	current, e := dir.Stat()
	if e != nil || !os.SameFile(id, current) || dir.Sync() != nil {
		return "", ErrOwnedEvidence
	}
	at, e := os.Lstat(path)
	now, e2 := os.Lstat(parent)
	if e != nil || e2 != nil || !private(at, false) || !private(now, true) || !os.SameFile(made, at) || !os.SameFile(id, now) {
		return "", ErrOwnedEvidence
	}
	return sha(body), nil
}
func readServerEnvelope(path string, out any) (string, error) {
	parent, id, e := privateParent(path)
	if e != nil {
		return "", e
	}
	at, e := os.Lstat(path)
	if e != nil || !private(at, false) || at.Size() < 1 || at.Size() > 64*1024 {
		return "", ErrOwnedEvidence
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return "", e
	}
	defer f.Close()
	opened, e := f.Stat()
	if e != nil || !os.SameFile(at, opened) {
		return "", ErrOwnedEvidence
	}
	body, e := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if e != nil || int64(len(body)) != at.Size() {
		return "", ErrOwnedEvidence
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(out) != nil {
		return "", ErrOwnedEvidence
	}
	want, e := json.Marshal(out)
	if e != nil || !bytes.Equal(append(want, '\n'), body) {
		return "", ErrOwnedEvidence
	}
	after, e := os.Lstat(path)
	now, e2 := os.Lstat(parent)
	if e != nil || e2 != nil || !private(after, false) || !private(now, true) || !os.SameFile(at, after) || after.Size() != at.Size() || !os.SameFile(id, now) {
		return "", ErrOwnedEvidence
	}
	return sha(body), nil
}
func WriteServerRuntimeAnnouncement(path, nonce string, value ServerRuntimeAnnouncement) (string, error) {
	if !isSHA(nonce) || value.validate() != nil {
		return "", ErrOwnedEvidence
	}
	return writeServerEnvelope(path, serverAnnouncementEnvelope{1, nonce, value})
}
func ReadServerRuntimeAnnouncement(path, nonce string) (ServerRuntimeAnnouncement, string, error) {
	var e serverAnnouncementEnvelope
	sum, err := readServerEnvelope(path, &e)
	if err != nil || e.Schema != 1 || e.OwnerNonce != nonce || !isSHA(nonce) || e.Runtime.validate() != nil {
		return ServerRuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	return e.Runtime, sum, nil
}
func WriteServerRuntimeDrain(path, nonce string, value ServerRuntimeDrain) (string, error) {
	if !isSHA(nonce) || value.validate() != nil {
		return "", ErrOwnedEvidence
	}
	return writeServerEnvelope(path, serverDrainEnvelope{1, nonce, value})
}
func ReadServerRuntimeDrain(path, nonce string) (ServerRuntimeDrain, string, error) {
	var e serverDrainEnvelope
	sum, err := readServerEnvelope(path, &e)
	if err != nil || e.Schema != 1 || e.OwnerNonce != nonce || !isSHA(nonce) || e.Runtime.validate() != nil {
		return ServerRuntimeDrain{}, "", ErrOwnedEvidence
	}
	return e.Runtime, sum, nil
}
