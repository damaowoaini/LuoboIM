//go:build linux

package ownedcontrol

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// LocalChildExit only describes the retained exec.Cmd's OS exit. It is not a
// JoinEvidence, canonical service drain, exclusion observation or admission.
type LocalChildExit struct {
	Version          string       `json:"version"`
	Owner            ProcessOwner `json:"owner"`
	ObservedAbsentAt time.Time    `json:"observedAbsentAt"`
	WaitSucceeded    bool         `json:"waitSucceeded"`
	WaitErrorSHA256  string       `json:"waitErrorSHA256,omitempty"`
}

// StopForLocalCleanup is for a failed partial deployment, including a child
// which never reached announcement. Only an original retained exec handle can
// signal. The immutable intent is written before the single signal: after a
// lost result, a later call can only join/inspect, never signal a second time.
// No call changes s.joined or the durable launcher's canonical Joined state.
// All three original Stop/Wait paths share this retained object and its mu.
// Mark BEFORE Signal: a timeout/error is unknown, so a later path can only
// wait/inspect. No reconstructed object/PID is admitted after operator death.
func (s *OwnedService) signalStopOnceLocked() error {
	if s.cmd == nil || s.cmd.Process == nil {
		return ErrOwnedEvidence
	}
	if s.stopSignalAttempted {
		return nil
	}
	s.stopSignalAttempted = true
	return s.cmd.Process.Signal(syscall.SIGTERM)
}

func (s *OwnedService) StopForLocalCleanup(ctx context.Context) (LocalChildExit, error) {
	var empty LocalChildExit
	if s == nil || ctx == nil {
		return empty, ErrOwnedEvidence
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cmd == nil || s.cmd.Process == nil {
		return empty, ErrOwnedEvidence
	}
	// Original Start already attempted its single signal before returning an
	// unbound owner. We may join that exact cmd, never mint identity or re-signal.
	if s.owner.PID <= 1 {
		select {
		case <-s.done:
		case <-ctx.Done():
			return empty, ctx.Err()
		}
		if _, e := processIdentity(s.cmd.Process.Pid); !os.IsNotExist(e) {
			return empty, ErrOwnedEvidence
		}
		result := LocalChildExit{Version: "owned-local-unbound-child-exited-v1", Owner: s.owner, ObservedAbsentAt: time.Now().UTC(), WaitSucceeded: s.waitErr == nil}
		if s.waitErr != nil {
			result.WaitErrorSHA256 = sha([]byte(s.waitErr.Error()))
		}
		return result, ErrOwnedEvidence
	}
	if s.cmd.Process.Pid != s.owner.PID || !isSHA(s.owner.OwnerNonce) || !isSHA(s.owner.BinarySHA256) || !filepath.IsAbs(s.announcementPath) {
		return empty, ErrOwnedEvidence
	}
	type intent struct {
		Version string       `json:"version"`
		Owner   ProcessOwner `json:"owner"`
	}
	path := filepath.Join(filepath.Dir(s.announcementPath), "local-cleanup-"+s.owner.OwnerNonce+".json")
	expected := intent{"owned-local-child-cleanup-v1", s.owner}
	maySignal := false
	select {
	case <-s.done:
	default:
		// Once an original signal was attempted, there is no new OS action to
		// authorize. Only the retained Wait and actual PID absence may finish it.
		if !s.stopSignalAttempted {
			current, e := processIdentity(s.owner.PID)
			if e != nil || !sameProcess(current, s.owner) {
				e = ErrOwnedEvidence
			} else {
				e = actualImageContext(ctx, s.owner.PID, s.owner.Executable, s.owner.BinarySHA256)
			}
			if e != nil {
				// The original cmd may finish while procfs/image checks are in
				// progress. This permits only its local exit path, never a signal.
				select {
				case <-s.done:
				default:
					return empty, e
				}
			} else {
				select {
				case <-s.done:
				default:
					if e = ctx.Err(); e != nil {
						return empty, e
					}
					if _, e = os.Lstat(path); errors.Is(e, os.ErrNotExist) {
						if _, e = privateJSON(path, expected); e != nil {
							return empty, e
						}
						maySignal = true
					} else if e != nil {
						return empty, e
					} else {
						var saved intent
						if _, e = readCanonical(path, &saved); e != nil || saved != expected {
							return empty, ErrOwnedEvidence
						}
					}
					if maySignal {
						if e = s.signalStopOnceLocked(); e != nil {
							return empty, e
						}
					}
				}
			}
		}
	}
	select {
	case <-s.done:
	default:
		select {
		case <-s.done:
		case <-ctx.Done():
			select {
			case <-s.done:
			default:
				return empty, ctx.Err()
			}
		}
	}
	if _, e := processIdentity(s.owner.PID); !os.IsNotExist(e) {
		return empty, ErrOwnedEvidence
	}
	exitPath := filepath.Join(filepath.Dir(s.announcementPath), "local-exit-"+s.owner.OwnerNonce+".json")
	if _, e := os.Lstat(exitPath); e == nil {
		var saved LocalChildExit
		if _, e = readCanonical(exitPath, &saved); e != nil || saved.Version != "owned-local-child-exited-v1" || saved.Owner != s.owner || saved.ObservedAbsentAt.IsZero() || saved.WaitSucceeded != (s.waitErr == nil) {
			return empty, ErrOwnedEvidence
		}
		if s.waitErr != nil && saved.WaitErrorSHA256 != sha([]byte(s.waitErr.Error())) {
			return empty, ErrOwnedEvidence
		}
		if !saved.WaitSucceeded {
			return saved, ErrOwnedEvidence
		}
		return saved, nil
	} else if !errors.Is(e, os.ErrNotExist) {
		return empty, e
	}
	result := LocalChildExit{Version: "owned-local-child-exited-v1", Owner: s.owner, ObservedAbsentAt: time.Now().UTC(), WaitSucceeded: s.waitErr == nil}
	if s.waitErr != nil {
		result.WaitErrorSHA256 = sha([]byte(s.waitErr.Error()))
	}
	if _, e := privateJSON(exitPath, result); e != nil {
		return empty, e
	}
	if !result.WaitSucceeded {
		return result, ErrOwnedEvidence
	}
	return result, nil
}
