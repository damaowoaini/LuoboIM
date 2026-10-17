//go:build linux

package runtimeacceptance

import (
	"context"
	"encoding/json"
	"errors"
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

func cleanupSessionChildren(t *testing.T) (*Session, []string, []string) {
	t.Helper()
	base := t.TempDir()
	if e := os.Chmod(base, 0700); e != nil {
		t.Fatal(e)
	}
	dir := filepath.Join(base, "launcher")
	if e := os.Mkdir(dir, 0700); e != nil {
		t.Fatal(e)
	}
	launcher, e := ownedcontrol.OpenOwnedLauncher(dir, strings.Repeat("a", 64))
	if e != nil {
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
	binary, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	binary, e = filepath.EvalSymlinks(binary)
	if e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile(binary)
	if e != nil {
		t.Fatal(e)
	}
	for at, role := range []string{"rpc-msg", "msgtransfer-legacy"} {
		work := filepath.Join(base, role)
		if e = os.Mkdir(work, 0700); e != nil {
			t.Fatal(e)
		}
		count, release, ready := filepath.Join(work, "signals"), filepath.Join(work, "release"), filepath.Join(work, "ready")
		releases = append(releases, release)
		counts = append(counts, count)
		code := 0
		if at == 1 {
			code = 7
		}
		boot, e := writePrivate(filepath.Join(work, "bootstrap.json"), struct {
			Signal, Release, Ready string
			Exit                   int
		}{count, release, ready, code})
		if e != nil {
			t.Fatal(e)
		}
		receipt, e := writePrivate(filepath.Join(work, "build.json"), struct {
			Schema int    `json:"schema"`
			Source string `json:"sourceSHA256"`
			Binary string `json:"binarySHA256"`
			Bytes  int    `json:"binaryBytes"`
			Tool   string `json:"toolchainSHA256"`
		}{1, strings.Repeat("b", 64), digestBytes(body), len(body), strings.Repeat("c", 64)})
		if e != nil {
			t.Fatal(e)
		}
		child, e := launcher.Start(context.Background(), ownedcontrol.ServiceSpec{Role: role, BinaryPath: binary, BinarySHA256: digestBytes(body), SourceSHA256: strings.Repeat("b", 64), BuildReceiptSHA256: receipt, BuildReceiptPath: filepath.Join(work, "build.json"), BootstrapPath: filepath.Join(work, "bootstrap.json"), BootstrapSHA256: boot, BootstrapEnvironmentKey: "LUOBO_OWNED_SERVER_BOOTSTRAP_PATH", BootstrapSHAEnvironmentKey: "LUOBO_OWNED_SERVER_BOOTSTRAP_SHA256", Work: work, Args: []string{"--luobo-local-cleanup-child"}})
		if child != nil {
			session.Legacy = append(session.Legacy, child)
		}
		if e != nil {
			t.Fatal(e)
		}
		waitCleanupFile(t, ready)
	}
	return session, releases, counts
}
func TestFullgraphEarlyFailureKeepsOriginalChildrenForFinalCleanup(t *testing.T) {
	for _, kind := range []string{"cancel-audit-before-stop", "timeout-before-local-wait-complete"} {
		t.Run(kind, func(t *testing.T) {
			session, releases, counts := cleanupSessionChildren(t)
			primary := errors.New("original audit failure")
			if kind == "cancel-audit-before-stop" {
				// Real missing ZK/owner audit can return before collect reaches Stop. It
				// cannot mark this newly launched pair cleaned or suppress final cleanup.
				if session.CleanupComplete() {
					t.Fatal("early audit failure was marked cleaned")
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
				primary = errors.Join(primary, session.Cleanup(ctx))
				cancel()
				if session.CleanupComplete() {
					t.Fatal("timed out original Wait was marked complete")
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			finished := make(chan error, 1)
			go func() { finished <- cleanupAcceptanceSession(ctx, session) }()
			// Both actual children must receive their original stop even when the
			// first returned a noncanonical error. Release each only after its signal.
			for at := len(counts) - 1; at >= 0; at-- {
				waitCleanupFile(t, counts[at])
				if e := os.WriteFile(releases[at], []byte("release"), 0600); e != nil {
					t.Fatal(e)
				}
			}
			outcome := errors.Join(primary, <-finished)
			if !errors.Is(outcome, primary) {
				t.Fatal("cleanup erased primary failure")
			}
			if !session.CleanupComplete() || outcome == nil {
				t.Fatal("not all original child Wait/Close completed or failure became success")
			}
			for _, p := range counts {
				raw, e := os.ReadFile(p)
				if e != nil || string(raw) != "1" {
					t.Fatal("an error stranded another child or repeated its signal", e)
				}
			}
			again := session.Cleanup(ctx)
			if again == nil {
				t.Fatal("noncanonical child error was erased on repeated cleanup")
			}
		})
	}
}
