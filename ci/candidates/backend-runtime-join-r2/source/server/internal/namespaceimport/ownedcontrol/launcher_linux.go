//go:build linux

package ownedcontrol

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// Launcher is the sole source-owned exec entry. A durable hold is irreversible
// within this generation. Closing an FD or restarting the supervisor does not
// reopen admission. An old unknown child can only be inspected, never adopted.
type Launcher struct {
	mu          sync.Mutex
	dir, header string
	lock        *os.File
	lockID      os.FileInfo
	rootID      os.FileInfo
	poison      bool
	children    map[string]*OwnedService
}
type launcherEntry struct {
	Owner            ProcessOwner  `json:"owner"`
	AnnouncementPath string        `json:"announcementPath"`
	Joined           *JoinEvidence `json:"joined,omitempty"`
}
type launcherState struct {
	Schema         int             `json:"schema"`
	HeaderSHA256   string          `json:"headerSHA256"`
	Sequence       uint64          `json:"sequence"`
	PreviousSHA256 string          `json:"previousSHA256"`
	Supervisor     ProcessOwner    `json:"supervisor"`
	Entries        []launcherEntry `json:"entries"`
}
type launcherHead struct {
	Sequence uint64 `json:"sequence"`
	SHA256   string `json:"sha256"`
}
type holdRecord struct {
	Schema                                     int `json:"schema"`
	HeaderSHA256, LeaseID, PreviousStateSHA256 string
	HeldAt                                     time.Time
}

// AdmissionHold cannot be constructed from a path/SHA/network DTO. It retains
// the actual launcher and is validated against the private marker on every use.
type AdmissionHold struct {
	launcher     *Launcher
	record       holdRecord
	sha          string
	exclusionAt  time.Time
	exclusionSHA string
}

func privateJSON(path string, value any) (string, error) {
	parent, id, err := privateParent(path)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return "", err
	}
	defer root.Close()
	body, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	body = append(body, '\n')
	if len(body) > 1024*1024 {
		return "", ErrOwnedEvidence
	}
	f, err := root.OpenFile(filepath.Base(path), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	n, err := f.Write(body)
	if err == nil && n != len(body) {
		err = ErrOwnedEvidence
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	directory, err := os.Open(parent)
	if err != nil {
		return "", err
	}
	err = directory.Sync()
	directory.Close()
	now, e := os.Lstat(parent)
	if err != nil || e != nil || !os.SameFile(id, now) {
		return "", ErrOwnedEvidence
	}
	return sha(body), nil
}
func readCanonical(path string, out any) ([]byte, error) {
	body, err := boundedFile(path, 1024*1024, true)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return nil, ErrOwnedEvidence
	}
	want, err := json.Marshal(out)
	if err != nil || !bytes.Equal(append(want, '\n'), body) {
		return nil, ErrOwnedEvidence
	}
	return body, nil
}
func OpenOwnedLauncher(dir, headerSHA string) (*Launcher, error) {
	if !isSHA(headerSHA) {
		return nil, ErrOwnedEvidence
	}
	parent, id, err := privateParent(filepath.Join(dir, "launcher.lock"))
	if err != nil || parent != dir {
		return nil, ErrOwnedEvidence
	}
	f, err := os.OpenFile(filepath.Join(dir, "launcher.lock"), os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !private(info, false) {
		f.Close()
		return nil, ErrOwnedEvidence
	}
	l := &Launcher{dir: dir, header: headerSHA, lock: f, lockID: info, rootID: id, children: map[string]*OwnedService{}}
	if err = l.enter(); err != nil {
		f.Close()
		return nil, err
	}
	defer l.leave()
	if _, err = os.Lstat(filepath.Join(dir, "HEAD")); errors.Is(err, os.ErrNotExist) {
		names, e := os.ReadDir(dir)
		if e != nil || len(names) != 1 {
			f.Close()
			return nil, ErrOwnedEvidence
		}
		owner, e := processIdentity(os.Getpid())
		if e != nil {
			f.Close()
			return nil, e
		}
		state := launcherState{Schema: 1, HeaderSHA256: headerSHA, Supervisor: owner, Entries: []launcherEntry{}}
		if e = l.commit(launcherHead{}, state); e != nil {
			f.Close()
			return nil, e
		}
	} else if _, _, err = l.load(); err != nil {
		f.Close()
		return nil, err
	}
	return l, nil
}
func (l *Launcher) enter() error {
	if l == nil || l.lock == nil || l.poison {
		return ErrOwnedEvidence
	}
	if err := unix.Flock(int(l.lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return err
	}
	root, e := os.Lstat(l.dir)
	lock, e2 := os.Lstat(filepath.Join(l.dir, "launcher.lock"))
	if e != nil || e2 != nil || !private(root, true) || !private(lock, false) || !os.SameFile(root, l.rootID) || !os.SameFile(lock, l.lockID) {
		unix.Flock(int(l.lock.Fd()), unix.LOCK_UN)
		return ErrOwnedEvidence
	}
	return nil
}
func (l *Launcher) leave() {
	if !l.poison {
		unix.Flock(int(l.lock.Fd()), unix.LOCK_UN)
	}
}
func (l *Launcher) load() (launcherHead, launcherState, error) {
	var h launcherHead
	var st launcherState
	if _, e := readCanonical(filepath.Join(l.dir, "HEAD"), &h); e != nil || h.Sequence == 0 || !isSHA(h.SHA256) {
		return h, st, ErrOwnedEvidence
	}
	body, e := readCanonical(filepath.Join(l.dir, fmt.Sprintf("state-%020d.json", h.Sequence)), &st)
	if e != nil || sha(body) != h.SHA256 || st.Schema != 1 || st.HeaderSHA256 != l.header || st.Sequence != h.Sequence {
		return h, st, ErrOwnedEvidence
	}
	if _, e = os.Lstat(filepath.Join(l.dir, fmt.Sprintf("state-%020d.json", h.Sequence+1))); !errors.Is(e, os.ErrNotExist) {
		return h, st, ErrOwnedEvidence
	}
	return h, st, nil
}
func (l *Launcher) commit(h launcherHead, st launcherState) error {
	if h.Sequence == ^uint64(0) {
		return ErrOwnedEvidence
	}
	st.Sequence = h.Sequence + 1
	st.PreviousSHA256 = h.SHA256
	sum, e := privateJSON(filepath.Join(l.dir, fmt.Sprintf("state-%020d.json", st.Sequence)), st)
	if e != nil {
		return e
	}
	next := launcherHead{st.Sequence, sum}
	temp := filepath.Join(l.dir, fmt.Sprintf("HEAD-%020d.tmp", st.Sequence))
	if _, e = privateJSON(temp, next); e != nil {
		return e
	}
	if e = os.Rename(temp, filepath.Join(l.dir, "HEAD")); e != nil {
		return e
	}
	d, e := os.Open(l.dir)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (l *Launcher) admissionOpen() error {
	if _, e := os.Lstat(filepath.Join(l.dir, "HELD")); !errors.Is(e, os.ErrNotExist) {
		return ErrOwnedEvidence
	}
	_, st, e := l.load()
	if e != nil {
		return e
	}
	self, e := processIdentity(os.Getpid())
	if e != nil || !sameProcess(self, st.Supervisor) {
		return ErrOwnedEvidence
	}
	return nil
}

// Start serializes the real exec and durable owner registration with Hold. If
// owner persistence fails the lock stays poisoned/held until the actual owner
// joins; no subsequent launcher can silently open a replacement generation.
func (l *Launcher) Start(ctx context.Context, spec ServiceSpec) (*OwnedService, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.enter(); e != nil {
		return nil, e
	}
	defer l.leave()
	if e := l.admissionOpen(); e != nil {
		return nil, e
	}
	h, st, e := l.load()
	if e != nil {
		return nil, e
	}
	for _, row := range st.Entries {
		if row.Owner.Role == spec.Role {
			return nil, ErrOwnedEvidence
		}
	}
	service, e := startOwnedService(ctx, spec)
	if service != nil {
		service.launcher = l
		l.children[spec.Role] = service
		st.Entries = append(st.Entries, launcherEntry{Owner: service.owner, AnnouncementPath: service.announcementPath})
		if pe := l.commit(h, st); pe != nil {
			l.poison = true
			return service, pe
		}
	}
	return service, e
}
func (l *Launcher) Hold(ctx context.Context) (*AdmissionHold, error) {
	if ctx == nil || ctx.Err() != nil {
		return nil, ErrOwnedEvidence
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.enter(); e != nil {
		return nil, e
	}
	defer l.leave()
	path := filepath.Join(l.dir, "HELD")
	var record holdRecord
	// readCanonical deliberately treats an absent private document as invalid.
	// Distinguish the FIRST missing HELD path here, before the strict reader;
	// an existing symlink, corrupt record or other I/O failure never becomes a
	// new lease. CREATE_EXCL below still arbitrates a concurrent first writer.
	if _, e := os.Lstat(path); e == nil {
		if _, e := readCanonical(path, &record); e != nil {
			return nil, e
		}
		if record.Schema != 1 || record.HeaderSHA256 != l.header || !isSHA(record.LeaseID) {
			return nil, ErrOwnedEvidence
		}
		body, _ := json.Marshal(record)
		return &AdmissionHold{launcher: l, record: record, sha: sha(append(body, '\n'))}, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return nil, e
	}
	h, _, e := l.load()
	if e != nil {
		return nil, e
	}
	var random [32]byte
	if _, e = rand.Read(random[:]); e != nil {
		return nil, e
	}
	record = holdRecord{1, l.header, hex.EncodeToString(random[:]), h.SHA256, time.Now().UTC()}
	sum, e := privateJSON(path, record)
	if e != nil {
		return nil, e
	}
	return &AdmissionHold{launcher: l, record: record, sha: sum}, nil
}
func (h *AdmissionHold) Validate() error {
	if h == nil || h.launcher == nil {
		return ErrOwnedEvidence
	}
	l := h.launcher
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.enter(); e != nil {
		return e
	}
	defer l.leave()
	var row holdRecord
	body, e := readCanonical(filepath.Join(l.dir, "HELD"), &row)
	if e != nil || sha(body) != h.sha || row != h.record {
		return ErrOwnedEvidence
	}
	return nil
}
func (h *AdmissionHold) Services() ([]*OwnedService, error) {
	if e := h.Validate(); e != nil {
		return nil, e
	}
	l := h.launcher
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.enter(); e != nil {
		return nil, e
	}
	defer l.leave()
	_, state, e := l.load()
	if e != nil {
		return nil, e
	}
	out := make([]*OwnedService, 0, len(state.Entries))
	for _, r := range state.Entries {
		s := l.children[r.Owner.Role]
		if s == nil {
			if r.Joined == nil || !sameProcess(r.Joined.Owner, r.Owner) {
				return nil, ErrOwnedEvidence
			}
			actual, identityErr := processIdentity(r.Owner.PID)
			if identityErr == nil && sameProcess(actual, r.Owner) {
				return nil, ErrOwnedEvidence
			}
			if identityErr != nil && !os.IsNotExist(identityErr) {
				return nil, identityErr
			}
			ann, server, annSHA, e := readOwnedAnnouncement(r.AnnouncementPath, r.Owner.OwnerNonce, r.Owner.Role)
			if e != nil || annSHA != r.Joined.AnnouncementSHA256 || ann.InstanceID != r.Joined.RuntimeInstanceID {
				return nil, ErrOwnedEvidence
			}
			raw, e := boundedFile(ann.DrainPath, 1024*1024, true)
			if e != nil || sha(raw) != r.Joined.DrainSHA256 {
				return nil, ErrOwnedEvidence
			}
			done := make(chan struct{})
			close(done)
			s = &OwnedService{launcher: l, owner: r.Owner, announcementPath: r.AnnouncementPath, announcement: ann, serverAnnouncement: server, announcementSHA: annSHA, done: done, joined: true, join: *r.Joined}
			l.children[r.Owner.Role] = s
		}
		if !sameProcess(s.owner, r.Owner) {
			return nil, ErrOwnedEvidence
		}
		out = append(out, s)
	}
	return out, nil
}

// Close never removes HELD or journal data. An interrupted migration remains
// closed. Resuming unknown children requires inspect/operator resolution.
func (l *Launcher) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.children) > 0 {
		for _, s := range l.children {
			select {
			case <-s.done:
			default:
				return ErrOwnedEvidence
			}
		}
	}
	if l.lock == nil {
		return nil
	}
	e := l.lock.Close()
	l.lock = nil
	return e
}

func (h *AdmissionHold) HeaderSHA256() string {
	if h == nil {
		return ""
	}
	return h.record.HeaderSHA256
}
func (h *AdmissionHold) LeaseID() string {
	if h == nil {
		return ""
	}
	return h.record.LeaseID
}
func (h *AdmissionHold) RecordSHA256() string {
	if h == nil {
		return ""
	}
	return h.sha
}

// This local capability is added only by the actual Agent after all joins,
// effective credential denials and final broker drain. A bare launcher Hold
// cannot call metadata preparation merely by knowing a digest or private path.
func (h *AdmissionHold) ValidateExclusion() error {
	if e := h.Validate(); e != nil {
		return e
	}
	if !isSHA(h.exclusionSHA) || h.exclusionAt.IsZero() || time.Since(h.exclusionAt) < 0 || time.Since(h.exclusionAt) > time.Minute {
		return ErrOwnedEvidence
	}
	return nil
}

func (l *Launcher) recordJoin(service *OwnedService, evidence JoinEvidence) (JoinEvidence, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.enter(); e != nil {
		return JoinEvidence{}, e
	}
	defer l.leave()
	h, st, e := l.load()
	if e != nil {
		return JoinEvidence{}, e
	}
	found := false
	for at, row := range st.Entries {
		if sameProcess(row.Owner, service.owner) {
			if row.Joined != nil {
				old := *row.Joined
				if old.Owner != evidence.Owner || old.RuntimeInstanceID != evidence.RuntimeInstanceID || old.AnnouncementSHA256 != evidence.AnnouncementSHA256 || old.DrainSHA256 != evidence.DrainSHA256 {
					return JoinEvidence{}, ErrOwnedEvidence
				}
				return old, nil
			}
			value := evidence
			st.Entries[at].Joined = &value
			found = true
			break
		}
	}
	if !found {
		return JoinEvidence{}, ErrOwnedEvidence
	}
	if e = l.commit(h, st); e != nil {
		return JoinEvidence{}, e
	}
	return evidence, nil
}
