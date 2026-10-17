//go:build linux

package ownedcontrol

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
)

// The helper subprocess is a real child of this test owner, not a production
// role/admission fixture. No hold, signed graph, Ready or business grant exists.
type stopAllChildInput struct {
	Nonce, Started, Mode string
	Drain                ServerRuntimeDrain
	Path                 string
}

func TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit(t *testing.T) {
	if path := os.Getenv("LUOBO_STOP_ALL_TEST_CHILD"); path != "" {
		raw, e := os.ReadFile(path)
		var input stopAllChildInput
		if e != nil || json.Unmarshal(raw, &input) != nil {
			os.Exit(71)
		}
		stopped, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM)
		defer cancel()
		f, e := os.OpenFile(input.Started, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			os.Exit(72)
		}
		if _, e = f.Write([]byte("started\n")); e != nil || f.Sync() != nil || f.Close() != nil {
			os.Exit(73)
		}
		<-stopped.Done()
		if input.Mode == "canonical" {
			if _, e = WriteServerRuntimeDrain(input.Path, input.Nonce, input.Drain); e != nil {
				os.Exit(74)
			}
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	if os.Chmod(dir, 0700) != nil {
		t.Fatal("private child custody unavailable")
	}
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	executable, e = filepath.EvalSymlinks(executable)
	if e != nil {
		t.Fatal(e)
	}
	body, e := os.ReadFile(executable)
	if e != nil {
		t.Fatal(e)
	}
	binarySHA := sha(body)
	body = nil
	group := &NamespaceRuntimeLaunchGroup{dir: dir, entries: map[string]NamespaceRuntimeProcess{}, order: []string{"api", "push", "rpc-user"}}
	var children []*OwnedService
	t.Cleanup(func() {
		for _, child := range children {
			select {
			case <-child.done:
				continue
			default:
			}
			current, e := processIdentity(child.owner.PID)
			if e == nil && sameProcess(current, child.owner) {
				_ = child.cmd.Process.Signal(syscall.SIGTERM)
			}
			select {
			case <-child.done:
			case <-time.After(3 * time.Second):
				t.Error("exact test child still unjoined")
			}
		}
	})
	for _, role := range group.order {
		work := filepath.Join(dir, role)
		if os.Mkdir(work, 0700) != nil {
			t.Fatal("private child work unavailable")
		}
		nonce := strings.Repeat("a", 64)
		instance := uuid.NewString()
		boot := strings.Repeat("b", 64)
		policy := strings.Repeat("c", 64)
		ann := ServerRuntimeAnnouncement{Version: "owned-server-owner-ready-v1", Role: role, Mode: "legacy", InstanceID: instance, DatabaseSHA256: strings.Repeat("d", 64), BootstrapSHA256: boot, PolicySHA256: policy, DrainPath: filepath.Join(work, "drain.json"), OwnedStopStepNames: []string{"rpc-handlers", "redis", "discovery"}}
		drain := ServerRuntimeDrain{Version: "owned-server-drained-v1", Role: role, Mode: "legacy", InstanceID: instance, DatabaseSHA256: ann.DatabaseSHA256, BootstrapSHA256: boot, PolicySHA256: policy, JoinedAndClosed: append([]string(nil), ann.OwnedStopStepNames...)}
		mode := "canonical"
		if role == "push" {
			mode = "announced-without-canonical-drain"
		}
		input := stopAllChildInput{nonce, filepath.Join(work, "started"), mode, drain, ann.DrainPath}
		raw, e := json.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		inputPath := filepath.Join(work, "input.json")
		if os.WriteFile(inputPath, raw, 0600) != nil {
			t.Fatal("child private input unavailable")
		}
		cmd := exec.Command(executable, "-test.run=^TestNamespaceRuntimeStopKeepsEveryOriginalChildOwnedAfterOneNoncanonicalExit$")
		cmd.Env = append(os.Environ(), "LUOBO_STOP_ALL_TEST_CHILD="+inputPath)
		log, e := os.OpenFile(filepath.Join(work, "child.log"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			t.Fatal(e)
		}
		cmd.Stdout = log
		cmd.Stderr = log
		if cmd.Start() != nil {
			log.Close()
			t.Fatal("test child did not start")
		}
		child := &OwnedService{cmd: cmd, done: make(chan struct{}), announcement: RuntimeAnnouncement{InstanceID: instance, DrainPath: ann.DrainPath}, serverAnnouncement: &ann, announcementSHA: strings.Repeat("e", 64)}
		go func(s *OwnedService) {
			s.waitErr = s.cmd.Wait()
			if log.Sync() != nil || log.Close() != nil {
				s.waitErr = ErrOwnedEvidence
			}
			close(s.done)
		}(child)
		// Retain the actual handle immediately, including a failed owner observation.
		children = append(children, child)
		owner, e := processIdentity(cmd.Process.Pid)
		if e != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-child.done:
			case <-time.After(3 * time.Second):
				t.Error("unobserved exact child failed to exit")
			}
			t.Fatal("real test process identity unavailable")
		}
		owner.Role = role
		owner.BinarySHA256 = binarySHA
		owner.SourceSHA256 = strings.Repeat("f", 64)
		owner.BootstrapSHA256 = boot
		owner.OwnerNonce = nonce
		child.owner = owner
		group.entries[role] = NamespaceRuntimeProcess{Service: child}
		until := time.NewTimer(3 * time.Second)
		tick := time.NewTicker(time.Millisecond)
		ready := false
		for !ready {
			if b, e := os.ReadFile(input.Started); e == nil && string(b) == "started\n" {
				ready = true
				break
			}
			select {
			case <-until.C:
				t.Fatal("real child signal admission unavailable")
			case <-tick.C:
			}
		}
		until.Stop()
		tick.Stop()
	}
	t.Run("unknown-middle-child-does-not-strand-later-original-owner", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if e := group.Stop(ctx); e == nil {
			t.Fatal("missing canonical drain was promoted to success")
		}
		for _, child := range children {
			select {
			case <-child.done:
			default:
				t.Fatal("another original child was stranded")
			}
			if _, e := processIdentity(child.owner.PID); !os.IsNotExist(e) {
				t.Fatal("original child is still present")
			}
		}
	})
	t.Run("good-drains-retained-and-unknown-never-gets-a-join", func(t *testing.T) {
		for _, role := range []string{"api", "rpc-user"} {
			if !group.entries[role].Service.joined {
				t.Fatal("other original canonical drain was lost")
			}
		}
		if group.entries["push"].Service.joined {
			t.Fatal("unknown child acquired canonical join")
		}
		if _, e := os.Lstat(filepath.Join(dir, "push-runtime-child-exit.json")); !os.IsNotExist(e) {
			t.Fatal("unknown child got canonical exit receipt")
		}
		var failure struct {
			Version, Role        string
			Owner                ProcessOwner
			Outcome, ErrorSHA256 string
		}
		if _, e := readServerEnvelope(filepath.Join(dir, "push-stop-failure.json"), &failure); e != nil || failure.Role != "push" || failure.Owner.PID != group.entries["push"].Service.owner.PID || !isSHA(failure.ErrorSHA256) {
			t.Fatal("failure lost original owner custody")
		}
	})
	t.Run("repeat-Stop-does-not-renew-signal-or-erase-failure", func(t *testing.T) {
		path := filepath.Join(dir, "push-stop-failure.json")
		first, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if e = group.Stop(ctx); e == nil {
			t.Fatal("repeat call hid the unknown outcome")
		}
		last, e := os.ReadFile(path)
		if e != nil || string(first) != string(last) {
			t.Fatal("first failure evidence was rewritten")
		}
		if len(group.stopAttempts) != 3 || !group.stopping {
			t.Fatal("original admission or signal ownership renewed")
		}
	})
}
