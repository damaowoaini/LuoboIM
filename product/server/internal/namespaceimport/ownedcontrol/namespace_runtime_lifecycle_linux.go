//go:build linux

package ownedcontrol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport"
)

type NamespaceRuntimeStartedValidator interface {
	ValidateNamespaceRuntimeStarted(context.Context, string, []ObservedNamespaceRuntime) error
}
type NamespaceRuntimeStartedObservation struct {
	Runtime            ObservedNamespaceRuntime `json:"runtime"`
	AnnouncementSHA256 string                   `json:"announcement_sha256"`
}

// This is a real process observation, not another Ready marker. It is available
// only after the original canonical announcements, same process/image/start,
// private nonce and exact KNOWN Mongo admission have all been rechecked.
func (g *NamespaceRuntimeLaunchGroup) AwaitStarted(ctx context.Context, publicationSHA string) ([]NamespaceRuntimeStartedObservation, error) {
	return g.awaitStarted(ctx, publicationSHA, nil)
}

func (g *NamespaceRuntimeLaunchGroup) AwaitStartedWithObservation(ctx context.Context, publicationSHA string) ([]NamespaceRuntimeStartedObservation, NamespaceRuntimeCallObservation, error) {
	observation := NamespaceRuntimeCallObservation{Role: "none", Predicate: "started-input"}
	rows, err := g.awaitStarted(ctx, publicationSHA, &observation)
	return rows, observation, err
}

func (g *NamespaceRuntimeLaunchGroup) awaitStarted(ctx context.Context, publicationSHA string, observation *NamespaceRuntimeCallObservation) ([]NamespaceRuntimeStartedObservation, error) {
	observation.enter("", "started-input")
	if g == nil || ctx == nil || ctx.Done() == nil || !isSHA(publicationSHA) {
		return nil, ErrOwnedEvidence
	}
	observation.enter("", "started-validator")
	validator, ok := g.validator.(NamespaceRuntimeStartedValidator)
	if !ok {
		return nil, ErrOwnedEvidence
	}
	graph, e := g.observeWithObservation(ctx, observation)
	if e != nil && observation != nil {
		observation.enter(observation.Role, "started-observe-"+observation.Predicate)
	}
	if e != nil {
		return nil, e
	}
	rows := graph.AuditRows()
	observation.enter("", "started-publication")
	if e = validator.ValidateNamespaceRuntimeStarted(ctx, publicationSHA, rows); e != nil {
		return nil, e
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	observation.enter("", "started-state")
	if g.stopping || g.transferred {
		return nil, ErrOwnedEvidence
	}
	var observed []NamespaceRuntimeStartedObservation
	for at, r := range g.requirements {
		p := g.entries[r.Role]
		sum, e := awaitNamespaceRuntimeStartedAnnouncement(ctx, p, rows[at], r, g.header, observation)
		if e != nil {
			return nil, e
		}
		current, e := observeNamespaceRuntimeWithObservation(ctx, p, observation)
		if e != nil && observation != nil {
			observation.enter(observation.Role, "started-reobserve-"+observation.Predicate)
		}
		if e != nil {
			return nil, e
		}
		observation.enter(r.Role, "started-row-identity")
		x, _ := json.Marshal(current)
		y, _ := json.Marshal(rows[at])
		if !bytes.Equal(x, y) {
			return nil, ErrOwnedEvidence
		}
		observed = append(observed, NamespaceRuntimeStartedObservation{current, sum})
	}
	observation.enter("", "started-seal")
	raw, e := json.Marshal(observed)
	if e != nil {
		return nil, e
	}
	sum := sha(raw)
	if g.startedSHA != "" {
		if g.startedSHA != sum || g.publicationSHA != publicationSHA {
			return nil, ErrOwnedEvidence
		}
	} else {
		record := struct {
			Version, HeaderSHA256, AdmissionSHA256, GraphSHA256 string
			Observations                                        []NamespaceRuntimeStartedObservation
		}{"namespace-runtime-actual-started-v1", g.header.SHA256(), publicationSHA, sum, observed}
		path := filepath.Join(g.dir, "runtime-actual-started.json")
		observation.enter("", "started-record")
		if e = writeOrInspectRuntimeEvidence(path, record); e != nil {
			return nil, e
		}
		g.startedSHA, g.publicationSHA, g.started = sum, publicationSHA, observed
	}
	observation.enter("", "started-complete")
	return cloneRuntimeStarted(observed), nil
}

// Keep the original announcement call and all identity/mode checks together.
// A timeout/cancellation belongs to this original call, never a new evidence
// rejection; the supervisor decides whether its existing bounded turn retries.
func awaitNamespaceRuntimeStartedAnnouncement(ctx context.Context, p NamespaceRuntimeProcess, row ObservedNamespaceRuntime, r NamespaceRuntimeRequirement, h namespaceimport.Header, observation *NamespaceRuntimeCallObservation) (string, error) {
	s := p.Service
	var startup ServiceStartupObservation
	var trace *ServiceStartupObservation
	if observation != nil {
		trace = &startup
	}
	observation.enter(r.Role, "started-announcement")
	owner, a, e := s.awaitAnnouncement(ctx, trace)
	if observation != nil {
		observation.enter(r.Role, "started-announcement-"+string(startup.Stage))
	}
	if e != nil {
		return "", e
	}
	observation.enter(r.Role, "started-owner-identity")
	if owner.PID != row.PID || owner.StartToken != row.StartToken || owner.BootID != row.BootID || sha([]byte(owner.OwnerNonce)) != row.OwnerNonceSHA256 || a.InstanceID != row.Registration.InstanceID || a.DatabaseSHA256 != sha([]byte(h.Database)) || a.Epoch != h.Epoch || a.ActivationSHA256 != h.ActivationSHA256 || a.BootstrapSHA256 != r.BootstrapSHA256 {
		return "", ErrOwnedEvidence
	}
	s.mu.Lock()
	sum := s.announcementSHA
	server := s.serverAnnouncement
	s.mu.Unlock()
	observation.enter(r.Role, "started-announcement-evidence")
	if !isSHA(sum) || r.Role != "msgtransfer-managed" && (server == nil || server.Mode != "required") || r.Role == "msgtransfer-managed" && server != nil {
		return "", ErrOwnedEvidence
	}
	return sum, nil
}

func cloneRuntimeStarted(rows []NamespaceRuntimeStartedObservation) []NamespaceRuntimeStartedObservation {
	raw, _ := json.Marshal(rows)
	var copy []NamespaceRuntimeStartedObservation
	_ = json.Unmarshal(raw, &copy)
	return copy
}

// Stop remains owned even when the caller times out. An exclusive signal intent
// precedes its one actual signal; subsequent calls only wait/inspect that child.
// A partial unannounced child can be joined for cleanup, never certified drained.
func (g *NamespaceRuntimeLaunchGroup) Stop(ctx context.Context) error {
	if g == nil || ctx == nil {
		return ErrOwnedEvidence
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.transferred {
		return ErrOwnedEvidence
	}
	g.stopping = true
	if g.stopAttempts == nil {
		g.stopAttempts = map[string]bool{}
	}
	var result error
	gatewayPending := false
	for _, role := range namespaceRuntimeStopOrder(g.order) {
		if gatewayPending && gatewayPresenceDependency(role) {
			// The caller can retry the original stop. Keep these RPCs alive
			// while that gateway can still be finishing a presence write.
			continue
		}
		if e := g.stopOriginalRuntimeEntry(ctx, role); e != nil {
			// Unknown/noncanonical remains an error, but cannot strand unrelated
			// original children. Each role retains its one signal intent/owner.
			result = errors.Join(result, e)
			if recordErr := g.recordOriginalRuntimeStopFailure(role, e); recordErr != nil {
				result = errors.Join(result, recordErr)
			}
		}
		if role == "msggateway" {
			if gateway := g.entries[role].Service; gateway != nil {
				select {
				case <-gateway.done:
				default:
					gatewayPending = true
				}
			}
		}
	}
	return result
}

func gatewayPresenceDependency(role string) bool {
	switch role {
	case "rpc-user", "rpc-friend", "rpc-msg":
		return true
	}
	return false
}

func namespaceRuntimeStopOrder(start []string) []string {
	gateway := slices.Index(start, "msggateway")
	order := make([]string, 0, len(start))
	var presence []string
	for at := len(start) - 1; at >= 0; at-- {
		role := start[at]
		if gateway >= 0 && at > gateway && gatewayPresenceDependency(role) {
			presence = append(presence, role)
			continue
		}
		order = append(order, role)
		if at == gateway {
			order = append(order, presence...)
		}
	}
	return order
}

func (g *NamespaceRuntimeLaunchGroup) stopOriginalRuntimeEntry(ctx context.Context, role string) error {
	s := g.entries[role].Service
	if s == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	joined := s.joined
	owner := s.owner
	announced := s.announcementSHA != ""
	s.mu.Unlock()
	if joined {
		return nil
	}
	maySignal := !g.stopAttempts[role]
	if maySignal {
		g.stopAttempts[role] = true
		if _, e := writeServerEnvelope(filepath.Join(g.dir, role+"-stop-intent.json"), struct {
			Version string
			Owner   ProcessOwner
		}{"namespace-runtime-stop-intent-v1", owner}); e != nil {
			return e
		}
	}
	if e := stopRuntimeOwnedChild(ctx, s, maySignal, announced); e != nil {
		return e
	}
	s.mu.Lock()
	evidence := s.join
	isJoined := s.joined
	s.mu.Unlock()
	path := filepath.Join(g.dir, role+"-runtime-child-exit.json")
	record := struct {
		Version        string
		Owner          ProcessOwner
		CanonicalDrain bool
		Join           JoinEvidence
	}{"namespace-runtime-child-exit-v1", owner, isJoined, evidence}
	if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
		if _, e = writeServerEnvelope(path, record); e != nil {
			return e
		}
	} else if e != nil {
		return e
	} else {
		var old struct {
			Version        string
			Owner          ProcessOwner
			CanonicalDrain bool
			Join           JoinEvidence
		}
		if _, e = readServerEnvelope(path, &old); e != nil {
			return e
		}
		x, _ := json.Marshal(old)
		y, _ := json.Marshal(record)
		if !bytes.Equal(x, y) {
			return ErrOwnedEvidence
		}
	}
	return nil
}

// This is private failure custody only, never JoinEvidence, a drain or permit.
// No credential/error body is serialized. Unknown outcomes remain observable
// even after the other children have successfully closed their own resources.
func (g *NamespaceRuntimeLaunchGroup) recordOriginalRuntimeStopFailure(role string, cause error) error {
	code := "unconfirmed-child-stop"
	if errors.Is(cause, context.DeadlineExceeded) {
		code = "deadline"
	}
	if errors.Is(cause, context.Canceled) {
		code = "canceled"
	}
	var owner ProcessOwner
	if s := g.entries[role].Service; s != nil {
		s.mu.Lock()
		owner = s.owner
		s.mu.Unlock()
	}
	row := struct {
		Version     string
		Role        string
		Owner       ProcessOwner
		Outcome     string
		ErrorSHA256 string
	}{"namespace-runtime-stop-failure-v1", role, owner, code, sha([]byte(cause.Error()))}
	path := filepath.Join(g.dir, role+"-stop-failure.json")
	// Retain the first exact failure. A later Inspect/join cannot erase it or
	// convert it into a successful canonical drain.
	if _, e := os.Lstat(path); e == nil {
		var prior struct {
			Version     string
			Role        string
			Owner       ProcessOwner
			Outcome     string
			ErrorSHA256 string
		}
		if _, e = readServerEnvelope(path, &prior); e != nil {
			return e
		}
		a, _ := json.Marshal(prior.Owner)
		b, _ := json.Marshal(owner)
		if prior.Version != row.Version || prior.Role != role || !bytes.Equal(a, b) || !isSHA(prior.ErrorSHA256) {
			return ErrOwnedEvidence
		}
		return nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return e
	}
	_, e := writeServerEnvelope(path, row)
	return e
}

func stopRuntimeOwnedChild(ctx context.Context, s *OwnedService, maySignal, requireDrain bool) error {
	if s == nil || ctx == nil {
		return ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.joined {
		return nil
	}
	if s.cmd == nil || s.cmd.Process == nil {
		return ErrOwnedEvidence
	}
	select {
	case <-s.done:
	default:
		current, e := processIdentity(s.owner.PID)
		if e != nil || !sameProcess(current, s.owner) || actualImage(s.owner.PID, s.owner.Executable, s.owner.BinarySHA256) != nil {
			return ErrOwnedEvidence
		}
		if maySignal {
			if e = s.signalStopOnceLocked(); e != nil {
				return e
			}
		}
	}
	select {
	case <-s.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if s.waitErr != nil {
		return ErrOwnedEvidence
	}
	if _, e := processIdentity(s.owner.PID); !os.IsNotExist(e) {
		return ErrOwnedEvidence
	}
	if !requireDrain {
		return nil
	} // local cleanup only, never a canonical JoinEvidence
	a := s.announcement
	var sum string
	if s.serverAnnouncement != nil {
		d, hash, e := ReadServerRuntimeDrain(a.DrainPath, s.owner.OwnerNonce)
		original := s.serverAnnouncement
		if e != nil || d.Role != original.Role || d.Mode != original.Mode || d.InstanceID != original.InstanceID || d.DatabaseSHA256 != original.DatabaseSHA256 || d.Epoch != original.Epoch || d.ActivationSHA256 != original.ActivationSHA256 || d.BootstrapSHA256 != original.BootstrapSHA256 || d.PolicySHA256 != original.PolicySHA256 || !sameRuntimeSteps(d.JoinedAndClosed, original.OwnedStopStepNames) {
			return ErrOwnedEvidence
		}
		sum = hash
	} else {
		var d struct {
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
		raw, e := readCanonical(a.DrainPath, &d)
		if e != nil || d.Version != "managed-ordinary-drained-v1" || d.InstanceID != a.InstanceID || d.DatabaseSHA256 != a.DatabaseSHA256 || d.Epoch != a.Epoch || d.ActivationSHA256 != a.ActivationSHA256 || d.BootstrapSHA256 != a.BootstrapSHA256 || d.PolicySHA256 != a.PolicySHA256 || d.SourceOutcomeUnconfirmed || !sameRuntimeSteps(d.JoinedAndClosed, a.OwnedStopStepNames) {
			return ErrOwnedEvidence
		}
		sum = sha(raw)
	}
	s.join = JoinEvidence{s.owner, a.InstanceID, s.announcementSHA, sum, time.Now().UTC()}
	s.joined = true
	return nil
}
func sameRuntimeSteps(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

func writeOrInspectRuntimeEvidence(path string, value any) error {
	if _, e := os.Lstat(path); errors.Is(e, os.ErrNotExist) {
		_, e = writeServerEnvelope(path, value)
		return e
	} else if e != nil {
		return e
	}
	var saved json.RawMessage
	raw, e := readCanonical(path, &saved)
	if e != nil {
		return e
	}
	want, e := json.Marshal(value)
	if e != nil || !bytes.Equal(raw, append(want, '\n')) {
		return ErrOwnedEvidence
	}
	return nil
}
