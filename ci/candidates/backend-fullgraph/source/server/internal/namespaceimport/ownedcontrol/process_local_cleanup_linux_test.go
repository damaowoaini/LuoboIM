//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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

func cleanupSignalChild(t *testing.T, code int) (*OwnedService, string, string) {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0700); e != nil {
		t.Fatal(e)
	}
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
	bootstrap := filepath.Join(dir, "child.json")
	ready, release, count := filepath.Join(dir, "ready"), filepath.Join(dir, "release"), filepath.Join(dir, "signals")
	raw, _ := json.Marshal(struct {
		Signal, Release, Ready string
		Exit                   int
	}{count, release, ready, code})
	if e = os.WriteFile(bootstrap, raw, 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(binary, "--luobo-local-cleanup-child")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + dir, "LUOBO_OWNED_SERVER_BOOTSTRAP_PATH=" + bootstrap}
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	s := &OwnedService{cmd: cmd, done: make(chan struct{}), announcementPath: filepath.Join(dir, "announcement.json"), announcementSHA: strings.Repeat("c", 64)}
	go func() { s.waitErr = cmd.Wait(); close(s.done) }()
	t.Cleanup(func() {
		select {
		case <-s.done:
		default:
			_ = cmd.Process.Kill()
			<-s.done
		}
	})
	waitCleanupFile(t, ready)
	owner, e := processIdentity(cmd.Process.Pid)
	if e != nil {
		t.Fatal(e)
	}
	owner.Role = "rpc-msg"
	owner.OwnerNonce = strings.Repeat("a", 64)
	owner.BinarySHA256 = sha(body)
	s.owner = owner
	return s, release, count
}
func TestOwnedCleanupSharesSingleSignalAcrossOriginalStopPaths(t *testing.T) {
	for _, first := range []string{"canonical-first", "runtime-first", "local-first"} {
		t.Run(first, func(t *testing.T) {
			s, release, count := cleanupSignalChild(t, 7)
			call := func(kind string) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
				defer cancel()
				switch kind {
				case "canonical-first":
					_, _ = s.StopAndJoin(ctx)
				case "runtime-first":
					_ = stopRuntimeOwnedChild(ctx, s, true, false)
				case "local-first":
					_, _ = s.StopForLocalCleanup(ctx)
				}
			}
			call(first)
			waitCleanupFile(t, count)
			call("canonical-first")
			call("runtime-first")
			call("local-first")
			raw, e := os.ReadFile(count)
			if e != nil || string(raw) != "1" {
				t.Fatal("a second retained path re-signalled the original child", e)
			}
			if e = os.WriteFile(release, []byte("release"), 0600); e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			select {
			case <-s.done:
			case <-ctx.Done():
				t.Fatal("original child Wait did not complete before local exit observation")
			}
			exit, e := s.StopForLocalCleanup(ctx)
			if !errors.Is(e, ErrOwnedEvidence) || exit.Version != "owned-local-child-exited-v1" || exit.WaitSucceeded || exit.Owner != s.owner || exit.ObservedAbsentAt.IsZero() || s.joined {
				t.Fatal("nonzero original Wait became canonical or remained unjoined", e)
			}
			if _, e = os.Stat(filepath.Join(filepath.Dir(release), "drain.json")); !os.IsNotExist(e) {
				t.Fatal("cleanup invented drain")
			}
		})
	}
	t.Run("unbound start failure only waits original done", func(t *testing.T) {
		s, release, count := cleanupSignalChild(t, 7)
		s.mu.Lock()
		e := s.signalStopOnceLocked()
		s.owner = ProcessOwner{}
		s.mu.Unlock()
		if e != nil {
			t.Fatal(e)
		}
		waitCleanupFile(t, count)
		short, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		_, _ = s.StopForLocalCleanup(short)
		cancel()
		if e = os.WriteFile(release, []byte("release"), 0600); e != nil {
			t.Fatal(e)
		}
		ctx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		exit, e := s.StopForLocalCleanup(ctx)
		raw, _ := os.ReadFile(count)
		if !errors.Is(e, ErrOwnedEvidence) || exit.Version != "owned-local-unbound-child-exited-v1" || exit.Owner.PID != 0 || exit.ObservedAbsentAt.IsZero() || string(raw) != "1" || s.joined {
			t.Fatal("unbound retained child got identity or another signal", e)
		}
	})
}
