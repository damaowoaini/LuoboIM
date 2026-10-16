package namespaceinventory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

type chunkSession struct {
	Version  int       `json:"version"`
	ID       string    `json:"id"`
	Created  time.Time `json:"createdAt"`
	State    string    `json:"state"`
	Identity Identity  `json:"identity"`
	Limits   Limits    `json:"pageLimits"`
	SHA      string    `json:"sha256"`
}
type chunkCheckpoint struct {
	Version  int    `json:"version"`
	Session  string `json:"session"`
	Sequence int64  `json:"sequence"`
	Previous string `json:"previousSHA256"`
	Pass     int    `json:"pass"`
	Page     string `json:"pageFile"`
	PageSHA  string `json:"pageSHA256"`
	SHA      string `json:"sha256"`
}
type chunkPosition struct {
	Sequence int64
	SHA      string
	Pass     int
	Cursor   PageCursor
	Complete bool
}

func jsonSHA(v interface{}) string {
	b, e := json.Marshal(v)
	if e != nil {
		return ""
	}
	return digest(b)
}
func sessionSHA(v chunkSession) string       { v.SHA = ""; return jsonSHA(v) }
func checkpointSHA(v chunkCheckpoint) string { v.SHA = ""; return jsonSHA(v) }
func sameCursor(a, b PageCursor) bool        { return jsonSHA(a) == jsonSHA(b) }
func checkpointName(n int64) string          { return fmt.Sprintf("checkpoint-%020d.json", n) }

// Existing completed files are immutable. Failed writes are preserved; no
// partial checkpoint is overwritten, guessed complete, or silently discarded.
func writePrivateJSON(path string, v interface{}) (string, error) {
	raw, e := json.Marshal(v)
	if e != nil || len(raw) >= MaxPrivateSnapshot {
		return "", ErrLimit
	}
	raw = append(raw, '\n')
	parent, name, e := privateParent(path)
	if e != nil {
		return "", e
	}
	defer unix.Close(parent)
	fd, e := unix.Openat(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if e != nil {
		return "", ErrPrivate
	}
	f := os.NewFile(uintptr(fd), name)
	n, we := f.Write(raw)
	se, ce := f.Sync(), f.Close()
	de := unix.Fsync(parent)
	if n != len(raw) || we != nil || se != nil || ce != nil || de != nil {
		return "", ErrPrivate
	}
	return digest(raw), nil
}
func loadPrivateJSON(path string, maximum int, v interface{}) error {
	b, e := ReadPrivateDocument(path, maximum)
	if e != nil {
		return e
	}
	return DecodeStrict(b, v)
}
func validUUID(s string) bool {
	id, e := uuid.Parse(s)
	return e == nil && id != uuid.Nil && id.String() == s
}
func pageLimits(l Limits) bool { return l.valid() && l.Rows <= 4096 }
func nextPosition(p chunkPosition, page Page) chunkPosition {
	p.Cursor = page.After
	if page.After.Phase == "done" {
		if p.Pass == 1 {
			p.Pass = 2
			p.Cursor = firstPageCursor()
		} else {
			p.Complete = true
		}
	}
	return p
}

func validatePage(page Page, l Limits) error {
	if !page.Before.valid() || !page.After.valid() || page.Before.Phase == "done" || sameCursor(page.Before, page.After) || len(page.Mongo)+len(page.Redis) > l.Rows {
		return ErrInvalid
	}
	if page.Before.Phase == "mongo" {
		if len(page.Redis) != 0 || page.After.Phase == "done" {
			return ErrInvalid
		}
		if page.After.Phase == "redis" && page.Before.Collection != len(collections)-1 {
			return ErrInvalid
		}
		if page.After.Phase == "mongo" && (page.After.Collection < page.Before.Collection || page.After.Collection > page.Before.Collection+1) {
			return ErrInvalid
		}
	} else {
		if len(page.Mongo) != 0 || page.After.Phase == "mongo" {
			return ErrInvalid
		}
		if page.After.Phase == "done" && page.Before.Prefix != len(prefixes)-1 {
			return ErrInvalid
		}
		if page.After.Phase == "redis" && (page.After.Prefix < page.Before.Prefix || page.After.Prefix > page.Before.Prefix+1) {
			return ErrInvalid
		}
	}
	used := 0
	for _, row := range page.Mongo {
		if !allowedCollection(row.Collection) || !hexText(row.KeySHA, 64) || len(row.Raw) > l.EntryBytes || used > l.Bytes-len(row.Raw) {
			return ErrLimit
		}
		used += len(row.Raw)
	}
	for _, row := range page.Redis {
		n := len(row.Key) + len(row.Dump) + len(row.RawHead)
		if !allowedKey(row.Key) || !allowedType(row.Type) || row.PTTL < -1 || n > l.EntryBytes || used > l.Bytes-n {
			return ErrLimit
		}
		used += n
	}
	return nil
}

func loadSession(dir string) (chunkSession, error) {
	var s chunkSession
	if e := loadPrivateJSON(filepath.Join(dir, "session.json"), 64<<10, &s); e != nil {
		return s, e
	}
	if s.Version != 1 || !validUUID(s.ID) || s.Created.IsZero() || s.State != State || !s.Identity.valid() || !pageLimits(s.Limits) || !hexText(s.SHA, 64) || s.SHA != sessionSHA(s) {
		return s, ErrInvalid
	}
	return s, nil
}

func lockJournal(dir string) (*os.File, error) {
	parent, name, e := privateParent(filepath.Join(dir, "lock"))
	if e != nil {
		return nil, e
	}
	defer unix.Close(parent)
	fd, e := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if e != nil {
		return nil, ErrPrivate
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0777 != 0600 || int(st.Uid) != os.Getuid() || st.Nlink != 1 || unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		_ = unix.Close(fd)
		return nil, ErrPrivate
	}
	return os.NewFile(uintptr(fd), name), nil
}

// A resume follows only the contiguous, authenticated-by-hash LOCAL chain.
// Hashes bind private file integrity; they are NOT a signed drain attestation.
// A complete next checkpoint survives a lost CLI ACK. A partial checkpoint
// stops safely. Orphan page files are preserved, not used as committed rows.
func replayJournal(ctx context.Context, dir string, s chunkSession, visit func(Page, int) error) (chunkPosition, error) {
	p := chunkPosition{SHA: s.SHA, Pass: 1, Cursor: firstPageCursor()}
	for {
		if e := ctx.Err(); e != nil {
			return p, e
		}
		if p.Sequence >= math.MaxInt64-1 {
			return p, ErrLimit
		}
		path := filepath.Join(dir, checkpointName(p.Sequence+1))
		if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
			return p, nil
		} else if e != nil {
			return p, ErrPrivate
		}
		if p.Complete {
			return p, ErrInvalid
		}
		var c chunkCheckpoint
		if e := loadPrivateJSON(path, 64<<10, &c); e != nil {
			return p, e
		}
		if c.Version != 1 || c.Session != s.ID || c.Sequence != p.Sequence+1 || c.Pass != p.Pass || c.Previous != p.SHA || !hexText(c.PageSHA, 64) || c.SHA != checkpointSHA(c) || len(c.Page) != len("page-")+36+len(".json") || c.Page[:5] != "page-" || c.Page[len(c.Page)-5:] != ".json" || !validUUID(c.Page[5:len(c.Page)-5]) {
			return p, ErrInvalid
		}
		raw, e := ReadPrivateDocument(filepath.Join(dir, c.Page), MaxPrivateSnapshot)
		if e != nil {
			return p, e
		}
		if digest(raw) != c.PageSHA {
			return p, ErrInvalid
		}
		var page Page
		if DecodeStrict(raw, &page) != nil || validatePage(page, s.Limits) != nil || !sameCursor(page.Before, p.Cursor) {
			return p, ErrInvalid
		}
		if e := visit(page, p.Pass); e != nil {
			return p, e
		}
		p.Sequence, p.SHA = c.Sequence, c.SHA
		p = nextPosition(p, page)
	}
}

func createJournal(dir string, s chunkSession) error {
	parent, name, e := privateParent(dir)
	if e != nil {
		return e
	}
	defer unix.Close(parent)
	if unix.Mkdirat(parent, name, 0700) != nil {
		return ErrPrivate
	}
	if unix.Fsync(parent) != nil {
		return ErrPrivate
	}
	if _, e := writePrivateJSON(filepath.Join(dir, "session.json"), s); e != nil {
		return e
	}
	_, e = writePrivateJSON(filepath.Join(dir, "lock"), struct {
		Version int `json:"version"`
	}{1})
	return e
}

func appendCheckpoint(dir string, s chunkSession, p chunkPosition, page Page) (chunkPosition, error) {
	if p.Complete || p.Sequence >= math.MaxInt64-1 || !sameCursor(page.Before, p.Cursor) || validatePage(page, s.Limits) != nil {
		return p, ErrInvalid
	}
	id, e := uuid.NewRandom()
	if e != nil {
		return p, ErrSource
	}
	filename := "page-" + id.String() + ".json"
	h, e := writePrivateJSON(filepath.Join(dir, filename), page)
	if e != nil {
		return p, e
	}
	c := chunkCheckpoint{Version: 1, Session: s.ID, Sequence: p.Sequence + 1, Previous: p.SHA, Pass: p.Pass, Page: filename, PageSHA: h}
	c.SHA = checkpointSHA(c)
	if _, e := writePrivateJSON(filepath.Join(dir, checkpointName(c.Sequence)), c); e != nil {
		return p, e
	}
	p.Sequence, p.SHA = c.Sequence, c.SHA
	return nextPosition(p, page), nil
}
