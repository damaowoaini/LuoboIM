//go:build linux

package ownedcontrol

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ServiceSpec is chosen by the source-owned fresh deployment launcher. There is
// no adopt-by-arbitrary-PID API: only an actual exec.Cmd started here owns Stop.
// Binary/source pins are taken from the reviewed build receipt, not child JSON.
type ServiceSpec struct {
	Role                       string
	BinaryPath                 string
	BinarySHA256               string
	SourceSHA256               string
	BuildReceiptSHA256         string
	BuildReceiptPath           string
	BootstrapPath              string
	BootstrapSHA256            string
	BootstrapEnvironmentKey    string
	BootstrapSHAEnvironmentKey string
	Work                       string
	Args                       []string
}
type ProcessOwner struct {
	Role               string `json:"role"`
	PID                int    `json:"pid"`
	UID                uint32 `json:"uid"`
	StartToken         string `json:"startToken"`
	BootID             string `json:"bootID"`
	Executable         string `json:"executable"`
	BinarySHA256       string `json:"binarySHA256"`
	SourceSHA256       string `json:"sourceSHA256"`
	BuildReceiptSHA256 string `json:"buildReceiptSHA256"`
	BootstrapSHA256    string `json:"bootstrapSHA256"`
	OwnerNonce         string `json:"ownerNonce"`
}
type OwnedService struct {
	launcher           *Launcher
	mu                 sync.Mutex
	cmd                *exec.Cmd
	owner              ProcessOwner
	announcementPath   string
	announcement       RuntimeAnnouncement
	serverAnnouncement *ServerRuntimeAnnouncement
	announcementSHA    string
	done               chan struct{}
	waitErr            error
	joined             bool
	join               JoinEvidence
}
type JoinEvidence struct {
	Owner              ProcessOwner `json:"owner"`
	RuntimeInstanceID  string       `json:"runtimeInstanceID"`
	AnnouncementSHA256 string       `json:"announcementSHA256"`
	DrainSHA256        string       `json:"drainSHA256"`
	ExitedAt           time.Time    `json:"exitedAt"`
}

// Service stdout/stderr remain PRIVATE and bounded; they are never copied to
// public capture artifacts. Overflow invalidates successful join evidence.
type boundedServiceOutput struct {
	mu       sync.Mutex
	file     *os.File
	written  int64
	overflow bool
}

func (w *boundedServiceOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(p)
	remaining := int64(8*1024*1024) - w.written
	if int64(len(p)) > remaining {
		w.overflow = true
		p = p[:int(remaining)]
	}
	if len(p) > 0 {
		n, e := w.file.Write(p)
		w.written += int64(n)
		if e != nil {
			return n, e
		}
		if n != len(p) {
			return n, io.ErrShortWrite
		}
	}
	return original, nil
}
func actualImage(pid int, path, expected string) error {
	before, e := processIdentity(pid)
	if e != nil || before.Executable != path {
		return ErrOwnedEvidence
	}
	f, e := os.Open("/proc/" + strconv.Itoa(pid) + "/exe")
	if e != nil {
		return e
	}
	defer f.Close()
	image, e := f.Stat()
	at, e2 := os.Stat(path)
	if e != nil || e2 != nil || !image.Mode().IsRegular() || !os.SameFile(image, at) || image.Size() < 1 || image.Size() > 256*1024*1024 {
		return ErrOwnedEvidence
	}
	body, e := io.ReadAll(io.LimitReader(f, 256*1024*1024+1))
	if e != nil || int64(len(body)) != image.Size() || sha(body) != expected {
		return ErrOwnedEvidence
	}
	after, e := processIdentity(pid)
	if e != nil || !sameProcess(before, after) {
		return ErrOwnedEvidence
	}
	return nil
}
func boundedFile(path string, maximum int64, privateFile bool) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() < 1 || before.Size() > maximum || privateFile && !private(before, false) {
		return nil, ErrOwnedEvidence
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, ErrOwnedEvidence
	}
	body, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	current, pathErr := os.Lstat(path)
	if err != nil || pathErr != nil || !os.SameFile(before, after) || !os.SameFile(before, current) || before.Size() != after.Size() || int64(len(body)) != before.Size() {
		return nil, ErrOwnedEvidence
	}
	return body, nil
}
func processIdentity(pid int) (ProcessOwner, error) {
	if pid <= 1 {
		return ProcessOwner{}, ErrOwnedEvidence
	}
	base := "/proc/" + strconv.Itoa(pid)
	// procfs reports zero st_size; read only these exact metadata files with caps.
	read := func(path string, cap int64) ([]byte, error) {
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		defer f.Close()
		b, e := io.ReadAll(io.LimitReader(f, cap+1))
		if e != nil || int64(len(b)) > cap {
			return nil, ErrOwnedEvidence
		}
		return b, nil
	}
	stat, err := read(base+"/stat", 16*1024)
	if err != nil {
		return ProcessOwner{}, err
	}
	at := bytes.LastIndexByte(stat, ')')
	if at < 0 {
		return ProcessOwner{}, ErrOwnedEvidence
	}
	fields := strings.Fields(string(stat[at+1:]))
	if len(fields) < 20 {
		return ProcessOwner{}, ErrOwnedEvidence
	}
	start := fields[19]
	if _, err = strconv.ParseUint(start, 10, 64); err != nil {
		return ProcessOwner{}, ErrOwnedEvidence
	}
	status, err := read(base+"/status", 64*1024)
	if err != nil {
		return ProcessOwner{}, err
	}
	uid := uint64(^uint32(0))
	for _, line := range strings.Split(string(status), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			v := strings.Fields(line)
			if len(v) != 5 {
				return ProcessOwner{}, ErrOwnedEvidence
			}
			uid, err = strconv.ParseUint(v[1], 10, 32)
			if err != nil {
				return ProcessOwner{}, err
			}
		}
	}
	if uid != uint64(os.Geteuid()) {
		return ProcessOwner{}, ErrOwnedEvidence
	}
	exe, err := os.Readlink(base + "/exe")
	if err != nil || !filepath.IsAbs(exe) || strings.HasSuffix(exe, " (deleted)") {
		return ProcessOwner{}, ErrOwnedEvidence
	}
	boot, err := read("/proc/sys/kernel/random/boot_id", 256)
	if err != nil || !uuidPattern.MatchString(strings.TrimSpace(string(boot))) {
		return ProcessOwner{}, ErrOwnedEvidence
	}
	return ProcessOwner{PID: pid, UID: uint32(uid), StartToken: start, BootID: strings.TrimSpace(string(boot)), Executable: exe}, nil
}
func sameProcess(a, b ProcessOwner) bool {
	return a.PID == b.PID && a.UID == b.UID && a.StartToken == b.StartToken && a.BootID == b.BootID && a.Executable == b.Executable
}

// startOwnedService is private: only Launcher.Start can admit an actual exec.
func startOwnedService(ctx context.Context, spec ServiceSpec) (*OwnedService, error) {
	if ctx == nil || spec.Role == "" || !filepath.IsAbs(spec.BinaryPath) || !isSHA(spec.BinarySHA256) || !isSHA(spec.SourceSHA256) || !isSHA(spec.BuildReceiptSHA256) || !isSHA(spec.BootstrapSHA256) || !strings.HasPrefix(spec.BootstrapEnvironmentKey, "LUOBO_") || !strings.HasPrefix(spec.BootstrapSHAEnvironmentKey, "LUOBO_") || spec.BootstrapSHAEnvironmentKey == spec.BootstrapEnvironmentKey || strings.ContainsAny(spec.BootstrapEnvironmentKey+spec.BootstrapSHAEnvironmentKey, "=\x00") {
		return nil, ErrOwnedEvidence
	}
	receipt, err := boundedFile(spec.BuildReceiptPath, 64*1024, true)
	if err != nil || sha(receipt) != spec.BuildReceiptSHA256 {
		return nil, ErrOwnedEvidence
	}
	var built struct {
		Schema          int    `json:"schema"`
		SourceSHA256    string `json:"sourceSHA256"`
		BinarySHA256    string `json:"binarySHA256"`
		BinaryBytes     int64  `json:"binaryBytes"`
		ToolchainSHA256 string `json:"toolchainSHA256"`
	}
	dec := json.NewDecoder(bytes.NewReader(receipt))
	dec.DisallowUnknownFields()
	if dec.Decode(&built) != nil || built.Schema != 1 || built.SourceSHA256 != spec.SourceSHA256 || built.BinarySHA256 != spec.BinarySHA256 || built.BinaryBytes < 1 || !isSHA(built.ToolchainSHA256) {
		return nil, ErrOwnedEvidence
	}
	canonical, err := filepath.EvalSymlinks(spec.BinaryPath)
	if err != nil || canonical != spec.BinaryPath {
		return nil, ErrOwnedEvidence
	}
	binary, err := boundedFile(spec.BinaryPath, 256*1024*1024, false)
	if err != nil || sha(binary) != spec.BinarySHA256 || int64(len(binary)) != built.BinaryBytes {
		return nil, ErrOwnedEvidence
	}
	binary = nil
	bootstrap, err := boundedFile(spec.BootstrapPath, 1024*1024, true)
	if err != nil || sha(bootstrap) != spec.BootstrapSHA256 {
		return nil, ErrOwnedEvidence
	}
	bootstrap = nil
	work, identity, err := privateParent(filepath.Join(spec.Work, "owner.json"))
	if err != nil || work != spec.Work {
		return nil, ErrOwnedEvidence
	}
	_ = identity
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(random[:])
	announcementPath := filepath.Join(work, "runtime-announcement-"+nonce+".json")
	stdout, err := os.OpenFile(filepath.Join(work, "service-"+nonce+".log"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(spec.BinaryPath, append([]string(nil), spec.Args...)...)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + work, spec.BootstrapEnvironmentKey + "=" + spec.BootstrapPath, spec.BootstrapSHAEnvironmentKey + "=" + spec.BootstrapSHA256, "LUOBO_MANAGED_OWNER_ANNOUNCEMENT_PATH=" + announcementPath, "LUOBO_MANAGED_OWNER_NONCE=" + nonce}
	output := &boundedServiceOutput{file: stdout}
	cmd.Stdout = output
	cmd.Stderr = output
	if err = cmd.Start(); err != nil {
		stdout.Close()
		return nil, err
	}
	s := &OwnedService{cmd: cmd, announcementPath: announcementPath, done: make(chan struct{})}
	go func() {
		s.waitErr = cmd.Wait()
		syncErr := stdout.Sync()
		closeErr := stdout.Close()
		if syncErr != nil || closeErr != nil || output.overflow {
			s.waitErr = errors.Join(s.waitErr, ErrOwnedEvidence)
		}
		close(s.done)
	}()
	actual, err := processIdentity(cmd.Process.Pid)
	if err != nil || actual.Executable != spec.BinaryPath || actualImage(cmd.Process.Pid, spec.BinaryPath, spec.BinarySHA256) != nil {
		cmd.Process.Signal(syscall.SIGTERM)
		return s, ErrOwnedEvidence
	}
	actual.Role = spec.Role
	actual.BinarySHA256 = spec.BinarySHA256
	actual.SourceSHA256 = spec.SourceSHA256
	actual.BuildReceiptSHA256 = spec.BuildReceiptSHA256
	actual.BootstrapSHA256 = spec.BootstrapSHA256
	actual.OwnerNonce = nonce
	s.owner = actual
	return s, nil
}
func (s *OwnedService) AwaitAnnouncement(ctx context.Context) (ProcessOwner, RuntimeAnnouncement, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.joined {
		return s.owner, s.announcement, nil
	}
	if s.cmd == nil || s.owner.PID <= 1 {
		return ProcessOwner{}, RuntimeAnnouncement{}, ErrOwnedEvidence
	}
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ProcessOwner{}, RuntimeAnnouncement{}, ctx.Err()
		case <-s.done:
			return ProcessOwner{}, RuntimeAnnouncement{}, ErrOwnedEvidence
		default:
		}
		current, err := processIdentity(s.owner.PID)
		if err != nil || !sameProcess(s.owner, current) {
			return ProcessOwner{}, RuntimeAnnouncement{}, ErrOwnedEvidence
		}
		if _, err = os.Lstat(s.announcementPath); err == nil {
			a, server, hash, err := readOwnedAnnouncement(s.announcementPath, s.owner.OwnerNonce, s.owner.Role)
			if err != nil || a.BootstrapSHA256 != s.owner.BootstrapSHA256 {
				return ProcessOwner{}, RuntimeAnnouncement{}, ErrOwnedEvidence
			}
			s.announcement = a
			s.serverAnnouncement = server
			s.announcementSHA = hash
			return s.owner, a, nil
		}
		select {
		case <-ctx.Done():
			return ProcessOwner{}, RuntimeAnnouncement{}, ctx.Err()
		case <-tick.C:
		}
	}
}

// StopAndJoin signals only the actually owned child after a fresh PID/start/UID
// comparison. Timeout retains ownership; later calls join the SAME process. A
// nonzero exit or a missing/partial/mismatched drain can never fabricate success.
func (s *OwnedService) StopAndJoin(ctx context.Context) (JoinEvidence, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.joined {
		return s.join, nil
	}
	if s.cmd == nil || s.announcementSHA == "" {
		return JoinEvidence{}, ErrOwnedEvidence
	}
	select {
	case <-s.done:
	default:
		current, err := processIdentity(s.owner.PID)
		if err != nil || !sameProcess(s.owner, current) {
			return JoinEvidence{}, ErrOwnedEvidence
		}
		if err = s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			return JoinEvidence{}, err
		}
	}
	select {
	case <-ctx.Done():
		return JoinEvidence{}, ctx.Err()
	case <-s.done:
	}
	if s.waitErr != nil {
		return JoinEvidence{}, ErrOwnedEvidence
	}
	body, err := boundedFile(s.announcement.DrainPath, 1024*1024, true)
	if err != nil {
		return JoinEvidence{}, err
	}
	if s.serverAnnouncement != nil {
		drain, hash, e := ReadServerRuntimeDrain(s.announcement.DrainPath, s.owner.OwnerNonce)
		a := s.serverAnnouncement
		if e != nil || drain.Role != a.Role || drain.Mode != a.Mode || drain.InstanceID != a.InstanceID || drain.DatabaseSHA256 != a.DatabaseSHA256 || drain.Epoch != a.Epoch || drain.ActivationSHA256 != a.ActivationSHA256 || drain.BootstrapSHA256 != a.BootstrapSHA256 || drain.PolicySHA256 != a.PolicySHA256 || len(drain.JoinedAndClosed) != len(a.OwnedStopStepNames) {
			return JoinEvidence{}, ErrOwnedEvidence
		}
		for i, n := range a.OwnedStopStepNames {
			if drain.JoinedAndClosed[i] != n {
				return JoinEvidence{}, ErrOwnedEvidence
			}
		}
		if hash != sha(body) {
			return JoinEvidence{}, ErrOwnedEvidence
		}
		evidence := JoinEvidence{s.owner, a.InstanceID, s.announcementSHA, hash, time.Now().UTC()}
		if s.launcher == nil {
			return JoinEvidence{}, ErrOwnedEvidence
		}
		persisted, e := s.launcher.recordJoin(s, evidence)
		if e != nil {
			return JoinEvidence{}, e
		}
		s.join = persisted
		s.joined = true
		return s.join, nil
	}
	var drain struct {
		Version                  string          `json:"version"`
		InstanceID               string          `json:"instanceID"`
		DatabaseSHA256           string          `json:"databaseSHA256"`
		Epoch                    string          `json:"epoch"`
		ActivationSHA256         string          `json:"activationSHA256"`
		BootstrapSHA256          string          `json:"bootstrapSHA256"`
		PolicySHA256             string          `json:"policySHA256"`
		JoinedAndClosed          []string        `json:"joinedAndClosed"`
		Offline                  json.RawMessage `json:"offline"`
		SourceOutcomeUnconfirmed bool            `json:"sourceOutcomeUnconfirmed"`
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(&drain) != nil {
		return JoinEvidence{}, ErrOwnedEvidence
	}
	canonical, err := json.Marshal(drain)
	if err != nil || !bytes.Equal(append(canonical, '\n'), body) {
		return JoinEvidence{}, ErrOwnedEvidence
	}
	a := s.announcement
	if drain.Version != "managed-ordinary-drained-v1" || drain.InstanceID != a.InstanceID || drain.DatabaseSHA256 != a.DatabaseSHA256 || drain.Epoch != a.Epoch || drain.ActivationSHA256 != a.ActivationSHA256 || drain.BootstrapSHA256 != a.BootstrapSHA256 || drain.PolicySHA256 != a.PolicySHA256 || drain.SourceOutcomeUnconfirmed || len(drain.JoinedAndClosed) != len(a.OwnedStopStepNames) {
		return JoinEvidence{}, ErrOwnedEvidence
	}
	for i, n := range a.OwnedStopStepNames {
		if drain.JoinedAndClosed[i] != n {
			return JoinEvidence{}, ErrOwnedEvidence
		}
	}
	evidence := JoinEvidence{s.owner, a.InstanceID, s.announcementSHA, sha(body), time.Now().UTC()}
	if s.launcher == nil {
		return JoinEvidence{}, ErrOwnedEvidence
	}
	persisted, e := s.launcher.recordJoin(s, evidence)
	if e != nil {
		return JoinEvidence{}, e
	}
	s.join = persisted
	s.joined = true
	return s.join, nil
}

// The server and managed shapes remain distinct on disk. Normalization here
// exposes only common identity to the supervisor; it creates no fake offline
// drain and never changes the original managed writer contract.
func readOwnedAnnouncement(path, nonce, role string) (RuntimeAnnouncement, *ServerRuntimeAnnouncement, string, error) {
	a, sum, e := ReadRuntimeAnnouncement(path, nonce)
	if e == nil {
		if role != "msgtransfer-managed" {
			return RuntimeAnnouncement{}, nil, "", ErrOwnedEvidence
		}
		return a, nil, sum, nil
	}
	v, sum, e := ReadServerRuntimeAnnouncement(path, nonce)
	if e != nil || v.Role != role {
		return RuntimeAnnouncement{}, nil, "", ErrOwnedEvidence
	}
	normalized := RuntimeAnnouncement{Version: v.Version, InstanceID: v.InstanceID, DatabaseSHA256: v.DatabaseSHA256, Epoch: v.Epoch, ActivationSHA256: v.ActivationSHA256, BootstrapSHA256: v.BootstrapSHA256, PolicySHA256: v.PolicySHA256, DrainPath: v.DrainPath, OwnedStopStepNames: append([]string(nil), v.OwnedStopStepNames...)}
	return normalized, &v, sum, nil
}
