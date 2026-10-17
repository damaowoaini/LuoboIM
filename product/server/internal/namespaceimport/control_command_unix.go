//go:build darwin || linux

package namespaceimport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// CommandControlPlane invokes a SOURCE-PINNED deployment-agent executable. It is
// an execution port, not a fabricated default deployment service. That agent
// must authenticate to the real owned runtime/Kafka/deployment registry, preserve
// its operation records, and sign the precise observations with Config's pinned
// provider key. This adapter never accepts a key or endpoint from stdout.
type CommandControlPlane struct {
	program, sha256, privateWork string
	timeout                      time.Duration
}

func NewCommandControlPlane(program, sha256, privateWork string, timeout time.Duration) (*CommandControlPlane, error) {
	if !filepath.IsAbs(program) || !validSHA(sha256) || !filepath.IsAbs(privateWork) || timeout <= 0 || timeout > time.Minute {
		return nil, ErrInvalid
	}
	root, err := filepath.EvalSymlinks(privateWork)
	if err != nil || root != privateWork {
		return nil, ErrInvalid
	}
	st, err := os.Lstat(root)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 {
		return nil, ErrInvalid
	}
	p := &CommandControlPlane{program: program, sha256: sha256, privateWork: privateWork, timeout: timeout}
	if err = p.checkProgram(); err != nil {
		return nil, err
	}
	return p, nil
}
func (p *CommandControlPlane) checkProgram() error {
	canonical, err := filepath.EvalSymlinks(p.program)
	if err != nil || canonical != p.program {
		return ErrInvalid
	}
	fd, err := unix.Open(p.program, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), p.program)
	defer f.Close()
	var a, b unix.Stat_t
	if unix.Fstat(fd, &a) != nil || a.Mode&unix.S_IFMT != unix.S_IFREG || a.Mode&0022 != 0 || a.Mode&0111 == 0 || a.Uid != 0 && a.Uid != uint32(os.Getuid()) || a.Nlink != 1 || a.Size < 1 || a.Size > 8*1024*1024 {
		return ErrInvalid
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, 8*1024*1024+1))
	if err != nil {
		return err
	}
	if unix.Fstat(fd, &b) != nil || a.Dev != b.Dev || a.Ino != b.Ino || a.Size != b.Size || n != a.Size || hex.EncodeToString(h.Sum(nil)) != p.sha256 {
		return ErrConflict
	}
	return nil
}

type limitedOutput struct {
	body  bytes.Buffer
	limit int
}

func (o *limitedOutput) Write(p []byte) (int, error) {
	if len(p) > o.limit-o.body.Len() {
		return 0, ErrInvalid
	}
	return o.body.Write(p)
}
func (p *CommandControlPlane) invoke(ctx context.Context, mode string, r ProbeRequest) (FenceObservation, error) {
	if p == nil || mode != "acquire" && mode != "observe" || r.Header.Validate() != nil || len(r.Nonce) != 48 {
		return FenceObservation{}, ErrInvalid
	}
	if err := p.checkProgram(); err != nil {
		return FenceObservation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	body, err := json.Marshal(struct {
		Schema    int          `json:"schema"`
		Operation string       `json:"operation"`
		Request   ProbeRequest `json:"request"`
	}{1, mode, r})
	if err != nil {
		return FenceObservation{}, err
	}
	cmd := exec.CommandContext(ctx, p.program, "--namespace-import-control-v1")
	cmd.Dir = p.privateWork
	// No shell, inherited API token, user-controlled argv, or credential output.
	// The source-owned agent uses its explicit private bootstrap/OS credential.
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + p.privateWork}
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdin = bytes.NewReader(body)
	out := &limitedOutput{limit: journalBytes}
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return FenceObservation{}, ErrUntrusted
	}
	if err = p.checkProgram(); err != nil {
		return FenceObservation{}, err
	}
	var observation FenceObservation
	if strictJSON(out.body.Bytes(), &observation) != nil {
		return FenceObservation{}, ErrUntrusted
	}
	return observation, nil // Controller verifies nonce/header/signature/ALL records.
}
func (p *CommandControlPlane) Acquire(ctx context.Context, r ProbeRequest) (FenceObservation, error) {
	return p.invoke(ctx, "acquire", r)
}
func (p *CommandControlPlane) Observe(ctx context.Context, r ProbeRequest) (FenceObservation, error) {
	return p.invoke(ctx, "observe", r)
}
