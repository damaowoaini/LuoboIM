//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

// Private, real owned-process retirement. It is not a JSON/source-hash permit,
// not the local-child-only Stop receipt, and not the complete writer fence.
// The target Agent must still retire every declared writer/consumer, exclude
// credentials and drain Kafka AFTER these genuine previous readers exited.
type HistoryReaderRetirementEvidence struct {
	Observation        ObservedHistoryReader `json:"observation"`
	AnnouncementSHA256 string                `json:"announcement_sha256"`
	DrainSHA256        string                `json:"drain_sha256"`
	ExitedAt           time.Time             `json:"exited_at"`
}
type VerifiedHistoryReaderRetirement struct {
	mu           sync.Mutex
	previous     namespaceimport.Header
	graphSHA256  string
	group        *HistoryReaderLaunchGroup
	observations []ObservedHistoryReader
	processes    []HistoryReaderProcess
	attempted    []bool
	evidence     []HistoryReaderRetirementEvidence
}

// This must be called while the original operator still owns the actual
// exec.Cmd handles. Unknown/orphan PIDs are never adopted from an old file.
// Failure retains the original group and every partial private record; it
// grants no transition and cannot be retried as a different identity.
func (g *HistoryReaderLaunchGroup) BeginEpochRetirement(ctx context.Context, previous namespaceimport.Header, publishedGraphSHA string) (*VerifiedHistoryReaderRetirement, error) {
	if g == nil || ctx == nil || ctx.Done() == nil || previous.Validate() != nil || !isSHA(publishedGraphSHA) {
		return nil, ErrOwnedEvidence
	}
	g.mu.Lock()
	if g.stopping {
		g.mu.Unlock()
		return nil, ErrOwnedEvidence
	}
	g.stopping = true
	g.mu.Unlock()
	observed, e := g.Observe(ctx)
	if e != nil {
		return nil, e
	}
	rows := observed.AuditRows()
	raw, e := json.Marshal(rows)
	if e != nil || sha(raw) != publishedGraphSHA || len(rows) != 2 {
		return nil, ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	cap := &VerifiedHistoryReaderRetirement{previous: previous, graphSHA256: publishedGraphSHA, group: g, observations: rows, attempted: make([]bool, 2)}
	for n, role := range []string{"rpc-conversation", "rpc-msg"} {
		p := g.entries[role]
		if p.Service == nil || p.Bootstrap.Header != previous || rows[n].Role != role {
			return nil, ErrOwnedEvidence
		}
		cap.processes = append(cap.processes, p)
	}
	return cap, nil
}

// One original role per call. A timeout retains this same private handle and
// its durable signal intent; advancing again only waits/inspects the original
// child. It NEVER sends another SIGTERM for an unknown prior signal dispatch.
func (r *VerifiedHistoryReaderRetirement) Advance(ctx context.Context) (bool, error) {
	if r == nil || ctx == nil || ctx.Done() == nil {
		return false, ErrOwnedEvidence
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.group == nil || len(r.processes) != 2 || len(r.observations) != 2 || len(r.attempted) != 2 {
		return false, ErrOwnedEvidence
	}
	at := len(r.evidence)
	if at == 2 {
		return true, nil
	}
	if at > 2 {
		return false, ErrOwnedEvidence
	}
	r.group.mu.Lock()
	defer r.group.mu.Unlock()
	p := r.processes[at]
	s := p.Service
	expected := r.observations[at]
	owner, announcement, e := s.AwaitAnnouncement(ctx)
	if e != nil {
		return false, e
	}
	if s.serverAnnouncement == nil || s.serverAnnouncement.Mode != "required" || announcement.InstanceID != expected.Registration.InstanceID || announcement.Epoch != r.previous.Epoch || announcement.ActivationSHA256 != r.previous.ActivationSHA256 || announcement.DatabaseSHA256 != sha([]byte(r.previous.Database)) || announcement.BootstrapSHA256 != p.Bootstrap.Requirement.BootstrapSHA256 || owner.PID != expected.PID || owner.StartToken != expected.StartToken || owner.BootID != expected.BootID || sha([]byte(owner.OwnerNonce)) != expected.OwnerNonceSHA256 {
		return false, ErrOwnedEvidence
	}
	first := !r.attempted[at]
	if first {
		// Before the real signal. A failed private intent cannot authorize a fresh
		// process start or another actuator dispatch after a negative live read.
		r.attempted[at] = true
		intent := struct {
			Version              string
			PreviousHeaderSHA256 string
			GraphSHA256          string
			Observation          ObservedHistoryReader
		}{"history-reader-retirement-intent-v1", r.previous.SHA256(), r.graphSHA256, expected}
		if _, e = writeServerEnvelope(filepath.Join(r.group.dir, expected.Role+"-epoch-retirement-intent.json"), intent); e != nil {
			return false, e
		}
	}
	evidence, e := stopHistoryReaderForEpoch(ctx, s, expected, first)
	if e != nil {
		return false, e
	}
	path := filepath.Join(r.group.dir, expected.Role+"-epoch-retirement.json")
	if _, e = os.Lstat(path); os.IsNotExist(e) {
		if _, e = writeServerEnvelope(path, evidence); e != nil {
			return false, e
		}
	} else if e != nil {
		return false, e
	} else {
		var known HistoryReaderRetirementEvidence
		if _, e = readServerEnvelope(path, &known); e != nil {
			return false, e
		}
		a, _ := json.Marshal(known)
		b, _ := json.Marshal(evidence)
		if string(a) != string(b) {
			return false, ErrOwnedEvidence
		}
	}
	r.evidence = append(r.evidence, evidence)
	return len(r.evidence) == 2, nil
}

// Deliberately does NOT call Launcher.recordJoin: these replacement children
// were never members of its already-retired legacy set. The evidence is bound
// to their original private group/nonce and cannot certify other old writers.
func stopHistoryReaderForEpoch(ctx context.Context, s *OwnedService, expected ObservedHistoryReader, maySignal bool) (HistoryReaderRetirementEvidence, error) {
	var out HistoryReaderRetirementEvidence
	if s == nil || ctx == nil {
		return out, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil || s.serverAnnouncement == nil || s.announcementSHA == "" || s.owner.PID != expected.PID || s.owner.StartToken != expected.StartToken || s.owner.BootID != expected.BootID || s.owner.BinarySHA256 != expected.BinarySHA256 || s.owner.SourceSHA256 != expected.SourceSHA256 || s.owner.BuildReceiptSHA256 != expected.BuildReceiptSHA256 || sha([]byte(s.owner.OwnerNonce)) != expected.OwnerNonceSHA256 {
		return out, ErrOwnedEvidence
	}
	select {
	case <-s.done:
	default:
		live, e := processIdentity(s.owner.PID)
		if e != nil || !sameProcess(live, s.owner) || actualImage(s.owner.PID, s.owner.Executable, s.owner.BinarySHA256) != nil {
			return out, ErrOwnedEvidence
		}
		if maySignal {
			if e = s.cmd.Process.Signal(syscall.SIGTERM); e != nil {
				return out, e
			}
		}
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		return out, ctx.Err()
	}
	if s.waitErr != nil {
		return out, ErrOwnedEvidence
	}
	if _, e := processIdentity(s.owner.PID); e == nil {
		return out, ErrOwnedEvidence
	}
	drain, sum, e := ReadServerRuntimeDrain(s.announcement.DrainPath, s.owner.OwnerNonce)
	a := s.serverAnnouncement
	if e != nil || drain.Role != a.Role || drain.Mode != a.Mode || drain.InstanceID != a.InstanceID || drain.DatabaseSHA256 != a.DatabaseSHA256 || drain.Epoch != a.Epoch || drain.ActivationSHA256 != a.ActivationSHA256 || drain.BootstrapSHA256 != a.BootstrapSHA256 || drain.PolicySHA256 != a.PolicySHA256 || len(drain.JoinedAndClosed) != len(a.OwnedStopStepNames) {
		return out, ErrOwnedEvidence
	}
	for n, step := range a.OwnedStopStepNames {
		if drain.JoinedAndClosed[n] != step {
			return out, ErrOwnedEvidence
		}
	}
	exited := time.Now().UTC()
	if s.joined {
		if s.join.Owner != s.owner || s.join.DrainSHA256 != sum || s.join.AnnouncementSHA256 != s.announcementSHA || s.join.ExitedAt.IsZero() {
			return out, ErrOwnedEvidence
		}
		exited = s.join.ExitedAt
	}
	out = HistoryReaderRetirementEvidence{Observation: expected, AnnouncementSHA256: s.announcementSHA, DrainSHA256: sum, ExitedAt: exited}
	s.join = JoinEvidence{s.owner, a.InstanceID, s.announcementSHA, sum, out.ExitedAt}
	s.joined = true
	return out, nil
}

// Local-only check for a bounded Mongo retry. The live target exclusion is a
// different mandatory capability, not a timestamp copied from the old epoch.
func (r *VerifiedHistoryReaderRetirement) ValidateLocal(target *AdmissionHold, previous, next namespaceimport.Header, publishedGraphSHA string) error {
	if r == nil {
		return ErrOwnedEvidence
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if target == nil || target.ValidateExclusion() != nil || target.HeaderSHA256() != next.SHA256() || previous != r.previous || previous.Epoch == next.Epoch || previous.ActivationSHA256 == next.ActivationSHA256 || previous.Tenant != next.Tenant || previous.Database != next.Database || previous.StorageInstance != next.StorageInstance || previous.ReplicaSet != next.ReplicaSet || previous.RedisRunID != next.RedisRunID || previous.RedisDB != next.RedisDB || previous.KafkaClusterID != next.KafkaClusterID || publishedGraphSHA != r.graphSHA256 || len(r.processes) != 2 || len(r.evidence) != 2 {
		return ErrOwnedEvidence
	}
	for n, p := range r.processes {
		e := r.evidence[n]
		s := p.Service
		if s == nil || s.cmd == nil || e.ExitedAt.IsZero() || e.ExitedAt.After(target.record.HeldAt) || e.Observation.Role != []string{"rpc-conversation", "rpc-msg"}[n] || !isSHA(e.AnnouncementSHA256) || !isSHA(e.DrainSHA256) {
			return ErrOwnedEvidence
		}
		select {
		case <-s.done:
		default:
			return ErrOwnedEvidence
		}
		s.mu.Lock()
		known := s.joined && s.waitErr == nil && s.owner.PID == e.Observation.PID && s.owner.StartToken == e.Observation.StartToken && s.join.DrainSHA256 == e.DrainSHA256 && s.join.AnnouncementSHA256 == e.AnnouncementSHA256
		s.mu.Unlock()
		if !known {
			return ErrOwnedEvidence
		}
	}
	return nil
}
func (r *VerifiedHistoryReaderRetirement) Evidence() []HistoryReaderRetirementEvidence {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	body, _ := json.Marshal(r.evidence)
	var copy []HistoryReaderRetirementEvidence
	_ = json.Unmarshal(body, &copy)
	return copy
}
