//go:build linux

package ownedcontrol

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// This record stays PRIVATE. The collector must match Owner to the original
// private owner record before projecting only closed outcome labels. It grants
// no PID absence, drain, image, namespace admission, Ready or business authority.
type NamespaceRuntimeCachedWaitObservation struct {
	Role           string        `json:"role"`
	OwnerState     string        `json:"ownerState"`
	Owner          *ProcessOwner `json:"owner,omitempty"`
	WaitState      string        `json:"waitState"`
	WaitErrorClass string        `json:"waitErrorClass"`
	ProcessState   string        `json:"processState"`
	ExitCode       *int          `json:"exitCode,omitempty"`
	Signal         string        `json:"signal"`
	StopIntent     string        `json:"stopIntent"`
	JoinRecorded   bool          `json:"joinRecorded"`
}

// CachedWaitObservations reads only already retained owners. g.mu excludes the
// group's original Stop; each s.mu synchronizes its existing signal intent.
// The original done channel is the sole barrier for reading Cmd.ProcessState
// and waitErr, both written by the one original Wait goroutine. Pending never
// reads either field. This does not wait, probe a PID/image, send a signal,
// change a join, retry admission or claim which child physically failed first.
func (g *NamespaceRuntimeLaunchGroup) CachedWaitObservations() []NamespaceRuntimeCachedWaitObservation {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	rows := make([]NamespaceRuntimeCachedWaitObservation, 0, len(g.requirements))
	for _, required := range g.requirements {
		row := NamespaceRuntimeCachedWaitObservation{Role: required.Role, OwnerState: "not-launched", WaitState: "unobserved", WaitErrorClass: "unobserved", ProcessState: "unobserved", Signal: "unobserved", StopIntent: "unobserved"}
		service := g.entries[required.Role].Service
		if service == nil {
			rows = append(rows, row)
			continue
		}
		service.mu.Lock()
		if service.cmd == nil || service.cmd.Process == nil || service.owner.PID != service.cmd.Process.Pid || service.owner.UID != uint32(os.Getuid()) || service.owner.Role != required.Role || service.owner.Executable != service.cmd.Path || service.owner.StartToken == "" || service.owner.BootID == "" || !isSHA(service.owner.OwnerNonce) || service.owner.BinarySHA256 != required.BinarySHA256 || service.owner.SourceSHA256 != required.SourceSHA256 || service.owner.BuildReceiptSHA256 != required.BuildReceiptSHA256 || service.owner.BootstrapSHA256 != required.BootstrapSHA256 {
			row.OwnerState = "owner-mismatch"
			service.mu.Unlock()
			rows = append(rows, row)
			continue
		}
		owner := service.owner
		row.OwnerState, row.Owner, row.JoinRecorded = "matched", &owner, service.joined
		row.StopIntent = "not-attempted"
		if service.stopSignalAttempted {
			row.StopIntent = "attempted"
		}
		row.WaitState = "pending"
		select {
		case <-service.done:
			row.WaitState = "returned"
			row.WaitErrorClass = cachedRuntimeWaitErrorClass(service.waitErr)
			row.ProcessState, row.Signal = "absent", "none"
			if state := service.cmd.ProcessState; state != nil {
				row.ProcessState = "other"
				if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
					row.ProcessState = "signaled"
					row.Signal = cachedRuntimeSignalClass(status.Signal())
				} else if state.Exited() {
					row.ProcessState = "exited"
					code := state.ExitCode()
					if code >= 0 && code <= 255 {
						row.ExitCode = &code
					}
				}
			}
		default:
		}
		service.mu.Unlock()
		rows = append(rows, row)
	}
	return rows
}

func cachedRuntimeWaitErrorClass(err error) string {
	if err == nil {
		return "none"
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit != nil {
		if errors.Is(err, ErrOwnedEvidence) {
			return "exit-error-with-owned-evidence"
		}
		return "exit-error"
	}
	if errors.Is(err, ErrOwnedEvidence) {
		return "owned-evidence"
	}
	return "other"
}
func cachedRuntimeSignalClass(signal syscall.Signal) string {
	switch signal {
	case syscall.SIGHUP:
		return "SIGHUP"
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGQUIT:
		return "SIGQUIT"
	case syscall.SIGILL:
		return "SIGILL"
	case syscall.SIGTRAP:
		return "SIGTRAP"
	case syscall.SIGABRT:
		return "SIGABRT"
	case syscall.SIGBUS:
		return "SIGBUS"
	case syscall.SIGFPE:
		return "SIGFPE"
	case syscall.SIGKILL:
		return "SIGKILL"
	case syscall.SIGSEGV:
		return "SIGSEGV"
	case syscall.SIGPIPE:
		return "SIGPIPE"
	case syscall.SIGALRM:
		return "SIGALRM"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGXCPU:
		return "SIGXCPU"
	case syscall.SIGXFSZ:
		return "SIGXFSZ"
	case syscall.SIGSYS:
		return "SIGSYS"
	default:
		return "other"
	}
}
