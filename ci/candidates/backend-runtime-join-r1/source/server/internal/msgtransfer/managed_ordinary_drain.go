package msgtransfer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"

	"github.com/openimsdk/open-im-server/v3/internal/deliveryprofile"
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
)

type ManagedOrdinaryDrain struct {
	Version                  string                   `json:"version"`
	InstanceID               string                   `json:"instanceID"`
	DatabaseSHA256           string                   `json:"databaseSHA256"`
	Epoch                    string                   `json:"epoch"`
	ActivationSHA256         string                   `json:"activationSHA256"`
	BootstrapSHA256          string                   `json:"bootstrapSHA256"`
	PolicySHA256             string                   `json:"policySHA256"`
	JoinedAndClosed          []string                 `json:"joinedAndClosed"`
	Offline                  KafkaOfflineDrainSummary `json:"offline"`
	SourceOutcomeUnconfirmed bool                     `json:"sourceOutcomeUnconfirmed"`
}

func (r *ManagedOrdinaryRuntime) DrainSummary(ctx context.Context) (ManagedOrdinaryDrain, error) {
	if r == nil || ctx == nil {
		return ManagedOrdinaryDrain{}, messagedelivery.ErrInvalid
	}
	select {
	case <-ctx.Done():
		return ManagedOrdinaryDrain{}, ErrManagedOrdinaryShutdownPending
	case <-r.stopLock:
	}
	defer func() { r.stopLock <- struct{}{} }()
	if !r.complete || r.completed != len(r.steps) || !r.intakeStarted || r.intake == nil || r.offline == nil {
		return ManagedOrdinaryDrain{}, ErrManagedOrdinaryShutdownPending
	}
	offline, err := r.offline.worker.DrainSummary()
	if err != nil {
		return ManagedOrdinaryDrain{}, err
	}
	names := make([]string, len(r.steps))
	for i, step := range r.steps {
		names[i] = step.name
	}
	// These facts concern local ownership only. Unknown callback/vendor/socket
	// outcomes are not rolled back or reclassified by a successful shutdown.
	return ManagedOrdinaryDrain{Version: "managed-ordinary-drained-v1", InstanceID: r.instance, DatabaseSHA256: deliveryprofile.SHA([]byte(r.bootstrap.Database)), Epoch: r.bootstrap.Epoch, ActivationSHA256: r.bootstrap.ActivationSHA256, BootstrapSHA256: r.bootstrap.inputSHA, PolicySHA256: r.bootstrap.PolicySHA256, JoinedAndClosed: names, Offline: offline, SourceOutcomeUnconfirmed: r.intake.Failure() != nil}, nil
}

// O_EXCL private output, same file/parent before and after fsync. A partial or
// failed result remains in place for inspection; it is never overwritten on a
// later Stop or mistaken for a complete drain merely because the path exists.
func (r *ManagedOrdinaryRuntime) WriteDrain(ctx context.Context) (digest string, resultErr error) {
	summary, err := r.DrainSummary(ctx)
	if err != nil {
		return "", err
	}
	path := r.bootstrap.DrainPath
	if !ordinaryCanonicalPath(path) {
		return "", ErrManagedOrdinaryShutdownPending
	}
	parentPath := filepath.Dir(path)
	canonical, err := filepath.EvalSymlinks(parentPath)
	if err != nil || canonical != parentPath {
		return "", ErrManagedOrdinaryShutdownPending
	}
	private := func(info os.FileInfo, directory bool) bool {
		if info == nil {
			return false
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != uint32(os.Geteuid()) || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return false
		}
		if directory {
			return info.IsDir() && info.Mode().Perm() == 0700
		}
		return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && st.Nlink == 1
	}
	parent, err := os.Lstat(parentPath)
	if err != nil || !private(parent, true) {
		return "", ErrManagedOrdinaryShutdownPending
	}
	directory, err := os.OpenFile(parentPath, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", ErrManagedOrdinaryShutdownPending
	}
	defer func() {
		if directory.Close() != nil {
			digest = ""
			resultErr = ErrManagedOrdinaryShutdownPending
		}
	}()
	opened, err := directory.Stat()
	if err != nil || !os.SameFile(parent, opened) {
		return "", ErrManagedOrdinaryShutdownPending
	}
	root, err := os.OpenRoot(parentPath)
	if err != nil {
		return "", ErrManagedOrdinaryShutdownPending
	}
	defer func() {
		if root.Close() != nil {
			digest = ""
			resultErr = ErrManagedOrdinaryShutdownPending
		}
	}()
	rooted, err := root.Stat(".")
	if err != nil || !os.SameFile(parent, rooted) {
		return "", ErrManagedOrdinaryShutdownPending
	}
	name := filepath.Base(path)
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", ErrManagedOrdinaryShutdownPending
	}
	defer func() {
		if file.Close() != nil {
			digest = ""
			resultErr = ErrManagedOrdinaryShutdownPending
		}
	}()
	identity, err := file.Stat()
	if err != nil {
		return "", ErrManagedOrdinaryShutdownPending
	}
	current := func() bool {
		now, e := os.Lstat(parentPath)
		if e != nil || !private(now, true) || !os.SameFile(parent, now) {
			return false
		}
		child, e := root.Lstat(name)
		if e != nil || !private(child, false) || !os.SameFile(identity, child) {
			return false
		}
		atPath, e := os.Lstat(path)
		return e == nil && private(atPath, false) && os.SameFile(identity, atPath)
	}
	raw, err := json.Marshal(summary)
	if err != nil || !current() {
		return "", ErrManagedOrdinaryShutdownPending
	}
	raw = append(raw, '\n')
	if n, err := file.Write(raw); err != nil || n != len(raw) {
		return "", ErrManagedOrdinaryShutdownPending
	}
	if file.Sync() != nil || directory.Sync() != nil || !current() {
		return "", ErrManagedOrdinaryShutdownPending
	}
	return deliveryprofile.SHA(raw), nil
}
