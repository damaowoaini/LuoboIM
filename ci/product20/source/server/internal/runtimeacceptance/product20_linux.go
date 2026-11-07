//go:build linux

package runtimeacceptance

// A fresh HTTP + real gateway-wire acceptance tool. These two tool-only overlay
// files are not added to the compiled product14 source identity or its old
// token-v2 oracles. No production handler or existing acceptance function changes.
import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/base64"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/OpenIMSDK/protocol/constant"
	"github.com/OpenIMSDK/protocol/msg"
	"github.com/OpenIMSDK/protocol/sdkws"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

type Product20Binary struct {
	Name         string `json:"name"`
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Bytes        int64  `json:"bytes"`
	SourceSHA256 string `json:"sourceSHA256"`
}
type Product20Plan struct {
	Version             string            `json:"version"`
	Work                string            `json:"work"`
	Core                PrivatePlan       `json:"core"`
	ChartTemplatePath   string            `json:"chartTemplatePath"`
	ChartTemplateSHA256 string            `json:"chartTemplateSHA256"`
	SQLRootPassword     string            `json:"sqlRootPassword"`
	Products            []Product20Binary `json:"products"`
}
type product20Result struct {
	Version                     string                   `json:"version"`
	Accepted                    bool                     `json:"accepted"`
	Phase                       string                   `json:"phase"`
	CoreSourceSHA256            string                   `json:"coreSourceSHA256"`
	ProductProcesses            int                      `json:"productProcesses"`
	CoreOriginalPIDs            []int                    `json:"coreOriginalPIDs"`
	SixOriginalPIDs             []int                    `json:"sixOriginalPIDs"`
	RealRegistrations           int                      `json:"realRegistrations"`
	RealPasswordLogins          int                      `json:"realPasswordLogins"`
	DualTokens                  bool                     `json:"dualTokens"`
	ChatTokenSelfRead           bool                     `json:"chatTokenSelfRead"`
	ConfigHTTP                  bool                     `json:"configHTTP"`
	WrongPasswordRejected       bool                     `json:"wrongPasswordRejected"`
	BusinessPermissions         bool                     `json:"businessPermissions"`
	OrdinaryDispatches          int                      `json:"ordinaryDispatches"`
	PeerMessages                int                      `json:"peerMessages"`
	HistoryMessages             int                      `json:"historyMessages"`
	ClientIDHash                string                   `json:"clientIDHash,omitempty"`
	ServerIDHash                string                   `json:"serverIDHash,omitempty"`
	MessageSeq                  int64                    `json:"messageSeq"`
	UsersHash                   []string                 `json:"usersHash,omitempty"`
	FreshSQLSeedOnly            bool                     `json:"freshSQLSeedOnly"`
	AuxiliaryListenersJoined    bool                     `json:"auxiliaryListenersJoined"`
	OriginalSixChildrenExited   bool                     `json:"originalSixChildrenExited"`
	OriginalCoreCleanupComplete bool                     `json:"originalCoreCleanupComplete"`
	ChartCanonicalDrainClaimed  bool                     `json:"chartCanonicalDrainClaimed"`
	WebUIAccepted               bool                     `json:"webUIAccepted"`
	DeploymentAccepted          bool                     `json:"deploymentAccepted"`
	FailureSHA256               string                   `json:"failureSHA256,omitempty"`
	StartupBoundary             product20StartupBoundary `json:"startupBoundary"`
	HTTPExchanges               []product20HTTPExchange  `json:"httpExchanges"`
	ConfigProxyStatus           int                      `json:"configProxyStatus"`
}

// This diagnostic observes original returns only. It never formats an error,
// samples a live ProcessState, or authorizes readiness/cleanup.
type product20StartupCause struct {
	Present     bool     `json:"present"`
	Classes     []string `json:"classes"`
	Truncated   bool     `json:"truncated"`
	Errno       int      `json:"errno"`
	MySQLNumber int      `json:"mysqlNumber"`
	ExecState   string   `json:"execState"`
	ExitCode    int      `json:"exitCode"`
	Signal      int      `json:"signal"`
}
type product20StartupBoundary struct {
	Version      string                `json:"version"`
	Entered      bool                  `json:"entered"`
	Substep      string                `json:"substep"`
	Role         string                `json:"role"`
	SixForked    int                   `json:"sixForked"`
	SixReady     int                   `json:"sixReady"`
	PrimaryCause product20StartupCause `json:"primaryCause"`
}

func (o *product20StartupBoundary) at(step, role string) {
	o.Substep, o.Role = step, role
}
func product20StartupCauseOf(err error) product20StartupCause {
	out := product20StartupCause{Present: err != nil, Classes: []string{}, ExecState: "none", ExitCode: -1}
	if err == nil {
		return out
	}
	pending := []error{err}
	seen := map[string]bool{}
	for visited := 0; len(pending) > 0 && visited < 16; visited++ {
		current := pending[0]
		pending = pending[1:]
		if current == nil {
			continue
		}
		class := "other"
		switch e := current.(type) {
		case *exec.ExitError:
			class = "exec-exit"
			if e.ProcessState != nil {
				out.ExecState = "other"
				if state, ok := e.ProcessState.Sys().(syscall.WaitStatus); ok {
					if state.Exited() {
						out.ExecState, out.ExitCode, out.Signal = "exited", state.ExitStatus(), 0
					} else if state.Signaled() {
						out.ExecState, out.ExitCode, out.Signal = "signaled", -1, int(state.Signal())
					}
				}
			}
		case syscall.Errno:
			class = "syscall-errno"
			if out.Errno == 0 && e > 0 && e <= 4095 {
				out.Errno = int(e)
			}
		case *os.PathError:
			class = "os-path"
		case *os.LinkError:
			class = "os-link"
		case *os.SyscallError:
			class = "os-syscall"
		case *net.OpError:
			class = "net-op"
		case *net.DNSError:
			class = "net-dns"
		case *strconv.NumError:
			class = "number-parse"
		case *mysql.MySQLError:
			class = "mysql"
			out.MySQLNumber = int(e.Number)
		case x509.CertificateInvalidError, x509.UnknownAuthorityError, x509.HostnameError:
			class = "x509"
		case *yaml.TypeError:
			class = "yaml"
		case *json.UnsupportedTypeError, *json.UnsupportedValueError:
			class = "json"
		}
		if current == context.Canceled {
			class = "context-canceled"
		}
		if current == context.DeadlineExceeded {
			class = "context-deadline"
		}
		if !seen[class] && len(out.Classes) < 8 {
			seen[class] = true
			out.Classes = append(out.Classes, class)
		} else if !seen[class] {
			out.Truncated = true
		}
		switch e := current.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range e.Unwrap() {
				if child != nil && len(pending) < 16 {
					pending = append(pending, child)
				} else if child != nil {
					out.Truncated = true
				}
			}
		case interface{ Unwrap() error }:
			if child := e.Unwrap(); child != nil {
				pending = append(pending, child)
			}
		}
	}
	if len(pending) > 0 {
		out.Truncated = true
	}
	return out
}

var product20Names = []string{"admin-rpc", "chat-rpc", "office-rpc", "admin-api", "chat-api", "route-service"}
var product20Safe = regexp.MustCompile(`^[a-z0-9_]{1,63}$`)
var product20Hex = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ReadProduct20Plan(path, expected string) (Product20Plan, error) {
	var p Product20Plan
	if loadPinnedPrivate(path, expected, 128<<10, &p) != nil || p.Version != "fresh-product20-http-ws-v1" || canonicalDirectory(p.Work) != nil || p.Core.Scenario != "ack" || len(p.SQLRootPassword) != 64 || !product20Hex.MatchString(p.SQLRootPassword) || len(p.Products) != 6 {
		return p, errors.New("product20 private input rejected")
	}
	if p.Core.Version != "owned-fullgraph-acceptance-v1" || canonicalDirectory(p.Core.Work) != nil || p.Core.Work == p.Work || filepath.Dir(p.Core.Work) != filepath.Dir(p.Work) {
		return p, errors.New("product20 core allocation differs")
	}
	for _, path := range []string{p.Core.PlanePath, p.Core.CredentialsPath, p.Core.BuildsPath, p.ChartTemplatePath} {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return p, errors.New("original private paths required")
		}
	}
	seen := map[string]bool{}
	for _, b := range p.Products {
		if seen[b.Name] || !product20Hex.MatchString(b.SHA256) || !product20Hex.MatchString(b.SourceSHA256) || b.Bytes <= 0 || b.Bytes > 128<<20 {
			return p, errors.New("six exact product artifacts required")
		}
		seen[b.Name] = true
	}
	for _, name := range product20Names {
		if !seen[name] {
			return p, errors.New("six exact product roles required")
		}
	}
	return p, nil
}

func product20File(path string, expected string, size int64) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("canonical product executable required")
	}
	canonical, e := filepath.EvalSymlinks(path)
	if e != nil || canonical != path {
		return errors.New("product executable alias rejected")
	}
	fd, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer fd.Close()
	a, err := fd.Stat()
	if err != nil {
		return err
	}
	st, ok := a.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Getuid()) || st.Nlink != 1 || !a.Mode().IsRegular() || a.Mode().Perm()&0022 != 0 || a.Mode().Perm()&0111 == 0 || a.Size() != size {
		return errors.New("original product file custody rejected")
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(fd, size+1), make([]byte, 1<<20))
	b, e := fd.Stat()
	current, ee := os.Lstat(path)
	if err != nil || e != nil || ee != nil || !os.SameFile(a, b) || !os.SameFile(a, current) || a.Size() != b.Size() || a.ModTime() != b.ModTime() || n != size || hex.EncodeToString(h.Sum(nil)) != expected {
		return errors.New("original product file body changed")
	}
	return nil
}
func product20Random() (string, error) {
	b := make([]byte, 32)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}
func product20MD5(value string) string { b := md5.Sum([]byte(value)); return hex.EncodeToString(b[:]) }

type product20Child struct {
	cmd   *exec.Cmd
	done  chan struct{}
	err   error
	start string
	files []*os.File
}

func product20StartToken(pid int) (string, error) {
	b, e := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if e != nil {
		return "", e
	}
	i := bytes.LastIndexByte(b, ')')
	if i < 0 {
		return "", errors.New("original child stat rejected")
	}
	fields := strings.Fields(string(b[i+1:]))
	if len(fields) < 20 {
		return "", errors.New("original child start token absent")
	}
	return fields[19], nil
}
func product20StartChild(ctx context.Context, binary Product20Binary, work string, args []string, environment []string) (*product20Child, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if e := product20File(binary.Path, binary.SHA256, binary.Bytes); e != nil {
		return nil, e
	}
	if e := os.Mkdir(work, 0700); e != nil {
		return nil, e
	}
	out, e := os.OpenFile(filepath.Join(work, "stdout.private"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return nil, e
	}
	errout, e := os.OpenFile(filepath.Join(work, "stderr.private"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		out.Close()
		return nil, e
	}
	// Keep all six fresh products in the original operator/guard process group.
	// Pdeathsig is a backup; every returned original handle is still retained.
	cmd := exec.Command(binary.Path, args...)
	cmd.Dir = work
	cmd.Env = environment
	cmd.Stdout = out
	cmd.Stderr = errout
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	if e = cmd.Start(); e != nil {
		out.Close()
		errout.Close()
		return nil, e
	}
	c := &product20Child{cmd: cmd, done: make(chan struct{}), files: []*os.File{out, errout}}
	c.start, e = product20StartToken(cmd.Process.Pid)
	go func() { c.err = cmd.Wait(); close(c.done) }()
	if e != nil {
		return c, e
	}
	_, e = writePrivate(filepath.Join(work, "original-child.json"), map[string]any{"pid": cmd.Process.Pid, "startToken": c.start, "path": binary.Path, "sha256": binary.SHA256, "uid": os.Getuid()})
	return c, e
}
func (c *product20Child) requestStop() error {
	if c == nil {
		return nil
	}
	select {
	case <-c.done:
		return nil
	default:
	}
	if c.start != "" {
		start, e := product20StartToken(c.cmd.Process.Pid)
		if e != nil || start != c.start {
			select {
			case <-c.done:
				return nil
			default:
				return errors.New("original product child ownership changed")
			}
		}
	}
	// If start-token capture itself failed, use only the retained os.Process
	// handle from this very cmd.Start, never a looked-up/adopted PID.
	if e := c.cmd.Process.Signal(syscall.SIGTERM); e != nil && !errors.Is(e, os.ErrProcessDone) {
		return e
	}
	return nil
}
func (c *product20Child) waitStopped(ctx context.Context) error {
	if c == nil {
		return nil
	}
	defer func() {
		for _, f := range c.files {
			_ = f.Close()
		}
	}()
	select {
	case <-c.done:
		if c.err == nil {
			return nil
		}
		var exit *exec.ExitError
		if errors.As(c.err, &exit) {
			if s, ok := exit.Sys().(syscall.WaitStatus); ok && s.Signaled() && s.Signal() == syscall.SIGTERM {
				return nil
			}
		}
		return c.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func product20StopChildren(ctx context.Context, children []*product20Child) error {
	var e error
	for i := len(children) - 1; i >= 0; i-- {
		e = errors.Join(e, children[i].requestStop())
	}
	for i := len(children) - 1; i >= 0; i-- {
		e = errors.Join(e, children[i].waitStopped(ctx))
	}
	return e
}
func product20WaitPort(ctx context.Context, port int, child *product20Child) error {
	timer := time.NewTicker(100 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-child.done:
			return errors.Join(errors.New("original service exited before readiness"), child.err)
		case <-timer.C:
			c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
			if e == nil {
				return c.Close()
			}
		}
	}
}

// Both listeners are task-owned and joined. The CA is trusted only by the
// selected child processes/client; no system CA or DNS configuration changes.
type product20Aux struct {
	server       *http.Server
	listener     net.Listener
	done         chan error
	smtp         net.Listener
	smtpDone     chan struct{}
	mu           sync.Mutex
	smtpConns    map[net.Conn]bool
	smtpWorkers  sync.WaitGroup
	codes        map[string]string
	caPath       string
	roots        *x509.CertPool
	cert         tls.Certificate
	smtpPassword string
	transports   []*http.Transport
	configStatus atomic.Int32
}

func product20NewAux(work, password string, observation *product20StartupBoundary) (*product20Aux, error) {
	observation.at("aux-key", "aux")
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		return nil, e
	}
	observation.at("aux-serial", "aux")
	serial, e := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if e != nil {
		return nil, e
	}
	ca := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "萝卜对话 task product20 CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(2 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	observation.at("aux-ca", "aux")
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, &key.PublicKey, key)
	if e != nil {
		return nil, e
	}
	le := *ca
	le.SerialNumber = new(big.Int).Add(serial, big.NewInt(1))
	le.IsCA = false
	le.KeyUsage = x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment
	le.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	le.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	observation.at("aux-leaf", "aux")
	leaf, e := x509.CreateCertificate(rand.Reader, &le, ca, &key.PublicKey, key)
	if e != nil {
		return nil, e
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	observation.at("aux-tls", "aux")
	cert, e := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf}), pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}))
	if e != nil {
		return nil, e
	}
	path := filepath.Join(work, "task-ca.pem")
	observation.at("aux-ca-write", "aux")
	if e = os.WriteFile(path, caPEM, 0600); e != nil {
		return nil, e
	}
	observation.at("aux-trust-pool", "aux")
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("task CA rejected")
	}
	return &product20Aux{caPath: path, roots: roots, cert: cert, smtpPassword: password, smtpConns: map[net.Conn]bool{}, codes: map[string]string{}}, nil
}
func (a *product20Aux) start(observation *product20StartupBoundary) error {
	observation.at("aux-port-floor-read", "aux")
	portFloor, e := os.ReadFile("/proc/sys/net/ipv4/ip_unprivileged_port_start")
	if e != nil {
		return e
	}
	observation.at("aux-port-floor-parse", "aux")
	floor, e := strconv.Atoi(strings.TrimSpace(string(portFloor)))
	if e == nil {
		observation.at("aux-port-floor-policy", "aux")
	}
	if e != nil || floor > 443 {
		return errors.New("task netnamespace does not permit original HTTPS port")
	}
	observation.at("aux-https-listen", "aux")
	ln, e := net.Listen("tcp", "127.0.0.1:443")
	if e != nil {
		return e
	}
	a.listener = ln
	proxies := map[string]*httputil.ReverseProxy{}
	for prefix, port := range map[string]int{"/chat": 31008, "/api": 31000, "/msg_gateway": 31001} {
		u, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
		proxy := httputil.NewSingleHostReverseProxy(u)
		original := proxy.Director
		p := prefix
		proxy.Director = func(r *http.Request) {
			original(r)
			r.URL.Path = strings.TrimPrefix(r.URL.Path, p)
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
			r.URL.RawPath = ""
			r.Host = "127.0.0.1"
		}
		transport := &http.Transport{Proxy: nil, ResponseHeaderTimeout: 10 * time.Second}
		proxy.Transport = transport
		a.transports = append(a.transports, transport)
		proxy.ModifyResponse = func(response *http.Response) error {
			if p == "/chat" && response.Request != nil && response.Request.URL.Path == "/client_config/get" {
				a.configStatus.Store(int32(response.StatusCode))
			}
			return nil
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, request *http.Request, _ error) {
			if p == "/chat" && request.URL.Path == "/client_config/get" {
				a.configStatus.Store(http.StatusBadGateway)
			}
			http.Error(w, "task upstream unavailable", http.StatusBadGateway)
		}
		proxies[prefix] = proxy
	}
	a.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, p := range proxies {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				p.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})}
	a.done = make(chan error, 1)
	go func() {
		a.done <- a.server.Serve(tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{a.cert}, MinVersion: tls.VersionTLS12}))
	}()
	observation.at("aux-smtp-listen", "aux")
	a.smtp, e = net.Listen("tcp", "127.0.0.1:2587")
	if e != nil {
		return e
	}
	a.smtpDone = make(chan struct{})
	go func() {
		defer close(a.smtpDone)
		for {
			conn, e := a.smtp.Accept()
			if e != nil {
				return
			}
			a.mu.Lock()
			a.smtpConns[conn] = true
			a.smtpWorkers.Add(1)
			a.mu.Unlock()
			go a.receiveMail(conn)
		}
	}()
	return nil
}
func (a *product20Aux) receiveMail(conn net.Conn) {
	original := conn
	defer func() {
		_ = conn.Close()
		a.mu.Lock()
		delete(a.smtpConns, original)
		a.mu.Unlock()
		a.smtpWorkers.Done()
	}()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReaderSize(conn, 32<<10)
	write := func(s string) bool { _, e := io.WriteString(conn, s+"\r\n"); return e == nil }
	if !write("220 task product20 SMTP") {
		return
	}
	recipient := ""
	secure := false
	authenticated := false
	for i := 0; i < 32; i++ {
		line, e := reader.ReadString('\n')
		if e != nil || len(line) > 2048 {
			return
		}
		line = strings.TrimSpace(line)
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"):
			if secure {
				if !write("250-task\r\n250 AUTH PLAIN") {
					return
				}
			} else {
				if !write("250-task\r\n250 STARTTLS") {
					return
				}
			}
		case upper == "STARTTLS" && !secure:
			if !write("220 begin TLS") {
				return
			}
			tlsconn := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{a.cert}, MinVersion: tls.VersionTLS12})
			if tlsconn.Handshake() != nil {
				return
			}
			conn = tlsconn
			reader = bufio.NewReaderSize(conn, 32<<10)
			secure = true
		case secure && strings.HasPrefix(upper, "AUTH PLAIN"):
			fields := strings.Fields(line)
			if len(fields) != 3 {
				return
			}
			decoded, e := base64.StdEncoding.DecodeString(fields[2])
			if e != nil || string(decoded) != "\x00product20-sender@product20.invalid\x00"+a.smtpPassword {
				write("535 refused")
				return
			}
			authenticated = true
			if !write("235 task authentication accepted") {
				return
			}
		case secure && authenticated && strings.HasPrefix(upper, "MAIL FROM:"):
			if !write("250 OK") {
				return
			}
		case secure && authenticated && strings.HasPrefix(upper, "RCPT TO:"):
			recipient = strings.Trim(strings.TrimSpace(line[len("RCPT TO:"):]), "<>")
			if !strings.HasSuffix(recipient, "@product20.invalid") || !strings.HasPrefix(recipient, "product20-") {
				write("550 task recipient refused")
				return
			}
			if !write("250 OK") {
				return
			}
		case upper == "DATA" && recipient != "":
			if !write("354 end with dot") {
				return
			}
			var body bytes.Buffer
			for {
				part, e := reader.ReadString('\n')
				if e != nil || body.Len()+len(part) > 16<<10 {
					return
				}
				if part == ".\r\n" {
					break
				}
				body.WriteString(part)
			}
			decoded, e := io.ReadAll(io.LimitReader(quotedprintable.NewReader(bytes.NewReader(body.Bytes())), 32<<10))
			if e != nil {
				return
			}
			matches := regexp.MustCompile(`验证码为:([0-9]{4,8})`).FindSubmatch(decoded)
			if len(matches) != 2 {
				return
			}
			a.mu.Lock()
			if len(a.codes) >= 2 && a.codes[recipient] == "" {
				a.mu.Unlock()
				return
			}
			a.codes[recipient] = string(matches[1])
			a.mu.Unlock()
			if !write("250 stored") {
				return
			}
		case upper == "QUIT":
			write("221 bye")
			return
		default:
			if !write("502 command refused") {
				return
			}
		}
	}
}
func (a *product20Aux) code(address string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	value := a.codes[address]
	if value == "" {
		return "", errors.New("actual SMTP code not observed")
	}
	delete(a.codes, address)
	return value, nil
}
func (a *product20Aux) close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	var result error
	if a.smtp != nil {
		result = errors.Join(result, a.smtp.Close())
		<-a.smtpDone
		a.mu.Lock()
		for conn := range a.smtpConns {
			_ = conn.Close()
		}
		a.mu.Unlock()
		a.smtpWorkers.Wait()
	}
	if a.server != nil {
		result = errors.Join(result, a.server.Shutdown(ctx))
		select {
		case e := <-a.done:
			if !errors.Is(e, http.ErrServerClosed) {
				result = errors.Join(result, e)
			}
		case <-ctx.Done():
			result = errors.Join(result, ctx.Err())
		}
	}
	for _, transport := range a.transports {
		transport.CloseIdleConnections()
	}
	return result
}

func product20SQL(ctx context.Context, password, database, user, secret string) (*sql.DB, error) {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = secret
	cfg.Net = "tcp"
	cfg.Addr = "127.0.0.1:3306"
	cfg.DBName = database
	cfg.ParseTime = true
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 10 * time.Second
	cfg.WriteTimeout = 10 * time.Second
	if user == "root" {
		cfg.Passwd = password
	}
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		return nil, e
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	if e = retryRead(ctx, func() error { return db.PingContext(ctx) }); e != nil {
		db.Close()
		return nil, e
	}
	return db, nil
}
func product20CreateSQL(ctx context.Context, password, nonce string) (routeDB, chartDB, routeUser, chartUser, routePass, chartPass string, err error) {
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(nonce) {
		err = errors.New("fresh SQL nonce required")
		return
	}
	admin, e := product20SQL(ctx, password, "", "root", password)
	if e != nil {
		err = e
		return
	}
	defer func() { err = errors.Join(err, admin.Close()) }()
	routeDB = "p20_route_" + nonce
	chartDB = "p20_chart_" + nonce
	routeUser = "p20_route_" + nonce[:16]
	chartUser = "p20_chart_" + nonce[:16]
	routePass, err = product20Random()
	if err != nil {
		return
	}
	chartPass, err = product20Random()
	if err != nil {
		return
	}
	for _, row := range [][3]string{{routeDB, routeUser, routePass}, {chartDB, chartUser, chartPass}} {
		if !product20Safe.MatchString(row[0]) || !product20Safe.MatchString(row[1]) || !product20Hex.MatchString(row[2]) {
			err = errors.New("task SQL identity rejected")
			return
		}
		for _, statement := range []string{"CREATE DATABASE `" + row[0] + "` CHARACTER SET utf8mb4", "CREATE USER '" + row[1] + "'@'%' IDENTIFIED BY '" + row[2] + "'", "GRANT ALL PRIVILEGES ON `" + row[0] + "`.* TO '" + row[1] + "'@'%'"} {
			if _, err = admin.ExecContext(ctx, statement); err != nil {
				return
			}
		}
	}
	return
}

type product20SQLBinding struct {
	Nonce    string `json:"nonce"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
}

// Called only by this independently compiled tool's strict fixture overlay,
// before any role config/bootstrap is sealed. No product binary is rewritten.
func product20FixtureSQL(b *Bootstrap) (map[string]any, error) {
	var value product20SQLBinding
	raw, e := namespaceinventory.ReadPrivateDocument(filepath.Join(b.Plan.Work, "product20-sql.json"), 4096)
	if e != nil || namespaceinventory.DecodeStrict(raw, &value) != nil || !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(value.Nonce) || value.Nonce != b.Resource.Nonce || value.Database != "p20_chart_"+value.Nonce || value.User != "p20_chart_"+value.Nonce[:16] || !product20Hex.MatchString(value.Password) {
		return nil, errors.New("original task SQL fixture binding differs")
	}
	return map[string]any{"address": []string{"127.0.0.1:3306"}, "username": value.User, "password": value.Password, "database": value.Database, "maxOpenConn": 8, "maxIdleConn": 2, "maxLifeTime": 60, "logLevel": 1}, nil
}

func product20Configuration(p Product20Plan, b *Bootstrap, aux *product20Aux, db, user, password string, observation *product20StartupBoundary) (string, error) {
	observation.at("chart-template-read", "chart")
	raw, e := os.ReadFile(p.ChartTemplatePath)
	if e != nil || digestBytes(raw) != p.ChartTemplateSHA256 {
		return "", errors.New("public chart template differs")
	}
	observation.at("chart-template-decode", "chart")
	var value map[string]any
	if yaml.Unmarshal(raw, &value) != nil || value == nil {
		return "", errors.New("public chart configuration shape rejected")
	}
	// OpenKeeper also uses the root as the URI scheme. Match the core fixture.
	value["zookeeper"] = map[string]any{"schema": b.Header.Tenant, "zkAddr": []string{"127.0.0.1:2181"}, "username": "", "password": ""}
	value["rpc"] = map[string]any{"listenIP": "127.0.0.1", "registerIP": "127.0.0.1"}
	value["rpcPort"] = map[string]any{"openImAdminPort": []int{31210}, "openImChatPort": []int{31220}, "openImOfficePort": []int{31230}}
	value["rpcRegisterName"] = map[string]any{"openImAdminName": "admin", "openImChatName": "chat", "openImOfficeName": "office", "openImRtcName": "fullgraph_" + b.Resource.Nonce + "_rpc_rtc"}
	value["chatApi"] = map[string]any{"openImChatApiPort": []int{31008}, "listenIP": "127.0.0.1"}
	value["adminApi"] = map[string]any{"openImAdminApiPort": []int{31009}, "listenIP": "127.0.0.1"}
	value["mysql"] = map[string]any{"address": []string{"127.0.0.1:3306"}, "username": user, "password": password, "database": db, "maxOpenConn": 8, "maxIdleConn": 2, "maxLifeTime": 60, "logLevel": 1, "slowThreshold": 500}
	value["redis"] = map[string]any{"address": []string{"127.0.0.1:6379"}, "username": b.Credential["redisRuntimeUser"], "password": b.Credential["redisRuntimePassword"]}
	value["mongo"] = map[string]any{"uri": mongoURI(b.Credential["mongoRuntimeUser"], b.Credential["mongoRuntimePassword"], b.Header.Database, false), "database": b.Header.Database, "maxPoolSize": 4}
	value["secret"] = b.Credential["serviceSecret"]
	observation.at("chart-secret", "chart")
	chatSecret, e := product20Random()
	if e != nil {
		return "", e
	}
	adminBootstrapPassword, e := product20Random()
	if e != nil {
		return "", e
	}
	value["chatSecret"] = chatSecret
	value["adminDelegation"] = map[string]any{"issuer": b.Header.Tenant}
	value["adminList"] = []map[string]any{{"adminID": "task_product20_admin", "nickname": "任务专属管理员", "imAdmin": b.ManagerID}}
	value["openIMUrl"] = "https://127.0.0.1/api"
	value["serverTag"] = "P20" + b.Resource.Nonce[:8]
	value["verifyCode"] = map[string]any{"use": "mail", "len": 6, "validTime": 300, "validCount": 5, "uintTime": 86400, "maxCount": 10, "superCode": "", "mail": map[string]any{"title": "萝卜对话任务验证码", "senderMail": "product20-sender@product20.invalid", "senderAuthorizationCode": password, "smtpAddr": "127.0.0.1", "smtpPort": 2587}}
	value["adminProvisioning"] = map[string]any{"enabled": false}
	value["ipTrust"] = map[string]any{"trustedProxies": []string{"127.0.0.1/32"}, "trustedForwarders": []string{"127.0.0.1/32"}}
	value["log"] = map[string]any{"storageLocation": p.Work, "remainLogLevel": 4, "isStdout": false, "isJson": true, "withStack": false, "rotationTime": 24, "remainRotationCount": 1}
	observation.at("chart-directory", "chart")
	dir := filepath.Join(p.Work, "chart-config")
	if e = os.Mkdir(dir, 0700); e != nil {
		return "", e
	}
	observation.at("chart-encode", "chart")
	body, e := yaml.Marshal(value)
	if e != nil {
		return "", e
	}
	observation.at("chart-write", "chart")
	if _, e = writePrivate(filepath.Join(dir, "admin-bootstrap-passwords.private.json"), map[string]string{"task_product20_admin": adminBootstrapPassword}); e != nil {
		return "", e
	}
	if e = os.WriteFile(filepath.Join(dir, "config.yaml"), body, 0600); e != nil {
		return "", e
	}
	return dir, nil
}

type product20Envelope struct {
	ErrCode *int            `json:"errCode"`
	Data    json.RawMessage `json:"data"`
	Detail  json.RawMessage `json:"errDlt"`
}

type product20HTTPExchange struct {
	Operation   string `json:"operation"`
	Outcome     string `json:"outcome"`
	Status      int    `json:"status"`
	AppCode     *int   `json:"appCode"`
	DetailClass string `json:"detailClass"`
}

type product20HTTPClient struct {
	*http.Client
	exchanges *[]product20HTTPExchange
}

func (client product20HTTPClient) post(ctx context.Context, operation, endpoint, token string, payload any) (product20Envelope, error) {
	var out product20Envelope
	exchange := product20HTTPExchange{Operation: operation, Outcome: "request", DetailClass: "none"}
	defer func() { *client.exchanges = append(*client.exchanges, exchange) }()
	raw, e := json.Marshal(payload)
	if e != nil {
		return out, e
	}
	request, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	if e != nil {
		return out, e
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("operationID", uuid.NewString())
	if token != "" {
		request.Header.Set("token", token)
	}
	exchange.Outcome = "transport"
	response, e := client.Do(request)
	if e != nil {
		return out, e
	}
	defer response.Body.Close()
	exchange.Status = response.StatusCode
	exchange.Outcome = "http"
	body, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil || len(body) > 2<<20 || response.StatusCode != http.StatusOK {
		return out, errors.New("real product HTTP transport failed")
	}
	if json.Unmarshal(body, &out) != nil || out.ErrCode == nil {
		exchange.Outcome = "malformed"
		return out, errors.New("real product HTTP response malformed")
	}
	exchange.Outcome = "envelope"
	if *out.ErrCode >= 0 && *out.ErrCode <= 0xffffffff {
		exchange.AppCode = out.ErrCode
	}
	if *out.ErrCode != 0 {
		// Only fixed route errors get labels. No upstream details leave the process.
		var detail string
		_ = json.Unmarshal(out.Detail, &detail)
		switch detail {
		case "默认服务器暂时不可用":
			exchange.DetailClass = "route-default-unavailable"
		case "服务器配置没有有效HTTPS域名", "服务器域名配置错误", "生产服务器仅允许HTTPS 443端口":
			exchange.DetailClass = "route-domain"
		case "目标服务器暂时不可用":
			exchange.DetailClass = "route-upstream-unavailable"
		case "目标服务器响应异常":
			exchange.DetailClass = "route-upstream-http"
		case "目标服务器响应格式错误", "目标服务器响应缺少状态码":
			exchange.DetailClass = "route-upstream-envelope"
		}
	}
	return out, nil
}

type product20Account struct {
	UserID     string `json:"userID"`
	ChatToken  string `json:"chatToken"`
	IMToken    string `json:"imToken"`
	ServerInfo struct {
		ServerID string   `json:"serverID"`
		Domains  []string `json:"domains"`
	} `json:"serverInfo"`
}

func product20Registration(ctx context.Context, client product20HTTPClient, aux *product20Aux, email, password string) (product20Account, error) {
	var account product20Account
	verify, e := client.post(ctx, "verify", "http://127.0.0.1:31310/api/verify/send", "", map[string]any{"email": email, "usedFor": 1})
	if e != nil || (verify.ErrCode == nil || *verify.ErrCode != 0) {
		return account, errors.New("actual route verification failed")
	}
	code, e := aux.code(email)
	if e != nil {
		return account, e
	}
	registration, e := client.post(ctx, "register", "http://127.0.0.1:31310/api/auth/register", "", map[string]any{"verifyCode": code, "autoLogin": true, "platform": 5, "deviceID": "product20-registration", "deviceModel": "task-Linux", "user": map[string]any{"email": email, "password": product20MD5(password), "nickname": "任务用户", "areaCode": ""}})
	if e != nil || (registration.ErrCode == nil || *registration.ErrCode != 0) || json.Unmarshal(registration.Data, &account) != nil || account.UserID == "" || account.ChatToken == "" || account.IMToken == "" {
		return account, errors.New("actual route registration did not return dual credentials")
	}
	return account, nil
}
func product20Login(ctx context.Context, client product20HTTPClient, email, password string) (product20Account, error) {
	var account product20Account
	reply, e := client.post(ctx, "login", "http://127.0.0.1:31310/api/auth/login", "", map[string]any{"email": email, "password": product20MD5(password), "platform": 5, "deviceID": "product20-password", "deviceModel": "task-Linux"})
	if e != nil || (reply.ErrCode == nil || *reply.ErrCode != 0) || json.Unmarshal(reply.Data, &account) != nil || account.UserID == "" || account.IMToken == "" || account.ChatToken == "" || account.ServerInfo.ServerID == "" || len(account.ServerInfo.Domains) != 1 || account.ServerInfo.Domains[0] != "127.0.0.1" {
		return account, errors.New("actual password login or tenant credentials rejected")
	}
	return account, nil
}

type product20Wire struct {
	ReqIdentifier int32
	Token         string
	SendID        string
	OperationID   string
	MsgIncr       string
	Data          []byte
	ErrCode       int
	ErrMsg        string
}

func product20Dial(ctx context.Context, roots *x509.CertPool, a product20Account) (*websocket.Conn, error) {
	u := url.URL{Scheme: "wss", Host: "127.0.0.1", Path: "/msg_gateway", RawQuery: url.Values{"sendID": {a.UserID}, "token": {a.IMToken}, "platformID": {"5"}}.Encode()}
	d := websocket.Dialer{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, HandshakeTimeout: 10 * time.Second}
	c, response, e := d.DialContext(ctx, u.String(), nil)
	if e != nil || response == nil || response.StatusCode != http.StatusSwitchingProtocols {
		if c != nil {
			c.Close()
		}
		return nil, errors.New("real gateway websocket handshake rejected")
	}
	c.SetReadLimit(1 << 20)
	return c, nil
}
func product20WireWrite(conn *websocket.Conn, value product20Wire) error {
	var body bytes.Buffer
	if e := gob.NewEncoder(&body).Encode(value); e != nil {
		return e
	}
	if e := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); e != nil {
		return e
	}
	return conn.WriteMessage(websocket.BinaryMessage, body.Bytes())
}
func product20WireRead(conn *websocket.Conn) (product20Wire, error) {
	var value product20Wire
	if e := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); e != nil {
		return value, e
	}
	kind, body, e := conn.ReadMessage()
	if e != nil || kind != websocket.BinaryMessage {
		return value, errors.New("real gateway binary response absent")
	}
	e = gob.NewDecoder(bytes.NewReader(body)).Decode(&value)
	return value, e
}
func product20Matches(groups map[string]*sdkws.PullMsgs, clientID string) []*sdkws.MsgData {
	matches := []*sdkws.MsgData{}
	for _, group := range groups {
		for _, message := range group.GetMsgs() {
			if message.GetClientMsgID() == clientID {
				matches = append(matches, message)
			}
		}
	}
	return matches
}
func product20Message(ctx context.Context, a, b product20Account, roots *x509.CertPool, onDispatch func()) (clientID, serverID string, seq int64, err error) {
	sender, e := product20Dial(ctx, roots, a)
	if e != nil {
		err = e
		return
	}
	defer sender.Close()
	peer, e := product20Dial(ctx, roots, b)
	if e != nil {
		err = e
		return
	}
	defer peer.Close()
	stopSender := context.AfterFunc(ctx, func() { _ = sender.Close() })
	defer stopSender()
	stopPeer := context.AfterFunc(ctx, func() { _ = peer.Close() })
	defer stopPeer()
	clientID = uuid.NewString()
	payload, _ := json.Marshal(map[string]string{"content": "萝卜对话 product20 ordinary acceptance"})
	message := &sdkws.MsgData{SendID: a.UserID, RecvID: b.UserID, ClientMsgID: clientID, SenderPlatformID: 5, SenderNickname: "任务用户", SessionType: 1, MsgFrom: 100, ContentType: 101, Content: payload, CreateTime: time.Now().UnixMilli(), Options: map[string]bool{constant.IsHistory: true, constant.IsPersistent: true, constant.IsSenderSync: true, constant.IsConversationUpdate: true, constant.IsSenderConversationUpdate: true, constant.IsUnreadCount: true, constant.IsOfflinePush: false}}
	encoded, e := proto.Marshal(message)
	if e != nil {
		err = e
		return
	}
	operation := uuid.NewString()
	// Exactly one send. Any missing/unknown ACK fails without redispatch.
	onDispatch()
	if err = product20WireWrite(sender, product20Wire{ReqIdentifier: 1003, SendID: a.UserID, OperationID: operation, MsgIncr: "1", Data: encoded}); err != nil {
		return
	}
	acknowledged := false
	for i := 0; i < 32; i++ {
		frame, e := product20WireRead(sender)
		if e != nil {
			err = e
			return
		}
		if frame.ReqIdentifier != 1003 {
			continue
		}
		var ack msg.SendMsgResp
		if frame.OperationID != operation || frame.MsgIncr != "1" || frame.ErrCode != 0 || proto.Unmarshal(frame.Data, &ack) != nil || ack.GetClientMsgID() != clientID || ack.GetServerMsgID() == "" {
			err = errors.New("original message acknowledgement rejected")
			return
		}
		serverID = ack.GetServerMsgID()
		acknowledged = true
		break
	}
	if !acknowledged {
		err = errors.New("original ordinary acknowledgement missing")
		return
	}
	for i := 0; i < 32; i++ {
		frame, e := product20WireRead(peer)
		if e != nil {
			err = e
			return
		}
		if frame.ReqIdentifier != 2001 {
			continue
		}
		var push sdkws.PushMessages
		if proto.Unmarshal(frame.Data, &push) != nil {
			err = errors.New("actual peer push malformed")
			return
		}
		matches := product20Matches(push.GetMsgs(), clientID)
		if len(matches) == 0 {
			continue
		}
		if len(matches) != 1 || matches[0].GetSendID() != a.UserID || matches[0].GetRecvID() != b.UserID || matches[0].GetServerMsgID() != serverID || matches[0].GetSeq() <= 0 || !bytes.Equal(matches[0].GetContent(), payload) {
			err = errors.New("actual peer message identity/body differs")
			return
		}
		seq = matches[0].GetSeq()
		break
	}
	if seq <= 0 {
		err = errors.New("actual peer message absent")
		return
	}
	uids := []string{a.UserID, b.UserID}
	if uids[1] < uids[0] {
		uids[0], uids[1] = uids[1], uids[0]
	}
	cid := "si_" + strings.Join(uids, "_")
	request := &sdkws.PullMessageBySeqsReq{UserID: b.UserID, SeqRanges: []*sdkws.SeqRange{{ConversationID: cid, Begin: seq, End: seq, Num: 1}}}
	encoded, e = proto.Marshal(request)
	if e != nil {
		err = e
		return
	}
	operation = uuid.NewString()
	if err = product20WireWrite(peer, product20Wire{ReqIdentifier: 1002, SendID: b.UserID, OperationID: operation, MsgIncr: "2", Data: encoded}); err != nil {
		return
	}
	for i := 0; i < 32; i++ {
		frame, e := product20WireRead(peer)
		if e != nil {
			err = e
			return
		}
		if frame.ReqIdentifier != 1002 {
			continue
		}
		if frame.ErrCode != 0 || frame.OperationID != operation || frame.MsgIncr != "2" {
			err = errors.New("actual positive-sequence history response rejected")
			return
		}
		var pulled sdkws.PullMessageBySeqsResp
		if e = proto.Unmarshal(frame.Data, &pulled); e != nil {
			err = e
			return
		}
		matches := product20Matches(pulled.GetMsgs(), clientID)
		if len(matches) != 1 || matches[0].GetSendID() != a.UserID || matches[0].GetRecvID() != b.UserID || matches[0].GetServerMsgID() != serverID || matches[0].GetSeq() != seq || !bytes.Equal(matches[0].GetContent(), payload) {
			err = errors.New("actual positive-sequence history identity/body differs")
			return
		}
		return
	}
	err = errors.New("actual positive-sequence history completion absent")
	return
}

// The controller installs only this new scenario's finite business Redis policy
// before PrepareFresh. Existing token-v2 principals/ACLs are never modified.
// This probe reruns actual existing permission checks against that fresh plane;
// expected refusals do not pass through the product writer observer.
func product20BusinessRedis(ctx context.Context, p PrivatePlan) (err error) {
	var plane ResourcePlane
	var credentials map[string]string
	if loadPinnedPrivate(p.PlanePath, p.PlaneSHA256, 128<<10, &plane) != nil || plane.ValidateLive(ctx) != nil || loadPinnedPrivate(p.CredentialsPath, p.CredentialsSHA256, 64<<10, &credentials) != nil {
		return errors.New("original fresh business plane required")
	}
	admin := redisClient(credentials["redisAdminUser"], credentials["redisAdminPassword"])
	defer func() { err = errors.Join(err, admin.Close()) }()
	// Only this task's runtime principal gains the finite ordinary/registration
	// key domains. The legacy principal and four original scenarios are intact.
	if e := retryRead(ctx, func() error { return admin.Ping(ctx).Err() }); e != nil {
		return e
	}
	args := []any{"ACL", "SETUSER", credentials["redisRuntimeUser"], "resetkeys", "resetchannels", "-@all"}
	for _, key := range []string{"USER_INFO:*", "USER_GLOBAL_RECV_MSG_OPT_KEY:*", "ACCOUNT_DISABLED:*", "ONLINE_STATUS:*", "UID_PID_TOKEN_STATUS:*", "FCM_TOKEN:*", "CHAT_UID_TOKEN_STATUS:*", "ROUTE_REGISTRATION_NONCE:*", "LUOBO_SERVICE_NONCE:*", "CHAT:LOGIN_FAIL:*", "CHAT:HOURLY_LOGIN_REGISTER_IP:*", "CONVERSATION:*", "CONVERSATION_IDS:*", "CONVERSATION_IDS_HASH:*", "CONVERSATION_HAS_READ_SEQ:*", "RECV_MSG_OPT:*", "CONVERSATION_NOT_RECEIVE_MESSAGE_USER_IDS:*", "FRIEND_IDS:*", "COMMON_FRIENDS_IDS:*", "FRIEND_INFO:*", "BLACK_IDS:*", "MAX_SEQ:*", "MIN_SEQ:*", "CON_USER_MIN_SEQ:*", "HAS_READ_SEQ:*", "MESSAGE_CACHE:*", "MESSAGE_CLIENT_SEQ:*", "MESSAGE_DEL_USER_LIST:*", "USER_DEL_MESSAGES_LIST:*", "USER_BADGE_UNREAD_COUNT_SUM:*", "cron_clear_msg_and_fix_seq", "cron_conversations_destruct_msgs", "GLOBAL_CONFIG:FORBID_NEW_MEMBER_HISTORY"} {
		args = append(args, "~"+key)
	}
	args = append(args, "&sensitive_word_reload")
	for _, command := range []string{"auth", "hello", "ping", "quit", "info", "acl|whoami", "client|info", "client|setinfo", "get", "set", "setnx", "del", "exists", "hget", "hmget", "hgetall", "hset", "hsetnx", "hdel", "expire", "pexpire", "ttl", "pttl", "eval", "evalsha", "incr", "incrby", "decr", "sadd", "srem", "scard", "smembers", "subscribe", "unsubscribe", "publish"} {
		args = append(args, "+"+command)
	}
	return admin.Do(ctx, args...).Err()
}
func RunProduct20(ctx context.Context, p Product20Plan) (runErr error) {
	result := product20Result{Version: "fresh-product20-http-ws-result-v1", Phase: "fresh-core-prepare", FreshSQLSeedOnly: true, HTTPExchanges: []product20HTTPExchange{}}
	result.StartupBoundary = product20StartupBoundary{Version: "product20-startup-boundary-v1", Substep: "not-entered", Role: "none", PrimaryCause: product20StartupCauseOf(nil)}
	observation := &result.StartupBoundary
	var boot *Bootstrap
	var session *Session
	var aux *product20Aux
	children := []*product20Child{}
	var migration *product20Child
	var routePool, chartPool *sql.DB
	defer func() {
		// Snapshot the primary return before cleanup can join secondary errors.
		observation.SixForked = len(children)
		if result.Phase == "fresh-sql-and-six-real-services" {
			observation.PrimaryCause = product20StartupCauseOf(runErr)
		}
		cleanup, stop := context.WithTimeout(context.Background(), 120*time.Second)
		defer stop()
		childrenErr := product20StopChildren(cleanup, children)
		migrationErr := product20StopChildren(cleanup, []*product20Child{migration})
		runErr = errors.Join(runErr, childrenErr, migrationErr)
		result.OriginalSixChildrenExited = len(children) == 6 && childrenErr == nil && migrationErr == nil
		if aux != nil {
			e := aux.close(cleanup)
			result.ConfigProxyStatus = int(aux.configStatus.Load())
			runErr = errors.Join(runErr, e)
			result.AuxiliaryListenersJoined = e == nil
		}
		if routePool != nil {
			runErr = errors.Join(runErr, routePool.Close())
		}
		if chartPool != nil {
			runErr = errors.Join(runErr, chartPool.Close())
		}
		runErr = errors.Join(runErr, cleanupAcceptanceSession(cleanup, session))
		result.OriginalCoreCleanupComplete = session != nil && session.CleanupComplete()
		if boot != nil {
			runErr = errors.Join(runErr, boot.Close(cleanup))
		}
		result.Accepted = runErr == nil && result.ProductProcesses == 20 && result.RealRegistrations == 2 && result.RealPasswordLogins == 2 && result.OrdinaryDispatches == 1 && result.PeerMessages == 1 && result.HistoryMessages == 1 && result.WrongPasswordRejected && result.BusinessPermissions && result.ConfigHTTP && result.ChatTokenSelfRead && result.OriginalSixChildrenExited && result.OriginalCoreCleanupComplete && result.AuxiliaryListenersJoined
		if !result.Accepted && runErr == nil {
			runErr = errors.New("incomplete product20 acceptance")
		}
		if runErr != nil {
			result.FailureSHA256 = digestBytes([]byte(runErr.Error()))
		}
		if _, e := writePrivate(filepath.Join(p.Work, "product20-result.json"), result); e != nil {
			runErr = errors.Join(runErr, e)
		}
	}()
	var e error
	var plane ResourcePlane
	if e = loadPinnedPrivate(p.Core.PlanePath, p.Core.PlaneSHA256, 128<<10, &plane); e != nil {
		return e
	}
	if e = plane.ValidateLive(ctx); e != nil {
		return e
	}
	routeDB, chartDB, routeUser, chartUser, routePass, chartPass, e := product20CreateSQL(ctx, p.SQLRootPassword, plane.Nonce)
	if e != nil {
		return e
	}
	routePool, e = product20SQL(ctx, p.SQLRootPassword, routeDB, routeUser, routePass)
	if e != nil {
		return e
	}
	chartPool, e = product20SQL(ctx, p.SQLRootPassword, chartDB, chartUser, chartPass)
	if e != nil {
		return e
	}
	// Fresh fixture tenant/policy preconditions only. Real account creation is
	// exclusively the two production HTTP registrations below.
	if _, e = chartPool.ExecContext(ctx, "CREATE TABLE client_config (`key` varchar(255) NOT NULL PRIMARY KEY, value TEXT NOT NULL)"); e != nil {
		return e
	}
	for key, value := range map[string]string{"needVerificationCodeRegister": "1", "needInvitationCodeRegister": "0", "ordinaryUserAddFriend": "1", "allowSendMsgNotFriend": "1"} {
		if _, e = chartPool.ExecContext(ctx, "INSERT INTO client_config(`key`,value) VALUES(?,?)", key, value); e != nil {
			return e
		}
	}
	if _, e = writePrivate(filepath.Join(p.Core.Work, "product20-sql.json"), product20SQLBinding{Nonce: plane.Nonce, Database: chartDB, User: chartUser, Password: chartPass}); e != nil {
		return e
	}
	if e = product20BusinessRedis(ctx, p.Core); e != nil {
		return e
	}
	boot, e = PrepareFresh(ctx, p.Core)
	if e != nil {
		return e
	}
	result.CoreSourceSHA256 = boot.Services.Requirements[0].SourceSHA256
	result.BusinessPermissions = boot.BusinessPermissions.Version != ""
	session, e = Start(ctx, boot.Inputs)
	if e != nil {
		return e
	}
	if session.Runtime == nil || len(session.Runtime.Started) != 14 || session.Runtime.AdmissionSHA256 == "" {
		return errors.New("actual original core graph not admitted")
	}
	for _, role := range session.Runtime.Started {
		result.CoreOriginalPIDs = append(result.CoreOriginalPIDs, role.Runtime.PID)
	}
	result.ProductProcesses = 14
	result.Phase = "fresh-sql-and-six-real-services"
	observation.Entered = true
	aux, e = product20NewAux(p.Work, chartPass, observation)
	if e != nil {
		return e
	}
	if e = aux.start(observation); e != nil {
		return e
	}
	chartConfig, e := product20Configuration(p, boot, aux, chartDB, chartUser, chartPass, observation)
	if e != nil {
		return e
	}
	baseEnv := []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME"), "TMPDIR=" + p.Work, "SSL_CERT_FILE=" + aux.caPath}
	products := map[string]Product20Binary{}
	for _, b := range p.Products {
		products[b.Name] = b
	}
	observation.at("route-secret", "route-service")
	routeSecret, e := product20Random()
	if e != nil {
		return e
	}
	observation.at("route-upstream-encode", "route-service")
	shared, e := json.Marshal(map[string]string{boot.Header.Tenant: boot.Credential["serviceSecret"]})
	if e != nil {
		return e
	}
	routeEnv := append(append([]string{}, baseEnv...), "ROUTE_PORT=31310", "ROUTE_LISTEN_HOST=127.0.0.1", "ROUTE_MYSQL_HOST=127.0.0.1", "ROUTE_MYSQL_PORT=3306", "ROUTE_MYSQL_USER="+routeUser, "ROUTE_MYSQL_PASSWORD="+routePass, "ROUTE_MYSQL_DATABASE="+routeDB, "ROUTE_ADMIN_TOKEN="+routeSecret, "ROUTE_UPSTREAM_SECRETS="+string(shared), "ROUTE_ALLOWED_ORIGINS=https://127.0.0.1")
	observation.at("route-migrate-start", "route-service")
	migration, e = product20StartChild(ctx, products["route-service"], filepath.Join(p.Work, "route-migrate"), []string{"--migrate"}, routeEnv)
	if e != nil {
		return e
	}
	observation.at("route-migrate-wait", "route-service")
	select {
	case <-migration.done:
		for _, f := range migration.files {
			_ = f.Close()
		}
		if migration.err != nil {
			return migration.err
		}
	case <-ctx.Done():
		return ctx.Err()
	}
	observation.at("route-tenant-seed", "route-service")
	if _, e = routePool.ExecContext(ctx, "INSERT INTO server_config(server_id,server_name,domains,is_default,status,max_users,current_users,description,created_at,updated_at) VALUES(?,?,?,1,1,0,0,?,NOW(),NOW())", boot.Header.Tenant, "萝卜对话 task tenant", "127.0.0.1", "fresh product20 fixture only"); e != nil {
		return e
	}
	ports := map[string]int{"admin-rpc": 31210, "chat-rpc": 31220, "office-rpc": 31230, "admin-api": 31009, "chat-api": 31008, "route-service": 31310}
	for _, name := range product20Names {
		args := []string{"-config_folder_path", filepath.Join(chartConfig, "config.yaml"), "-port", strconv.Itoa(ports[name])}
		env := baseEnv
		if name == "admin-rpc" {
			env = append(append([]string{}, baseEnv...), "LUOBO_ADMIN_BOOTSTRAP_PASSWORDS_FILE="+filepath.Join(chartConfig, "admin-bootstrap-passwords.private.json"))
		}
		if name == "chat-api" {
			env = append(append([]string{}, baseEnv...), "LUOBO_ROUTE_SHARED_SECRET="+boot.Credential["serviceSecret"])
		}
		if name == "route-service" {
			args = nil
			env = routeEnv
		}
		observation.at("product-start", name)
		child, e := product20StartChild(ctx, products[name], filepath.Join(p.Work, name), args, env)
		if child != nil {
			children = append(children, child)
			result.SixOriginalPIDs = append(result.SixOriginalPIDs, child.cmd.Process.Pid)
		}
		if e != nil {
			return e
		}
		observation.at("product-wait-port", name)
		if e = product20WaitPort(ctx, ports[name], child); e != nil {
			return e
		}
		result.ProductProcesses++
		observation.SixReady++
	}
	observation.at("complete", "none")
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{RootCAs: aux.roots, MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	httpClient := &http.Client{Transport: transport, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("task redirect refused") }}
	client := product20HTTPClient{Client: httpClient, exchanges: &result.HTTPExchanges}
	result.Phase = "actual-route-config-and-registration"
	configuration, e := client.post(ctx, "config", "http://127.0.0.1:31310/api/client_config/get", "", map[string]any{})
	if e != nil || (configuration.ErrCode == nil || *configuration.ErrCode != 0) {
		return errors.New("actual route configuration failed")
	}
	var configReply struct {
		Config map[string]string `json:"config"`
	}
	if json.Unmarshal(configuration.Data, &configReply) != nil || configReply.Config["needVerificationCodeRegister"] != "1" {
		return errors.New("actual policy response differs")
	}
	result.ConfigHTTP = true
	accounts := []product20Account{}
	passwords := []string{}
	emails := []string{}
	for i := 0; i < 2; i++ {
		email := "product20-" + strconv.Itoa(i) + "-" + boot.Resource.Nonce + "@product20.invalid"
		password, e := product20Random()
		if e != nil {
			return e
		}
		account, e := product20Registration(ctx, client, aux, email, password)
		if e != nil {
			return e
		}
		result.RealRegistrations++
		emails = append(emails, email)
		passwords = append(passwords, password)
		accounts = append(accounts, account)
	}
	result.Phase = "actual-route-password-login-dual-token"
	for i := range accounts {
		actual, e := product20Login(ctx, client, emails[i], passwords[i])
		if e != nil {
			return e
		}
		if actual.UserID != accounts[i].UserID || actual.ServerInfo.ServerID != boot.Header.Tenant {
			return errors.New("original account or tenant changed across password login")
		}
		accounts[i] = actual
		result.RealPasswordLogins++
		result.UsersHash = append(result.UsersHash, digestBytes([]byte(actual.UserID)))
	}
	if accounts[0].UserID == accounts[1].UserID {
		return errors.New("two distinct actual accounts required")
	}
	result.DualTokens = true
	for _, account := range accounts {
		self, e := client.post(ctx, "self", "https://127.0.0.1/chat/user/find/full", account.ChatToken, map[string]any{"userIDs": []string{account.UserID}})
		var profile struct {
			Users []struct {
				UserID string `json:"userID"`
			} `json:"users"`
		}
		if e != nil || self.ErrCode == nil || *self.ErrCode != 0 || json.Unmarshal(self.Data, &profile) != nil || len(profile.Users) != 1 || profile.Users[0].UserID != account.UserID {
			return errors.New("actual chat credential self identity rejected")
		}
	}
	result.ChatTokenSelfRead = true
	wrong, e := client.post(ctx, "wrong-password", "http://127.0.0.1:31310/api/auth/login", "", map[string]any{"email": emails[0], "password": product20MD5(passwords[0] + "wrong"), "platform": 5, "deviceID": "product20-password", "deviceModel": "task-Linux"})
	if e != nil || (wrong.ErrCode == nil || *wrong.ErrCode != 20001) {
		return errors.New("actual fixed password rejection missing")
	}
	result.WrongPasswordRejected = true
	result.Phase = "actual-gateway-ordinary-message-once"
	clientID, serverID, seq, e := product20Message(ctx, accounts[0], accounts[1], aux.roots, func() { result.OrdinaryDispatches++ })
	if e != nil {
		return e
	}
	result.PeerMessages = 1
	result.HistoryMessages = 1
	result.MessageSeq = seq
	result.ClientIDHash = digestBytes([]byte(clientID))
	result.ServerIDHash = digestBytes([]byte(serverID))
	result.Phase = "ordinary-peer-history-accepted-local-cleanup-required"
	return nil
}
