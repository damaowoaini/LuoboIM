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
	"github.com/openimsdk/open-im-server/v3/internal/messagedelivery"
	"github.com/openimsdk/open-im-server/v3/internal/msgtransfer"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceimport/ownedcontrol"
	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
	"github.com/openimsdk/open-im-server/v3/pkg/common/serviceauth"
	"github.com/openimsdk/open-im-server/v3/pkg/msgprocessor"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
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
type product20BrowserPlan struct {
	Socket string `json:"socket"`
	Dist   string `json:"dist"`
}

type product20BrowserResult struct {
	ConfigAccepted   bool                        `json:"configAccepted"`
	LoginAccepted    bool                        `json:"loginAccepted"`
	GatewayConnected bool                        `json:"gatewayConnected"`
	SelfAccepted     bool                        `json:"selfAccepted"`
	WorkspaceVisible bool                        `json:"workspaceVisible"`
	Observation      product20BrowserObservation `json:"observation"`
}

type product20BrowserObservation struct {
	Stage           string `json:"stage"`
	DocumentStatus  int    `json:"documentStatus"`
	ConfigStatus    int    `json:"configStatus"`
	ConfigRequested bool   `json:"configRequested"`
	ChromeFailure   string `json:"chromeFailure"`
}

func (r product20BrowserResult) accepted() bool {
	return r.ConfigAccepted && r.LoginAccepted && r.GatewayConnected && r.SelfAccepted && r.WorkspaceVisible && r.Observation.Stage == "complete"
}

type Product20Plan struct {
	Version             string                `json:"version"`
	Work                string                `json:"work"`
	Core                PrivatePlan           `json:"core"`
	ChartTemplatePath   string                `json:"chartTemplatePath"`
	ChartTemplateSHA256 string                `json:"chartTemplateSHA256"`
	SQLRootPassword     string                `json:"sqlRootPassword"`
	Products            []Product20Binary     `json:"products"`
	Browser             *product20BrowserPlan `json:"browser,omitempty"`
}
type product20Result struct {
	Scope                       string                   `json:"scope,omitempty"`
	Browser                     *product20BrowserResult  `json:"browser,omitempty"`
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

	RegistrationFailure *product20RegistrationObservation    `json:"registrationFailure,omitempty"`
	MessageObservation  *product20MessageObservation         `json:"messageObservation,omitempty"`
	GatewayPresence     *product20GatewayPresenceObservation `json:"gatewayPresence,omitempty"`
	DeliverySnapshot    *product20DeliverySnapshot           `json:"deliverySnapshot,omitempty"`
	ManagedDrain        *product20ManagedDrainObservation    `json:"managedDrain,omitempty"`
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
	if p.Browser != nil {
		if p.Browser.Socket != filepath.Join(p.Work, "browser-smoke.sock") ||
			p.Browser.Dist != filepath.Join(filepath.Dir(p.Work), "web-bundle", "dist") || canonicalDirectory(p.Browser.Dist) != nil {
			return p, errors.New("browser socket and bundle must be inside the original task allocation")
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

	hopMu         sync.Mutex
	hops          []product20RegistrationHop
	hopsTruncated bool
}

type product20RegistrationHop struct {
	Hop     string `json:"hop"`
	Status  int    `json:"status"`
	AppCode *int   `json:"appCode"`
}

// Only the finite registration dependencies are observed across this task.
// Background work can also call these routes, so these are run-scoped events.
func (a *product20Aux) observeRegistrationHop(path string, status int) func(*int) {
	var hop string
	switch path {
	case "/chat/account/register":
		hop = "chart-register"
	case "/chat/internal/registration/receipt":
		hop = "chart-receipt"
	case "/api/auth/user_token":
		hop = "core-token"
	case "/api/user/get_users_info":
		hop = "core-user-read"
	case "/api/user/user_register":
		hop = "core-user-create"
	default:
		return nil
	}
	a.hopMu.Lock()
	defer a.hopMu.Unlock()
	if len(a.hops) >= 16 {
		a.hopsTruncated = true
		return nil
	}
	index := len(a.hops)
	a.hops = append(a.hops, product20RegistrationHop{Hop: hop, Status: status})
	return func(code *int) {
		a.hopMu.Lock()
		defer a.hopMu.Unlock()
		a.hops[index].AppCode = code
	}
}

// Capture only complete small JSON envelopes as the original proxy reads them.
// A partial or larger body still flows through the proxy unchanged.
type product20EnvelopeTap struct {
	io.ReadCloser
	body    []byte
	observe func(*int)
}

func (tap *product20EnvelopeTap) Read(p []byte) (int, error) {
	n, err := tap.ReadCloser.Read(p)
	if tap.observe == nil {
		return n, err
	}
	if len(tap.body)+n > 16<<10 {
		tap.body, tap.observe = nil, nil
		return n, err
	}
	tap.body = append(tap.body, p[:n]...)
	if err != nil {
		var response struct {
			Code *int `json:"errCode"`
		}
		if err == io.EOF && json.Unmarshal(tap.body, &response) == nil && response.Code != nil && *response.Code >= 0 && *response.Code <= 0xffffffff {
			tap.observe(response.Code)
		}
		tap.body, tap.observe = nil, nil
	}
	return n, err
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
func (a *product20Aux) start(observation *product20StartupBoundary, browser *product20BrowserPlan) error {
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
			if response.Request != nil {
				path := p + response.Request.URL.Path
				if path == "/chat/client_config/get" {
					a.configStatus.Store(int32(response.StatusCode))
				}
				if observe := a.observeRegistrationHop(path, response.StatusCode); observe != nil {
					response.Body = &product20EnvelopeTap{ReadCloser: response.Body, observe: observe}
				}
			}
			return nil
		}
		proxy.ErrorHandler = func(w http.ResponseWriter, request *http.Request, _ error) {
			path := p + request.URL.Path
			if path == "/chat/client_config/get" {
				a.configStatus.Store(http.StatusBadGateway)
			}
			a.observeRegistrationHop(path, http.StatusBadGateway)
			http.Error(w, "task upstream unavailable", http.StatusBadGateway)
		}
		proxies[prefix] = proxy
	}
	var route *httputil.ReverseProxy
	var assets http.Handler
	if browser != nil {
		upstream, _ := url.Parse("http://127.0.0.1:31310")
		route = httputil.NewSingleHostReverseProxy(upstream)
		transport := &http.Transport{ResponseHeaderTimeout: 10 * time.Second}
		route.Transport = transport
		a.transports = append(a.transports, transport)
		assets = http.FileServer(http.Dir(browser.Dist))
	}
	a.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if route != nil && r.Method == http.MethodPost {
			switch r.URL.Path {
			case "/api/client_config/get", "/api/auth/login", "/api/auth/register", "/api/auth/reset-password", "/api/verify/send", "/api/verify/check":
				route.ServeHTTP(w, r)
				return
			}
		}
		for prefix, p := range proxies {
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				p.ServeHTTP(w, r)
				return
			}
		}
		if assets != nil && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			name := r.URL.Path
			if name == "/" {
				name = "/index.html"
			}
			if filepath.Clean(name) == name && strings.HasPrefix(name, "/") {
				if info, err := os.Stat(filepath.Join(browser.Dist, name)); err == nil && info.Mode().IsRegular() {
					w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
					w.Header().Set("Cross-Origin-Embedder-Policy", "credentialless")
					assets.ServeHTTP(w, r)
					return
				}
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
		case "注册正在确认，请稍后重试；账号创建成功后会自动恢复绑定":
			exchange.DetailClass = "route-registration-pending"
		case "账号注册和路由已恢复，请返回登录":
			exchange.DetailClass = "route-registration-recovered"
		case "该租户尚未配置安全注册通道":
			exchange.DetailClass = "route-registration-unconfigured"
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

var errProduct20Registration = errors.New("actual route registration did not return dual credentials")

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
		return account, errProduct20Registration
	}
	return account, nil
}

type product20RegistrationObservation struct {
	Attempt       int                        `json:"attempt"`
	RouteState    string                     `json:"routeState"`
	ChartState    string                     `json:"chartState"`
	ChartReason   string                     `json:"chartReason"`
	IMProvisioned bool                       `json:"imProvisioned"`
	RunScopedHops []product20RegistrationHop `json:"runScopedHops"`
	HopsTruncated bool                       `json:"hopsTruncated"`
}

func (o *product20RegistrationObservation) readState(ctx context.Context, route, chart *sql.DB, tenant, email string) {
	sum := sha256.Sum256([]byte(strings.ToLower(email)))
	identity := "email:" + base64.RawURLEncoding.EncodeToString(sum[:])
	var id, state string
	e := route.QueryRowContext(ctx, "SELECT registration_id,state FROM registration_intent WHERE account_key=? AND server_id=?", identity, tenant).Scan(&id, &state)
	switch {
	case errors.Is(e, sql.ErrNoRows):
		o.RouteState = "missing"
		return
	case e != nil:
		return
	}
	switch state {
	case "reserved", "confirmed", "bound":
		o.RouteState = state
	default:
		o.RouteState = "other"
	}
	o.ChartState = "unavailable"
	var job, reason sql.NullString
	e = chart.QueryRowContext(ctx, `SELECT j.state,j.reason,r.im_provisioned_at IS NOT NULL
		FROM route_registration_receipts r
		LEFT JOIN admin_provision_jobs j ON j.receipt_id=r.registration_id AND j.user_id=r.user_id AND j.source='public_creation'
		WHERE r.registration_id=? AND r.normalized_identity=?`, id, identity).Scan(&job, &reason, &o.IMProvisioned)
	switch {
	case errors.Is(e, sql.ErrNoRows):
		o.ChartState = "missing"
		return
	case e != nil:
		o.IMProvisioned = false
		return
	}
	switch job.String {
	case "awaiting_im", "pending", "conflict", "direct_only", "bound":
		o.ChartState = job.String
	default:
		if job.Valid {
			o.ChartState = "other"
		} else {
			o.ChartState = "receipt-only"
		}
	}
	switch reason.String {
	case "":
		o.ChartReason = "none"
	case "defaults_unconfirmed", "payload_integrity", "unknown_creation_source", "identity_unconfirmed", "identity_changed", "public_receipt_unconfirmed", "im_owner_unconfirmed", "delivery_unconfirmed":
		o.ChartReason = reason.String
	default:
		o.ChartReason = "other"
	}
}

func product20Login(ctx context.Context, client product20HTTPClient, email, password string) (product20Account, error) {
	var account product20Account
	reply, e := client.post(ctx, "login", "http://127.0.0.1:31310/api/auth/login", "", map[string]any{"email": email, "password": product20MD5(password), "platform": 5, "deviceID": "product20-password", "deviceModel": "task-Linux"})
	if e != nil || (reply.ErrCode == nil || *reply.ErrCode != 0) || json.Unmarshal(reply.Data, &account) != nil || account.UserID == "" || account.IMToken == "" || account.ChatToken == "" || account.ServerInfo.ServerID == "" || len(account.ServerInfo.Domains) != 1 || account.ServerInfo.Domains[0] != "127.0.0.1" {
		return account, errors.New("actual password login or tenant credentials rejected")
	}
	return account, nil
}

// Only the message caller writes this diagnostic; heartbeat workers never do.
// None of these fields contains message data, peer identity, or error text.
type product20MessageObservation struct {
	Step            string `json:"step"`
	AckAccepted     bool   `json:"ackAccepted"`
	PeerAccepted    bool   `json:"peerAccepted"`
	HistoryAccepted bool   `json:"historyAccepted"`
	ReadOutcome     string `json:"readOutcome"`
	ReadTimeout     bool   `json:"readTimeout"`
	ReadCloseCode   int    `json:"readCloseCode"`
}

func (o *product20MessageObservation) readError(err error) {
	o.ReadOutcome = "read-error"
	var timeout net.Error
	if errors.As(err, &timeout) {
		o.ReadTimeout = timeout.Timeout()
	}
	var closed *websocket.CloseError
	if errors.As(err, &closed) && closed.Code >= 1000 && closed.Code <= 4999 {
		o.ReadCloseCode = closed.Code
	}
}

// WriteControl is safe alongside the caller's sole data writer and reader.
// Each worker has one cancellation owner and is joined before its socket closes.
func product20Heartbeat(ctx context.Context, conn *websocket.Conn) func() {
	lifetime, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-lifetime.Done():
				return
			case <-ticker.C:
				if lifetime.Err() != nil {
					return
				}
				if conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second)) != nil {
					return
				}
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

// Preserve the existing protobuf return while observing only a closed outcome.
func product20MessageDecode(body []byte, value proto.Message, observation *product20MessageObservation) error {
	err := proto.Unmarshal(body, value)
	if err != nil {
		observation.ReadOutcome = "decode-error"
	}
	return err
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
func product20WireRead(conn *websocket.Conn, observation *product20MessageObservation) (product20Wire, error) {
	var value product20Wire
	observation.ReadOutcome, observation.ReadTimeout, observation.ReadCloseCode = "none", false, 0
	if e := conn.SetReadDeadline(time.Now().Add(30 * time.Second)); e != nil {
		observation.readError(e)
		return value, e
	}
	kind, body, e := conn.ReadMessage()
	if e != nil {
		observation.readError(e)
		return value, errors.New("real gateway binary response absent")
	}
	if kind != websocket.BinaryMessage {
		observation.ReadOutcome = "nonbinary"
		return value, errors.New("real gateway binary response absent")
	}
	e = gob.NewDecoder(bytes.NewReader(body)).Decode(&value)
	if e != nil {
		observation.ReadOutcome = "decode-error"
	}
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

type product20GatewayPresenceObservation struct {
	Available    bool `json:"available"`
	SenderOnline bool `json:"senderOnline"`
	PeerOnline   bool `json:"peerOnline"`
}

func product20GatewayPresence(parent context.Context, client *http.Client, boot *Bootstrap, sender, peer product20Account) *product20GatewayPresenceObservation {
	unavailable := &product20GatewayPresenceObservation{}
	if client == nil || boot == nil || sender.UserID == "" || peer.UserID == "" || sender.UserID == peer.UserID || sender.IMToken == "" || peer.IMToken == "" {
		return unavailable
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	secret := boot.Credential["serviceSecret"]
	// Both requests and all response material stay private. This helper uses the
	// original HTTP client directly, outside the acceptance HTTP exchange ledger.
	post := func(endpoint, logicalPath, token string, payload any) (json.RawMessage, bool) {
		body, e := json.Marshal(payload)
		if e != nil {
			return nil, false
		}
		request, e := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if e != nil {
			return nil, false
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("operationID", uuid.NewString())
		if token != "" {
			request.Header.Set("token", token)
		}
		if logicalPath != "" {
			proof, e := serviceauth.Sign([]byte(secret), serviceauth.Internal, http.MethodPost, logicalPath, token, body, time.Now())
			if e != nil {
				return nil, false
			}
			for key, values := range proof {
				request.Header[key] = values
			}
		}
		response, e := client.Do(request)
		if e != nil {
			return nil, false
		}
		defer response.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		if e != nil || len(raw) > 64<<10 || response.StatusCode != http.StatusOK {
			return nil, false
		}
		var envelope product20Envelope
		if json.Unmarshal(raw, &envelope) != nil || envelope.ErrCode == nil || *envelope.ErrCode != 0 {
			return nil, false
		}
		return envelope.Data, true
	}
	authData, ok := post("https://127.0.0.1/api/auth/user_token", "", "", map[string]any{
		"secret": secret, "userID": boot.ManagerID, "platformID": constant.AdminPlatformID,
	})
	var manager struct {
		Token string `json:"token"`
	}
	if !ok || json.Unmarshal(authData, &manager) != nil || manager.Token == "" {
		return unavailable
	}
	data, ok := post("https://127.0.0.1/api/user/get_users_online_status", "/user/get_users_online_status", manager.Token,
		map[string]any{"userIDs": []string{sender.UserID, peer.UserID}})
	var rows []struct {
		UserID               string `json:"userID"`
		Status               string `json:"status"`
		DetailPlatformStatus []struct {
			Platform string `json:"platform"`
			Status   string `json:"status"`
			Token    string `json:"token"`
		} `json:"detailPlatformStatus"`
	}
	if !ok || json.Unmarshal(data, &rows) != nil || len(rows) != 2 {
		return unavailable
	}
	seen := map[string]bool{}
	result := &product20GatewayPresenceObservation{Available: true}
	for _, row := range rows {
		if seen[row.UserID] || row.UserID != sender.UserID && row.UserID != peer.UserID || row.Status != constant.OnlineStatus && row.Status != constant.OfflineStatus {
			return unavailable
		}
		seen[row.UserID] = true
		expectedToken := sender.IMToken
		if row.UserID == peer.UserID {
			expectedToken = peer.IMToken
		}
		online := false
		if row.Status == constant.OnlineStatus {
			for _, detail := range row.DetailPlatformStatus {
				if detail.Platform == constant.PlatformIDToName(constant.WebPlatformID) && detail.Status == constant.OnlineStatus && detail.Token == expectedToken {
					online = true
				}
			}
		}
		if row.UserID == sender.UserID {
			result.SenderOnline = online
		} else {
			result.PeerOnline = online
		}
	}
	return result
}

type product20DeliverySnapshot struct {
	State             string `json:"state"`
	DutyState         string `json:"dutyState"`
	KafkaReceipt      *bool  `json:"kafkaReceipt"`
	SourceReceipt     *bool  `json:"sourceReceipt"`
	MaxPending        *bool  `json:"maxPending"`
	SenderReadPending *bool  `json:"senderReadPending"`
	OnlinePending     *bool  `json:"onlinePending"`
	PushPending       *bool  `json:"pushPending"`
	OnlineUnconfirmed *bool  `json:"onlineUnconfirmed"`
	PeerDenied        *bool  `json:"peerDenied"`
	OnlineAttempts    *int64 `json:"onlineAttempts"`
	AttemptsTruncated bool   `json:"attemptsTruncated"`
}

type product20DeliveryDuty struct {
	ID                string    `bson:"_id"`
	BranchID          string    `bson:"branch_id"`
	ConversationID    string    `bson:"conversation_id"`
	Persist           *bool     `bson:"persist"`
	Seq               int64     `bson:"seq"`
	SourceID          string    `bson:"source_id"`
	State             string    `bson:"state"`
	MaxPending        *bool     `bson:"max_pending"`
	SenderReadPending *bool     `bson:"sender_read_pending"`
	OnlinePending     *bool     `bson:"online_pending"`
	PushPending       *bool     `bson:"push_pending"`
	OnlineUnconfirmed *bool     `bson:"online_last_unconfirmed"`
	VisibilityDenied  *[]string `bson:"visibility_denied"`
	OnlineAttempts    *int64    `bson:"online_attempts"`
}

func product20ReceiptObserved(ctx context.Context, db *mongo.Database, epoch, source, kind string) *bool {
	body, _ := json.Marshal([]string{epoch, kind, source})
	filter := bson.M{"_id": digestBytes(body), "epoch": epoch, "source_id": source}
	if kind == "kafka-source" {
		filter["kind"] = "kafka-source-v1"
	}
	err := db.Collection(messagedelivery.Receipts).FindOne(ctx, filter, options.FindOne().
		SetProjection(bson.M{"_id": 1}).SetMaxTime(2*time.Second)).Err()
	switch {
	case err == nil:
		found := true
		return &found
	case errors.Is(err, mongo.ErrNoDocuments):
		found := false
		return &found
	default:
		return nil
	}
}

// Inspect only the original task's matched message. These reads confer no
// recovery or delivery authority and leave the original message result intact.
func product20ReadDeliverySnapshot(parent context.Context, boot *Bootstrap, operation, sender, peer string) *product20DeliverySnapshot {
	out := &product20DeliverySnapshot{State: "unavailable", DutyState: "unknown"}
	if boot == nil || boot.AdminMongo == nil {
		return out
	}
	id, err := uuid.Parse(operation)
	if err != nil || id.String() != operation {
		return out
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	db := boot.AdminMongo.Database(boot.Header.Database, options.Database().
		SetReadPreference(readpref.Primary()).
		SetReadConcern(readconcern.Majority()))
	filter := bson.M{"epoch": boot.Header.Epoch, "producer_version": "ordinary-kafka-v1", "source_operation_id": operation, "sender_id": sender, "conversation_id": msgprocessor.GetConversationIDBySessionType(constant.SingleChatType, sender, peer)}
	projection := bson.M{"_id": 1, "branch_id": 1, "conversation_id": 1, "persist": 1, "seq": 1, "source_id": 1, "state": 1, "max_pending": 1, "sender_read_pending": 1, "online_pending": 1, "push_pending": 1, "online_last_unconfirmed": 1, "visibility_denied": 1, "online_attempts": 1}
	cursor, err := db.Collection(messagedelivery.Effects).Find(ctx, filter, options.Find().
		SetProjection(projection).SetLimit(2).SetBatchSize(2).SetMaxTime(2*time.Second))
	if err != nil {
		return out
	}
	defer cursor.Close(ctx)
	var rows []product20DeliveryDuty
	if err := cursor.All(ctx, &rows); err != nil {
		return out
	}
	switch len(rows) {
	case 0:
		out.State = "absent"
		return out
	case 1:
	default:
		out.State = "ambiguous"
		return out
	}
	duty := rows[0]
	branch, branchErr := uuid.Parse(duty.BranchID)
	source, sourceErr := uuid.Parse(duty.SourceID)
	keyBody, _ := json.Marshal([]string{boot.Header.Epoch, "branch", duty.BranchID})
	if branchErr != nil || branch.String() != duty.BranchID || sourceErr != nil || source.String() != duty.SourceID ||
		duty.ID != digestBytes(keyBody) || duty.Persist == nil || !*duty.Persist || duty.Seq <= 0 ||
		duty.ConversationID != msgprocessor.GetConversationIDBySessionType(constant.SingleChatType, sender, peer) {
		return out
	}
	out.State = "observed"
	switch duty.State {
	case "ready", "done":
		out.DutyState = duty.State
	case "":
	default:
		out.DutyState = "other"
	}
	out.MaxPending, out.SenderReadPending = duty.MaxPending, duty.SenderReadPending
	out.OnlinePending, out.PushPending = duty.OnlinePending, duty.PushPending
	out.OnlineUnconfirmed = duty.OnlineUnconfirmed
	if duty.OnlineAttempts != nil && *duty.OnlineAttempts >= 0 {
		attempts := *duty.OnlineAttempts
		if attempts > 255 {
			attempts = 255
			out.AttemptsTruncated = true
		}
		out.OnlineAttempts = &attempts
	}
	if duty.VisibilityDenied != nil {
		denied := false
		peerHash := digestBytes([]byte(peer))
		for _, value := range *duty.VisibilityDenied {
			if value == peerHash {
				denied = true
			}
		}
		out.PeerDenied = &denied
	}
	out.KafkaReceipt = product20ReceiptObserved(ctx, db, boot.Header.Epoch, duty.SourceID, "kafka-source")
	out.SourceReceipt = product20ReceiptObserved(ctx, db, boot.Header.Epoch, duty.SourceID, "source")
	return out
}

type product20ManagedDrainObservation struct {
	Available                bool `json:"available"`
	SourceOutcomeUnconfirmed bool `json:"sourceOutcomeUnconfirmed"`
}

func product20ReadManagedDrain(boot *Bootstrap) *product20ManagedDrainObservation {
	out := &product20ManagedDrainObservation{}
	var specs []ownedcontrol.ServiceSpec
	for _, spec := range boot.Services.Specs {
		if spec.Role == "msgtransfer-managed" {
			specs = append(specs, spec)
		}
	}
	if len(specs) != 1 {
		return out
	}
	spec := specs[0]
	var configuration msgtransfer.OwnedOrdinaryBootstrap
	if loadPinnedPrivate(spec.BootstrapPath, spec.BootstrapSHA256, 16<<10, &configuration) != nil ||
		configuration.Database != boot.Header.Database ||
		configuration.Epoch != boot.Header.Epoch ||
		configuration.ActivationSHA256 != boot.Header.ActivationSHA256 ||
		configuration.DrainPath != filepath.Join(spec.Work, "drain.json") {
		return out
	}
	var requirements []ownedcontrol.NamespaceRuntimeRequirement
	for _, requirement := range boot.Services.Requirements {
		if requirement.Role == spec.Role {
			requirements = append(requirements, requirement)
		}
	}
	if len(requirements) != 1 || requirements[0].SourceSHA256 != spec.SourceSHA256 ||
		requirements[0].BinarySHA256 != spec.BinarySHA256 ||
		requirements[0].BootstrapSHA256 != spec.BootstrapSHA256 {
		return out
	}
	raw, err := namespaceinventory.ReadPrivateDocument(configuration.DrainPath, 1<<20)
	if err != nil {
		return out
	}
	var summary msgtransfer.ManagedOrdinaryDrain
	if namespaceinventory.DecodeStrict(raw, &summary) != nil ||
		summary.Version != "managed-ordinary-drained-v1" ||
		summary.DatabaseSHA256 != digestBytes([]byte(boot.Header.Database)) ||
		summary.Epoch != boot.Header.Epoch ||
		summary.ActivationSHA256 != boot.Header.ActivationSHA256 ||
		summary.BootstrapSHA256 != spec.BootstrapSHA256 ||
		summary.PolicySHA256 != configuration.PolicySHA256 {
		return out
	}
	var fields map[string]json.RawMessage
	var unconfirmed bool
	if json.Unmarshal(raw, &fields) != nil ||
		len(fields["sourceOutcomeUnconfirmed"]) == 0 ||
		json.Unmarshal(fields["sourceOutcomeUnconfirmed"], &unconfirmed) != nil {
		return out
	}
	out.Available, out.SourceOutcomeUnconfirmed = true, unconfirmed
	return out
}

func product20Message(ctx context.Context, a, b product20Account, roots *x509.CertPool, observation *product20MessageObservation, onDispatch func(), onFailure func(context.Context, string)) (clientID, serverID string, seq int64, err error) {
	observation.Step = "sender-handshake"
	sender, e := product20Dial(ctx, roots, a)
	if e != nil {
		err = e
		return
	}
	defer sender.Close()
	stopSenderHeartbeat := product20Heartbeat(ctx, sender)
	defer stopSenderHeartbeat()
	observation.Step = "peer-handshake"
	peer, e := product20Dial(ctx, roots, b)
	if e != nil {
		err = e
		return
	}
	defer peer.Close()
	stopPeerHeartbeat := product20Heartbeat(ctx, peer)
	defer stopPeerHeartbeat()
	stopSender := context.AfterFunc(ctx, func() { _ = sender.Close() })
	defer stopSender()
	stopPeer := context.AfterFunc(ctx, func() { _ = peer.Close() })
	defer stopPeer()
	var operation string
	defer func() {
		if err != nil && observation.Step == "peer-read" && observation.AckAccepted && onFailure != nil {
			defer func() { _ = recover() }()
			onFailure(ctx, operation)
		}
	}()
	observation.Step = "send-write"
	clientID = uuid.NewString()
	payload, _ := json.Marshal(map[string]string{"content": "萝卜对话 product20 ordinary acceptance"})
	message := &sdkws.MsgData{SendID: a.UserID, RecvID: b.UserID, ClientMsgID: clientID, SenderPlatformID: 5, SenderNickname: "任务用户", SessionType: 1, MsgFrom: 100, ContentType: 101, Content: payload, CreateTime: time.Now().UnixMilli(), Options: map[string]bool{constant.IsHistory: true, constant.IsPersistent: true, constant.IsSenderSync: true, constant.IsConversationUpdate: true, constant.IsSenderConversationUpdate: true, constant.IsUnreadCount: true, constant.IsOfflinePush: false}}
	encoded, e := proto.Marshal(message)
	if e != nil {
		err = e
		return
	}
	operation = uuid.NewString()
	// Exactly one send. Any missing/unknown ACK fails without redispatch.
	onDispatch()
	if err = product20WireWrite(sender, product20Wire{ReqIdentifier: 1003, SendID: a.UserID, OperationID: operation, MsgIncr: "1", Data: encoded}); err != nil {
		return
	}
	observation.Step = "ack-read"
	acknowledged := false
	for i := 0; i < 32; i++ {
		frame, e := product20WireRead(sender, observation)
		if e != nil {
			err = e
			return
		}
		if frame.ReqIdentifier != 1003 {
			continue
		}
		var ack msg.SendMsgResp
		if frame.OperationID != operation || frame.MsgIncr != "1" || frame.ErrCode != 0 || product20MessageDecode(frame.Data, &ack, observation) != nil || ack.GetClientMsgID() != clientID || ack.GetServerMsgID() == "" {
			err = errors.New("original message acknowledgement rejected")
			return
		}
		serverID = ack.GetServerMsgID()
		acknowledged = true
		observation.AckAccepted = true
		break
	}
	if !acknowledged {
		err = errors.New("original ordinary acknowledgement missing")
		return
	}
	observation.Step = "peer-read"
	for i := 0; i < 32; i++ {
		frame, e := product20WireRead(peer, observation)
		if e != nil {
			err = e
			return
		}
		if frame.ReqIdentifier != 2001 {
			continue
		}
		var push sdkws.PushMessages
		if product20MessageDecode(frame.Data, &push, observation) != nil {
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
		observation.PeerAccepted = true
		break
	}
	if seq <= 0 {
		err = errors.New("actual peer message absent")
		return
	}
	observation.Step = "history-write"
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
	observation.Step = "history-read"
	for i := 0; i < 32; i++ {
		frame, e := product20WireRead(peer, observation)
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
		if e = product20MessageDecode(frame.Data, &pulled, observation); e != nil {
			err = e
			return
		}
		matches := product20Matches(pulled.GetMsgs(), clientID)
		if len(matches) != 1 || matches[0].GetSendID() != a.UserID || matches[0].GetRecvID() != b.UserID || matches[0].GetServerMsgID() != serverID || matches[0].GetSeq() != seq || !bytes.Equal(matches[0].GetContent(), payload) {
			err = errors.New("actual positive-sequence history identity/body differs")
			return
		}
		observation.HistoryAccepted = true
		observation.Step = "complete"
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
func product20RunBrowser(ctx context.Context, plan *product20BrowserPlan, cert tls.Certificate, email, password, userID string) (product20BrowserResult, error) {
	unavailable := product20BrowserResult{Observation: product20BrowserObservation{Stage: "unavailable", ChromeFailure: "none"}}
	result := unavailable
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return result, errors.New("browser fixture certificate rejected")
	}
	pin := sha256.Sum256(leaf.RawSubjectPublicKeyInfo)
	input, err := json.Marshal(map[string]string{"email": email, "password": password, "userID": userID, "certificateSPKI": base64.StdEncoding.EncodeToString(pin[:])})
	if err != nil {
		return result, errors.New("browser fixture input rejected")
	}
	deadline, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	info, err := os.Lstat(plan.Socket)
	if err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		return unavailable, errors.New("private browser socket unavailable")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) {
		return unavailable, errors.New("private browser socket owner differs")
	}
	conn, err := (&net.Dialer{}).DialContext(deadline, "unix", plan.Socket)
	if err != nil {
		return unavailable, errors.New("private browser connection unavailable")
	}
	defer conn.Close()
	stop := context.AfterFunc(deadline, func() { _ = conn.Close() })
	defer stop()
	if sent, err := conn.Write(input); err != nil || sent != len(input) {
		return unavailable, errors.New("private browser input transfer failed")
	}
	if err = conn.(*net.UnixConn).CloseWrite(); err != nil {
		return unavailable, errors.New("private browser input did not close")
	}
	raw, readErr := io.ReadAll(io.LimitReader(conn, 4097))
	if readErr != nil || len(raw) == 0 || len(raw) > 4096 || namespaceinventory.DecodeStrict(raw, &result) != nil {
		return unavailable, errors.New("browser result unavailable")
	}
	if !result.accepted() {
		return result, errors.New("browser acceptance incomplete")
	}
	return result, nil
}

func RunProduct20(ctx context.Context, p Product20Plan) (runErr error) {
	result := product20Result{Version: "fresh-product20-http-ws-result-v1", Phase: "fresh-core-prepare", FreshSQLSeedOnly: true, HTTPExchanges: []product20HTTPExchange{}}
	if p.Browser != nil {
		result.Scope = "web-login"
	}
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
		if result.DeliverySnapshot != nil && boot != nil && result.OriginalCoreCleanupComplete {
			func() {
				defer func() { _ = recover() }()
				result.ManagedDrain = product20ReadManagedDrain(boot)
			}()
		}
		if boot != nil {
			runErr = errors.Join(runErr, boot.Close(cleanup))
		}
		result.Accepted = runErr == nil && result.ProductProcesses == 20 && result.RealRegistrations == 2 && result.RealPasswordLogins == 2 && result.OrdinaryDispatches == 1 && result.PeerMessages == 1 && result.HistoryMessages == 1 && result.WrongPasswordRejected && result.BusinessPermissions && result.ConfigHTTP && result.ChatTokenSelfRead && result.OriginalSixChildrenExited && result.OriginalCoreCleanupComplete && result.AuxiliaryListenersJoined
		if p.Browser != nil {
			result.WebUIAccepted = runErr == nil && result.Browser != nil && result.Browser.accepted() &&
				result.ProductProcesses == 20 && result.RealRegistrations == 1 && result.BusinessPermissions &&
				result.OriginalSixChildrenExited && result.OriginalCoreCleanupComplete && result.AuxiliaryListenersJoined
			result.Accepted = result.WebUIAccepted
		}
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
	if e = aux.start(observation, p.Browser); e != nil {
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
	if p.Browser == nil {
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
	}
	accounts := []product20Account{}
	passwords := []string{}
	emails := []string{}
	accountCount := 2
	if p.Browser != nil {
		accountCount = 1
	}
	for i := 0; i < accountCount; i++ {
		email := "product20-" + strconv.Itoa(i) + "-" + boot.Resource.Nonce + "@product20.invalid"
		password, e := product20Random()
		if e != nil {
			return e
		}
		account, e := product20Registration(ctx, client, aux, email, password)
		if e != nil {
			if errors.Is(e, errProduct20Registration) {
				failure := &product20RegistrationObservation{Attempt: i + 1, RouteState: "unavailable", ChartState: "not-checked", ChartReason: "none"}
				readCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
				failure.readState(readCtx, routePool, chartPool, boot.Header.Tenant, email)
				stop()
				aux.hopMu.Lock()
				failure.RunScopedHops = append([]product20RegistrationHop{}, aux.hops...)
				failure.HopsTruncated = aux.hopsTruncated
				aux.hopMu.Unlock()
				result.RegistrationFailure = failure
			}
			return e
		}
		result.RealRegistrations++
		emails = append(emails, email)
		passwords = append(passwords, password)
		accounts = append(accounts, account)
	}
	if p.Browser != nil {
		result.Phase = "actual-web-browser-login"
		browser, err := product20RunBrowser(ctx, p.Browser, aux.cert, emails[0], passwords[0], accounts[0].UserID)
		result.Browser = &browser
		if err != nil {
			return err
		}
		result.Phase = "web-browser-login-accepted-local-cleanup-required"
		return nil
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
	result.MessageObservation = &product20MessageObservation{Step: "sender-handshake", ReadOutcome: "none"}
	clientID, serverID, seq, e := product20Message(ctx, accounts[0], accounts[1], aux.roots, result.MessageObservation, func() { result.OrdinaryDispatches++ }, func(snapshot context.Context, operation string) {
		result.DeliverySnapshot = product20ReadDeliverySnapshot(snapshot, boot, operation, accounts[0].UserID, accounts[1].UserID)
		result.GatewayPresence = product20GatewayPresence(snapshot, httpClient, boot, accounts[0], accounts[1])
	})
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
