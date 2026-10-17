//go:build darwin || linux

// Package ownedcontrol implements the private local/CI execution boundary of
// namespace import. Runtime readiness announcements are not drain or read grants.
package ownedcontrol

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var ErrOwnedEvidence = errors.New("owned namespace control evidence invalid")
var noncePattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func sha(body []byte) string  { h := sha256.Sum256(body); return hex.EncodeToString(h[:]) }
func isSHA(value string) bool { return noncePattern.MatchString(value) }

// Only the successfully started runtime supplies these fields. The supervisor
// independently captures real PID/start token/UID/executable/binary/source pins.
// OwnedStopStepNames is intended ownership/order, never a claim of joined steps.
type RuntimeAnnouncement struct {
	Version            string   `json:"version"`
	InstanceID         string   `json:"instanceID"`
	DatabaseSHA256     string   `json:"databaseSHA256"`
	Epoch              string   `json:"epoch"`
	ActivationSHA256   string   `json:"activationSHA256"`
	BootstrapSHA256    string   `json:"bootstrapSHA256"`
	PolicySHA256       string   `json:"policySHA256"`
	DrainPath          string   `json:"drainPath"`
	OwnedStopStepNames []string `json:"ownedStopStepNames"`
}
type announcementEnvelope struct {
	Schema     int                 `json:"schema"`
	OwnerNonce string              `json:"ownerNonce"`
	Runtime    RuntimeAnnouncement `json:"runtime"`
}

func (a RuntimeAnnouncement) validate() error {
	if a.Version != "managed-ordinary-owner-ready-v1" || !uuidPattern.MatchString(a.InstanceID) || !uuidPattern.MatchString(a.Epoch) || !isSHA(a.DatabaseSHA256) || !isSHA(a.ActivationSHA256) || !isSHA(a.BootstrapSHA256) || !isSHA(a.PolicySHA256) || !filepath.IsAbs(a.DrainPath) || len(a.OwnedStopStepNames) == 0 {
		return ErrOwnedEvidence
	}
	seen := map[string]bool{}
	for _, n := range a.OwnedStopStepNames {
		if n == "" || len(n) > 128 || seen[n] {
			return ErrOwnedEvidence
		}
		seen[n] = true
	}
	return nil
}
func private(info os.FileInfo, dir bool) bool {
	if info == nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	if dir {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && st.Nlink == 1
}
func privateParent(path string) (string, os.FileInfo, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", nil, ErrOwnedEvidence
	}
	parent := filepath.Dir(path)
	canonical, err := filepath.EvalSymlinks(parent)
	if err != nil || canonical != parent {
		return "", nil, ErrOwnedEvidence
	}
	i, err := os.Lstat(parent)
	if err != nil || !private(i, true) {
		return "", nil, ErrOwnedEvidence
	}
	return parent, i, nil
}

// WriteRuntimeAnnouncement is called after actual successful Start, before the
// command reports ready. Failure leaves the original owner responsible for Stop
// and join. Supervisor variables are optional only as a PAIR at the CLI layer:
// LUOBO_MANAGED_OWNER_ANNOUNCEMENT_PATH + LUOBO_MANAGED_OWNER_NONCE.
func WriteRuntimeAnnouncement(path, ownerNonce string, value RuntimeAnnouncement) (resultSHA string, resultErr error) {
	if !noncePattern.MatchString(ownerNonce) || value.validate() != nil {
		return "", ErrOwnedEvidence
	}
	parent, identity, err := privateParent(path)
	if err != nil {
		return "", err
	}
	directory, err := os.OpenFile(parent, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(identity, opened) {
		return "", ErrOwnedEvidence
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer root.Close()
	f, err := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	defer func() {
		if f.Close() != nil {
			resultSHA = ""
			resultErr = ErrOwnedEvidence
		}
	}()
	created, err := f.Stat()
	if err != nil || !private(created, false) {
		return "", ErrOwnedEvidence
	}
	body, err := json.Marshal(announcementEnvelope{1, ownerNonce, value})
	if err != nil {
		return "", err
	}
	body = append(body, '\n')
	if len(body) > 64*1024 {
		return "", ErrOwnedEvidence
	}
	if n, err := f.Write(body); err != nil || n != len(body) {
		return "", ErrOwnedEvidence
	}
	if f.Sync() != nil || directory.Sync() != nil {
		return "", ErrOwnedEvidence
	}
	now, err := os.Lstat(parent)
	if err != nil || !private(now, true) || !os.SameFile(identity, now) {
		return "", ErrOwnedEvidence
	}
	current, err := os.Lstat(path)
	if err != nil || !private(current, false) || !os.SameFile(created, current) {
		return "", ErrOwnedEvidence
	}
	return sha(body), nil
}

// ReadRuntimeAnnouncement requires exact canonical bytes produced above and
// stable private file custody; it does not trust a child-reported process ID.
func ReadRuntimeAnnouncement(path, ownerNonce string) (RuntimeAnnouncement, string, error) {
	if !noncePattern.MatchString(ownerNonce) {
		return RuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	parent, parentID, err := privateParent(path)
	if err != nil {
		return RuntimeAnnouncement{}, "", err
	}
	before, err := os.Lstat(path)
	if err != nil || !private(before, false) || before.Size() < 1 || before.Size() > 64*1024 {
		return RuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return RuntimeAnnouncement{}, "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return RuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	body, err := io.ReadAll(io.LimitReader(f, 64*1024+1))
	if err != nil || len(body) != int(before.Size()) {
		return RuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	var envelope announcementEnvelope
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&envelope) != nil || envelope.Schema != 1 || envelope.OwnerNonce != ownerNonce || envelope.Runtime.validate() != nil {
		return RuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	canonical, err := json.Marshal(envelope)
	if err != nil || !bytes.Equal(append(canonical, '\n'), body) {
		return RuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	after, err := os.Lstat(path)
	if err != nil || !private(after, false) || !os.SameFile(before, after) || before.Size() != after.Size() {
		return RuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	currentParent, err := os.Lstat(parent)
	if err != nil || !private(currentParent, true) || !os.SameFile(parentID, currentParent) {
		return RuntimeAnnouncement{}, "", ErrOwnedEvidence
	}
	return envelope.Runtime, sha(body), nil
}
