//go:build darwin || linux

package namespaceimport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

const journalBytes = 256 * 1024

type journalHead struct {
	Revision       uint64 `json:"revision"`
	SHA256         string `json:"sha256"`
	PreviousSHA256 string `json:"previous_sha256"`
}
type journalEvent struct {
	PreviousSHA256 string `json:"previous_sha256"`
	State          State  `json:"state"`
}

// FileJournal has a process-held flock and immutable O_EXCL events. Recovery is
// O(1) per operation: HEAD -> one exact event, never a full history replay. A
// failed fsync/rename is ambiguous and the caller must Inspect, not dispatch again.
type FileJournal struct {
	mu     sync.Mutex
	dir    int
	lock   int
	closed bool
}

func OpenPrivateJournal(path string) (*FileJournal, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil || canonical != abs {
		return nil, ErrInvalid
	}
	dir, err := unix.Open(abs, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if unix.Fstat(dir, &st) != nil || st.Mode&0777 != 0700 || st.Uid != uint32(os.Getuid()) {
		unix.Close(dir)
		return nil, ErrInvalid
	}
	lock, err := unix.Openat(dir, "owner.lock", unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		unix.Close(dir)
		return nil, err
	}
	if unix.Fstat(lock, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 || unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB) != nil {
		unix.Close(lock)
		unix.Close(dir)
		return nil, ErrConflict
	}
	return &FileJournal{dir: dir, lock: lock}, nil
}
func strictJSON(body []byte, out any) error {
	if len(body) == 0 || len(body) > journalBytes {
		return ErrInvalid
	}
	// The standard decoder alone accepts duplicate keys. Preserve all object
	// nesting/number-range checks before decoding the typed private record.
	d := json.NewDecoder(bytes.NewReader(body))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 64 {
			return ErrInvalid
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		if delim, ok := t.(json.Delim); ok {
			switch delim {
			case '{':
				seen := map[string]bool{}
				for d.More() {
					k, err := d.Token()
					if err != nil {
						return err
					}
					key, ok := k.(string)
					if !ok || seen[key] {
						return ErrInvalid
					}
					seen[key] = true
					if err = walk(depth + 1); err != nil {
						return err
					}
				}
				end, err := d.Token()
				if err != nil || end != json.Delim('}') {
					return ErrInvalid
				}
			case '[':
				for d.More() {
					if err := walk(depth + 1); err != nil {
						return err
					}
				}
				end, err := d.Token()
				if err != nil || end != json.Delim(']') {
					return ErrInvalid
				}
			default:
				return ErrInvalid
			}
		}
		return nil
	}
	if err := walk(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	return nil
}
func (j *FileJournal) read(name string) ([]byte, error) {
	fd, err := unix.Openat(j.dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	var a, b unix.Stat_t
	if unix.Fstat(fd, &a) != nil || a.Mode&unix.S_IFMT != unix.S_IFREG || a.Mode&0777 != 0600 || a.Nlink != 1 || a.Uid != uint32(os.Getuid()) || a.Size <= 0 || a.Size > journalBytes {
		return nil, ErrInvalid
	}
	body, err := io.ReadAll(io.LimitReader(f, journalBytes+1))
	if err != nil {
		return nil, err
	}
	if unix.Fstat(fd, &b) != nil || a.Dev != b.Dev || a.Ino != b.Ino || a.Size != b.Size || len(body) != int(a.Size) {
		return nil, ErrConflict
	}
	return body, nil
}
func eventName(revision uint64, sha string) string {
	return fmt.Sprintf("event-%020d-%s.json", revision, sha)
}
func (j *FileJournal) load() (State, bool, error) {
	b, err := j.read("HEAD")
	if errorsIsMissing(err) {
		// Missing HEAD is fresh only when the directory has no earlier event or
		// temp state. Never fork a migration after a torn/removed head.
		fd, openErr := unix.Openat(j.dir, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return State{}, false, openErr
		}
		f := os.NewFile(uintptr(fd), "journal-directory")
		names, listErr := f.Readdirnames(2)
		f.Close()
		if listErr != nil && listErr != io.EOF {
			return State{}, false, listErr
		}
		for _, name := range names {
			if name != "owner.lock" {
				return State{}, false, ErrCommitUnconfirmed
			}
		}
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var h journalHead
	if strictJSON(b, &h) != nil || h.Revision == 0 || !validSHA(h.SHA256) || h.Revision == 1 && h.PreviousSHA256 != "" || h.Revision > 1 && !validSHA(h.PreviousSHA256) {
		return State{}, false, ErrConflict
	}
	b, err = j.read(eventName(h.Revision, h.SHA256))
	if err != nil {
		return State{}, false, err
	}
	var e journalEvent
	if strictJSON(b, &e) != nil || e.State.Revision != h.Revision || digest(e.State) != h.SHA256 || e.PreviousSHA256 != h.PreviousSHA256 {
		return State{}, false, ErrConflict
	}
	return clone(e.State), true, nil
}
func errorsIsMissing(err error) bool { return err == unix.ENOENT }
func (j *FileJournal) Load(ctx context.Context) (State, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return State{}, false, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return State{}, false, err
	}
	return j.load()
}
func (j *FileJournal) create(name string, body []byte) error {
	if len(body) == 0 || len(body) > journalBytes {
		return ErrInvalid
	}
	fd, err := unix.Openat(j.dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	n, err := f.Write(body)
	if err == nil && n != len(body) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	return err
}
func (j *FileJournal) CompareAndSwap(ctx context.Context, expected string, next State) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	old, found, err := j.load()
	if err != nil {
		return err
	}
	if !found && (expected != "" || next.Revision != 1) || found && (expected != digest(old) || old.Revision == ^uint64(0) || next.Revision != old.Revision+1 || next.Header != old.Header) {
		return ErrConflict
	}
	sha := digest(next)
	event, err := json.Marshal(journalEvent{expected, clone(next)})
	if err != nil {
		return err
	}
	name := eventName(next.Revision, sha)
	if err = j.create(name, event); err == unix.EEXIST {
		b, readErr := j.read(name)
		if readErr != nil || !bytes.Equal(b, event) {
			return ErrConflict
		}
	} else if err != nil {
		return err
	}
	if err = unix.Fsync(j.dir); err != nil {
		return err
	}
	h, err := json.Marshal(journalHead{next.Revision, sha, expected})
	if err != nil {
		return err
	}
	n, err := nonce()
	if err != nil {
		return err
	}
	tmp := "head-" + n + ".tmp"
	if err = j.create(tmp, h); err != nil {
		return err
	}
	if err = unix.Renameat(j.dir, tmp, j.dir, "HEAD"); err != nil {
		return err
	}
	return unix.Fsync(j.dir)
}
func (j *FileJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	unlockErr := unix.Flock(j.lock, unix.LOCK_UN)
	lockErr := unix.Close(j.lock)
	dirErr := unix.Close(j.dir)
	if unlockErr != nil {
		return unlockErr
	}
	if lockErr != nil {
		return lockErr
	}
	return dirErr
}
