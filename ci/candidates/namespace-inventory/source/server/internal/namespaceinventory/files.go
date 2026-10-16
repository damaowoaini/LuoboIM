package namespaceinventory

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"golang.org/x/sys/unix"
)

const State = "untrusted-incomplete-current-inventory-only"
const MaxPrivateSnapshot = 128 << 20

func closeDirectory(fd int) { _ = unix.Close(fd) }
func requireAbsent(fd int, name string) error {
	var info unix.Stat_t
	err := unix.Fstatat(fd, name, &info, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	return ErrPrivate
}

// Reject duplicate/case-fold aliases at every object depth, not just a
// last-wins struct decode. Our schema never uses account IDs as JSON map keys.
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrInvalid
	}
	token, err := d.Token()
	if err != nil {
		return ErrInvalid
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok {
				return ErrInvalid
			}
			fold := strings.ToLower(name)
			if seen[fold] {
				return ErrInvalid
			}
			seen[fold] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return ErrInvalid
	}
	end, err := d.Token()
	if err != nil || delim == '{' && end != json.Delim('}') || delim == '[' && end != json.Delim(']') {
		return ErrInvalid
	}
	return nil
}
func DecodeStrict(raw []byte, out interface{}) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	if err := uniqueJSON(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		return ErrInvalid
	}
	return nil
}
func privateParent(path string) (int, string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return -1, "", ErrPrivate
	}
	parent := filepath.Dir(path)
	actual, err := filepath.EvalSymlinks(parent)
	if err != nil || actual != parent {
		return -1, "", ErrPrivate
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, "", ErrPrivate
	}
	for _, part := range strings.Split(strings.TrimPrefix(parent, "/"), "/") {
		if part == "" {
			continue
		}
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		closeErr := unix.Close(fd)
		if e != nil || closeErr != nil {
			if next >= 0 {
				_ = unix.Close(next)
			}
			return -1, "", ErrPrivate
		}
		fd = next
	}
	var info unix.Stat_t
	if unix.Fstat(fd, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Mode&0777 != 0700 || int(info.Uid) != os.Getuid() {
		_ = unix.Close(fd)
		return -1, "", ErrPrivate
	}
	return fd, filepath.Base(path), nil
}
func ReadPrivateDocument(path string, maximum int) (result []byte, resultErr error) {
	if maximum < 1 || maximum > MaxPrivateSnapshot {
		return nil, ErrPrivate
	}
	parent, name, err := privateParent(path)
	if err != nil {
		return nil, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrPrivate
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() {
		if file.Close() != nil {
			result = nil
			resultErr = ErrPrivate
		}
	}()
	var info unix.Stat_t
	if unix.Fstat(fd, &info) != nil || info.Mode&unix.S_IFMT != unix.S_IFREG || info.Mode&0777 != 0600 || int(info.Uid) != os.Getuid() || info.Nlink != 1 || info.Size < 1 || info.Size > int64(maximum) {
		return nil, ErrPrivate
	}
	before, err := file.Stat()
	if err != nil {
		return nil, ErrPrivate
	}
	raw, err := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	if err != nil || int64(len(raw)) != info.Size {
		return nil, ErrPrivate
	}
	after, err := file.Stat()
	var pathAfter unix.Stat_t
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() || after.Mode() != before.Mode() || !after.ModTime().Equal(before.ModTime()) || unix.Fstatat(parent, name, &pathAfter, unix.AT_SYMLINK_NOFOLLOW) != nil || pathAfter.Ino != info.Ino || pathAfter.Dev != info.Dev {
		return nil, ErrPrivate
	}
	return raw, nil
}

func validateSnapshot(s *Snapshot) error {
	if s == nil || s.Version != 1 || s.State != State || s.Created.IsZero() || !s.Identity.valid() || !hexText(s.SHA, 64) || s.SHA != snapshotDigest(*s) {
		return ErrInvalid
	}
	id, e := uuid.Parse(s.ID)
	if e != nil || id == uuid.Nil || id.String() != s.ID {
		return ErrInvalid
	}
	l := DefaultLimits()
	used := 0
	seen := map[string]bool{}
	if len(s.Mongo)+len(s.Redis) > l.Rows {
		return ErrLimit
	}
	for _, r := range s.Mongo {
		used += len(r.Raw)
		key := "mongo:" + r.Collection + ":" + r.KeySHA
		if !allowedCollection(r.Collection) || !hexText(r.KeySHA, 64) || seen[key] || len(r.Raw) > l.EntryBytes {
			return ErrInvalid
		}
		seen[key] = true
	}
	for _, r := range s.Redis {
		used += len(r.Key) + len(r.Dump) + len(r.RawHead)
		key := "redis:" + r.Key
		if !allowedKey(r.Key) || !allowedType(r.Type) || seen[key] || len(r.Key)+len(r.Dump)+len(r.RawHead) > l.EntryBytes || r.PTTL < -1 {
			return ErrInvalid
		}
		seen[key] = true
	}
	if used > l.Bytes {
		return ErrLimit
	}
	reasons := fixedReasons(s.Identity)
	if !reflect.DeepEqual(s.Reasons, reasons) {
		return ErrInvalid
	}
	// Rebuild the finite taxonomy from private raw evidence; an edited JSON
	// summary cannot inject credentials or identifiers into public reason keys.
	if !reflect.DeepEqual(s.Observations, observations(s.Mongo, s.Redis)) {
		return ErrInvalid
	}
	return nil // Integrity of an explicitly UNTRUSTED observation, not authority.
}

func SaveExclusive(path string, s *Snapshot) error {
	if err := validateSnapshot(s); err != nil {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil || len(raw) > MaxPrivateSnapshot {
		return ErrLimit
	}
	raw = append(raw, '\n')
	parent, name, err := privateParent(path)
	if err != nil {
		return err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return ErrPrivate
	}
	file := os.NewFile(uintptr(fd), name)
	n, writeErr := file.Write(raw)
	syncErr := file.Sync()
	closeErr := file.Close()
	dirErr := unix.Fsync(parent)
	if n != len(raw) || writeErr != nil || syncErr != nil || closeErr != nil || dirErr != nil {
		return ErrPrivate
	}
	return nil // On any error keep the owned partial file; never resume/overwrite.
}
func Inspect(path string) (PublicSummary, error) {
	raw, err := ReadPrivateDocument(path, MaxPrivateSnapshot)
	if err != nil {
		return PublicSummary{}, err
	}
	var s Snapshot
	if err := DecodeStrict(raw, &s); err != nil {
		return PublicSummary{}, err
	}
	if err := validateSnapshot(&s); err != nil {
		return PublicSummary{}, err
	}
	return s.Public(), nil
}
