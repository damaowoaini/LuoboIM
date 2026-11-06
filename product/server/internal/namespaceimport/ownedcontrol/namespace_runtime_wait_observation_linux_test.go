//go:build linux

package ownedcontrol

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestNamespaceRuntimeCachedWaitKeepsRealExitSignalPendingAndOriginalStopDistinct(t *testing.T) {
	if mode := os.Getenv("LUOBO_CACHED_WAIT_TEST_CHILD"); mode != "" {
		var gate [1]byte
		if _, err := io.ReadFull(os.Stdin, gate[:]); err != nil {
			os.Exit(68)
		}
		switch mode {
		case "zero":
			os.Exit(0)
		case "one":
			os.Exit(1)
		case "term":
			_ = syscall.Kill(os.Getpid(), syscall.SIGTERM)
			select {}
		default:
			os.Exit(69)
		}
	}
	start := func(t *testing.T, mode string) (*NamespaceRuntimeLaunchGroup, *OwnedService, func()) {
		t.Helper()
		executable, err := os.Executable()
		if err != nil {
			t.Fatal("original executable unavailable")
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			t.Fatal("original executable identity unavailable")
		}
		cmd := exec.Command(executable, "-test.run=^TestNamespaceRuntimeCachedWaitKeepsRealExitSignalPendingAndOriginalStopDistinct$")
		cmd.Env = []string{"LUOBO_CACHED_WAIT_TEST_CHILD=" + mode, "GOTRACEBACK=none"}
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		gate, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal("original gate unavailable")
		}
		if err = cmd.Start(); err != nil {
			gate.Close()
			t.Fatal("original child unavailable")
		}
		owner, err := processIdentity(cmd.Process.Pid)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			gate.Close()
			t.Fatal("original child identity unavailable")
		}
		required := NamespaceRuntimeRequirement{Role: "api", BinarySHA256: strings.Repeat("a", 64), SourceSHA256: strings.Repeat("b", 64), BuildReceiptSHA256: strings.Repeat("c", 64), BootstrapSHA256: strings.Repeat("d", 64)}
		owner.Role, owner.BinarySHA256, owner.SourceSHA256, owner.BuildReceiptSHA256, owner.BootstrapSHA256, owner.OwnerNonce = required.Role, required.BinarySHA256, required.SourceSHA256, required.BuildReceiptSHA256, required.BootstrapSHA256, strings.Repeat("e", 64)
		service := &OwnedService{cmd: cmd, owner: owner, done: make(chan struct{})}
		group := &NamespaceRuntimeLaunchGroup{requirements: []NamespaceRuntimeRequirement{required}, entries: map[string]NamespaceRuntimeProcess{"api": {Service: service}}}
		go func() {
			service.waitErr = cmd.Wait()
			if mode == "one" {
				service.waitErr = errors.Join(service.waitErr, ErrOwnedEvidence, errors.New("PRIVATE_WAIT_ERROR"))
			}
			close(service.done)
		}()
		t.Cleanup(func() {
			_ = gate.Close()
			select {
			case <-service.done:
			default:
				_ = cmd.Process.Kill()
				select {
				case <-service.done:
				case <-time.After(3 * time.Second):
					t.Error("original test child did not join")
				}
			}
		})
		release := func() { _, _ = gate.Write([]byte{1}); _ = gate.Close() }
		return group, service, release
	}
	await := func(t *testing.T, s *OwnedService) {
		t.Helper()
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
			t.Fatal("original cached Wait did not return")
		}
	}
	t.Run("real-original-wait-exit-signal-and-private-error", func(t *testing.T) {
		for _, mode := range []string{"zero", "one", "term"} {
			group, service, release := start(t, mode)
			release()
			await(t, service)
			state := service.cmd.ProcessState
			waitErr := service.waitErr
			rows := group.CachedWaitObservations()
			if len(rows) != 1 || rows[0].OwnerState != "matched" || rows[0].WaitState != "returned" || rows[0].StopIntent != "not-attempted" || rows[0].JoinRecorded || service.stopSignalAttempted || service.joined || service.cmd.ProcessState != state || service.waitErr != waitErr {
				t.Fatal("original wait identity or lifecycle changed")
			}
			row := rows[0]
			switch mode {
			case "zero":
				if row.ProcessState != "exited" || row.ExitCode == nil || *row.ExitCode != 0 || row.WaitErrorClass != "none" || row.Signal != "none" {
					t.Fatal("actual exit0 lost")
				}
			case "one":
				if row.ProcessState != "exited" || row.ExitCode == nil || *row.ExitCode != 1 || row.WaitErrorClass != "exit-error-with-owned-evidence" {
					t.Fatal("actual exit1 and output evidence lost")
				}
			case "term":
				if row.ProcessState != "signaled" || row.ExitCode != nil || row.Signal != "SIGTERM" || row.WaitErrorClass != "exit-error" {
					t.Fatal("actual original signal lost")
				}
			}
			raw, err := json.Marshal(rows)
			if err != nil || strings.Contains(string(raw), "PRIVATE_WAIT_ERROR") || strings.Contains(string(raw), "exit status") {
				t.Fatal("raw Wait text escaped")
			}
		}
	})
	t.Run("pending-owner-rejection-concurrent-wait-and-original-stop-intent", func(t *testing.T) {
		group, service, _ := start(t, "zero")
		snapshot := make(chan []NamespaceRuntimeCachedWaitObservation, 1)
		go func() { snapshot <- group.CachedWaitObservations() }()
		select {
		case rows := <-snapshot:
			if len(rows) != 1 || rows[0].WaitState != "pending" || rows[0].ProcessState != "unobserved" || rows[0].WaitErrorClass != "unobserved" || rows[0].ExitCode != nil || rows[0].StopIntent != "not-attempted" {
				t.Fatal("pending child was observed as an exit")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cached snapshot waited on original child")
		}
		service.mu.Lock()
		original := service.owner
		service.owner.SourceSHA256 = strings.Repeat("f", 64)
		service.mu.Unlock()
		rows := group.CachedWaitObservations()
		if rows[0].OwnerState != "owner-mismatch" || rows[0].Owner != nil || rows[0].WaitState != "unobserved" || rows[0].StopIntent != "unobserved" {
			t.Fatal("wrong original owner disclosed an outcome")
		}
		service.mu.Lock()
		service.owner = original
		service.mu.Unlock()
		group.requirements = append(group.requirements, NamespaceRuntimeRequirement{Role: "push"})
		rows = group.CachedWaitObservations()
		if len(rows) != 2 || rows[1].OwnerState != "not-launched" || rows[1].WaitState != "unobserved" {
			t.Fatal("missing original child looked exited")
		}
		service.mu.Lock()
		err := service.signalStopOnceLocked()
		service.mu.Unlock()
		if err != nil {
			t.Fatal("original test stop unavailable")
		}
		// The original Wait writes concurrently. Every snapshot may read its state
		// only after done closes; race builds check this actual handoff.
		for n := 0; n < 32; n++ {
			rows = group.CachedWaitObservations()
			if rows[0].StopIntent != "attempted" {
				t.Fatal("original stop intent lost")
			}
		}
		await(t, service)
		rows = group.CachedWaitObservations()
		if rows[0].WaitState != "returned" || rows[0].ProcessState != "signaled" || rows[0].Signal != "SIGTERM" || rows[0].StopIntent != "attempted" || service.joined {
			t.Fatal("original stop and actual signal were conflated with a clean drain")
		}
		service.mu.Lock()
		err = service.signalStopOnceLocked()
		service.mu.Unlock()
		if err != nil {
			t.Fatal("existing single-signal intent changed")
		}
	})
}
