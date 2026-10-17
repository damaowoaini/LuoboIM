//go:build linux

package runtimeacceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A retained local child only, not a service/Ready/authority fixture. It waits
// after real SIGTERM until the original test releases it, making cross-path
// repeated signals observable without DB, RPC or a manufactured drain.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--luobo-local-cleanup-child" {
		var v struct {
			Signal, Release, Ready string
			Exit                   int
		}
		b, e := os.ReadFile(os.Getenv("LUOBO_OWNED_SERVER_BOOTSTRAP_PATH"))
		if e != nil || json.Unmarshal(b, &v) != nil {
			os.Exit(93)
		}
		ch := make(chan os.Signal, 8)
		signal.Notify(ch, syscall.SIGTERM)
		defer signal.Stop(ch)
		if os.WriteFile(v.Ready, []byte("ready"), 0600) != nil {
			os.Exit(94)
		}
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		n := 0
		for {
			select {
			case <-ch:
				n++
				if os.WriteFile(v.Signal, []byte(strconv.Itoa(n)), 0600) != nil {
					os.Exit(95)
				}
			case <-tick.C:
				if _, e = os.Stat(v.Release); e == nil {
					os.Exit(v.Exit)
				}
			}
		}
	}
	os.Exit(m.Run())
}
func waitCleanupFile(t *testing.T, path string) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		if _, e := os.Stat(path); e == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("retained child phase missing")
}

type runtimeCleanupDiagnostic struct {
	Version            string `json:"version"`
	Kind               string `json:"kind"`
	Stage              string `json:"stage"`
	ErrorClass         string `json:"errorClass"`
	ShortErrorClass    string `json:"shortErrorClass"`
	Role               string `json:"role"`
	BinaryBytes        int    `json:"binaryBytes"`
	ChildReturned      bool   `json:"childReturned"`
	FinalReturned      bool   `json:"finalReturned"`
	ObservedExits      int    `json:"observedExits"`
	Complete           bool   `json:"complete"`
	LauncherClosed     bool   `json:"launcherClosed"`
	Signal0            string `json:"signal0"`
	Signal1            string `json:"signal1"`
	PrimaryPreserved   bool   `json:"primaryPreserved"`
	ElapsedMicros      int64  `json:"elapsedMicros"`
	FirstStopMicros    int64  `json:"firstStopMicros"`
	FinalCleanupMicros int64  `json:"finalCleanupMicros"`
}

func runtimeCleanupErrorClass(e error) string {
	switch {
	case e == nil:
		return "none"
	case errors.Is(e, context.DeadlineExceeded):
		return "deadline"
	case errors.Is(e, context.Canceled):
		return "cancelled"
	case errors.Is(e, ownedcontrol.ErrOwnedEvidence):
		return "owned-evidence"
	case errors.Is(e, os.ErrNotExist):
		return "not-exist"
	case errors.Is(e, os.ErrPermission):
		return "permission"
	default:
		return "other"
	}
}

func cleanupSessionChildren(t *testing.T, observation *runtimeCleanupDiagnostic) (*Session, []string, []string) {
	t.Helper()
	observation.Stage = "private-directory"
	base := t.TempDir()
	if e := os.Chmod(base, 0700); e != nil {
		observation.ErrorClass = runtimeCleanupErrorClass(e)
		t.Fatal(e)
	}
	dir := filepath.Join(base, "launcher")
	if e := os.Mkdir(dir, 0700); e != nil {
		observation.ErrorClass = runtimeCleanupErrorClass(e)
		t.Fatal(e)
	}
	observation.Stage = "launcher-open"
	launcher, e := ownedcontrol.OpenOwnedLauncher(dir, strings.Repeat("a", 64))
	if e != nil {
		observation.ErrorClass = runtimeCleanupErrorClass(e)
		t.Fatal(e)
	}
	session := &Session{inputs: Inputs{Launcher: launcher}}
	releases, counts := []string{}, []string{}
	t.Cleanup(func() {
		for _, p := range releases {
			_ = os.WriteFile(p, []byte("release"), 0600)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = session.Cleanup(ctx)
	})
	observation.Stage = "executable-path"
	binary, e := os.Executable()
	if e != nil {
		observation.ErrorClass = runtimeCleanupErrorClass(e)
		t.Fatal(e)
	}
	observation.Stage = "executable-canonical"
	binary, e = filepath.EvalSymlinks(binary)
	if e != nil {
		observation.ErrorClass = runtimeCleanupErrorClass(e)
		t.Fatal(e)
	}
	observation.Stage = "executable-read"
	body, e := os.ReadFile(binary)
	if e != nil {
		observation.ErrorClass = runtimeCleanupErrorClass(e)
		t.Fatal(e)
	}
	observation.BinaryBytes = len(body)
	for at, role := range []string{"rpc-msg", "msgtransfer-legacy"} {
		observation.Role = role
		observation.Stage = "child-directory"
		work := filepath.Join(base, role)
		if e = os.Mkdir(work, 0700); e != nil {
			observation.ErrorClass = runtimeCleanupErrorClass(e)
			t.Fatal(e)
		}
		count, release, ready := filepath.Join(work, "signals"), filepath.Join(work, "release"), filepath.Join(work, "ready")
		releases = append(releases, release)
		counts = append(counts, count)
		code := 0
		if at == 1 {
			code = 7
		}
		observation.Stage = "bootstrap-write"
		boot, e := writePrivate(filepath.Join(work, "bootstrap.json"), struct {
			Signal, Release, Ready string
			Exit                   int
		}{count, release, ready, code})
		if e != nil {
			observation.ErrorClass = runtimeCleanupErrorClass(e)
			t.Fatal(e)
		}
		observation.Stage = "receipt-write"
		receipt, e := writePrivate(filepath.Join(work, "build.json"), struct {
			Schema int    `json:"schema"`
			Source string `json:"sourceSHA256"`
			Binary string `json:"binarySHA256"`
			Bytes  int    `json:"binaryBytes"`
			Tool   string `json:"toolchainSHA256"`
		}{1, strings.Repeat("b", 64), digestBytes(body), len(body), strings.Repeat("c", 64)})
		if e != nil {
			observation.ErrorClass = runtimeCleanupErrorClass(e)
			t.Fatal(e)
		}
		observation.Stage = "child-start"
		child, e := launcher.Start(context.Background(), ownedcontrol.ServiceSpec{Role: role, BinaryPath: binary, BinarySHA256: digestBytes(body), SourceSHA256: strings.Repeat("b", 64), BuildReceiptSHA256: receipt, BuildReceiptPath: filepath.Join(work, "build.json"), BootstrapPath: filepath.Join(work, "bootstrap.json"), BootstrapSHA256: boot, BootstrapEnvironmentKey: "LUOBO_OWNED_SERVER_BOOTSTRAP_PATH", BootstrapSHAEnvironmentKey: "LUOBO_OWNED_SERVER_BOOTSTRAP_SHA256", Work: work, Args: []string{"--luobo-local-cleanup-child"}})
		observation.ChildReturned = child != nil
		if child != nil {
			session.Legacy = append(session.Legacy, child)
		}
		if e != nil {
			observation.ErrorClass = runtimeCleanupErrorClass(e)
			t.Fatal(e)
		}
		observation.Stage = "child-ready"
		waitCleanupFile(t, ready)
	}
	observation.Stage = "fixture-ready"
	observation.Role = "none"
	return session, releases, counts
}
func TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup(t *testing.T) {
	for _, kind := range []string{"cancel-audit-before-stop", "timeout-before-local-wait-complete"} {
		t.Run(kind, func(t *testing.T) {
			observedAt := time.Now()
			observation := runtimeCleanupDiagnostic{Version: "runtime-cleanup-fixture-observation-v1", Kind: kind, Stage: "begin", ErrorClass: "none", ShortErrorClass: "unobserved", Role: "none", Signal0: "unobserved", Signal1: "unobserved"}
			defer func() {
				observation.ElapsedMicros = time.Since(observedAt).Microseconds()
				if raw, e := json.Marshal(observation); e == nil {
					fmt.Printf("LUOBO_RUNTIME_CLEANUP_DIAG_V1 %s\n", raw)
				}
			}()
			session, releases, counts := cleanupSessionChildren(t, &observation)
			primary := errors.New("original audit failure")
			if kind == "cancel-audit-before-stop" {
				// Real missing ZK/owner audit can return before collect reaches Stop. It
				// cannot mark this newly launched pair cleaned or suppress final cleanup.
				observation.Stage = "assert-early-incomplete"
				if session.CleanupComplete() {
					observation.Complete = true
					t.Fatal("early audit failure was marked cleaned")
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				observation.Stage = "short-cleanup"
				firstStop := time.Now()
				shortResult := session.Cleanup(ctx)
				observation.FirstStopMicros = time.Since(firstStop).Microseconds()
				observation.ShortErrorClass = runtimeCleanupErrorClass(shortResult)
				primary = errors.Join(primary, shortResult)
				cancel()
				observation.Stage = "assert-short-incomplete"
				if session.CleanupComplete() {
					observation.Complete = true
					t.Fatal("timed out original Wait was marked complete")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			observation.Stage = "final-cleanup-start"
			finalStarted := time.Now()
			finished := make(chan error, 1)
			go func() { finished <- cleanupAcceptanceSession(ctx, session) }()
			// Both actual children must receive their original stop even when the
			// first returned a noncanonical error. Release each only after its signal.
			for at := len(counts) - 1; at >= 0; at-- {
				observation.Stage = "signal-wait"
				observation.Role = []string{"rpc-msg", "msgtransfer-legacy"}[at]
				waitCleanupFile(t, counts[at])
				if at == 0 {
					observation.Signal0 = "seen"
				} else {
					observation.Signal1 = "seen"
				}
				observation.Stage = "release-write"
				if e := os.WriteFile(releases[at], []byte("release"), 0600); e != nil {
					observation.ErrorClass = runtimeCleanupErrorClass(e)
					t.Fatal(e)
				}
			}
			finalResult := <-finished
			observation.FinalReturned = true
			observation.FinalCleanupMicros = time.Since(finalStarted).Microseconds()
			observation.Stage = "final-cleanup-return"
			observation.Role = "none"
			observation.ErrorClass = runtimeCleanupErrorClass(finalResult)
			session.cleanupMu.Lock()
			observation.ObservedExits = len(session.localExited)
			observation.LauncherClosed = session.launcherClosed
			session.cleanupMu.Unlock()
			outcome := errors.Join(primary, finalResult)
			observation.PrimaryPreserved = errors.Is(outcome, primary)
			observation.Stage = "assert-primary"
			if !errors.Is(outcome, primary) {
				t.Fatal("cleanup erased primary failure")
			}
			observation.Stage = "assert-complete"
			observation.Complete = session.CleanupComplete()
			if !session.CleanupComplete() || outcome == nil {
				t.Fatal("not all original child Wait/Close completed or failure became success")
			}
			observation.Stage = "count-check"
			for at, p := range counts {
				raw, e := os.ReadFile(p)
				state := "other"
				if e != nil {
					state = "unreadable"
				} else if string(raw) == "1" {
					state = "one"
				}
				if at == 0 {
					observation.Signal0 = state
				} else {
					observation.Signal1 = state
				}
				if e != nil || string(raw) != "1" {
					t.Fatal("an error stranded another child or repeated its signal", e)
				}
			}
			observation.Stage = "repeat-cleanup"
			again := session.Cleanup(ctx)
			observation.ErrorClass = runtimeCleanupErrorClass(again)
			if again == nil {
				t.Fatal("noncanonical child error was erased on repeated cleanup")
			}
			observation.Stage = "complete"
		})
	}
}
