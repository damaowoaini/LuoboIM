//go:build linux

package ownedcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// Transfer preserves actual exec.Cmd handles. A journal/old PID is never an
// adoption input. Readers must first finish their separate genuine retirement;
// other producers/consumers remain alive for the next Agent's ordered drain.
type NamespaceRuntimeTransfer struct {
	group                *NamespaceRuntimeLaunchGroup
	target               *Launcher
	state                launcherState
	head                 launcherHead
	attempted, committed bool
}

func (g *NamespaceRuntimeLaunchGroup) TransferToNextEpoch(ctx context.Context, next *Launcher, target namespaceimport.Header, writers []OwnedWriter) (*NamespaceRuntimeTransfer, error) {
	if g == nil || g.hold == nil || ctx == nil || next == nil || target.Validate() != nil || target.Epoch == g.header.Epoch || target.ActivationSHA256 == g.header.ActivationSHA256 || target.Database != g.header.Database || target.Tenant != g.header.Tenant || target.StorageInstance != g.header.StorageInstance || target.ReplicaSet != g.header.ReplicaSet || target.RedisRunID != g.header.RedisRunID || target.RedisDB != g.header.RedisDB || target.KafkaClusterID != g.header.KafkaClusterID || next.header != target.SHA256() || next == g.hold.launcher {
		return nil, ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.transferred || g.stopping || g.transfer != nil || !isSHA(g.startedSHA) || !isSHA(g.publicationSHA) || len(g.started) != len(NamespaceRuntimeRoles()) || len(writers) != len(g.started) {
		return nil, ErrOwnedEvidence
	}
	selected := map[string]OwnedWriter{}
	for _, w := range writers {
		if selected[w.Role].Role != "" || w.RuntimeMode != "required" || w.RuntimeEpoch != g.header.Epoch || w.RuntimeActivationSHA256 != g.header.ActivationSHA256 || !validWriterRuntime(w) {
			return nil, ErrOwnedEvidence
		}
		selected[w.Role] = w
	}
	for at, observed := range g.started {
		role := observed.Runtime.Role
		s := g.entries[role].Service
		w := selected[role]
		if s == nil || w.Role != role || w.BinarySHA256 != observed.Runtime.BinarySHA256 || w.SourceSHA256 != observed.Runtime.SourceSHA256 || w.BootstrapSHA256 != observed.Runtime.Registration.BootstrapSHA256 {
			return nil, ErrOwnedEvidence
		}
		expectedPhase := "producer"
		if role == "msgtransfer-managed" {
			expectedPhase = "consumer"
		}
		if w.Phase != expectedPhase {
			return nil, ErrOwnedEvidence
		}
		s.mu.Lock()
		joined := s.joined
		known := s.announcementSHA == observed.AnnouncementSHA256 && s.owner.PID == observed.Runtime.PID && s.owner.StartToken == observed.Runtime.StartToken && s.owner.BootID == observed.Runtime.BootID
		s.mu.Unlock()
		if !known {
			return nil, ErrOwnedEvidence
		}
		if role == "rpc-msg" || role == "rpc-conversation" {
			if !joined {
				return nil, ErrOwnedEvidence
			}
			select {
			case <-s.done:
			default:
				return nil, ErrOwnedEvidence
			}
			if _, e := processIdentity(s.owner.PID); !os.IsNotExist(e) {
				return nil, ErrOwnedEvidence
			}
		} else {
			if joined {
				return nil, ErrOwnedEvidence
			}
			actual, e := observeNamespaceRuntime(ctx, g.entries[role])
			if e != nil {
				return nil, e
			}
			x, _ := json.Marshal(actual)
			y, _ := json.Marshal(g.started[at].Runtime)
			if !bytes.Equal(x, y) {
				return nil, ErrOwnedEvidence
			}
		}
	}
	next.mu.Lock()
	defer next.mu.Unlock()
	if e := next.enter(); e != nil {
		return nil, e
	}
	defer next.leave()
	if e := next.admissionOpen(); e != nil {
		return nil, e
	}
	h, st, e := next.load()
	if e != nil || len(st.Entries) != 0 || len(next.children) != 0 {
		return nil, ErrOwnedEvidence
	}
	for _, r := range g.requirements {
		s := g.entries[r.Role].Service
		s.mu.Lock()
		row := launcherEntry{Owner: s.owner, AnnouncementPath: s.announcementPath}
		if s.joined {
			joined := s.join
			row.Joined = &joined
		}
		s.mu.Unlock()
		st.Entries = append(st.Entries, row)
	}
	st.Sequence = h.Sequence + 1
	st.PreviousSHA256 = h.SHA256
	cap := &NamespaceRuntimeTransfer{group: g, target: next, state: st, head: h}
	g.transfer = cap
	// The target is closed before writing either intent or target journal. A
	// lost fsync/HEAD ACK is inspect-only; it never permits new Start or hold.
	next.poison = true
	intent := struct {
		Version, PreviousHeaderSHA256, TargetHeaderSHA256, StartedGraphSHA256, AdmissionSHA256 string
		Entries                                                                                []launcherEntry
	}{"retained-runtime-owner-transfer-v1", g.header.SHA256(), target.SHA256(), g.startedSHA, g.publicationSHA, st.Entries}
	if _, e = writeServerEnvelope(filepath.Join(g.dir, "next-epoch-owner-transfer-intent.json"), intent); e != nil {
		return cap, e
	}
	cap.attempted = true
	if e = next.commit(h, st); e != nil {
		return cap, e
	}
	cap.attachLocked()
	return cap, nil
}
func (t *NamespaceRuntimeTransfer) attachLocked() {
	for _, r := range t.group.requirements {
		s := t.group.entries[r.Role].Service
		s.mu.Lock()
		s.launcher = t.target
		s.mu.Unlock()
		t.target.children[r.Role] = s
	}
	t.target.poison = false
	t.group.transferred = true
	t.committed = true
}

// Read only the exact private commit after an unknown journal/fsync result.
// This does not retry commit, signal, process start, capture or Mongo writes.
func (t *NamespaceRuntimeTransfer) Inspect() (bool, error) {
	if t == nil || t.group == nil || t.target == nil || !t.attempted {
		return false, ErrOwnedEvidence
	}
	g, l := t.group, t.target
	g.mu.Lock()
	defer g.mu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if g.transfer != t || g.stopping || l.lock == nil {
		return false, ErrOwnedEvidence
	}
	if e := unix.Flock(int(l.lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
		return false, e
	}
	defer l.leave()
	root, e := os.Lstat(l.dir)
	lock, e2 := os.Lstat(filepath.Join(l.dir, "launcher.lock"))
	if e != nil || e2 != nil || !os.SameFile(root, l.rootID) || !os.SameFile(lock, l.lockID) || !private(root, true) || !private(lock, false) {
		return false, ErrOwnedEvidence
	}
	h, st, e := l.load()
	if e != nil {
		return false, e
	}
	expected := t.state
	expected.Sequence = t.head.Sequence + 1
	expected.PreviousSHA256 = t.head.SHA256
	x, _ := json.Marshal(st)
	y, _ := json.Marshal(expected)
	if h.Sequence != expected.Sequence || !bytes.Equal(x, y) {
		return false, ErrOwnedEvidence
	}
	if t.committed {
		if !g.transferred {
			return false, ErrOwnedEvidence
		}
		return true, nil
	}
	t.attachLocked()
	return true, nil
}
